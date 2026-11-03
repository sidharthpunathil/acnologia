package peer

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"regexp"
	"strings"
	"testing"

	"github.com/sidharthpunathil/acnologia/server/internal/crypto"
	"github.com/sidharthpunathil/acnologia/server/internal/store"
)

func TestIdentityStableAcrossRestarts(t *testing.T) {
	dir := t.TempDir()
	box, _ := crypto.New(bytes.Repeat([]byte{3}, 32))
	ctx := context.Background()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	first, _, err := Self(ctx, st, box, "test", "https://a.example")
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`^acn_srv_[a-z2-7]{20}$`).MatchString(first.ID) {
		t.Fatalf("id %q", first.ID)
	}
	if err := CheckIdentity(first); err != nil {
		t.Fatal(err)
	}
	// The private key is stored encrypted, never in the clear.
	raw := st.GetMeta(ctx, metaKey)
	priv, _ := Keys(ctx, st, box)
	if raw == "" || strings.Contains(raw, string(priv.Seed())) {
		t.Fatal("identity key not stored encrypted")
	}
	st.Close()

	st2, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st2.Close()
	second, _, err := Self(ctx, st2, box, "test", "https://a.example")
	if err != nil {
		t.Fatal(err)
	}
	if second.ID != first.ID || second.PublicKey != first.PublicKey {
		t.Fatalf("identity changed across restarts: %s then %s", first.ID, second.ID)
	}
	// Renaming keeps the id.
	_ = SetName(ctx, st2, "Laptop")
	third, _, _ := Self(ctx, st2, box, "test", "")
	if third.ID != first.ID || third.Name != "Laptop" {
		t.Fatalf("after rename %+v", third)
	}
}

func TestInviteRoundTripAndSignature(t *testing.T) {
	secret := NewInviteSecret()
	code := Invite{URL: "https://a.example/", ShareID: "shr_x", Secret: secret}.Encode()
	if !strings.HasPrefix(code, InvitePrefix) {
		t.Fatal(code)
	}
	inv, err := ParseInvite("  " + code + "\n")
	if err != nil || inv.URL != "https://a.example" || inv.ShareID != "shr_x" || inv.Secret != secret {
		t.Fatalf("%+v %v", inv, err)
	}
	for _, bad := range []string{"", "acninv_", "acninv_!!!", "acn_abc", Invite{URL: "javascript:alert(1)", ShareID: "s", Secret: secret}.Encode(),
		Invite{URL: "https://a.example", ShareID: "s", Secret: "short"}.Encode()} {
		if _, err := ParseInvite(bad); err == nil {
			t.Fatalf("accepted %q", bad)
		}
	}
	pub, priv, _ := ed25519.GenerateKey(nil)
	id := ServerID(pub)
	sig := SignRedeem(priv, "shr_x", InviteHash(secret), "nonce1234567890ab", id)
	if !VerifyRedeem(pub, sig, "shr_x", InviteHash(secret), "nonce1234567890ab", id) {
		t.Fatal("valid signature refused")
	}
	if VerifyRedeem(pub, sig, "shr_y", InviteHash(secret), "nonce1234567890ab", id) ||
		VerifyRedeem(pub, sig, "shr_x", InviteHash(secret), "other-nonce-12345", id) ||
		VerifyRedeem(pub, "bm90IGEgc2ln", "shr_x", InviteHash(secret), "nonce1234567890ab", id) {
		t.Fatal("tampered signature accepted")
	}
	other, _, _ := ed25519.GenerateKey(nil)
	if VerifyRedeem(other, sig, "shr_x", InviteHash(secret), "nonce1234567890ab", id) {
		t.Fatal("signature verified with another key")
	}
}

func TestSlugPrefix(t *testing.T) {
	for in, want := range map[string]string{"Sid's Laptop": "sid-s-laptop", "  ": "peer", "railway.app prod server one two": "railway-app-prod-server"} {
		if got := SlugPrefix(in); got != want || !ValidPrefix(got) {
			t.Fatalf("%q: %q", in, got)
		}
	}
}
