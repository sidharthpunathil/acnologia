// Package notify delivers pool events to webhooks: Slack, Discord, Telegram
// or any URL that accepts signed JSON.
//
// Emit never blocks the request path. Events go into a buffered queue and a
// single worker delivers them with retries. Repeats of the same event about
// the same subject are dropped for a while, so a dead pool does not page
// someone once per request.
package notify

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/sidharthpunathil/acnologia/server/internal/crypto"
	"github.com/sidharthpunathil/acnologia/server/internal/store"
)

// Event types.
const (
	KeyInvalid   = "key.invalid"
	KeyBanned    = "key.banned"
	KeyExhausted = "key.exhausted"
	KeyRecovered = "key.recovered"
	QuotaLow     = "quota.low"
	PoolEmpty    = "pool.empty"
	ServiceDown  = "service.down"
	ServiceUp    = "service.up"
	AppLimit     = "app.limit"
	Test         = "test"
	// Sharing between servers. These are never dropped as repeats.
	ShareCreated  = "share.created"
	ShareRedeemed = "share.redeemed"
	SharePaused   = "share.paused"
	ShareResumed  = "share.resumed"
	ShareRevoked  = "share.revoked"
	ShareUpdated  = "share.updated"
	// Share timers and key handover. Never dropped as repeats.
	ShareExpired       = "share.expired"
	ShareHandover      = "share.handover"
	ShareHandoverWiped = "share.handover_wiped"
	ShareRevokePush    = "share.revoke_push"
	// Sharing with people. Never dropped as repeats. share.revealed is
	// sent once per key, with ids only.
	ShareClaimLink       = "share.claim_link"
	ShareClaimed         = "share.claimed"
	ShareRevealed        = "share.revealed"
	PeerHandoverReceived = "peer.handover_received"
	PeerHandoverWiped    = "peer.handover_wiped"
	// ProviderOffersChanged is a provider refresh publishing new offers.
	// Never dropped as a repeat.
	ProviderOffersChanged = "provider.offers_changed"
)

// Types lists every event a webhook can subscribe to, with a description.
var Types = []struct {
	Type        string `json:"type"`
	Description string `json:"description"`
}{
	{KeyInvalid, "A key was rejected by the provider"},
	{KeyBanned, "A provider suspended the account behind a key"},
	{KeyExhausted, "A key ran out of quota"},
	{KeyRecovered, "A bad or exhausted key works again"},
	{QuotaLow, "A key has less than 10% of its quota left"},
	{PoolEmpty, "A request found no usable key in a service"},
	{ServiceDown, "A provider keeps failing and Acnologia stopped sending it traffic"},
	{ServiceUp, "A provider that was down answers again"},
	{AppLimit, "An app reached its request or budget limit"},
	{ShareCreated, "A share to another server was created"},
	{ShareRedeemed, "Another server redeemed a share invite"},
	{SharePaused, "A share to another server was paused"},
	{ShareResumed, "A paused share was resumed"},
	{ShareRevoked, "A share was revoked, or the other server left it"},
	{ShareUpdated, "A share's services, providers, limits or expiry changed"},
	{ShareExpired, "A share's timer ended and it expired"},
	{ShareHandover, "Keys were handed over to another server for the first time (renewals are not sent)"},
	{ShareHandoverWiped, "Keys handed over to another server are known to be deleted there"},
	{ShareRevokePush, "The result of telling another server to delete handed over keys"},
	{ShareClaimLink, "A new claim link was issued for a share with a person"},
	{ShareClaimed, "A person redeemed the claim link of a share"},
	{ShareRevealed, "A key was revealed to a person by a reveal share (one event per key, ids only)"},
	{PeerHandoverReceived, "Another server handed keys over to this one"},
	{PeerHandoverWiped, "Keys another server handed over were deleted here, with the reason"},
	{ProviderOffersChanged, "A provider refresh published new offers, prices or limits"},
}

func ValidType(t string) bool {
	for _, x := range Types {
		if x.Type == t {
			return true
		}
	}
	return false
}

