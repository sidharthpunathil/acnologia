package agent

import (
	"net"
	"net/url"
	"strings"
)

// sharedSuffixes are hosting platforms where every customer gets a name
// under the platform's domain. Two tenants there share the last two labels
// but not an owner, so a host under one of these only vouches for itself.
var sharedSuffixes = []string{
	"vercel.app", "now.sh", "netlify.app", "pages.dev", "workers.dev", "trycloudflare.com",
	"github.io", "gitlab.io", "githubusercontent.com", "herokuapp.com", "onrender.com",
	"railway.app", "fly.dev", "supabase.co", "supabase.in", "firebaseapp.com", "web.app",
	"appspot.com", "run.app", "cloudfunctions.net", "amazonaws.com", "cloudfront.net",
	"azurewebsites.net", "azure-api.net", "azureedge.net", "cloudapp.net", "windows.net",
	"ondigitalocean.app", "readthedocs.io", "gitbook.io", "notion.site", "deno.dev",
	"glitch.me", "repl.co", "replit.app", "replit.dev", "ngrok.io", "ngrok.app",
	"ngrok-free.app", "hf.space", "modal.run", "surge.sh", "webflow.io", "framer.app",
	"framer.website", "blogspot.com", "wixsite.com", "pythonanywhere.com", "bubbleapps.io",
}

// secondLevel are common second level labels under country code TLDs.
var secondLevel = map[string]bool{"co": true, "com": true, "net": true, "org": true, "gov": true, "ac": true, "edu": true, "ne": true, "or": true}

// RegistrableDomain approximates the public suffix rule: the last two labels,
// or the last three under a country second level such as co.uk or com.au.
// A host on a shared hosting platform, or an IP address, is its own domain,
// and a bare platform suffix such as vercel.app returns "" because it vouches
// for nobody.
func RegistrableDomain(host string) string {
	host = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(host), "."))
	if host == "" || net.ParseIP(strings.Trim(host, "[]")) != nil {
		return host
	}
	for _, s := range sharedSuffixes {
		if host == s {
			return ""
		}
		if strings.HasSuffix(host, "."+s) {
			return host
		}
	}
	labels := strings.Split(host, ".")
	if len(labels) <= 2 {
		return host
	}
	n := 2
	if secondLevel[labels[len(labels)-2]] && len(labels[len(labels)-1]) == 2 {
		n = 3
	}
	return strings.Join(labels[len(labels)-n:], ".")
}

// HostWithin reports whether host is domain or a subdomain of it. An IP
// address only matches itself.
func HostWithin(host, domain string) bool {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	domain = strings.ToLower(domain)
	if host == "" || domain == "" {
		return false
	}
	if host == domain {
		return true
	}
	if net.ParseIP(strings.Trim(domain, "[]")) != nil {
		return false
	}
	return strings.HasSuffix(host, "."+domain)
}

// ProviderDomains are the registrable domains of a provider's website, docs
// URL and base URL: the sites that speak for it.
func ProviderDomains(website, docsURL, baseURL string) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, raw := range []string{website, docsURL, baseURL} {
		raw = strings.TrimSpace(raw)
		if raw != "" && !strings.Contains(raw, "://") {
			raw = "https://" + raw
		}
		h := URLHost(raw)
		if h == "github.com" || strings.HasSuffix(h, ".github.com") {
			continue // a repo host is not the provider's site
		}
		if d := RegistrableDomain(h); d != "" && !seen[d] {
			seen[d] = true
			out = append(out, d)
		}
	}
	return out
}

// OnDomains reports whether raw is an http(s) URL on one of domains.
func OnDomains(raw string, domains []string) bool {
	h := URLHost(raw)
	for _, d := range domains {
		if HostWithin(h, d) {
			return true
		}
	}
	return false
}

// URLHost is the lower case host of an absolute http(s) URL with no user
// info, or "" when raw is anything else.
func URLHost(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil {
		return ""
	}
	return strings.ToLower(u.Hostname())
}
