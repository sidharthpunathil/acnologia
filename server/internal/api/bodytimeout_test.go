package api

import (
	"bufio"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// shortBodyTimeout shortens decode's body deadline for one test.
func shortBodyTimeout(t *testing.T) time.Duration {
	prev := bodyReadTimeout
	bodyReadTimeout = 200 * time.Millisecond
	t.Cleanup(func() { bodyReadTimeout = prev })
	return bodyReadTimeout
}

// rawPost sends a POST whose Content-Length promises 100 bytes but whose
// body is only sent, and returns the status and whether the server then
// closed the connection, giving up after 5 seconds.
func rawPost(t *testing.T, url, path, sent string) (status int, closed bool) {
	t.Helper()
	c, err := net.Dial("tcp", strings.TrimPrefix(url, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	req := "POST " + path + " HTTP/1.1\r\nHost: x\r\nContent-Type: application/json\r\nContent-Length: 100\r\n\r\n" + sent
	if _, err := c.Write([]byte(req)); err != nil {
		t.Fatal(err)
	}
	_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
	br := bufio.NewReader(c)
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		t.Fatalf("no answer to a slow body: %v", err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	_, err = br.ReadByte()
	ne, isNet := err.(net.Error)
	return resp.StatusCode, err != nil && !(isNet && ne.Timeout())
}

// TestDecodeBoundsSlowBodies: an open route answers and drops a client that
// sends its body a byte at a time, instead of holding the connection.
func TestDecodeBoundsSlowBodies(t *testing.T) {
	shortBodyTimeout(t)
	e := newGateEnv(t)
	for name, sent := range map[string]string{
		"stalled body":          "{",
		"stalled after a value": `{"password":"x"}`,
	} {
		status, closed := rawPost(t, e.srv.URL, "/api/gate/unlock", sent)
		if status != 400 || !closed {
			t.Fatalf("%s: status %d, closed %v", name, status, closed)
		}
	}
	// A whole body is unaffected.
	e.do(client(), "POST", "/api/gate/unlock", map[string]any{"password": testAccessPassword}, 200, nil)
}

// TestDecodeLiftsDeadlineForTheResponse: once the body is read, a handler
// may run and stream past the body deadline without its context ending.
func TestDecodeLiftsDeadlineForTheResponse(t *testing.T) {
	d := shortBodyTimeout(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var v map[string]any
		if !decode(w, r, &v) {
			return
		}
		time.Sleep(3 * d)
		if r.Context().Err() != nil {
			http.Error(w, "context ended", 500)
			return
		}
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()
	resp, err := http.Post(srv.URL, "application/json", strings.NewReader(`{"a":1}`))
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status %d: %s", resp.StatusCode, b)
	}
}
