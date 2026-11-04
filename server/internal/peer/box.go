package peer

import (
	"context"
	"crypto/ecdh"
	"crypto/hpke"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/sidharthpunathil/acnologia/server/internal/crypto"
	"github.com/sidharthpunathil/acnologia/server/internal/store"
)

// Key handover sealing. Each server has an X25519 key pair next to its
// Ed25519 identity, made on first use. An owner seals a key secret to the
// receiver's X25519 public key with HPKE (RFC 9180) in base mode:
// DHKEM(X25519, HKDF-SHA256), HKDF-SHA256 and AES-256-GCM. The HPKE info
// binds the share id, the owner's key id and the receiver's server id, so a
// sealed secret opens only for the key, share and server it was made for.

// metaBoxKey holds the X25519 private key, encrypted with the master key box.
const metaBoxKey = "server_box_key"

// SealPrefix starts every sealed secret, and names its format version. A
// later format gets a new prefix; Open refuses what it does not know. The
// redactor knows the prefix.
const SealPrefix = "acnseal1_"

// BoxKeys loads the server's X25519 private key, creating and storing it on
// first use, so servers made before key handover get one lazily. Creation
// only inserts when no key is stored and the stored key is read back, so two
// first users agree on one key. A failed read is an error, never a reason to
// make a new key.
func BoxKeys(ctx context.Context, st *store.Store, box *crypto.Box) (*ecdh.PrivateKey, error) {
	enc, found, err := st.LookupMeta(ctx, metaBoxKey)
	if err != nil {
		return nil, fmt.Errorf("server box key: %w", err)
	}
	if found {
		return decodeBoxKey(enc, box)
	}
	priv, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	blob, err := box.Encrypt(string(priv.Bytes()))
	if err != nil {
		return nil, err
	}
	if err := st.InsertMetaIfAbsent(ctx, metaBoxKey, base64.StdEncoding.EncodeToString(blob)); err != nil {
		return nil, fmt.Errorf("server box key: %w", err)
	}
	// Another caller may have stored its key first; use what is stored.
	enc, found, err = st.LookupMeta(ctx, metaBoxKey)
	if err != nil {
		return nil, fmt.Errorf("server box key: %w", err)
	}
	if !found {
		return nil, errors.New("server box key: key missing after it was stored")
	}
	return decodeBoxKey(enc, box)
}

// decodeBoxKey opens a stored X25519 private key.
func decodeBoxKey(enc string, box *crypto.Box) (*ecdh.PrivateKey, error) {
	blob, err := base64.StdEncoding.DecodeString(enc)
	if err != nil {
		return nil, fmt.Errorf("server box key: %w", err)
	}
	raw, err := box.Decrypt(blob)
	if err != nil {
		return nil, fmt.Errorf("server box key: %w", err)
	}
	return ecdh.X25519().NewPrivateKey([]byte(raw))
}

// BoxPublicKey is the base64 X25519 public key of a private key.
func BoxPublicKey(priv *ecdh.PrivateKey) string {
	return base64.StdEncoding.EncodeToString(priv.PublicKey().Bytes())
}

// ParseBoxPublicKey decodes a base64 X25519 public key.
func ParseBoxPublicKey(s string) (*ecdh.PublicKey, error) {
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil || len(b) != 32 {
		return nil, errors.New("box public key must be a base64 X25519 key")
	}
	return ecdh.X25519().NewPublicKey(b)
}

// SealInfo is the HPKE info string: what a sealed secret is bound to.
func SealInfo(shareID string, keyID int64, receiverID string) []byte {
	return []byte("acnologia-handover-v1\n" + shareID + "\n" + strconv.FormatInt(keyID, 10) + "\n" + receiverID)
}

var (
	suiteKDF  = hpke.HKDFSHA256
	suiteAEAD = hpke.AES256GCM
)

// Seal encrypts a key secret to a receiver's box public key, bound to the
// share, the key and the receiver. The result is SealPrefix followed by
// base64url(HPKE enc || ciphertext).
func Seal(receiverBoxKey string, shareID string, keyID int64, receiverID, secret string) (string, error) {
	pub, err := ParseBoxPublicKey(receiverBoxKey)
	if err != nil {
		return "", err
	}
	pk, err := hpke.NewDHKEMPublicKey(pub)
	if err != nil {
		return "", err
	}
	ct, err := hpke.Seal(pk, suiteKDF(), suiteAEAD(), SealInfo(shareID, keyID, receiverID), []byte(secret))
	if err != nil {
		return "", err
	}
	return SealPrefix + base64.RawURLEncoding.EncodeToString(ct), nil
}

// ErrSealed means a sealed secret did not open: tampered, made for another
// key, share or server, or in a format this server does not know.
var ErrSealed = errors.New("the sealed secret does not open here")

// Open decrypts a sealed secret with this server's box private key.
func Open(priv *ecdh.PrivateKey, sealed, shareID string, keyID int64, receiverID string) (string, error) {
	raw, ok := strings.CutPrefix(sealed, SealPrefix)
	if !ok || len(raw) > 64<<10 {
		return "", ErrSealed
	}
	ct, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return "", ErrSealed
	}
	sk, err := hpke.NewDHKEMPrivateKey(priv)
	if err != nil {
		return "", err
	}
	pt, err := hpke.Open(sk, suiteKDF(), suiteAEAD(), SealInfo(shareID, keyID, receiverID), ct)
	if err != nil {
		return "", ErrSealed
	}
	return string(pt), nil
}
