package main

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"sync/atomic"
	"testing"
	"time"
)

// TestServeWaitsForInFlight: a request in flight when the signal arrives
// finishes with its answer, and serve returns only after it and the
// background work are done, so the store is not closed under them.
func TestServeWaitsForInFlight(t *testing.T) {
	var finished, bgDone atomic.Bool
	started := make(chan struct{})
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		time.Sleep(400 * time.Millisecond)
		finished.Store(true) // stands in for the request's usage write
		_, _ = w.Write([]byte("ok"))
	})
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	served := make(chan error, 1)
	go func() {
		served <- serve(ctx, cancel, newHTTPServer("", h), ln, 5*time.Second, func() {
			<-ctx.Done()
			time.Sleep(100 * time.Millisecond)
			bgDone.Store(true)
		})
	}()

	type res struct {
		status int
		body   string
		err    error
	}
	got := make(chan res, 1)
	go func() {
		resp, err := http.Get("http://" + ln.Addr().String() + "/")
		if err != nil {
			got <- res{err: err}
			return
		}
		b, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		got <- res{resp.StatusCode, string(b), err}
	}()
	<-started
	cancel() // the signal

	if err := <-served; err != nil {
		t.Fatalf("serve: %v", err)
	}
	if !finished.Load() {
		t.Fatal("serve returned while a request was still in flight")
	}
	if !bgDone.Load() {
		t.Fatal("serve returned before the background work finished")
	}
	r := <-got
	if r.err != nil || r.status != 200 || r.body != "ok" {
		t.Fatalf("in-flight request: status %d body %q err %v", r.status, r.body, r.err)
	}
}

// TestServeGraceCloses: a request that outlives the grace is closed and
// serve still returns, bounded by the grace.
func TestServeGraceCloses(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	started := make(chan struct{})
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		w.(http.Flusher).Flush()
		select {
		case <-release:
		case <-r.Context().Done():
		}
	})
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	served := make(chan error, 1)
	go func() { served <- serve(ctx, cancel, newHTTPServer("", h), ln, 200*time.Millisecond, nil) }()
	go func() {
		if resp, err := http.Get("http://" + ln.Addr().String() + "/"); err == nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
		}
	}()
	<-started
	at := time.Now()
	cancel()
	select {
	case err := <-served:
		if err != nil {
			t.Fatalf("serve: %v", err)
		}
		if d := time.Since(at); d < 150*time.Millisecond {
			t.Fatalf("serve returned after %v, before the grace", d)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("serve did not return after the grace ran out")
	}
}

// TestServeErrorShutsDown: when Serve fails, serve stops the signal context
// so the background loops wind down, waits for them and returns the error.
func TestServeErrorShutsDown(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ln.Close() // Serve fails at once
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var bgDone atomic.Bool
	err = serve(ctx, cancel, newHTTPServer("", http.NotFoundHandler()), ln, time.Second, func() {
		<-ctx.Done()
		bgDone.Store(true)
	})
	if err == nil || errors.Is(err, http.ErrServerClosed) {
		t.Fatalf("serve returned %v, want the listener error", err)
	}
	if !bgDone.Load() {
		t.Fatal("serve returned before the background work finished")
	}
}
