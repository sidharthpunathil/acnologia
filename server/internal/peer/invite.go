package peer

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/url"
	"strings"

	"github.com/sidharthpunathil/acnologia/server/internal/crypto"
)

// InvitePrefix starts every invite code. The redactor knows it.
const InvitePrefix = "acninv_"

// Invite is what an invite code carries: where the owner lives, which share
// it is for, and the one time secret. Only the secret's hash is stored.
type Invite struct {
	URL     string `json:"u"`
	ShareID string `json:"s"`
	Secret  string `json:"k"`
}

// NewInviteSecret returns 32 random bytes, base64url encoded.
func NewInviteSecret() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// InviteHash is how an invite secret is stored and compared.
func InviteHash(secret string) string { return crypto.SHA256(secret) }

// Encode renders the invite code: acninv_ + base64url(json).
func (i Invite) Encode() string {
	b, _ := json.Marshal(i)
	return InvitePrefix + base64.RawURLEncoding.EncodeToString(b)
}

// ErrBadInvite means the text is not an invite code.
var ErrBadInvite = errors.New("that is not an Acnologia invite code")

// ParseInvite decodes an invite code and checks its parts.
func ParseInvite(code string) (Invite, error) {
	var i Invite
	code = strings.TrimSpace(code)
	raw, ok := strings.CutPrefix(code, InvitePrefix)
	if !ok || len(raw) > 4096 {
		return i, ErrBadInvite
	}
	b, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return i, ErrBadInvite
	}
	if err := json.Unmarshal(b, &i); err != nil {
		return i, ErrBadInvite
	}
	u, err := url.Parse(i.URL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
		return i, errors.New("the invite does not name a valid server URL")
	}
	i.URL = strings.TrimRight(i.URL, "/")
	if i.ShareID == "" || len(i.Secret) < 32 {
		return i, ErrBadInvite
	}
	return i, nil
}

// RedeemMessage is the canonical text a receiver signs to redeem a share.
func RedeemMessage(shareID, inviteHash, nonce, receiverID string) []byte {
	return []byte("acnologia-redeem-v1\n" + shareID + "\n" + inviteHash + "\n" + nonce + "\n" + receiverID)
}

// SignRedeem signs the redeem message.
func SignRedeem(priv ed25519.PrivateKey, shareID, inviteHash, nonce, receiverID string) string {
	return base64.StdEncoding.EncodeToString(ed25519.Sign(priv, RedeemMessage(shareID, inviteHash, nonce, receiverID)))
}

// VerifyRedeem checks a redeem signature against the receiver's public key.
func VerifyRedeem(pub ed25519.PublicKey, sig, shareID, inviteHash, nonce, receiverID string) bool {
	b, err := base64.StdEncoding.DecodeString(sig)
	if err != nil || len(b) != ed25519.SignatureSize {
		return false
	}
	return ed25519.Verify(pub, RedeemMessage(shareID, inviteHash, nonce, receiverID), b)
}

// RedeemRequest is the body of POST /api/peer/v1/redeem.
type RedeemRequest struct {
	Invite    string `json:"invite"`
	ShareID   string `json:"share_id"`
	ID        string `json:"id"`
	Name      string `json:"name"`
	URL       string `json:"url"`
	PublicKey string `json:"public_key"`
	Nonce     string `json:"nonce"`
	Signature string `json:"signature"`
}

// RedeemResponse answers a redeem: the share token, shown once.
type RedeemResponse struct {
	Token    string   `json:"token"`
	Owner    Identity `json:"owner"`
	Manifest Manifest `json:"manifest"`
}

// PreviewRequest is the body of POST /api/peer/v1/preview: what an invite
// shares, without redeeming it.
type PreviewRequest struct {
	Invite  string `json:"invite"`
	ShareID string `json:"share_id"`
}

// PreviewResponse names what an invite shares.
type PreviewResponse struct {
	Name      string            `json:"name"`
	Services  []ManifestService `json:"services"`
	Providers []string          `json:"providers"`
	ExpiresAt *string           `json:"invite_expires_at"`
}

// Manifest is what a share gives, as GET /api/peer/v1/manifest answers it.
// It never carries key data.
type Manifest struct {
	ShareID   string             `json:"share_id"`
	Name      string             `json:"name"`
	Status    string             `json:"status"`
	Limits    any                `json:"limits"`
	ExpiresAt *string            `json:"expires_at"`
	Services  []ManifestService  `json:"services"`
	Providers []ManifestProvider `json:"providers"`
}

// ManifestService is one shared service.
type ManifestService struct {
	Slug        string  `json:"slug"`
	Name        string  `json:"name"`
	Provider    string  `json:"provider"`
	Category    string  `json:"category"`
	Description string  `json:"description"`
	LogoURL     *string `json:"logo_url"`
	// Available counts the keys that can take a request right now.
	Available int `json:"available"`
}

// ManifestProvider is a provider spec in a manifest. Shared is true for a
// provider shared on its own; providers listed only because a shared service
// uses them have Shared false. A receiver that lacks the slug imports both
// kinds, the second as a dependency of the service that needs it.
type ManifestProvider struct {
	Slug        string          `json:"slug"`
	Name        string          `json:"name"`
	Category    string          `json:"category"`
	Description string          `json:"description"`
	Website     string          `json:"website"`
	DocsURL     string          `json:"docs_url"`
	Color       string          `json:"color"`
	Version     int             `json:"version"`
	Shared      bool            `json:"shared"`
	Spec        json.RawMessage `json:"spec"`
}
