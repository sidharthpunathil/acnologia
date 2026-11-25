package ctl

// `acn peers` lists the servers this one is connected to and `acn peers
// sync` fetches one's manifest again; `acn mcp sync` refreshes a service's
// upstream MCP tool catalog. Both syncs take --async, which starts a
// background job (acn jobs <id> --wait follows it), like the sync_peer and
// sync_service_mcp control tools.

import (
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"
)

type peerLink struct {
	ID         int64            `json:"id"`
	PeerName   string           `json:"peer_name"`
	PeerURL    string           `json:"peer_url"`
	Prefix     string           `json:"prefix"`
	Status     string           `json:"status"`
	Mode       string           `json:"mode"`
	LastSyncAt *time.Time       `json:"last_sync_at"`
	LastError  string           `json:"last_error"`
	Services   map[string]int64 `json:"services"`
}

func (c *cli) peers(args []string) error {
	if len(args) > 0 && args[0] == "sync" {
		return c.peersSync(args[1:])
	}
	if len(args) > 0 {
		return usageErrorf("usage: %[1]s peers, or %[1]s peers sync <peer> [--async]", c.name())
	}
	var raw json.RawMessage
	if err := c.call(http.MethodGet, "/api/admin/peers", nil, &raw); err != nil {
		return err
	}
	if c.json {
		return c.printJSON(raw)
	}
	var out struct {
		Peers []peerLink `json:"peers"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return err
	}
	if len(out.Peers) == 0 {
		fmt.Fprintln(c.out, "No connected servers.")
		return nil
	}
	tw := tabwriter.NewWriter(c.out, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tNAME\tMODE\tSTATUS\tSERVICES\tLAST SYNC\tLAST ERROR")
	for _, p := range out.Peers {
		last := "never"
		if p.LastSyncAt != nil {
			last = p.LastSyncAt.Local().Format(time.RFC3339)
		}
		fmt.Fprintf(tw, "%d\t%s\t%s\t%s\t%d\t%s\t%s\n", p.ID, clean(p.PeerName), clean(p.Mode), clean(p.Status), len(p.Services), last, clean(p.LastError))
	}
	return tw.Flush()
}

// peerByRef finds a connected server by id, name or slug prefix.
func (c *cli) peerByRef(ref string) (peerLink, error) {
	var out struct {
		Peers []peerLink `json:"peers"`
	}
	if err := c.call(http.MethodGet, "/api/admin/peers", nil, &out); err != nil {
		return peerLink{}, err
	}
	id, idErr := strconv.ParseInt(ref, 10, 64)
	for _, p := range out.Peers {
		if (idErr == nil && p.ID == id) || strings.EqualFold(p.PeerName, ref) || (p.Prefix != "" && strings.EqualFold(p.Prefix, ref)) {
			return p, nil
		}
	}
	return peerLink{}, &httpError{Status: 404, Code: "not_found", Message: fmt.Sprintf("no connected server %q; %s peers lists them", ref, c.name())}
}

func (c *cli) peersSync(args []string) error {
	fs := flag.NewFlagSet("peers sync", flag.ContinueOnError)
	fs.SetOutput(c.err)
	async := fs.Bool("async", false, "start a background job and return at once")
	pos, err := parseInterleaved(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return usageErrorf("usage: %s peers sync <peer id or name> [--async]", c.name())
	}
	p, err := c.peerByRef(pos[0])
	if err != nil {
		return err
	}
	path := fmt.Sprintf("/api/admin/peers/%d/sync", p.ID)
	if *async {
		path += "?async=1"
	}
	var raw json.RawMessage
	if err := c.call(http.MethodPost, path, nil, &raw); err != nil {
		return err
	}
	if *async {
		return c.printJobStarted(raw)
	}
	if c.json {
		return c.printJSON(raw)
	}
	var got peerLink
	if err := json.Unmarshal(raw, &got); err != nil {
		return err
	}
	if got.LastError != "" {
		fmt.Fprintf(c.out, "Synced %s (%s) with an error: %s\n", clean(got.PeerName), clean(got.Status), clean(got.LastError))
		return nil
	}
	fmt.Fprintf(c.out, "Synced %s: %d services, %s\n", clean(got.PeerName), len(got.Services), clean(got.Status))
	return nil
}

func (c *cli) mcpSync(args []string) error {
	fs := flag.NewFlagSet("mcp sync", flag.ContinueOnError)
	fs.SetOutput(c.err)
	async := fs.Bool("async", false, "start a background job and return at once")
	pos, err := parseInterleaved(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return usageErrorf("usage: %s mcp sync <service> [--async]", c.name())
	}
	sv, err := c.serviceBySlug(pos[0])
	if err != nil {
		return &httpError{Status: 404, Code: "not_found", Message: err.Error()}
	}
	path := fmt.Sprintf("/api/admin/services/%d/mcp/sync", sv.ID)
	if *async {
		path += "?async=1"
	}
	var raw json.RawMessage
	if err := c.call(http.MethodPost, path, nil, &raw); err != nil {
		return err
	}
	if *async {
		return c.printJobStarted(raw)
	}
	if c.json {
		return c.printJSON(raw)
	}
	var got struct {
		ToolCount int    `json:"tool_count"`
		LastError string `json:"last_error"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		return err
	}
	fmt.Fprintf(c.out, "Synced %d MCP tools for %s\n", got.ToolCount, sv.Slug)
	return nil
}
