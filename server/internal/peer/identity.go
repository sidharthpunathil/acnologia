// Package peer holds what two Acnologia servers need to share with each
// other: a stable server identity (an Ed25519 keypair), one time invites,
// the signed redeem message, the wire types, and the receiver's sync that
// turns a share's manifest into local services.
package peer

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/sidharthpunathil/acnologia/server/internal/crypto"
	"github.com/sidharthpunathil/acnologia/server/internal/store"
)

// Meta keys. The private key is stored encrypted with the master key box.
const (
	metaKey  = "server_identity_key"
	metaName = "server_name"
	metaURL  = "server_url"
)

// IDPrefix starts every server id.
const IDPrefix = "acn_srv_"

// Identity is what a server says about itself at /.well-known/acnologia.
type Identity struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Version   string `json:"version"`
	PublicKey string `json:"public_key"`
	URL       string `json:"url"`
	// BoxPublicKey is the X25519 key other servers seal handed over keys to.
	BoxPublicKey string `json:"box_public_key,omitempty"`
}

// Fingerprint is a short form of the identity's public key for people to compare.
func (i Identity) Fingerprint() string {
	pub, err := base64.StdEncoding.DecodeString(i.PublicKey)
	if err != nil {
		return ""
	}
	return Fingerprint(pub)
}

// ServerID derives the server id from its public key: acn_srv_ and the
// first 20 characters of the lowercase base32 SHA-256 of the key.
func ServerID(pub ed25519.PublicKey) string {
	sum := sha256.Sum256(pub)
	enc := strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(sum[:]))
	return IDPrefix + enc[:20]
}

// Fingerprint renders the first 16 bytes of SHA-256 of the public key as
// colon separated hex pairs in groups of two bytes.
func Fingerprint(pub []byte) string {
	sum := sha256.Sum256(pub)
	h := hex.EncodeToString(sum[:16])
	parts := make([]string, 0, 8)
	for i := 0; i < len(h); i += 4 {
		parts = append(parts, h[i:i+4])
	}
	return strings.Join(parts, ":")
}

// Keys loads the server's keypair, creating and storing it on first use.
func Keys(ctx context.Context, st *store.Store, box *crypto.Box) (ed25519.PrivateKey, error) {
	if enc := st.GetMeta(ctx, metaKey); enc != "" {
		blob, err := base64.StdEncoding.DecodeString(enc)
		if err != nil {
			return nil, fmt.Errorf("server identity: %w", err)
		}
		seed, err := box.Decrypt(blob)
		if err != nil {
			return nil, fmt.Errorf("server identity: %w", err)
		}
		if len(seed) != ed25519.SeedSize {
			return nil, errors.New("server identity: stored key has the wrong size")
		}
		return ed25519.NewKeyFromSeed([]byte(seed)), nil
	}
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	blob, err := box.Encrypt(string(priv.Seed()))
	if err != nil {
		return nil, err
	}
	if err := st.SetMeta(ctx, metaKey, base64.StdEncoding.EncodeToString(blob)); err != nil {
		return nil, err
	}
	// Another writer may have won the race; read back what is stored.
	return Keys(ctx, st, box)
}

// Name is the server's display name: what a superadmin saved, else the hostname.
func Name(ctx context.Context, st *store.Store) string {
	if n := st.GetMeta(ctx, metaName); n != "" {
		return n
	}
	h, err := os.Hostname()
	if err != nil || h == "" {
		return "Acnologia"
	}
	return h
}

// SavedURL is the public URL a superadmin saved, or "".
func SavedURL(ctx context.Context, st *store.Store) string { return st.GetMeta(ctx, metaURL) }

// SetName saves the display name.
func SetName(ctx context.Context, st *store.Store, name string) error {
	return st.SetMeta(ctx, metaName, name)
}

// SetURL saves the public URL.
func SetURL(ctx context.Context, st *store.Store, u string) error { return st.SetMeta(ctx, metaURL, u) }

// Self returns this server's identity. url is the public URL to announce.
func Self(ctx context.Context, st *store.Store, box *crypto.Box, version, url string) (Identity, ed25519.PrivateKey, error) {
	priv, err := Keys(ctx, st, box)
	if err != nil {
		return Identity{}, nil, err
	}
	bk, err := BoxKeys(ctx, st, box)
	if err != nil {
		return Identity{}, nil, err
	}
	pub := priv.Public().(ed25519.PublicKey)
	return Identity{
		ID: ServerID(pub), Name: Name(ctx, st), Version: version,
		PublicKey: base64.StdEncoding.EncodeToString(pub), URL: url, BoxPublicKey: BoxPublicKey(bk),
	}, priv, nil
}

// ParsePublicKey decodes a base64 Ed25519 public key.
func ParsePublicKey(s string) (ed25519.PublicKey, error) {
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil || len(b) != ed25519.PublicKeySize {
		return nil, errors.New("public key must be a base64 Ed25519 key")
	}
	return ed25519.PublicKey(b), nil
}

// CheckIdentity verifies that an identity's id matches its public key.
func CheckIdentity(i Identity) error {
	pub, err := ParsePublicKey(i.PublicKey)
	if err != nil {
		return err
	}
	if ServerID(pub) != i.ID {
		return errors.New("server id does not match its public key")
	}
	return nil
}
