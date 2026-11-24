// Package tokenmeter reads the tokens an LLM answer used as the answer
// passes through the proxy, without changing what the client gets.
package tokenmeter

import (
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
)

// Token metering: LLM providers report the tokens a call used in the
// answer, as usage.input_tokens and usage.output_tokens (Anthropic, OpenAI
// Responses), usage.prompt_tokens and usage.completion_tokens (OpenAI Chat
// and its look alikes) or usageMetadata.promptTokenCount and
// candidatesTokenCount (Gemini). A streamed answer reports them in its
// first and last events, so the meter keeps the head and the tail of the
// body as it passes through and reads the counts once it has been sent.
// Counts are cumulative where a stream repeats them, so the largest wins.

const (
	meterHead = 16 << 10
	meterTail = 64 << 10
)

var (
	tokenInRe  = regexp.MustCompile(`"(?:input_tokens|prompt_tokens|promptTokenCount)"\s*:\s*(\d{1,12})`)
	tokenOutRe = regexp.MustCompile(`"(?:output_tokens|completion_tokens|candidatesTokenCount)"\s*:\s*(\d{1,12})`)
	modelRe    = regexp.MustCompile(`"(?:model|modelVersion)"\s*:\s*"([^"\\]{1,200})"`)
)

// Meter is an io.ReadCloser that copies the start and end of what it
// reads. It never changes what the client gets.
type Meter struct {
	rc   io.ReadCloser
	head []byte
	tail []byte
}

// New meters rc.
func New(rc io.ReadCloser) *Meter { return &Meter{rc: rc} }

func (m *Meter) Read(p []byte) (int, error) {
	n, err := m.rc.Read(p)
	if n > 0 {
		b := p[:n]
		if room := meterHead - len(m.head); room > 0 {
			k := min(room, len(b))
			m.head = append(m.head, b[:k]...)
		}
		m.tail = append(m.tail, b...)
		if len(m.tail) > 2*meterTail {
			m.tail = append(m.tail[:0], m.tail[len(m.tail)-meterTail:]...)
		}
	}
	return n, err
}

func (m *Meter) Close() error { return m.rc.Close() }

// Counts returns the input and output tokens seen, 0 when none.
func (m *Meter) Counts() (in, out int64) {
	tail := m.tail
	if len(tail) > meterTail {
		tail = tail[len(tail)-meterTail:]
	}
	for _, chunk := range [][]byte{m.head, tail} {
		in = max(in, maxMatch(tokenInRe, chunk))
		out = max(out, maxMatch(tokenOutRe, chunk))
	}
	return in, out
}

// Model is the first model name the answer reports, or "". Answers name
// the model near the start (OpenAI, Anthropic) or in every event of a
// stream, so the head is enough. A nil Meter reports "".
func (m *Meter) Model() string {
	if m == nil {
		return ""
	}
	if sm := modelRe.FindSubmatch(m.head); sm != nil {
		return string(sm[1])
	}
	return ""
}

func maxMatch(re *regexp.Regexp, b []byte) int64 {
	var best int64
	for _, m := range re.FindAllSubmatch(b, -1) {
		if v, err := strconv.ParseInt(string(m[1]), 10, 64); err == nil && v > best {
			best = v
		}
	}
	return best
}

// Meterable says whether a successful answer of a service in category is
// worth reading for tokens: an LLM service, a JSON or event stream body,
// not compressed.
func Meterable(category string, resp *http.Response) bool {
	if !strings.EqualFold(strings.TrimSpace(category), "llm") {
		return false
	}
	if ce := strings.ToLower(strings.TrimSpace(resp.Header.Get("Content-Encoding"))); ce != "" && ce != "identity" {
		return false
	}
	ct := strings.ToLower(resp.Header.Get("Content-Type"))
	return strings.Contains(ct, "json") || strings.Contains(ct, "event-stream")
}
