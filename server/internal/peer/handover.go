package peer

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Share modes. Proxy keeps every key on the owner; handover seals the keys
// to the receiver, which then calls the provider itself. A handed over key
// cannot be taken back: revoke only makes a cooperating receiver delete it,
// and the lease bounds how long a receiver that stops hearing from the owner
// keeps it.
const (
	ModeProxy    = "proxy"
	ModeHandover = "handover"
)

// HandoverKeys answers GET /api/peer/v1/keys: the share's keys, each sealed
// to the receiver, with the services they belong to. Never a clear secret.
type HandoverKeys struct {
	ShareID  string            `json:"share_id"`
	LeaseSec int               `json:"lease_sec"`
	Services []HandoverService `json:"services"`
	Keys     []HandoverKey     `json:"keys"`
}

// HandoverService says how the receiver calls the provider with the keys of
// one shared service.
type HandoverService struct {
	Slug     string `json:"slug"`
	Name     string `json:"name"`
	Provider string `json:"provider"`
	BaseURL  string `json:"base_url"`
	AuthType string `json:"auth_type"`
	AuthName string `json:"auth_name"`
}

// HandoverKey is one handed over key. ID is the owner's key id.
type HandoverKey struct {
	ID         int64     `json:"id"`
	Service    string    `json:"service"`
	Label      string    `json:"label"`
	Tier       string    `json:"tier"`
	Sealed     string    `json:"sealed_secret"`
	ValidUntil time.Time `json:"valid_until"`
}

// UsageReport is what a receiver tells the owner about the keys it holds,
// on every sync (POST /api/peer/v1/report). It is cooperative: the owner
// shows it as reported by the other server.
type UsageReport struct {
	Keys []KeyReport `json:"keys"`
}

// KeyReport is one held key: the owner's key id, requests made with it here
// since it was handed over, and its status in this server's pool.
type KeyReport struct {
	ID       int64  `json:"id"`
	Requests int64  `json:"requests"`
	Status   string `json:"status"`
}

// RevokeRequest is the body of POST /api/peer/v1/revoke, which an owner
// sends to the receiver to make it delete handed over keys at once. It is
// signed with the owner's Ed25519 identity key.
type RevokeRequest struct {
	ShareID    string `json:"share_id"`
	OwnerID    string `json:"owner_id"`
	ReceiverID string `json:"receiver_id"`
	TS         int64  `json:"ts"`
	Nonce      string `json:"nonce"`
	Signature  string `json:"signature"`
}

// RevokeResponse answers a revoke push.
type RevokeResponse struct {
	OK    bool `json:"ok"`
	Wiped int  `json:"wiped"`
}

// RevokeSkew is how far a revoke push's timestamp may be from the
// receiver's clock.
const RevokeSkew = 5 * time.Minute

// RevokeMessage is the canonical text an owner signs to revoke a share.
func RevokeMessage(shareID, receiverID string, ts int64, nonce string) []byte {
	return []byte("acnologia-revoke-v1\n" + shareID + "\n" + receiverID + "\n" + strconv.FormatInt(ts, 10) + "\n" + nonce)
}

// SignRevoke builds a signed revoke request for a share's receiver.
func SignRevoke(priv ed25519.PrivateKey, ownerID, shareID, receiverID string, now time.Time) RevokeRequest {
	nb := make([]byte, 16)
	_, _ = rand.Read(nb)
	nonce := base64.RawURLEncoding.EncodeToString(nb)
	ts := now.Unix()
	return RevokeRequest{ShareID: shareID, OwnerID: ownerID, ReceiverID: receiverID, TS: ts, Nonce: nonce,
		Signature: base64.StdEncoding.EncodeToString(ed25519.Sign(priv, RevokeMessage(shareID, receiverID, ts, nonce)))}
}

// VerifyRevoke checks a revoke request's signature against the owner key.
func VerifyRevoke(pub ed25519.PublicKey, in RevokeRequest) bool {
	b, err := base64.StdEncoding.DecodeString(in.Signature)
	if err != nil || len(b) != ed25519.SignatureSize {
		return false
	}
	return ed25519.Verify(pub, RevokeMessage(in.ShareID, in.ReceiverID, in.TS, in.Nonce), b)
}

// FetchHandoverKeys reads a handover share's sealed keys with its token.
func FetchHandoverKeys(ctx context.Context, c *http.Client, base, token string) (HandoverKeys, error) {
	var out HandoverKeys
	err := call(ctx, c, http.MethodGet, strings.TrimRight(base, "/")+"/api/peer/v1/keys", token, nil, &out)
	return out, err
}

// SendReport tells the owner what this server holds and how it went.
func SendReport(ctx context.Context, c *http.Client, base, token string, rep UsageReport) error {
	if rep.Keys == nil {
		rep.Keys = []KeyReport{}
	}
	return call(ctx, c, http.MethodPost, strings.TrimRight(base, "/")+"/api/peer/v1/report", token, rep, nil)
}

// PushRevoke sends a signed revoke to the receiver's URL.
func PushRevoke(ctx context.Context, c *http.Client, receiverURL string, req RevokeRequest) (RevokeResponse, error) {
	var out RevokeResponse
	err := call(ctx, c, http.MethodPost, strings.TrimRight(receiverURL, "/")+"/api/peer/v1/revoke", "", req, &out)
	return out, err
}
