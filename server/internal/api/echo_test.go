package api

import (
	"io"
	"net/url"
	"strings"
	"testing"
)

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
	want := string(scrubBytes([]byte(body), keyForms(secret)))
	if strings.Contains(want, "0123456789") {
		t.Fatalf("buffered scrub left the key: %s", want)
	}
	if !strings.HasSuffix(want, `"tail":"sk/li"}`) {
		t.Fatalf("buffered scrub changed text that is not the key: %s", want)
	}
	for size := 1; size <= len(body); size++ {
		got, err := io.ReadAll(newScrubReader(&chunkReader{data: body, size: size}, secret))
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != want {
			t.Fatalf("reads of %d bytes: got %s, want %s", size, got, want)
		}
	}
}
