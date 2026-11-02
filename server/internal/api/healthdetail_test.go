package api

import (
	"fmt"
	"strings"
	"testing"

	"github.com/sidharthpunathil/acnologia/server/internal/store"
)

// A health check logs what it found, so an invalid key never shows up as "ok".
func TestHealthEventRecordsResult(t *testing.T) {
	e := newEnv(t)
	e.up.bad["badkey99"] = "Invalid API key. Your API key should be here"
	ks := e.addKeys(map[string]any{"secret": "goodkey1", "label": "good"}, map[string]any{"secret": "badkey99", "label": "bad"})

	lastHealth := func(keyID int64) string {
		t.Helper()
		var out struct{ Events []store.Event }
		e.do(e.super, "GET", fmt.Sprintf("/api/admin/events?kind=health&key_id=%d&limit=1", keyID), nil, 200, &out)
		if len(out.Events) == 0 {
			t.Fatalf("no health event for key %d", keyID)
		}
		return out.Events[0].Detail
	}

	// One key at a time.
	e.do(e.super, "POST", fmt.Sprintf("/api/admin/keys/%d/check", ks[1].ID), map[string]any{}, 200, nil)
	if d := lastHealth(ks[1].ID); !strings.HasPrefix(d, "invalid") {
		t.Fatalf("single check on an invalid key logged %q", d)
	}
	e.do(e.super, "POST", fmt.Sprintf("/api/admin/keys/%d/check", ks[0].ID), map[string]any{}, 200, nil)
	if d := lastHealth(ks[0].ID); d != "valid" {
		t.Fatalf("single check on a good key logged %q", d)
	}

	// The bulk check on a service.
	e.do(e.super, "POST", "/api/admin/keys/"+itoa(int(ks[1].ID))+"/reset", nil, 200, nil)
	e.do(e.super, "POST", "/api/admin/services/1/check", map[string]any{}, 200, nil)
	if d := lastHealth(ks[1].ID); !strings.HasPrefix(d, "invalid") {
		t.Fatalf("bulk check on an invalid key logged %q", d)
	}
	if d := lastHealth(ks[0].ID); d != "valid" {
		t.Fatalf("bulk check on a good key logged %q", d)
	}
}

func TestCheckMessagePlural(t *testing.T) {
	if m := checkMessage(checkSummary{Checked: 1, Valid: 1}); !strings.HasPrefix(m, "Checked 1 key:") {
		t.Fatalf("one key: %q", m)
	}
	if m := checkMessage(checkSummary{Checked: 2, Valid: 2}); !strings.HasPrefix(m, "Checked 2 keys:") {
		t.Fatalf("two keys: %q", m)
	}
}