// Event is what a webhook receives.
type Event struct {
	Type      string         `json:"type"`
	TS        time.Time      `json:"ts"`
	Message   string         `json:"message"`
	ServiceID int64          `json:"service_id,omitempty"`
	Service   string         `json:"service,omitempty"`
	KeyID     int64          `json:"key_id,omitempty"`
	Key       string         `json:"key,omitempty"`
	AppID     int64          `json:"app_id,omitempty"`
	App       string         `json:"app,omitempty"`
	Data      map[string]any `json:"data,omitempty"`
}

func (e Event) subject() string {
	return fmt.Sprintf("%d/%d/%d", e.ServiceID, e.KeyID, e.AppID)
}

// clears lists the events a recovery makes stale, so the next failure after
// a recovery is reported even inside the quiet window.
var clears = map[string][]string{
	KeyRecovered: {KeyInvalid, KeyExhausted, KeyBanned, QuotaLow},
	ServiceUp:    {ServiceDown},
}

type Notifier struct {
	Store  *store.Store
	Box    *crypto.Box
	Client *http.Client
	// Quiet is how long a repeat of the same event about the same subject is dropped.
	Quiet time.Duration

	queue chan Event
	mu    sync.Mutex
	seen  map[string]time.Time
	hooks []*store.Webhook
	when  time.Time
	now   func() time.Time
	sleep func(time.Duration)
}

func New(s *store.Store, box *crypto.Box, c *http.Client) *Notifier {
	return &Notifier{Store: s, Box: box, Client: c, Quiet: 10 * time.Minute,
		queue: make(chan Event, 512), seen: map[string]time.Time{}, now: time.Now, sleep: time.Sleep}
}

// Emit queues an event. It is safe on a nil Notifier and never blocks.
func (n *Notifier) Emit(e Event) {
	if n == nil {
		return
	}
	if e.TS.IsZero() {
		e.TS = n.now().UTC()
	}
	n.mu.Lock()
	for _, t := range clears[e.Type] {
		delete(n.seen, t+"|"+e.subject())
	}
	k := e.Type + "|" + e.subject()
	if last, ok := n.seen[k]; ok && n.now().Sub(last) < n.Quiet && !strings.HasPrefix(e.Type, "share.") && !strings.HasPrefix(e.Type, "peer.") && !strings.HasPrefix(e.Type, "provider.") {
		n.mu.Unlock()
		return
	}
	n.seen[k] = n.now()
	n.mu.Unlock()
	select {
	case n.queue <- e:
	default:
		slog.Warn("webhook queue full, dropping event", "type", e.Type)
	}
}

// Invalidate makes the next delivery reload webhooks from the database.
func (n *Notifier) Invalidate() {
	if n == nil {
		return
	}
	n.mu.Lock()
	n.when = time.Time{}
	n.mu.Unlock()
}

func (n *Notifier) loadHooks(ctx context.Context) []*store.Webhook {
	n.mu.Lock()
	if !n.when.IsZero() && n.now().Sub(n.when) < 30*time.Second {
		h := n.hooks
		n.mu.Unlock()
		return h
	}
	n.mu.Unlock()
	hooks, err := n.Store.ListWebhooks(ctx)
	if err != nil {
		slog.Warn("cannot load webhooks", "err", err)
		return nil
	}
	n.mu.Lock()
	n.hooks, n.when = hooks, n.now()
	n.mu.Unlock()
	return hooks
}

// Run delivers queued events until ctx ends.
func (n *Notifier) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case e := <-n.queue:
			for _, h := range n.loadHooks(ctx) {
				if h.Active && h.Wants(e.Type) {
					n.deliverWithRetry(ctx, h, e)
				}
			}
		}
	}
}

func (n *Notifier) deliverWithRetry(ctx context.Context, h *store.Webhook, e Event) {
	var status int
	var err error
	for attempt, wait := range []time.Duration{0, 2 * time.Second, 10 * time.Second} {
		if attempt > 0 {
			n.sleep(wait)
		}
		status, err = n.Deliver(ctx, h, e)
		if err == nil {
			break
		}
		if status >= 400 && status < 500 && status != 429 {
			break // the receiver rejected the payload; retrying will not help
		}
	}
	msg := ""
	if err != nil {
		msg = err.Error()
		slog.Warn("webhook delivery failed", "webhook", h.ID, "type", e.Type, "err", err)
	}
	_ = n.Store.RecordWebhookDelivery(context.WithoutCancel(ctx), h.ID, status, msg)
}

