package api

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/sidharthpunathil/acnologia/server/internal/crypto"
	"github.com/sidharthpunathil/acnologia/server/internal/respcache"
	"github.com/sidharthpunathil/acnologia/server/internal/store"
)

// Idempotency-Key, kept in the database (store/idempotency.go).
//
// A proxied request with an Idempotency-Key is answered from the stored
// answer of the first request with that key, for 24 hours, across restarts
// and whether or not the response cache is on. The key is scoped to the
// app, its token, the target and the request line (idempotencyKey), and the
// request body must match the first one's, or the answer is 422
// idempotency_key_reused.
//
// The first request writes a pending record before any upstream call. A
// second request with the key while it runs gets 409 idempotency_in_progress
// with Retry-After 1. The first request then stores its final answer, or,
// for an answer that says nothing about the request (408, 425, 429, 5xx) or
// no answer at all, deletes its record so a retry runs again; a panic or a
// client that hangs up releases it the same way. A pending record left by a
// server that stopped mid request is taken over once it is older than
// idemStaleAfter. An answer that ran but cannot be kept (an event stream, or
// larger than idemMaxBody) is delivered once, and a retry gets 422
// idempotency_unreplayable instead of running the request a second time.
//
// Stored answers are encrypted with the master key: a body may echo the key
// to an app allowed to read it.

const (
	// idemTTL is how long an Idempotency-Key's answer is replayed.
	idemTTL = 24 * time.Hour
	// idemMaxBody is the largest answer kept for replay.
	idemMaxBody = 4 << 20
	// idemStaleAfter is when a pending record whose request is not running
	// in this process may be taken over: longer than the proxy's wait for
	// an upstream's answer headers.
	idemStaleAfter = 5 * time.Minute
)

// idemClaim is a request's hold on its Idempotency-Key.
type idemClaim struct {
	s       *Server
	ctx     context.Context // never cancelled: the record must settle when the client hangs up
	hash    string
	owner   string
	unclaim func()
	settled bool
}

func idemHash(key string) string {
	h := sha256.Sum256([]byte(key))
	return hex.EncodeToString(h[:])
}

// beginIdempotent answers a request whose key already has a stored answer,
// is held by a running request, or was used with another body, and reports
// true. Otherwise it claims the key for this request: the caller must call
// release when the request ends.
func (s *Server) beginIdempotent(w http.ResponseWriter, r *http.Request, a *store.App, key, fp string) (*idemClaim, bool) {
	ctx := context.WithoutCancel(r.Context())
	hash := idemHash(key)
	if s.answerIdempotent(ctx, w, a, hash, fp, false) {
		return nil, true
	}
	// A request in this process holds the key: it is running.
	unclaim, ok := s.Cache.Claim(idempotencyPrefix(a.ID) + hash)
	if !ok {
		writeIdemInProgress(w)
		return nil, true
	}
	owner := crypto.RandomToken("", 12)
	now := time.Now()
	won, err := s.Store.ClaimIdempotency(ctx, hash, a.ID, a.TokenID, fp, owner, now, now.Add(idemTTL), now.Add(-idemStaleAfter))
	if err != nil {
		unclaim()
		writeStoreErr(w, err)
		return nil, true
	}
	if !won {
		// Another request settled it since, or a server that stopped left
		// it pending and it is not stale yet.
		unclaim()
		if !s.answerIdempotent(ctx, w, a, hash, fp, true) {
			writeIdemInProgress(w)
		}
		return nil, true
	}
	return &idemClaim{s: s, ctx: ctx, hash: hash, owner: owner, unclaim: unclaim}, false
}

