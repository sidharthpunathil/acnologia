package api

import (
	"context"
	"strings"
	"testing"
)

// A key reveal is audited without exception: when the audit row cannot be
// written, the secret is withheld.
func TestRevealWithholdsSecretWhenAuditFails(t *testing.T) {
	e := newEnv(t)
	keys := e.addKeys(map[string]any{"secret": "sk-reveal-me-123"})
	path := "/api/admin/keys/" + itoa(int(keys[0].ID)) + "/reveal"
	countReveals := func() int {
		var n int
		if err := e.st.DB.QueryRow(`SELECT COUNT(*) FROM audit WHERE action='key.reveal'`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}

	// Make the audit insert fail, as a full disk or a locked database would.
	if _, err := e.st.DB.Exec(`CREATE TRIGGER fail_reveal_audit BEFORE INSERT ON audit WHEN NEW.action='key.reveal'
		BEGIN SELECT RAISE(ABORT, 'audit unavailable'); END`); err != nil {
		t.Fatal(err)
	}
	var fail map[string]any
	e.do(e.super, "POST", path, map[string]any{}, 503, &fail)
	if errCode(fail) != "audit_unavailable" || fail["secret"] != nil {
		t.Fatalf("reveal without an audit row should be refused: %v", fail)
	}
	if n := countReveals(); n != 0 {
		t.Fatalf("no audit row expected, got %d", n)
	}

	if _, err := e.st.DB.Exec(`DROP TRIGGER fail_reveal_audit`); err != nil {
		t.Fatal(err)
	}
	var ok struct {
		Secret string `json:"secret"`
	}
	e.do(e.super, "POST", path, map[string]any{}, 200, &ok)
	if ok.Secret != "sk-reveal-me-123" || countReveals() != 1 {
		t.Fatalf("reveal with an audit row: secret %q, rows %d", ok.Secret, countReveals())
	}
}

// AddAuditErr reports a failed write; AddAudit keeps ignoring it.
func TestAddAuditErrReportsFailure(t *testing.T) {
	e := newEnv(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := e.st.AddAuditErr(ctx, nil, "test.action", "t", "d"); err == nil || !strings.Contains(err.Error(), "context canceled") {
		t.Fatalf("a cancelled write should fail: %v", err)
	}
	if err := e.st.AddAuditErr(context.Background(), nil, "test.action", "t", "d"); err != nil {
		t.Fatal(err)
	}
}
