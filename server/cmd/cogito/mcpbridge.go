package main

// `cogito mcp` bridges a stdio MCP client (for example Claude Desktop) to a
// Cogito server's streamable HTTP MCP endpoint. Each JSON-RPC message read
// from stdin is POSTed to /mcp with the app token; responses go to stdout.

import (
	"bufio"
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

func runMCPBridge(args []string) int {
	fs := flag.NewFlagSet("mcp", flag.ContinueOnError)
	url := fs.String("url", envOr("COGITO_URL", "http://localhost:8080"), "Cogito base URL (or COGITO_URL)")
	token := fs.String("token", os.Getenv("COGITO_TOKEN"), "app token cog_... (or COGITO_TOKEN)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if !strings.HasPrefix(*token, "cog_") {
		fmt.Fprintln(os.Stderr, "cogito mcp: set an app token with --token cog_... or COGITO_TOKEN")
		return 2
	}
	endpoint := strings.TrimRight(*url, "/") + "/mcp"
	client := &http.Client{Timeout: 10 * time.Minute}
	var outMu sync.Mutex
	write := func(b []byte) {
		outMu.Lock()
		defer outMu.Unlock()
		os.Stdout.Write(bytes.TrimSpace(b))
		os.Stdout.Write([]byte("\n"))
	}
	fail := func(id json.RawMessage, msg string) {
		if len(id) == 0 {
			return
		}
		b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "error": map[string]any{"code": -32603, "message": msg}})
		write(b)
	}
	var wg sync.WaitGroup
	sc := bufio.NewScanner(os.Stdin)
	sc.Buffer(make([]byte, 1<<20), 8<<20)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		msg := append([]byte(nil), line...)
		var head struct {
			ID json.RawMessage `json:"id"`
		}
		_ = json.Unmarshal(msg, &head)
		wg.Add(1)
		// Requests run concurrently so a slow tool call does not block pings.
		go func() {
			defer wg.Done()
			req, _ := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(msg))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Accept", "application/json")
			req.Header.Set("Authorization", "Bearer "+*token)
			resp, err := client.Do(req)
			if err != nil {
				fail(head.ID, "cannot reach Cogito at "+endpoint+": "+err.Error())
				return
			}
			defer resp.Body.Close()
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
			switch {
			case resp.StatusCode == http.StatusAccepted || len(bytes.TrimSpace(body)) == 0:
				// notification acknowledged, nothing to send back
			case resp.StatusCode == http.StatusOK:
				write(body)
			default:
				fail(head.ID, fmt.Sprintf("Cogito returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body))))
			}
		}()
	}
	wg.Wait()
	return 0
}

func envOr(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}