// answerIdempotent answers from the key's record when it decides the
// request: a stored answer, a body mismatch, an answer that could not be
// kept, or (when pendingAnswers is set) a request still running.
func (s *Server) answerIdempotent(ctx context.Context, w http.ResponseWriter, a *store.App, hash, fp string, pendingAnswers bool) bool {
	rec, err := s.Store.GetIdempotency(ctx, hash)
	if errors.Is(err, store.ErrNotFound) {
		return false
	}
	if err != nil {
		writeStoreErr(w, err)
		return true
	}
	if !rec.ExpiresAt.After(time.Now()) {
		return false
	}
	if rec.Fingerprint != fp {
		writeErr(w, 422, "idempotency_key_reused", "this Idempotency-Key was already used with a different request body")
		return true
	}
	switch rec.State {
	case store.IdemDone:
		e, err := s.idemEntry(rec)
		if err != nil {
			writeIdemUnreplayable(w, "its stored answer can no longer be read")
			return true
		}
		// A replay records no usage: the request was paid for when it ran.
		writeCached(w, e, "replay", hidesKey(a))
		return true
	case store.IdemUnreplayable:
		writeIdemUnreplayable(w, "its answer was a stream or larger than 4 MB, so it was not kept")
		return true
	}
	if pendingAnswers {
		writeIdemInProgress(w)
		return true
	}
	return false
}

func writeIdemInProgress(w http.ResponseWriter) {
	w.Header().Set("Retry-After", "1")
	writeErr(w, 409, "idempotency_in_progress", "a request with this Idempotency-Key is still running, retry shortly")
}

func writeIdemUnreplayable(w http.ResponseWriter, why string) {
	writeErr(w, 422, "idempotency_unreplayable", "the request with this Idempotency-Key already ran, but "+why+"; send a new key to run it again")
}

// settle stores the request's final answer for replay. An answer that says
// nothing about the request is not stored, and the release lets a retry run.
func (c *idemClaim) settle(status int, h http.Header, body []byte, keyID int64) {
	if c == nil || c.settled || !idemStorable(status) {
		return
	}
	if len(body) > idemMaxBody {
		c.unreplayable(status, keyID)
		return
	}
	hj, err := json.Marshal(h)
	if err != nil {
		return
	}
	buf := make([]byte, 0, binary.MaxVarintLen64+len(hj)+len(body))
	buf = binary.AppendUvarint(buf, uint64(len(hj)))
	buf = append(buf, hj...)
	buf = append(buf, body...)
	enc, err := c.s.Pool.Box.Encrypt(string(buf))
	if err != nil {
		slog.Warn("idempotency: cannot encrypt the answer", "err", err)
		return
	}
	c.finish(store.IdemDone, status, enc, keyID)
}

// unreplayable records that the request ran but its answer cannot be kept,
// so a retry is refused rather than run again.
func (c *idemClaim) unreplayable(status int, keyID int64) {
	if c == nil || c.settled || !idemStorable(status) {
		return
	}
	c.finish(store.IdemUnreplayable, status, nil, keyID)
}

func (c *idemClaim) finish(state string, status int, payload []byte, keyID int64) {
	ok, err := c.s.Store.FinishIdempotency(c.ctx, c.hash, c.owner, state, status, payload, keyID)
	if err != nil {
		slog.Warn("idempotency: cannot store the answer", "err", err)
		return
	}
	c.settled = ok
}

// release ends the claim. A record that was not settled is deleted, so a
// retry with the key runs the request.
func (c *idemClaim) release() {
	if c == nil {
		return
	}
	if !c.settled {
		if err := c.s.Store.ReleaseIdempotency(c.ctx, c.hash, c.owner); err != nil {
			slog.Warn("idempotency: cannot release the key", "err", err)
		}
	}
	c.unclaim()
}

// idemEntry decodes a stored answer.
func (s *Server) idemEntry(rec *store.IdemRecord) (*respcache.Entry, error) {
	plain, err := s.Pool.Box.Decrypt(rec.Payload)
	if err != nil {
		return nil, err
	}
	b := []byte(plain)
	n, k := binary.Uvarint(b)
	if k <= 0 || uint64(len(b)-k) < n {
		return nil, errors.New("bad idempotency record")
	}
	var h http.Header
	if err := json.Unmarshal(b[k:k+int(n)], &h); err != nil {
		return nil, err
	}
	return &respcache.Entry{Status: rec.Status, Header: h, Body: b[k+int(n):], KeyID: rec.KeyID}, nil
}
