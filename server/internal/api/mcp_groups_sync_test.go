package api

import (
	"sort"
	"testing"

	"github.com/sidharthpunathil/acnologia/server/internal/mcpbridge"
)

// The acn CLI checks --groups against its own copy of the control MCP's
// groups (mcpbridge stays free of server imports). This keeps the two
// lists the same, so a new group is never refused by the CLI.
func TestBridgeGroupsMatchServer(t *testing.T) {
	server := []string{}
	for _, g := range ctlGroups {
		server = append(server, g.ID)
	}
	bridge := append([]string{}, mcpbridge.Groups...)
	sort.Strings(server)
	sort.Strings(bridge)
	if len(server) != len(bridge) {
		t.Fatalf("server groups %v, acn groups %v", server, bridge)
	}
	for i := range server {
		if server[i] != bridge[i] {
			t.Fatalf("server groups %v, acn groups %v", server, bridge)
		}
	}
}
