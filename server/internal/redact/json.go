package redact

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
)

// opaque fields are ids, signatures and encrypted blobs: changing them breaks
// the request, and they never carry a user's secret.
var opaque = map[string]bool{
	"id": true, "tool_use_id": true, "type": true, "name": true, "role": true, "model": true,
	"signature": true, "data": true, "encrypted_content": true, "encrypted_index": true,
	"media_type": true, "cache_control": true,
}

// MessagesRequest redacts the body of an Anthropic Messages API request.
// The system prompt and every user turn (the user's text and all tool
// results) get the full treatment: known secrets and patterns. Assistant
// turns are the model's own output, replayed; they get the known secrets
// only, and thinking blocks and server tool results (web search and fetch)
// are left as they are, since the API checks their signatures and they hold
// only what the model already saw. Tool definitions are untouched.
//
// It returns body unchanged when nothing was replaced or it is not JSON.
func (r *Redactor) MessagesRequest(ctx context.Context, body []byte) []byte {
	var doc map[string]any
	d := json.NewDecoder(bytes.NewReader(body))
	d.UseNumber()
	if err := d.Decode(&doc); err != nil || doc == nil {
		return body
	}
	w := walker{ctx: ctx, r: r}
	if sys, ok := doc["system"]; ok {
		doc["system"] = w.walk(sys, "", true)
	}
	if msgs, ok := doc["messages"].([]any); ok {
		for i, m := range msgs {
			mm, ok := m.(map[string]any)
			if !ok {
				continue
			}
			full := mm["role"] != "assistant"
			mm["content"] = w.walk(mm["content"], "", full)
			msgs[i] = mm
		}
	}
	if !w.changed {
		return body
	}
	return encode(doc, body)
}

// JSON redacts every string in a JSON document (known secrets and
// patterns). Object keys are kept. Invalid JSON is redacted as text.
func (r *Redactor) JSON(ctx context.Context, body []byte) []byte {
	var doc any
	d := json.NewDecoder(bytes.NewReader(body))
	d.UseNumber()
	if err := d.Decode(&doc); err != nil {
		return []byte(r.String(ctx, string(body)))
	}
	w := walker{ctx: ctx, r: r, all: true}
	doc = w.walk(doc, "", true)
	if !w.changed {
		return body
	}
	return encode(doc, body)
}

func encode(doc any, fallback []byte) []byte {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(doc); err != nil {
		return fallback
	}
	return bytes.TrimRight(b.Bytes(), "\n")
}

type walker struct {
	ctx     context.Context
	r       *Redactor
	all     bool // no opaque fields: every string is text
	changed bool
}

func (w *walker) walk(v any, key string, full bool) any {
	switch x := v.(type) {
	case string:
		if !w.all && opaque[key] {
			return x
		}
		var y string
		if full {
			y = w.r.String(w.ctx, x)
		} else {
			y = w.r.Known(w.ctx, x)
		}
		if y != x {
			w.changed = true
		}
		return y
	case []any:
		for i := range x {
			x[i] = w.walk(x[i], key, full)
		}
		return x
	case map[string]any:
		if !w.all {
			if t, _ := x["type"].(string); t == "thinking" || t == "redacted_thinking" ||
				(strings.HasSuffix(t, "_tool_result") && t != "tool_result") || t == "server_tool_use" {
				return x
			}
		}
		for k, c := range x {
			x[k] = w.walk(c, k, full)
		}
		return x
	}
	return v
}