// Deliver sends one event to one webhook and returns the HTTP status.
func (n *Notifier) Deliver(ctx context.Context, h *store.Webhook, e Event) (int, error) {
	u, err := n.Box.Decrypt(h.URLEnc)
	if err != nil {
		return 0, fmt.Errorf("cannot decrypt webhook URL")
	}
	// Messages quote upstream errors, which can echo a key; the payload
	// leaves the server, so scrub it here.
	e.Message = n.Store.Scrub(ctx, e.Message)
	if len(e.Data) > 0 {
		data := make(map[string]any, len(e.Data))
		for k, v := range e.Data {
			if s, ok := v.(string); ok {
				v = n.Store.Scrub(ctx, s)
			}
			data[k] = v
		}
		e.Data = data
	}
	body, err := Payload(h.Format, e)
	if err != nil {
		return 0, err
	}
	cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, http.MethodPost, u, bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Acnologia-Webhooks/1")
	if h.Format == "json" && len(h.SecretEnc) > 0 {
		if secret, err := n.Box.Decrypt(h.SecretEnc); err == nil {
			ts := strconv.FormatInt(n.now().Unix(), 10)
			setHeader(req.Header, "Timestamp", ts)
			setHeader(req.Header, "Signature", "sha256="+Sign(secret, ts, body))
		}
	}
	setHeader(req.Header, "Event", e.Type)
	resp, err := n.Client.Do(req)
	if err != nil {
		// The URL is itself a secret (Slack, Discord and Telegram put the
		// token in it), and transport errors quote it.
		return 0, errors.New(strings.ReplaceAll(err.Error(), u, h.URLHint))
	}
	defer resp.Body.Close()
	rb, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
	if resp.StatusCode >= 300 {
		return resp.StatusCode, fmt.Errorf("receiver answered %d%s", resp.StatusCode, receiverText(resp.Header.Get("Content-Type"), rb))
	}
	return resp.StatusCode, nil
}

// receiverText is a short, readable form of an error body: HTML pages are
// left out and long bodies are cut so the dashboard stays legible.
func receiverText(_ string, body []byte) string {
	s := strings.Join(strings.Fields(string(body)), " ")
	// Some receivers label short plain answers as HTML (Slack's "no_team"), so
	// judge by the body rather than the content type: markup is left out.
	if s == "" || strings.HasPrefix(s, "<") {
		return ""
	}
	if r := []rune(s); len(r) > 160 {
		s = string(r[:160]) + "…"
	}
	return ": " + s
}

// Sign is the HMAC-SHA256 of "<timestamp>.<body>" in hex. Receivers should
// recompute it and reject old timestamps.
func Sign(secret, ts string, body []byte) string {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write([]byte(ts))
	m.Write([]byte("."))
	m.Write(body)
	return hex.EncodeToString(m.Sum(nil))
}

// Text is the one-line human form used by chat formats.
func Text(e Event) string { return "Acnologia: " + e.Message }

// Payload renders the body for a webhook format.
func Payload(format string, e Event) ([]byte, error) {
	switch format {
	case "slack":
		return json.Marshal(map[string]string{"text": Text(e)})
	case "discord":
		return json.Marshal(map[string]any{"content": Text(e), "allowed_mentions": map[string]any{"parse": []string{}}})
	case "telegram":
		return json.Marshal(map[string]any{"text": Text(e), "disable_web_page_preview": true})
	case "json", "":
		return json.Marshal(e)
	}
	return nil, fmt.Errorf("unknown webhook format %q", format)
}

var Formats = []string{"json", "slack", "discord", "telegram"}

func ValidFormat(f string) bool {
	for _, x := range Formats {
		if x == f {
			return true
		}
	}
	return false
}

// setHeader sets X-Acnologia-<name> and the old X-Cogito-<name>. The old
// header is kept for existing receivers that verify it; drop it in a later release.
func setHeader(h http.Header, name, value string) {
	h.Set("X-Acnologia-"+name, value)
	h.Set("X-Cogito-"+name, value)
}
