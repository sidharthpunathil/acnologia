// Package mcpbridge bridges a stdio MCP client (Claude Desktop, Claude Code,
// Cursor) to an Acnologia streamable HTTP MCP endpoint: `/mcp` for apps and
// `/mcp/control` for running the server. Each JSON-RPC message (or batch)
// read from stdin is POSTed with the token; responses go to stdout, one per
// line. Both `acnologia mcp` and `acn mcp` use it, so it depends only on the
// standard library.
package mcpbridge

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Options says where the bridge sends messages and how.
type Options struct {
	// Endpoint is the full MCP URL, for example https://key.example.com/mcp.
	Endpoint string
	// Token goes in Authorization: Bearer.
	Token string
	// Header is sent on every request in addition to the defaults (the
	// control filters, the access password).
	Header http.Header
	// Unauthorized is the hint added to the error a client sees when the
	// server answers 401, for example "run acn login".
	Unauthorized string
	// Client defaults to one with a 10 minute timeout.
	Client *http.Client
}

// Run reads JSON-RPC messages from stdin until it ends and returns once every
// reply is written. Requests run concurrently so a slow tool call does not
// block pings; replies may come back in any order, as MCP allows.
func Run(o Options, stdin io.Reader, stdout, stderr io.Writer) error {
	client := o.Client
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Minute}
	}
	var outMu sync.Mutex
	write := func(b []byte) {
		outMu.Lock()
		defer outMu.Unlock()
		_, _ = stdout.Write(bytes.TrimSpace(b))
		_, _ = stdout.Write([]byte("\n"))
	}
	var hintOnce sync.Once
	var wg sync.WaitGroup
	// The server's notification stream (tools/list_changed and the like)
	// is relayed to stdout until stdin ends.
	ctx, stop := context.WithCancel(context.Background())
	streamDone := make(chan struct{})
	go func() {
		defer close(streamDone)
		relayNotifications(ctx, o, write)
	}()
	defer func() {
		stop()
		<-streamDone
	}()
	sc := bufio.NewScanner(stdin)
	sc.Buffer(make([]byte, 1<<20), 8<<20)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		msg := append([]byte(nil), line...)
		ids, batch := requestIDs(msg)
		fail := func(text string) {
			if b := errorReply(ids, batch, text); b != nil {
				write(b)
			}
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			req, err := http.NewRequest(http.MethodPost, o.Endpoint, bytes.NewReader(msg))
			if err != nil {
				fail("cannot reach Acnologia at " + o.Endpoint + ": " + err.Error())
				return
			}
			for k, vs := range o.Header {
				for _, v := range vs {
					req.Header.Add(k, v)
				}
			}
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Accept", "application/json")
			req.Header.Set("Authorization", "Bearer "+o.Token)
			resp, err := client.Do(req)
			if err != nil {
				fail("cannot reach Acnologia at " + o.Endpoint + ": " + err.Error())
				return
			}
			defer resp.Body.Close()
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
			switch {
			case resp.StatusCode == http.StatusUnauthorized:
				text := unauthorized(body, o.Unauthorized)
				hintOnce.Do(func() { fmt.Fprintln(stderr, text) })
				fail(text)
			case resp.StatusCode == http.StatusAccepted || len(bytes.TrimSpace(body)) == 0:
				// notification acknowledged, nothing to send back
			case resp.StatusCode == http.StatusOK:
				write(body)
			default:
				fail(fmt.Sprintf("Acnologia returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body))))
			}
		}()
	}
	wg.Wait()
	if err := sc.Err(); err != nil {
		return fmt.Errorf("reading stdin: %w", err)
	}
	return nil
}

// relayNotifications holds GET Endpoint open as a server sent events stream
// and writes each message it carries to stdout, reconnecting when it drops.
// A server without the stream (an older one, or /mcp/control) answers
// something other than 200, and the bridge stops trying.
func relayNotifications(ctx context.Context, o Options, write func([]byte)) {
	streamClient := &http.Client{}
	if o.Client != nil && o.Client.Transport != nil {
		streamClient.Transport = o.Client.Transport
	}
	backoff := time.Second
	for ctx.Err() == nil {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, o.Endpoint, nil)
		if err != nil {
			return
		}
		for k, vs := range o.Header {
			for _, v := range vs {
				req.Header.Add(k, v)
			}
		}
		req.Header.Set("Accept", "text/event-stream")
		req.Header.Set("Authorization", "Bearer "+o.Token)
		resp, err := streamClient.Do(req)
		if err == nil {
			if resp.StatusCode != http.StatusOK || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
				resp.Body.Close()
				return
			}
			backoff = time.Second
			sc := bufio.NewScanner(resp.Body)
			sc.Buffer(make([]byte, 64<<10), 1<<20)
			var data []byte
			for sc.Scan() {
				line := sc.Bytes()
				switch {
				case len(line) == 0:
					if len(data) > 0 {
						write(data)
						data = nil
					}
				case bytes.HasPrefix(line, []byte("data:")):
					data = append(data, bytes.TrimSpace(line[5:])...)
				}
			}
			resp.Body.Close()
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		if backoff < time.Minute {
			backoff *= 2
		}
	}
}

// requestIDs returns the ids of the requests in msg. Notifications have no
// id, so a message without any needs no reply even when the server fails.
func requestIDs(msg []byte) (ids []json.RawMessage, batch bool) {
	type head struct {
		ID json.RawMessage `json:"id"`
	}
	if msg[0] == '[' {
		var hs []head
		_ = json.Unmarshal(msg, &hs)
		for _, h := range hs {
			if len(h.ID) > 0 && string(h.ID) != "null" {
				ids = append(ids, h.ID)
			}
		}
		return ids, true
	}
	var h head
	_ = json.Unmarshal(msg, &h)
	if len(h.ID) > 0 {
		ids = append(ids, h.ID)
	}
	return ids, false
}

// errorReply is the JSON-RPC error for each request in a failed message: one
// object for a single request, an array for a batch, nil when nothing waits.
func errorReply(ids []json.RawMessage, batch bool, text string) []byte {
	if len(ids) == 0 {
		return nil
	}
	one := func(id json.RawMessage) map[string]any {
		return map[string]any{"jsonrpc": "2.0", "id": id, "error": map[string]any{"code": -32603, "message": text}}
	}
	if !batch {
		b, _ := json.Marshal(one(ids[0]))
		return b
	}
	out := make([]map[string]any, len(ids))
	for i, id := range ids {
		out[i] = one(id)
	}
	b, _ := json.Marshal(out)
	return b
}

// unauthorized words a 401 so the person reading it in their MCP client
// knows what to do next.
func unauthorized(body []byte, hint string) string {
	var e struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	_ = json.Unmarshal(body, &e)
	text := "Acnologia refused the token (401)"
	if m := strings.TrimSpace(e.Error.Message); m != "" {
		text += ": " + m
	}
	if hint == "" {
		hint = "pass a valid token"
	}
	return text + "; " + hint
}
