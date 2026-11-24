package api

// Idempotency-Key on admin writes. A POST, PUT, PATCH or DELETE under
// /api/admin that carries an Idempotency-Key (a dashboard session or an
// admin API token, and the control MCP's idempotency_key argument) is run
// once: its answer is kept for 24 hours and a retry with the same key gets
// that answer back, marked Idempotent-Replayed: true, instead of a second
// write. The semantics follow the proxy's: a 2xx or 4xx answer is kept,
// except 408, 425 and 429 (those may succeed on a retry), and a 5xx is not;
// the key is scoped to the user and the token (or the user's sessions); the
// same key with a different method, path or body is refused with 422
// idempotency_key_reused; and while the first request runs, a retry gets
// 409 idempotency_in_progress with Retry-After. Answers live in their own
// table (store/admin_idem.go), encrypted with the master key, since some
// hold a secret shown once (a new app or API token). A key reveal and a
// backup are never kept: every reveal is audited, and a backup is a file.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/sidharthpunathil/acnologia/server/internal/store"
)

const (
	// adminIdemMaxBody is the largest answer kept; a bigger one runs again
	// on a retry.
	adminIdemMaxBody = 1 << 20
	// adminIdemMaxRequest bounds the request body read for the fingerprint.
	adminIdemMaxRequest = 8 << 20
	// adminIdemStale is when a running claim counts as lost (the server
	// stopped mid request) and the key may run again.
	adminIdemStale = 10 * time.Minute
	// adminIdemMaxKey bounds the key's length.
	adminIdemMaxKey = 255
)

// adminIdemCtxKey carries an idempotency key set in process (the control
// MCP's idempotency_key argument). Nothing outside this package sets it.
type adminIdemCtxKey struct{}

func withAdminIdemKey(ctx context.Context, key string) context.Context {
	return context.WithValue(ctx, adminIdemCtxKey{}, key)
}

func isWriteMethod(m string) bool {
	switch m {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	}
	return false
}

// adminIdemKey is the request's key: the header, else the in process one.
func adminIdemKey(r *http.Request) string {
	if k := strings.TrimSpace(r.Header.Get("Idempotency-Key")); k != "" {
		return k
	}
	k, _ := r.Context().Value(adminIdemCtxKey{}).(string)
	return strings.TrimSpace(k)
}

// adminIdemNever lists the writes whose answers are never kept.
func adminIdemNever(r *http.Request) bool {
	p := r.URL.Path
	return p == "/api/admin/backup" || (strings.HasPrefix(p, "/api/admin/keys/") && strings.HasSuffix(p, "/reveal"))
}

// adminIdemScope is who a key belongs to: the user, and the API token or
// the user's dashboard sessions.
func adminIdemScope(r *http.Request) string {
	u := userFrom(r)
	if u == nil {
		return ""
	}
	if t, ok := r.Context().Value(tokenKey).(*store.AdminToken); ok {
		return fmt.Sprintf("u%d|t%d", u.ID, t.ID)
	}
	return fmt.Sprintf("u%d|session", u.ID)
}

// adminIdemFingerprint hashes what makes a request the same request: its
// method, path, query (but dry_run) and body. A JSON body is compacted
// first, so whitespace alone does not make it different.
func adminIdemFingerprint(r *http.Request, body []byte) string {
	q := r.URL.Query()
	q.Del("dry_run")
	h := sha256.New()
	fmt.Fprintf(h, "%s %s?%s\n", r.Method, r.URL.Path, q.Encode())
	var buf bytes.Buffer
	if json.Compact(&buf, body) == nil {
		body = buf.Bytes()
	}
	h.Write(body)
	return hex.EncodeToString(h.Sum(nil))
}

// adminIdemKeep reports whether an answer with this status is kept.
func adminIdemKeep(status int) bool {
	switch status {
	case http.StatusRequestTimeout, http.StatusTooEarly, http.StatusTooManyRequests:
		return false
	}
	return status >= 200 && status < 500
}

