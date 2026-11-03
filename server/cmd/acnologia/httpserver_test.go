package main

import (
	"bufio"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestHTTPServerTimeouts(t *testing.T) {
	hs := newHTTPServer(":0", http.NotFoundHandler())
	if hs.ReadHeaderTimeout <= 0 || hs.IdleTimeout <= 0 {
		t.Fatalf("header %v, idle %v: both must be set", hs.ReadHeaderTimeout, hs.IdleTimeout)
	}
	// Either would cut long streaming responses.
	if hs.ReadTimeout != 0 || hs.WriteTimeout != 0 {
		t.Fatalf("read %v, write %v: must stay unset", hs.ReadTimeout, hs.WriteTimeout)
	}
}

// TestHTTPServerClosesIdleKeepAlive: an idle keep-alive connection is closed
// once IdleTimeout passes instead of being held forever.
func TestHTTPServerClosesIdleKeepAlive(t *testing.T) {
	ts := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	hs := newHTTPServer("", ts.Config.Handler)
	hs.IdleTimeout = 200 * time.Millisecond // the real value, shortened
	ts.Config = hs
	ts.Start()
	defer ts.Close()

	c, err := net.Dial("tcp", ts.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if _, err := c.Write([]byte("GET / HTTP/1.1\r\nHost: x\r\n\r\n")); err != nil {
		t.Fatal(err)
	}
	br := bufio.NewReader(c)
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, err := br.ReadByte(); err == nil {
		t.Fatal("unexpected bytes on an idle connection")
	} else if ne, ok := err.(net.Error); ok && ne.Timeout() {
		t.Fatal("the server kept an idle keep-alive connection open")
	}
}
