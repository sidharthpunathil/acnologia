package api

import (
	"bytes"
	"encoding/json"
	"io"
	"net/url"
	"strings"
)

// Some upstreams echo the key they were called with (account endpoints, error
// pages that quote the request). A caller that may not lease the key must not
// read it that way, so the proxy replaces it in the relayed body as well as
// in the headers.

// scrubMark replaces an echoed key.
var scrubMark = []byte("[key]")

// keyForms are the spellings of a secret an upstream may echo: raw, query
// escaped and path escaped.
func keyForms(secret string) [][]byte {
	if secret == "" {
		return nil
	}
	var out [][]byte
	seen := map[string]bool{}
	cands := []string{secret, url.QueryEscape(secret), url.PathEscape(secret)}
	// JSON escaped, as encoding/json writes it and with "/" escaped as "\/".
	if b, err := json.Marshal(secret); err == nil && len(b) >= 2 {
		js := string(b[1 : len(b)-1])
		cands = append(cands, js, strings.ReplaceAll(js, "/", `\/`))
	}
	for _, f := range cands {
		if f != "" && !seen[f] {
			seen[f] = true
			out = append(out, []byte(f))
		}
	}
	return out
}

// firstForm finds the earliest form in b, the longest one on a tie.
func firstForm(b []byte, forms [][]byte) (int, int) {
	at, n := -1, 0
	for _, f := range forms {
		if i := bytes.Index(b, f); i >= 0 && (at < 0 || i < at || (i == at && len(f) > n)) {
			at, n = i, len(f)
		}
	}
	return at, n
}

// scrubBytes replaces every form of the secret in b.
func scrubBytes(b []byte, forms [][]byte) []byte {
	if len(forms) == 0 {
		return b
	}
	if at, _ := firstForm(b, forms); at < 0 {
		return b
	}
	var out []byte
	for {
		at, n := firstForm(b, forms)
		if at < 0 {
			return append(out, b...)
		}
		out = append(append(out, b[:at]...), scrubMark...)
		b = b[at+n:]
	}
}

// keyPrefixTail is how many trailing bytes of b could be the start of a form
// that the next read completes.
func keyPrefixTail(b []byte, forms [][]byte) int {
	longest := 0
	for _, f := range forms {
		longest = max(longest, len(f))
	}
	for j := max(0, len(b)-longest+1); j < len(b); j++ {
		for _, f := range forms {
			if bytes.HasPrefix(f, b[j:]) {
				return len(b) - j
			}
		}
	}
	return 0
}

// scrubReader replaces the secret in a stream. It holds back only bytes that
// could begin the secret, so a streamed reply (server-sent events) is not
// delayed unless a chunk ends in what looks like the start of the key.
type scrubReader struct {
	r       io.Reader
	forms   [][]byte
	buf     []byte
	pending []byte // read, not yet released
	out     []byte // released, not yet returned
	err     error
}

func newScrubReader(r io.Reader, secret string) io.Reader {
	forms := keyForms(secret)
	if len(forms) == 0 {
		return r
	}
	return &scrubReader{r: r, forms: forms, buf: make([]byte, 32<<10)}
}

func (s *scrubReader) Read(p []byte) (int, error) {
	for len(s.out) == 0 {
		if s.err != nil {
			return 0, s.err
		}
		n, err := s.r.Read(s.buf)
		s.pending = append(s.pending, s.buf[:n]...)
		if err != nil {
			s.err = err
		}
		s.release(err != nil)
	}
	n := copy(p, s.out)
	s.out = s.out[n:]
	return n, nil
}

// release moves scrubbed bytes from pending to out. At the end of the stream
// everything goes; before it, a tail that may start the key stays.
func (s *scrubReader) release(final bool) {
	for {
		at, n := firstForm(s.pending, s.forms)
		if at < 0 {
			break
		}
		s.out = append(append(s.out, s.pending[:at]...), scrubMark...)
		s.pending = s.pending[at+n:]
	}
	keep := 0
	if !final {
		keep = keyPrefixTail(s.pending, s.forms)
	}
	s.out = append(s.out, s.pending[:len(s.pending)-keep]...)
	s.pending = append([]byte(nil), s.pending[len(s.pending)-keep:]...)
}
