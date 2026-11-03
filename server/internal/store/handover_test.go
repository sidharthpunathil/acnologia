package store

import (
	"context"
	"testing"
	"time"
)

func TestHandoverKeysWipeAndNonces(t *testing.T) {
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
	link, err := st.CreatePeerLink(ctx, &PeerLink{PeerID: "acn_srv_a", PeerURL: "https://a.example", PeerPublicKey: "k", RemoteShareID: "shr_1",
		Status: "active", Prefix: "a", Mode: ShareModeHandover}, 0)
	if err != nil || link.Mode != ShareModeHandover {
		t.Fatalf("link %+v %v", link, err)
	}
	now := time.Now()
	k, err := st.CreateKey(ctx, NewKey{ServiceID: sv.ID, Label: "h", SecretEnc: []byte("cipher"), SecretFP: "fp", Hint: "…", Tier: "primary",
		Hidden: true, Weight: 1, Priority: 100, Handover: &KeyHandoverRef{LinkID: link.ID, RemoteKeyID: 42, ValidUntil: now.Add(time.Hour)}})
	if err != nil {
		t.Fatal(err)
	}
	if !k.IsHandedOver() || *k.PeerRemoteKeyID != 42 || k.PeerValidUntil == nil {
		t.Fatalf("key %+v", k)
	}
	if ks, _ := st.ExpiredHandoverKeys(ctx, now); len(ks) != 0 {
		t.Fatal("expired too early")
	}
	if ks, _ := st.ExpiredHandoverKeys(ctx, now.Add(2*time.Hour)); len(ks) != 1 {
		t.Fatal("not expired after the lease")
	}
	if n, err := st.WipeKeys(ctx, []int64{k.ID}); err != nil || n != 1 {
		t.Fatalf("wipe %d %v", n, err)
	}
	if _, err := st.GetKey(ctx, k.ID); err != ErrNotFound {
		t.Fatal("key row survived the wipe")
	}

	exp := now.Add(10 * time.Minute)
	if ok, _ := st.UseNonce(ctx, "revoke:a", "n1", exp); !ok {
		t.Fatal("fresh nonce refused")
	}
	if ok, _ := st.UseNonce(ctx, "revoke:a", "n1", exp); ok {
		t.Fatal("replayed nonce accepted")
	}
	if ok, _ := st.UseNonce(ctx, "revoke:b", "n1", exp); !ok {
		t.Fatal("nonce scopes are not separate")
	}
}

// Shares made in the same second list newest first, the same way every
// time: share ids are random, so they cannot break the tie.
func TestListSharesStableWithinASecond(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	ids := []string{"shr_zzz", "shr_aaa", "shr_mmm"}
	for _, id := range ids {
		if _, err := s.CreateShare(ctx, &Share{ID: id, Name: id, Services: []string{"svc"}}, "", 0); err != nil {
			t.Fatal(err)
		}
	}
	for run := 0; run < 3; run++ {
		got, err := s.ListShares(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 3 || got[0].ID != "shr_mmm" || got[1].ID != "shr_aaa" || got[2].ID != "shr_zzz" {
			names := []string{}
			for _, sh := range got {
				names = append(names, sh.ID)
			}
			t.Fatalf("want newest first [shr_mmm shr_aaa shr_zzz], got %v", names)
		}
	}
}
