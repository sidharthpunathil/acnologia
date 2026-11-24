package keyscrub

import (
	"bytes"
	"io"
	"runtime"
	"strings"
	"testing"
)

// Guards for the streamed key scrubbing of proxy only and share apps: a key
// split across upstream reads at any offset is still replaced, and the
// reused buffers change no output.

// scrubSecrets have characters that each escaping changes (so every form
// differs), repeat their own start (so a partial match can begin inside a
// longer one), or are as plain as most keys.
var scrubSecrets = []string{
	echoSecret,
	`sk"q\<&>/x y`,
	"sk-plain-0123456789abcdef",
	"aab",
}

// scrubBody holds every form of secret: alone, back to back, cut short (a
// prefix the stream must hold back and then release), and at both ends.
func scrubBody(secret string, filler []byte) []byte {
	forms := Forms(secret)
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

func splitAt(b []byte, at ...int) [][]byte {
	var out [][]byte
	prev := 0
	for _, i := range at {
		i = min(max(i, prev), len(b))
		out = append(out, b[prev:i])
		prev = i
	}
	return append(out, b[prev:])
}

// readAllBy drains r with reads of at most size bytes.
func readAllBy(t testing.TB, r io.Reader, size int) []byte {
	t.Helper()
	var out []byte
	p := make([]byte, size)
	for {
		n, err := r.Read(p)
		out = append(out, p[:n]...)
		if err == io.EOF {
			return out
		}
		if err != nil {
			t.Fatal(err)
		}
	}
}

// legacyScrubReader is the scrubReader before its buffers were reused, kept
// as the reference the current one must match byte for byte.
type legacyScrubReader struct {
	r       io.Reader
	forms   [][]byte
	buf     []byte
	pending []byte
	out     []byte
	err     error
}

func (s *legacyScrubReader) Read(p []byte) (int, error) {
	for len(s.out) == 0 {
		if s.err != nil {
			return 0, s.err
		}
		n, err := s.r.Read(s.buf)
		s.pending = append(s.pending, s.buf[:n]...)
		if err != nil {
			s.err = err
		}
		for {
			at, n := firstForm(s.pending, s.forms)
			if at < 0 {
				break
			}
			s.out = append(append(s.out, s.pending[:at]...), scrubMark...)
			s.pending = s.pending[at+n:]
		}
		keep := 0
		if err == nil {
			keep = keyPrefixTail(s.pending, s.forms)
		}
		s.out = append(s.out, s.pending[:len(s.pending)-keep]...)
		s.pending = append([]byte(nil), s.pending[len(s.pending)-keep:]...)
	}
	n := copy(p, s.out)
	s.out = s.out[n:]
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

// checkScrubSplit streams body in the given chunks through the scrubReader
// and the legacy one, read size bytes at a time, and fails if any form of
// the secret gets through or the two outputs differ. It also wants the
// output of scrubbing the whole body at once, which holds unless two forms
// of the secret overlap each other (a fuzzed "%" has forms "%" and "%25").
func checkScrubSplit(t testing.TB, secret string, body []byte, chunks [][]byte, size int) {
	t.Helper()
	got := checkScrubStream(t, secret, chunks, size)
	if whole := Bytes(body, Forms(secret)); !bytes.Equal(got, whole) {
		t.Fatalf("secret %q chunks %q: streamed %q, whole body %q", secret, chunks, got, whole)
	}
}

func checkScrubStream(t testing.TB, secret string, chunks [][]byte, size int) []byte {
	t.Helper()
	clone := func() [][]byte {
		c := make([][]byte, len(chunks))
		copy(c, chunks)
		return c
	}
	forms := Forms(secret)
	got := readAllBy(t, NewReader(&chunkedReader{clone()}, secret), size)
	want := readAllBy(t, &legacyScrubReader{r: &chunkedReader{clone()}, forms: forms, buf: make([]byte, 32<<10)}, size)
	if hasAnyForm(got, forms) {
		t.Fatalf("secret %q leaked with chunks %q: %q", secret, chunks, got)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("secret %q chunks %q: got %q, legacy %q", secret, chunks, got, want)
	}
	return got
}

// TestScrubReaderEverySplit splits a body holding every key form at every
// offset, at every pair of offsets for one secret, and into single bytes.
func TestScrubReaderEverySplit(t *testing.T) {
	for _, secret := range scrubSecrets {
		body := scrubBody(secret, []byte("-x-"))
		for i := 0; i <= len(body); i++ {
			for _, size := range []int{1, 7, 32 << 10} {
				checkScrubSplit(t, secret, body, splitAt(body, i), size)
			}
		}
		one := make([][]byte, len(body))
		for i := range body {
			one[i] = body[i : i+1]
		}
		checkScrubSplit(t, secret, body, one, 3)
	}
	body := scrubBody(echoSecret, nil)
	for i := 0; i <= len(body); i++ {
		for j := i; j <= len(body); j++ {
			checkScrubSplit(t, echoSecret, body, splitAt(body, i, j), 32<<10)
		}
	}
}

// TestScrubReaderBigStream: a body far larger than one read keeps matching
// the legacy reader, with the key on and across read boundaries.
func TestScrubReaderBigStream(t *testing.T) {
	unit := scrubBody(echoSecret, bytes.Repeat([]byte("a"), 997))
	body := bytes.Repeat(unit, (1<<20)/len(unit)+1)
	for _, step := range []int{4096, 32 << 10, 32<<10 + 13} {
		var chunks [][]byte
		for i := 0; i < len(body); i += step {
			chunks = append(chunks, body[i:min(i+step, len(body))])
		}
		checkScrubSplit(t, echoSecret, body, chunks, 32<<10)
	}
}

// TestScrubReaderBuffersBounded: scrubbing 1 MiB allocates a small, fixed
// amount, not a copy of the body per read.
func TestScrubReaderBuffersBounded(t *testing.T) {
	body := bytes.Repeat([]byte("a"), 1<<20)
	p := make([]byte, 32<<10)
	scrubAll := func() {
		r := NewReader(bytes.NewReader(body), echoSecret)
		for {
			if _, err := r.Read(p); err != nil {
				return
			}
		}
	}
	scrubAll()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	const runs = 5
	for range runs {
		scrubAll()
	}
	runtime.ReadMemStats(&after)
	if per := (after.TotalAlloc - before.TotalAlloc) / runs; per > 256<<10 {
		t.Fatalf("scrubbing 1 MiB allocated %d bytes", per)
	}
}

// FuzzScrubReader puts every form of a fuzzed secret among fuzzed bytes and
// cuts the stream at two fuzzed offsets.
func FuzzScrubReader(f *testing.F) {
	for _, s := range scrubSecrets {
		f.Add(s, []byte("x"), uint16(3), uint16(40), uint8(1))
	}
	f.Add("q", []byte{}, uint16(0), uint16(1), uint8(0))
	f.Fuzz(func(t *testing.T, secret string, filler []byte, a, b uint16, size uint8) {
		// "[key]" replaces a key, so a key that overlaps it (one with a
		// bracket, or "k" or "ey") reads back out of a replacement. That
		// holds for any scrubber with this mark; real keys are not like it.
		if secret == "" || len(secret) > 64 || len(filler) > 256 || strings.ContainsAny(secret, "[]") {
			t.Skip()
		}
		for _, f := range Forms(secret) {
			if bytes.Contains(scrubMark, f) {
				t.Skip()
			}
		}
		body := scrubBody(secret, filler)
		i, j := int(a)%(len(body)+1), int(b)%(len(body)+1)
		checkScrubStream(t, secret, splitAt(body, min(i, j), max(i, j)), int(size)%64+1)
	})
}

// BenchmarkScrubReader1MiB scrubs a 1 MiB stream read 32 KiB at a time, with
// the current reader and the legacy one.
func BenchmarkScrubReader1MiB(b *testing.B) {
	body := bytes.Repeat([]byte("a"), 1<<20)
	p := make([]byte, 32<<10)
	run := func(b *testing.B, mk func() io.Reader) {
		b.SetBytes(int64(len(body)))
		b.ReportAllocs()
		for b.Loop() {
			r := mk()
			for {
				if _, err := r.Read(p); err != nil {
					break
				}
			}
		}
	}
	b.Run("current", func(b *testing.B) {
		run(b, func() io.Reader { return NewReader(bytes.NewReader(body), echoSecret) })
	})
	b.Run("legacy", func(b *testing.B) {
		run(b, func() io.Reader {
			return &legacyScrubReader{r: bytes.NewReader(body), forms: Forms(echoSecret), buf: make([]byte, 32<<10)}
		})
	})
}
