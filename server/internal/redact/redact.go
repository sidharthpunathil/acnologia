// Package redact keeps API key secrets out of everything that is not the key
// store: prompts and tool results sent to Claude, agent steps, run errors,
// events, the audit log, webhook payloads and log lines.
//
// A Redactor does two passes over a string:
//
//  1. Known secrets. Every pool key (plus per call extras such as a builder
//     run's test keys or a chat thread's vault, passed in the context with
//     WithSecrets) is replaced, raw and URL escaped, by "[key ...c21e]". The
//     pool can hold thousands of keys, so the decrypted set is loaded once
//     into a strings.Replacer (a trie: one pass over the text, whatever the
//     number of keys) and rebuilt only after Invalidate, which the store
//     calls whenever a key is created, replaced or deleted. Secrets shorter
//     than MinKnown are skipped: an exact match on a 5 character value would
//     rewrite ordinary words and numbers.
//  2. Patterns, for secrets nobody told us about (see Find): well known key
//     prefixes (sk-ant-, sk-, AIza, ghp_, xox, tvly-, AKIA, sk_live_, JWTs and
//     Acnologia's own acn_ and acnadm_ tokens, plus the older cog_ and cogadm_
//     ones), Bearer and Basic credentials, auth headers,
//     key=, api_key=, token=, secret= and password= in query strings, env
//     lines and JSON fields, and long high entropy tokens.
//
// The pattern pass trades recall for readability. It leaves alone hex
// strings of any length (sha256, git ids), UUIDs, plain words, camelCase
// identifiers, slugs and paths, because tool results and docs are full of
// them. The cost is that a short key with no prefix and no "key=" in front
// of it (for example a bare 12 character token in prose) is only caught when
// it is a known pool key or, in the Assistant, when keyparse finds it.
package redact

import (
	"context"
	"net/url"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
)

// MinKnown is the shortest known secret replaced by exact match.
const MinKnown = 8

// Loader returns every secret the Redactor should know, decrypted.
type Loader func(ctx context.Context) ([]string, error)

// Redactor replaces secrets in text. The zero value and a nil *Redactor are
// usable: they apply the context extras and the patterns only.
type Redactor struct {
	load  Loader
	mu    sync.Mutex // serializes reloads
	stale atomic.Bool
	set   atomic.Pointer[known]
	// static are secrets from the configuration (the access password), known
	// for the life of the process on top of what load returns.
	static []string
}

type known struct {
	r *strings.Replacer
	n int
}

// New returns a Redactor that loads its known secrets lazily from load.
func New(load Loader) *Redactor {
	r := &Redactor{load: load}
	r.stale.Store(true)
	return r
}

// AlsoKnow adds fixed secrets, such as the access password from the
// environment, to every known set this Redactor loads from now on.
func (r *Redactor) AlsoKnow(secrets ...string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	for _, s := range secrets {
		if strings.TrimSpace(s) != "" {
			r.static = append(r.static, s)
		}
	}
	r.mu.Unlock()
	r.stale.Store(true)
}

// Invalidate marks the known set out of date; the next use reloads it.
func (r *Redactor) Invalidate() {
	if r != nil {
		r.stale.Store(true)
	}
}

// Refresh reloads the known secrets now.
func (r *Redactor) Refresh(ctx context.Context) error {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.stale.Store(false)
	var secrets []string
	if r.load != nil {
		var err error
		secrets, err = r.load(context.WithoutCancel(ctx))
		if err != nil {
			r.stale.Store(true)
			return err
		}
	}
	secrets = append(secrets, r.static...)
	r.set.Store(&known{r: replacer(secrets), n: len(secrets)})
	return nil
}

// Size is how many known secrets are loaded.
func (r *Redactor) Size() int {
	if r == nil {
		return 0
	}
	if k := r.set.Load(); k != nil {
		return k.n
	}
	return 0
}

func (r *Redactor) current(ctx context.Context) *strings.Replacer {
	if r == nil {
		return nil
	}
	if r.stale.Load() {
		_ = r.Refresh(ctx) // on error the previous set (or none) stays in use
	}
	if k := r.set.Load(); k != nil {
		return k.r
	}
	return nil
}

// String removes known and pattern matched secrets from s.
func (r *Redactor) String(ctx context.Context, s string) string {
	return Patterns(r.Known(ctx, s))
}

// Known removes only the exact known secrets (pool keys and context extras).
func (r *Redactor) Known(ctx context.Context, s string) string {
	if s == "" {
		return s
	}
	if rep := r.current(ctx); rep != nil {
		s = rep.Replace(s)
	}
	if ex := extras(ctx); ex != nil {
		s = ex.Replace(s)
	}
	return s
}

// Placeholder is what a known secret becomes: its hint in brackets.
func Placeholder(secret string) string { return "[key " + Hint(secret) + "]" }

// Hint is the last four characters, in the same form as crypto.Hint.
func Hint(secret string) string {
	if len(secret) <= 4 {
		return "..."
	}
	return "..." + secret[len(secret)-4:]
}

// replacer builds one multi pattern replacer for the secrets, longest first
// so a secret that contains another is never split.
func replacer(secrets []string) *strings.Replacer {
	type pair struct{ old, new string }
	seen := map[string]bool{}
	pairs := []pair{}
	for _, s := range secrets {
		s = strings.TrimSpace(s)
		if len(s) < MinKnown || seen[s] {
			continue
		}
		seen[s] = true
		ph := Placeholder(s)
		pairs = append(pairs, pair{s, ph})
		for _, esc := range []string{url.QueryEscape(s), url.PathEscape(s)} {
			if esc != s && !seen[esc] {
				seen[esc] = true
				pairs = append(pairs, pair{esc, ph})
			}
		}
	}
	if len(pairs) == 0 {
		return nil
	}
	sort.SliceStable(pairs, func(i, j int) bool { return len(pairs[i].old) > len(pairs[j].old) })
	args := make([]string, 0, 2*len(pairs))
	for _, p := range pairs {
		args = append(args, p.old, p.new)
	}
	return strings.NewReplacer(args...)
}

type ctxKey struct{}

type extraSet struct {
	secrets []string
	r       *strings.Replacer
}

// WithSecrets returns a context whose redactions also cover secrets, on top
// of any added before. Use it for secrets that are not pool keys yet: a
// builder run's test keys, keys pasted into a chat.
func WithSecrets(ctx context.Context, secrets ...string) context.Context {
	prev, _ := ctx.Value(ctxKey{}).(*extraSet)
	all := []string{}
	if prev != nil {
		all = append(all, prev.secrets...)
	}
	for _, s := range secrets {
		if len(strings.TrimSpace(s)) >= MinKnown {
			all = append(all, strings.TrimSpace(s))
		}
	}
	if prev != nil && len(all) == len(prev.secrets) {
		return ctx
	}
	if len(all) == 0 {
		return ctx
	}
	return context.WithValue(ctx, ctxKey{}, &extraSet{secrets: all, r: replacer(all)})
}

// Secrets returns the extras carried by ctx.
func Secrets(ctx context.Context) []string {
	if ex, _ := ctx.Value(ctxKey{}).(*extraSet); ex != nil {
		return append([]string(nil), ex.secrets...)
	}
	return nil
}

func extras(ctx context.Context) *strings.Replacer {
	if ctx == nil {
		return nil
	}
	if ex, _ := ctx.Value(ctxKey{}).(*extraSet); ex != nil {
		return ex.r
	}
	return nil
}