func (s *Server) adminIdempotency(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !isWriteMethod(r.Method) || isDryRun(r) || adminIdemNever(r) {
			next.ServeHTTP(w, r)
			return
		}
		key := adminIdemKey(r)
		scope := adminIdemScope(r)
		if key == "" || scope == "" {
			next.ServeHTTP(w, r)
			return
		}
		if len(key) > adminIdemMaxKey {
			writeErr(w, 400, "invalid", fmt.Sprintf("Idempotency-Key is at most %d characters", adminIdemMaxKey))
			return
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, adminIdemMaxRequest+1))
		if err != nil {
			writeErr(w, 400, "bad_json", "could not read the request body: "+err.Error())
			return
		}
		if len(body) > adminIdemMaxRequest {
			writeErr(w, 413, "too_large", fmt.Sprintf("a request with an Idempotency-Key may carry at most %d MB", adminIdemMaxRequest>>20))
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		fp := adminIdemFingerprint(r, body)
		ctx := context.WithoutCancel(r.Context())
		ent, claimed, err := s.Store.ClaimAdminIdem(ctx, scope, key, fp, adminIdemStale)
		if err != nil {
			slog.Error("admin idempotency claim failed", "err", err)
			writeErr(w, 500, "internal", "internal error")
			return
		}
		if !claimed {
			switch {
			case ent.Fingerprint != fp:
				writeErr(w, 422, "idempotency_key_reused", "this Idempotency-Key was used with a different request; use a new key for a different request")
			case ent.State != store.AdminIdemDone:
				w.Header().Set("Retry-After", "1")
				writeErr(w, 409, "idempotency_in_progress", "a request with this Idempotency-Key is still running, retry shortly")
			default:
				s.replayAdminIdem(w, ent)
			}
			return
		}
		rec := &idemRecorder{ResponseWriter: w, status: http.StatusOK}
		kept := false
		defer func() {
			if !kept {
				_ = s.Store.ReleaseAdminIdem(ctx, scope, key)
			}
		}()
		next.ServeHTTP(rec, r)
		if !adminIdemKeep(rec.status) || rec.over || !strings.HasPrefix(rec.Header().Get("Content-Type"), "application/json") {
			return
		}
		enc, err := s.Pool.Box.Encrypt(rec.buf.String())
		if err != nil {
			return
		}
		if err := s.Store.FinishAdminIdem(ctx, scope, key, rec.status, rec.Header().Get("Content-Type"), enc); err == nil {
			kept = true
		}
	})
}

// replayAdminIdem writes a kept answer again.
func (s *Server) replayAdminIdem(w http.ResponseWriter, ent *store.AdminIdem) {
	body := ""
	if len(ent.BodyEnc) > 0 {
		b, err := s.Pool.Box.Decrypt(ent.BodyEnc)
		if err != nil {
			writeErr(w, 500, "decrypt_failed", "cannot decrypt the kept answer, check ACNOLOGIA_MASTER_KEY")
			return
		}
		body = b
	}
	if ent.ContentType != "" {
		w.Header().Set("Content-Type", ent.ContentType)
	}
	w.Header().Set("Idempotent-Replayed", "true")
	w.WriteHeader(ent.Status)
	_, _ = io.WriteString(w, body)
}

// idemRecorder passes a response through and keeps a copy of it, up to
// adminIdemMaxBody.
type idemRecorder struct {
	http.ResponseWriter
	status int
	wrote  bool
	buf    bytes.Buffer
	over   bool
}

func (r *idemRecorder) WriteHeader(code int) {
	if !r.wrote {
		r.status, r.wrote = code, true
	}
	r.ResponseWriter.WriteHeader(code)
}

func (r *idemRecorder) Write(b []byte) (int, error) {
	r.wrote = true
	if !r.over {
		if r.buf.Len()+len(b) > adminIdemMaxBody {
			r.over = true
			r.buf.Reset()
		} else {
			r.buf.Write(b)
		}
	}
	return r.ResponseWriter.Write(b)
}

func (r *idemRecorder) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (r *idemRecorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }
