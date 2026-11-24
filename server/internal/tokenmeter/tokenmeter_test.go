package tokenmeter

import (
	"io"
	"strings"
	"testing"
)

func meterOf(t *testing.T, body string) (int64, int64) {
	t.Helper()
	m := New(io.NopCloser(strings.NewReader(body)))
	got, err := io.ReadAll(m)
	if err != nil || string(got) != body {
		t.Fatalf("meter changed the body: %v", err)
	}
	return m.Counts()
}

// TestTokenMeterShapes: the counts of each provider's answer shape, plain
// and streamed, with a long middle between the head and the tail.
func TestTokenMeterShapes(t *testing.T) {
	filler := strings.Repeat(`data: {"type":"content_block_delta","delta":{"text":"lorem ipsum "}}`+"\n\n", 4000)
	cases := []struct {
		name    string
		body    string
		in, out int64
	}{
		{"openai chat", `{"choices":[],"usage":{"prompt_tokens":12,"completion_tokens":34,"total_tokens":46}}`, 12, 34},
		{"anthropic", `{"content":[],"usage":{"input_tokens":7,"cache_read_input_tokens":900,"output_tokens":5}}`, 7, 5},
		{"gemini", `{"candidates":[],"usageMetadata":{"promptTokenCount":3,"candidatesTokenCount":9,"totalTokenCount":12}}`, 3, 9},
		{"anthropic stream", `data: {"type":"message_start","message":{"usage":{"input_tokens":25,"output_tokens":1}}}` + "\n\n" + filler +
			`data: {"type":"message_delta","usage":{"output_tokens":512}}` + "\n\n", 25, 512},
		{"openai stream", filler + `data: {"choices":[],"usage":{"prompt_tokens":40,"completion_tokens":80}}` + "\n\ndata: [DONE]\n\n", 40, 80},
		{"none", `{"results":[1,2,3]}`, 0, 0},
	}
	for _, c := range cases {
		in, out := meterOf(t, c.body)
		if in != c.in || out != c.out {
			t.Errorf("%s: got %d/%d, want %d/%d", c.name, in, out, c.in, c.out)
		}
	}
}
