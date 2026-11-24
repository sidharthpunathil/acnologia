package api

// MCP tool errors and the suggestions error messages carry. The error
// registry and the envelope live in internal/apierr; apiError is its Error,
// so handlers keep writing apiError{Code: ..., Message: ...}.

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/sidharthpunathil/acnologia/server/internal/apierr"
	"github.com/sidharthpunathil/acnologia/server/internal/store"
)

// apiError is the error envelope's inner object (apierr.Error).
type apiError = apierr.Error

// ---- MCP tool errors ----

// toolErr is an MCP tool error with a code from the registry.
type toolErr struct {
	code       string
	msg        string
	retryAfter int
}

func (e *toolErr) Error() string { return e.msg }

func toolErrf(code, format string, args ...any) error {
	return &toolErr{code: code, msg: fmt.Sprintf(format, args...)}
}

// toolErrCode classifies an error from a tool: its own code when it has
// one, else a code read from the message, else tool_error.
func toolErrCode(err error) (code string, retry int) {
	var te *toolErr
	if errors.As(err, &te) {
		return te.code, te.retryAfter
	}
	switch {
	case errors.Is(err, errReadOnlyLease):
		return "read_only_token", 0
	case errors.Is(err, errLeaseNotFound):
		return "lease_not_found", 0
	case errors.Is(err, errLeaseRevoked):
		return "lease_revoked", 0
	case errors.Is(err, errPeerLease), errors.Is(err, errHandoverLease):
		return "lease_not_allowed", 0
	}
	m := strings.ToLower(err.Error())
	switch {
	case strings.Contains(m, "invalid arguments"), strings.HasSuffix(m, " is required"), strings.Contains(m, "does not take"):
		return "invalid_arguments", 0
	case strings.Contains(m, "read-only"):
		return "read_only_token", 0
	case strings.Contains(m, "cannot use service"), strings.Contains(m, "cannot use capability"):
		return "service_not_allowed", 0
	case strings.Contains(m, "no service named"):
		return "unknown_service", 0
	case strings.Contains(m, "unknown tool"), strings.Contains(m, "unknown or disabled tool"):
		return "unknown_tool", 0
	case strings.Contains(m, "reached its") && strings.Contains(m, "limit"):
		return "app_limit_reached", 0
	}
	return "tool_error", 0
}

// toolErrorInfo is what an MCP error result carries next to its text.
func toolErrorInfo(err error) map[string]any {
	code, retry := toolErrCode(err)
	info := map[string]any{"code": code, "message": publicText(err.Error())}
	if h, ok := apierr.Lookup(code); ok {
		info["hint"] = publicText(h.Hint)
		if !h.Fix.Empty() {
			info["fix"] = h.Fix
		}
		info["doc_url"] = h.DocURL(code)
	}
	if retry > 0 {
		info["retry_after_sec"] = retry
	}
	return info
}

// ---- suggestions ----

// closeSlugs are the slugs a few edits from name, or containing it (or
// contained in it), closest first, at most n.
func closeSlugs(name string, slugs []string, n int) []string {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" || len(name) > 128 {
		return nil
	}
	limit := max(2, len(name)/3)
	type cand struct {
		slug string
		d    int
	}
	var cs []cand
	for _, s := range slugs {
		ls := strings.ToLower(s)
		d := levenshtein(name, ls)
		if strings.Contains(ls, name) || strings.Contains(name, ls) {
			d = min(d, 1)
		}
		if d <= limit {
			cs = append(cs, cand{s, d})
		}
	}
	sort.SliceStable(cs, func(i, j int) bool {
		if cs[i].d != cs[j].d {
			return cs[i].d < cs[j].d
		}
		return cs[i].slug < cs[j].slug
	})
	out := []string{}
	for _, c := range cs {
		if len(out) == n {
			break
		}
		out = append(out, c.slug)
	}
	return out
}

// usableSlugs are the slugs of the services a may call, sorted. Only those:
// an error never names a service the app cannot use.
func (s *Server) usableSlugs(ctx context.Context, a *store.App) []string {
	svcs, err := s.Store.ListServices(ctx)
	if err != nil {
		return nil
	}
	out := []string{}
	for _, sv := range svcs {
		if a.CanUse(sv.Slug) && s.sharedAs(ctx, a, sv) {
			out = append(out, sv.Slug)
		}
	}
	sort.Strings(out)
	return out
}

// slugList names up to n slugs, and how many more there are.
func slugList(slugs []string, n int) string {
	if len(slugs) == 0 {
		return "none"
	}
	if len(slugs) <= n {
		return strings.Join(slugs, ", ")
	}
	return strings.Join(slugs[:n], ", ") + " and " + strconv.Itoa(len(slugs)-n) + " more"
}

// unknownServiceMsg says no service has slug, with close slugs the app may
// use.
func (s *Server) unknownServiceMsg(ctx context.Context, a *store.App, slug string) string {
	msg := "no service named " + slug
	if a != nil {
		if near := closeSlugs(slug, s.usableSlugs(ctx, a), 3); len(near) > 0 {
			msg += "; did you mean " + strings.Join(near, ", ") + "?"
		}
	}
	return msg
}

// notAllowedMsg says the app may not use slug, and which services it may.
func (s *Server) notAllowedMsg(ctx context.Context, a *store.App, slug string) string {
	return "this app cannot use service " + slug + "; it may use: " + slugList(s.usableSlugs(ctx, a), 20)
}
