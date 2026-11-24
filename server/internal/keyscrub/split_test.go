package keyscrub

import (
	"bytes"
	"io"
	"net/url"
	"strings"
	"testing"
	"testing/iotest"
)

// echoSecret has characters that query and path escaping change, so all
// three spellings differ.
const echoSecret = "sk/a+b=echo-secret-1"

// chunkReader returns its data a few bytes at a time, so a key form can be
// split across reads.
type chunkReader struct {
	data string
	size int
}

func (c *chunkReader) Read(p []byte) (int, error) {
	if c.data == "" {
		return 0, io.EOF
	}
	n := min(c.size, len(c.data), len(p))
	copy(p, c.data[:n])
	c.data = c.data[n:]
	return n, nil
}

// TestScrubReaderSplitReads masks a key in every form a body is likely to
// carry it (as is, query and path escaped, JSON escaped), also when a form
// is split across reads of any size, and passes everything else through.
func TestScrubReaderSplitReads(t *testing.T) {
	secret := "sk/live+key=0123456789"
	body := `{"a":"` + secret + `","q":"` + url.QueryEscape(secret) + `","p":"` + url.PathEscape(secret) +
		`","j":"sk\/live+key=0123456789","tail":"sk/li"}`
	want := string(Bytes([]byte(body), Forms(secret)))
	if strings.Contains(want, "0123456789") {
		t.Fatalf("buffered scrub left the key: %s", want)
	}
	if !strings.HasSuffix(want, `"tail":"sk/li"}`) {
		t.Fatalf("buffered scrub changed text that is not the key: %s", want)
	}
	for size := 1; size <= len(body); size++ {
		got, err := io.ReadAll(NewReader(&chunkReader{data: body, size: size}, secret))
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != want {
			t.Fatalf("reads of %d bytes: got %s, want %s", size, got, want)
		}
	}
}

// TestScrubReaderSplitKey: a key split across reads is still replaced, and
// bytes that cannot start the key are released at once.
func TestScrubReaderSplitKey(t *testing.T) {
	in := "a " + echoSecret + " b " + url.QueryEscape(echoSecret) + " c " + url.PathEscape(echoSecret) + " sk/ end"
	got, err := io.ReadAll(NewReader(iotest.OneByteReader(strings.NewReader(in)), echoSecret))
	if err != nil {
		t.Fatal(err)
	}
	if want := "a [key] b [key] c [key] sk/ end"; string(got) != want {
		t.Fatalf("got %q want %q", got, want)
	}
	// Mid stream, only a tail that may start the key is held back.
	r := &scrubReader{forms: Forms(echoSecret)}
	r.pending = []byte("data: x\n\n")
	r.release(false)
	if string(r.out) != "data: x\n\n" || len(r.pending) != 0 {
		t.Fatalf("held %q released %q", r.pending, r.out)
	}
	r.out, r.pending = nil, []byte("data: sk/a")
	r.release(false)
	if string(r.out) != "data: " || string(r.pending) != "sk/a" {
		t.Fatalf("held %q released %q", r.pending, r.out)
	}
	if b := Bytes([]byte("x"+echoSecret), Forms(echoSecret)); !bytes.Equal(b, []byte("x[key]")) {
		t.Fatalf("Bytes %q", b)
	}
}
