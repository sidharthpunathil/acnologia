package api

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/sidharthpunathil/acnologia/server/internal/pool"
	"github.com/sidharthpunathil/acnologia/server/internal/providers"
	"github.com/sidharthpunathil/acnologia/server/internal/store"
)

// Key affinity in the proxy: a request that must reach one key.
//
// A provider spec's affinity rules (providers/affinity.go) name requests
// that create a resource the provider keeps on the creating account (a
// prediction, a run, a batch, a file). When one succeeds, the ids in its
// answer are recorded for the key that served it, for resourceAffinityTTL.
// A later request that names one of those ids is sent to that key only.
//
// A caller can also bind its own requests: every request on a service with
// the same X-Acnologia-Key-Affinity value (per app) goes to one key, for
// headerAffinityTTL after the last answer.
//
// When the bound key cannot take the request (cooling down, busy past the
// queue timeout, disabled, deleted, or no longer allowed for the app) the
// answer is 503 key_unavailable_for_resource: another key would only get a
// 404 from the provider, or break the caller's session.

const (
	affinityHeader      = "X-Acnologia-Key-Affinity"
	resourceAffinityTTL = 7 * 24 * time.Hour
	headerAffinityTTL   = 30 * time.Minute
	// maxAffinityValue is the longest X-Acnologia-Key-Affinity value used.
	maxAffinityValue = 200
	// affinityGrabMax is the most of an answer read for resource ids.
	affinityGrabMax = 1 << 20
)

// keyAffinity is what binds one proxied request to a key.
type keyAffinity struct {
	hit providers.AffinityHit
	// key is the key the request must use, 0 when nothing binds it.
	key int64
	// header is the store ref of the caller's affinity value, "" when the
	// request carries none, and expires when its binding ends.
	header  string
	expires time.Time
}

// keyAffinityFor finds the key a request is bound to on the service.
func (s *Server) keyAffinityFor(ctx context.Context, r *http.Request, a *store.App, sv *store.Service, prov *providers.Spec, path string, body []byte) keyAffinity {
	aff := keyAffinity{hit: prov.MatchAffinity(r.Method, "/"+path, body)}
	now := time.Now()
	if id := aff.hit.FollowID; id != "" {
		if k, _, ok, err := s.Store.KeyAffinity(ctx, sv.ID, resourceRef(id), now); err != nil {
			slog.Warn("key affinity lookup failed", "service", sv.Slug, "err", err)
		} else if ok {
			aff.key = k
			return aff
		}
	}
	if v := strings.TrimSpace(r.Header.Get(affinityHeader)); v != "" && len(v) <= maxAffinityValue {
		aff.header = "h|" + strconv.FormatInt(a.ID, 10) + "|" + v
		if k, exp, ok, err := s.Store.KeyAffinity(ctx, sv.ID, aff.header, now); err != nil {
			slog.Warn("key affinity lookup failed", "service", sv.Slug, "err", err)
		} else if ok {
			aff.key, aff.expires = k, exp
		}
	}
	return aff
}

func resourceRef(id string) string { return "r|" + id }

// affinityAnswered notes that keyID answered the request (anything but a
// refusal): a caller's affinity value is bound to it, or its binding is
// renewed once half of it has run. The write outlives a client that hangs up.
func (s *Server) affinityAnswered(ctx context.Context, sv *store.Service, aff *keyAffinity, keyID int64) {
	if aff.header == "" || (aff.key == keyID && time.Until(aff.expires) > headerAffinityTTL/2) {
		return
	}
	if err := s.Store.SetKeyAffinity(context.WithoutCancel(ctx), sv.ID, aff.header, keyID, time.Now().Add(headerAffinityTTL)); err != nil {
		slog.Warn("key affinity not stored", "service", sv.Slug, "err", err)
	}
}

// grabber copies the start of an answer while it is relayed, so the ids in
// it can be read afterwards. Past affinityGrabMax it gives up.
type grabber struct {
	r    io.Reader
	buf  []byte
	over bool
}

func (g *grabber) Read(p []byte) (int, error) {
	n, err := g.r.Read(p)
	if n > 0 && !g.over {
		if len(g.buf)+n > affinityGrabMax {
			g.over, g.buf = true, nil
		} else {
			g.buf = append(g.buf, p[:n]...)
		}
	}
	return n, err
}

// grabFor wraps a successful answer's body when its ids must be recorded.
func grabFor(aff *keyAffinity, resp *http.Response) *grabber {
	if !aff.hit.Grab() || resp.StatusCode < 200 || resp.StatusCode > 299 || isEventStream(resp) {
		return nil
	}
	g := &grabber{r: resp.Body}
	resp.Body = readCloser{g, resp.Body}
	return g
}

// recordResources binds the resource ids in a relayed answer to the key
// that created them.
func (s *Server) recordResources(ctx context.Context, sv *store.Service, aff *keyAffinity, g *grabber, keyID int64) {
	if g == nil || g.over {
		return
	}
	ctx = context.WithoutCancel(ctx)
	now := time.Now()
	for _, id := range providers.AffinityIDs(g.buf, aff.hit.CreateID, aff.hit.Related) {
		ref := resourceRef(id)
		if k, _, ok, err := s.Store.KeyAffinity(ctx, sv.ID, ref, now); err == nil && ok && k == keyID {
			continue // already bound (a job polled again)
		}
		if err := s.Store.SetKeyAffinity(ctx, sv.ID, ref, keyID, now.Add(resourceAffinityTTL)); err != nil {
			slog.Warn("key affinity not stored", "service", sv.Slug, "err", err)
		}
	}
}

// writeKeyUnavailable answers a request whose bound key cannot take it. It
// reports false when err is not about a bound key.
func writeKeyUnavailable(w http.ResponseWriter, err error) bool {
	var nk *pool.NoKeyError
	if !errors.As(err, &nk) || !nk.Pinned {
		return false
	}
	body := map[string]any{"error": apiError{Code: "key_unavailable_for_resource",
		Message: "this request must use the key that created its resource (or that its " + affinityHeader +
			" is bound to), and that key cannot take it now: it is cooling down, busy, disabled, deleted or not allowed for this app"}}
	if retry := int((nk.RetryAfter + time.Second - 1) / time.Second); retry > 0 {
		w.Header().Set("Retry-After", itoa(retry))
		body["retry_after_sec"] = retry
	}
	writeJSON(w, 503, body)
	return true
}
