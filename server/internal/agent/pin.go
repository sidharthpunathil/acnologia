package agent

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/sidharthpunathil/acnologia/server/internal/providers"
)

// pinKeyHost decides whether the operator's test key may be sent to baseURL.
// The model chooses base_url, and text on a fetched page could steer it to an
// attacker host, so the key only goes to the site the operator pointed at:
// the registrable domain of a URL they gave, or of the provider being updated.
// It returns an empty string when sending is allowed, else a message for the model.
func (a *Agent) pinKeyHost(ctx context.Context, s *runState, baseURL string) string {
	u, err := url.Parse(baseURL)
	if err != nil || u.Hostname() == "" {
		return "base_url must be an absolute http(s) URL before a live check can run"
	}
	host := strings.ToLower(u.Hostname())
	allowed := a.pinnedDomains(ctx, s)
	if len(allowed) == 0 {
		if !s.pinNoted {
			s.pinNoted = true
			a.logf(ctx, s.run.ID, "info", "Test key host not pinned",
				"No docs URL or existing provider to compare with, so the test key goes to "+host+".")
		}
		return ""
	}
	for _, d := range allowed {
		if host == d || strings.HasSuffix(host, "."+d) {
			return ""
		}
	}
	a.logf(ctx, s.run.ID, "error", "Live check blocked", fmt.Sprintf("%s is outside %s", host, strings.Join(allowed, ", ")))
	return fmt.Sprintf("The operator's test key may only be sent to %s, and base_url points at %s. "+
		"If this API really lives on another domain, say so in your summary and submit without a live check; the operator will confirm it.",
		strings.Join(allowed, " or "), host)
}

// pinnedDomains lists the registrable domains the operator vouched for.
func (a *Agent) pinnedDomains(ctx context.Context, s *runState) []string {
	seen := map[string]bool{}
	var out []string
	add := func(raw string) {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			return
		}
		if !strings.Contains(raw, "://") {
			raw = "https://" + raw
		}
		u, err := url.Parse(raw)
		if err != nil || u.Hostname() == "" {
			return
		}
		h := strings.ToLower(u.Hostname())
		if h == "github.com" || strings.HasSuffix(h, ".github.com") {
			return // a repo link says nothing about where the API lives
		}
		if d := registrableDomain(h); !seen[d] {
			seen[d] = true
			out = append(out, d)
		}
	}
	for _, x := range s.run.Input.URLs {
		add(x)
	}
	if s.fixedSlug != "" {
		if p, err := a.Store.GetProvider(ctx, s.fixedSlug); err == nil {
			add(p.Website)
			add(p.DocsURL)
			if spec, err := providers.DecodeSpec(p.Spec); err == nil {
				add(spec.BaseURL)
			}
		}
	}
	return out
}

// secondLevel are common second level labels under country code TLDs.
var secondLevel = map[string]bool{"co": true, "com": true, "net": true, "org": true, "gov": true, "ac": true, "edu": true, "ne": true, "or": true}

// registrableDomain approximates the public suffix rule: the last two labels,
// or the last three under a country second level such as co.uk or com.au.
func registrableDomain(host string) string {
	labels := strings.Split(strings.TrimSuffix(host, "."), ".")
	if len(labels) <= 2 {
		return strings.Join(labels, ".")
	}
	n := 2
	if secondLevel[labels[len(labels)-2]] && len(labels[len(labels)-1]) == 2 {
		n = 3
	}
	return strings.Join(labels[len(labels)-n:], ".")
}
