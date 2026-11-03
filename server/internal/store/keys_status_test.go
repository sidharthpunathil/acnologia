package store

import (
	"context"
	"testing"
)

// TestApplyEffectKeepsOperatorStatus: a request that picked a key before an
// operator disabled it must not write a new status over the disable, while
// its other bookkeeping still lands.
func TestApplyEffectKeepsOperatorStatus(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	sv, err := st.CreateService(ctx, &Service{Slug: "s", Name: "S", Provider: "serpapi", BaseURL: "https://x.example", AuthType: "query",
		AuthName: "api_key", Strategy: "round_robin", CooldownSec: 1, MaxAttempts: 1})
	if err != nil {
		t.Fatal(err)
	}
	k, err := st.CreateKey(ctx, NewKey{ServiceID: sv.ID, Label: "k", SecretEnc: []byte("c"), SecretFP: "fp", Hint: "x", Tier: "primary", Weight: 1, Priority: 100})
	if err != nil {
		t.Fatal(err)
	}
	for _, held := range []string{StatusDisabled, StatusBanned} {
		h := held
		if err := st.UpdateKey(ctx, k.ID, KeyPatch{Status: &h}); err != nil {
			t.Fatal(err)
		}
		for _, next := range []string{StatusExhausted, StatusInvalid, StatusActive, StatusBanned} {
			n, msg := next, "late "+next
			if err := st.ApplyEffect(ctx, k.ID, KeyEffect{Status: &n, LastError: &msg}); err != nil {
				t.Fatal(err)
			}
			got, _ := st.GetKey(ctx, k.ID)
			if got.Status != held || got.LastError != msg {
				t.Fatalf("held %s, applied %s: status %q last_error %q", held, next, got.Status, got.LastError)
			}
		}
	}
	act := StatusActive
	if err := st.UpdateKey(ctx, k.ID, KeyPatch{Status: &act}); err != nil {
		t.Fatal(err)
	}
	inv := StatusInvalid
	if err := st.ApplyEffect(ctx, k.ID, KeyEffect{Status: &inv}); err != nil {
		t.Fatal(err)
	}
	if got, _ := st.GetKey(ctx, k.ID); got.Status != StatusInvalid {
		t.Fatalf("active key not marked invalid: %q", got.Status)
	}
}
