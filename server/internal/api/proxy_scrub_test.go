package api

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/sidharthpunathil/acnologia/server/internal/keyscrub"
)

// Guards for the proxy's key scrubbing end to end, and for the pooled relay
// buffer: a proxy only app never reads a key the upstream splits, and a
// pooled buffer never carries a scrubbed relay. The scrubber itself is
// tested in internal/keyscrub.

// scrubBody holds every form of secret: alone, back to back, cut short (a
// prefix the stream must hold back and then release), and at both ends.
func scrubBody(secret string, filler []byte) []byte {
	forms := keyscrub.Forms(secret)
	var b bytes.Buffer
	for _, f := range forms {
		b.Write(f)
		b.Write(filler)
		b.Write(f[:len(f)/2])
		b.Write(filler)
		b.Write(f)
		b.Write(f)
		b.WriteString(" data: ")
	}
	b.Write(forms[0][:len(forms[0])-1])
	b.Write(forms[len(forms)-1])
	return b.Bytes()
}

// chunkedReader returns one chunk per Read (or as much as p holds).
type chunkedReader struct{ chunks [][]byte }

func (c *chunkedReader) Read(p []byte) (int, error) {
	for len(c.chunks) > 0 && len(c.chunks[0]) == 0 {
		c.chunks = c.chunks[1:]
	}
	if len(c.chunks) == 0 {
		return 0, io.EOF
	}
	n := copy(p, c.chunks[0])
	c.chunks[0] = c.chunks[0][n:]
	return n, nil
}

func hasAnyForm(b []byte, forms [][]byte) bool {
	for _, f := range forms {
		if bytes.Contains(b, f) {
			return true
		}
	}
	return false
}

// splitUpstream echoes every form of the key it was called with, in two
// writes flushed apart at the offset given by ?at=.
type splitUpstream struct{ secret string }

func (u splitUpstream) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !strings.Contains(r.Header.Get("Authorization"), u.secret) {
		w.WriteHeader(401)
		return
	}
	body := scrubBody(u.secret, []byte(`","`))
	at, _ := strconv.Atoi(r.URL.Query().Get("at"))
	at = min(max(at, 0), len(body))
	w.Header().Set("Content-Type", "text/plain")
	w.WriteHeader(200)
	_, _ = w.Write(body[:at])
	w.(http.Flusher).Flush()
	time.Sleep(time.Millisecond)
	_, _ = w.Write(body[at:])
}

// TestProxyOnlyAppNeverSeesSplitKey: through the HTTP proxy, a proxy only app
// never reads the key wherever the upstream splits its echo, while an app
// that may lease the key gets the body byte for byte (the pooled relay).
func TestProxyOnlyAppNeverSeesSplitKey(t *testing.T) {
	up := splitUpstream{secret: echoSecret}
	e := newHPEnvWith(t, up, echoSecret)
	ponly := e.proxyOnlyApp(t)
	body := scrubBody(echoSecret, []byte(`","`))
	forms := keyscrub.Forms(echoSecret)
	get := func(token string, at int) []byte {
		req, _ := http.NewRequest("GET", e.srv.URL+"/proxy/gen/echo?at="+strconv.Itoa(at), nil)
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := e.client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != 200 {
			t.Fatalf("at %d: %d %s", at, resp.StatusCode, b)
		}
		return b
	}
	for at := 0; at <= len(body); at++ {
		if got := get(ponly, at); hasAnyForm(got, forms) || !bytes.Equal(got, keyscrub.Bytes(body, forms)) {
			t.Fatalf("split at %d: proxy only app read %q", at, got)
		}
		if got := get(e.token, at); !bytes.Equal(got, body) {
			t.Fatalf("split at %d: leasing app read %q", at, got)
		}
	}
}

// relayRecorder is a ResponseWriter that runs check on every Write.
type relayRecorder struct {
	h     http.Header
	code  int
	body  bytes.Buffer
	check func(p []byte)
}

func (r *relayRecorder) Header() http.Header { return r.h }
func (r *relayRecorder) WriteHeader(c int)   { r.code = c }
func (r *relayRecorder) Write(p []byte) (int, error) {
	r.check(p)
	return r.body.Write(p)
}

func relayResponse(chunks ...string) *http.Response {
	var c [][]byte
	for _, s := range chunks {
		c = append(c, []byte(s))
	}
	return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(&chunkedReader{c})}
}

func sameBacking(a, b []byte) bool { return len(a) > 0 && len(b) > 0 && &a[0] == &b[0] }

// TestRelayPooledBufferOnlyUnscrubbed: a scrubbed relay never writes from a
// pooled buffer; an unscrubbed one gives its buffer back only after the last
// Write, cleared.
func TestRelayPooledBufferOnlyUnscrubbed(t *testing.T) {
	s := &Server{}
	_, cancel := context.WithCancel(context.Background())
	defer cancel()
	v := relayView{keyID: 1, attempts: 1}

	// A scrubbed relay: seed the pool, and fail if the seeded buffer (or
	// any pooled one) is what the body is written from.
	for range 20 {
		seed := relayBufs.Get().(*[]byte)
		relayBufs.Put(seed)
		w := &relayRecorder{h: http.Header{}, check: func(p []byte) {
			if sameBacking(p, *seed) {
				t.Fatal("a scrubbed relay wrote from a pooled buffer")
			}
		}}
		s.deliver(w, relayResponse("a "+echoSecret, " b"), nil, v, cancel, echoSecret, true, capture{})
		if w.body.String() != "a [key] b" {
			t.Fatalf("scrubbed relay wrote %q", w.body.String())
		}
	}

	// An unscrubbed relay: while it writes, its buffer is not in the pool.
	reused := false
	for range 20 {
		var inUse []byte
		w := &relayRecorder{h: http.Header{}}
		w.check = func(p []byte) {
			inUse = p[:cap(p)]
			other := relayBufs.Get().(*[]byte)
			if sameBacking(*other, p) {
				t.Fatal("the relay's buffer was pooled before its last Write")
			}
			relayBufs.Put(other)
		}
		s.deliver(w, relayResponse("first ", echoSecret, " last"), nil, v, cancel, echoSecret, false, capture{})
		if w.body.String() != "first "+echoSecret+" last" {
			t.Fatalf("unscrubbed relay wrote %q", w.body.String())
		}
		// Back in the pool (sync.Pool may drop it, so look a few times),
		// with the bytes it carried cleared.
		for range 4 {
			b := relayBufs.Get().(*[]byte)
			if sameBacking(*b, inUse) {
				reused = true
				if bytes.Contains(*b, []byte(echoSecret)) || bytes.Contains(*b, []byte("first")) {
					t.Fatal("a pooled relay buffer kept response bytes")
				}
			}
			defer relayBufs.Put(b)
		}
	}
	if !reused {
		t.Fatal("the unscrubbed relay never returned its buffer to the pool")
	}
}
