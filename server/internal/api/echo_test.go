package api

import (
	"bytes"
	"net/url"
	"strings"
	"testing"
)

// TestEchoScrubberSplitWrites masks a secret in every form, also when a
// form is split across writes, and passes everything else through.
func TestEchoScrubberSplitWrites(t *testing.T) {
	secret := "sk/live+key=0123456789"
	body := `{"a":"` + secret + `","q":"` + url.QueryEscape(secret) + `","p":"` + url.PathEscape(secret) +
		`","j":"sk\/live+key=0123456789","tail":"sk/li"}`
	want := string(scrubBody([]byte(body), secret))
	if strings.Contains(want, "0123456789") {
		t.Fatalf("buffered scrub left the key: %s", want)
	}
	for size := 1; size <= len(body); size++ {
		var out bytes.Buffer
		sc := newEchoScrubber(&out, secret)
		for i := 0; i < len(body); i += size {
			end := min(i+size, len(body))
			if _, err := sc.Write([]byte(body[i:end])); err != nil {
				t.Fatal(err)
			}
		}
		sc.Flush()
		if out.String() != want {
			t.Fatalf("chunk %d: %s", size, out.String())
		}
	}
}
