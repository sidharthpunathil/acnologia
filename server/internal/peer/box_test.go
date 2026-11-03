package peer

import (
	"bytes"
	"context"
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/rand"
	"strings"
	"testing"
	"time"

	"github.com/sidharthpunathil/acnologia/server/internal/crypto"
	"github.com/sidharthpunathil/acnologia/server/internal/store"
)

func boxPair(t *testing.T) (*ecdh.PrivateKey, string) {
	t.Helper()
	priv, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return priv, BoxPublicKey(priv)
}

func TestSealOpenRoundTrip(t *testing.T) {
	priv, pub := boxPair(t)
	sealed, err := Seal(pub, "shr_1", 7, "acn_srv_b", "sk-secret-value")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(sealed, SealPrefix) || strings.Contains(sealed, "sk-secret-value") {
		t.Fatalf("sealed %q", sealed)
	}
	got, err := Open(priv, sealed, "shr_1", 7, "acn_srv_b")
	if err != nil || got != "sk-secret-value" {
		t.Fatalf("open: %q %v", got, err)
	}
	// Two seals of the same secret differ (fresh ephemeral key each time).
	again, _ := Seal(pub, "shr_1", 7, "acn_srv_b", "sk-secret-value")
	if again == sealed {
		t.Fatal("seal is deterministic")
	}
}

func TestSealFailsWhenTamperedOrMisbound(t *testing.T) {
	priv, pub := boxPair(t)
	other, _ := boxPair(t)
	sealed, err := Seal(pub, "shr_1", 7, "acn_srv_b", "sk-secret-value")
	if err != nil {
		t.Fatal(err)
	}
	// Flip one character of the ciphertext.
	b := []byte(sealed)
	i := len(b) - 5
	if b[i] == 'A' {
		b[i] = 'B'
	} else {
		b[i] = 'A'
	}
	cases := []struct {
		name   string
		key    *ecdh.PrivateKey
		sealed string
		share  string
		keyID  int64
		recv   string
	}{
		{"tampered", priv, string(b), "shr_1", 7, "acn_srv_b"},
		{"wrong receiver key", other, sealed, "shr_1", 7, "acn_srv_b"},
		{"other key id", priv, sealed, "shr_1", 8, "acn_srv_b"},
		{"other share", priv, sealed, "shr_2", 7, "acn_srv_b"},
		{"other receiver id", priv, sealed, "shr_1", 7, "acn_srv_c"},
		{"unknown version", priv, "acnseal2_" + strings.TrimPrefix(sealed, SealPrefix), "shr_1", 7, "acn_srv_b"},
		{"not base64", priv, SealPrefix + "!!!", "shr_1", 7, "acn_srv_b"},
	}
	for _, c := range cases {
		if _, err := Open(c.key, c.sealed, c.share, c.keyID, c.recv); err == nil {
			t.Errorf("%s: opened", c.name)
		}
	}
}

func TestBoxKeysStableAndEncrypted(t *testing.T) {
	dir := t.TempDir()
	box, _ := crypto.New(bytes.Repeat([]byte{3}, 32))
	ctx := context.Background()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	first, err := BoxKeys(ctx, st, box)
	if err != nil {
		t.Fatal(err)
	}
	second, _ := BoxKeys(ctx, st, box)
	if !first.Equal(second) {
		t.Fatal("box key changed")
	}
	if raw := st.GetMeta(ctx, metaBoxKey); raw == "" || strings.Contains(raw, string(first.Bytes())) {
		t.Fatal("box key not stored encrypted")
	}
	id, _, err := Self(ctx, st, box, "test", "https://a.example")
	if err != nil || id.BoxPublicKey != BoxPublicKey(first) {
		t.Fatalf("identity box key %q %v", id.BoxPublicKey, err)
	}
}

func TestRevokeSignature(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	otherPub, _, _ := ed25519.GenerateKey(rand.Reader)
	req := SignRevoke(priv, ServerID(pub), "shr_1", "acn_srv_b", time.Now())
	if !VerifyRevoke(pub, req) {
		t.Fatal("valid revoke refused")
	}
	if VerifyRevoke(otherPub, req) {
		t.Fatal("revoke verified with another key")
	}
	for _, mut := range []func(*RevokeRequest){
		func(r *RevokeRequest) { r.ShareID = "shr_2" },
		func(r *RevokeRequest) { r.ReceiverID = "acn_srv_c" },
		func(r *RevokeRequest) { r.TS++ },
		func(r *RevokeRequest) { r.Nonce += "x" },
	} {
		r := req
		mut(&r)
		if VerifyRevoke(pub, r) {
			t.Fatalf("changed revoke verified: %+v", r)
		}
	}
}

func TestRedeemBoxSignatureBindsBoxKey(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	sig := SignRedeemBox(priv, "shr_1", "h", "nonce-0123456789", "acn_srv_b", "BOXKEY")
	if !VerifyRedeemBox(pub, sig, "shr_1", "h", "nonce-0123456789", "acn_srv_b", "BOXKEY") {
		t.Fatal("valid redeem refused")
	}
	if VerifyRedeemBox(pub, sig, "shr_1", "h", "nonce-0123456789", "acn_srv_b", "OTHER") {
		t.Fatal("redeem verified with another box key")
	}
	if VerifyRedeem(pub, sig, "shr_1", "h", "nonce-0123456789", "acn_srv_b") {
		t.Fatal("v2 signature passed as v1")
	}
}
