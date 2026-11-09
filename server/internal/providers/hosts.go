package providers

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strings"
)

// Host is one more API host of a provider that serves each product on its own
// host and accepts one key on all of them (Omkar Cloud runs every scraper on
// <api>.omkar.cloud, for example). A caller picks a host with a first path
// segment of "~" plus its name:
//
//	/proxy/omkar/~tiktok-scraper/videos/details?video=...
//
// goes to https://tiktok-scraper.omkar.cloud/videos/details. Paths without
// that segment go to base_url as before. Offers, examples and guide
// endpoints keep the "/~name" prefix in their paths, so each product can
// carry its own price, limits and docs.
type Host struct {
	// Name is what follows "~" in the path: lowercase letters, digits and
	// hyphens.
	Name string `json:"name"`
	// URL is the host's base URL, on the same site as base_url.
	URL string `json:"url"`
	// Title is the product name people know, such as "TikTok Scraper".
	Title string `json:"title,omitempty"`
	// Group is the category the product sits in, such as "Social & Video",
	// so a long list can be shown grouped.
	Group       string `json:"group,omitempty"`
	Description string `json:"description,omitempty"`
	// DocsURL is the product's own reference or playground page.
	DocsURL string `json:"docs_url,omitempty"`
}

// MaxHosts caps the hosts of one spec.
const MaxHosts = 400

// HostMarker starts the path segment that picks a host.
const HostMarker = "~"

// ErrUnknownHost is a "/~name" path whose name the spec does not list.
var ErrUnknownHost = errors.New("unknown host")

var hostNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

// A spec with hosts covers many products, so it may document and price more.
func maxGuideEndpoints(s *Spec) int {
	if len(s.Hosts) > 0 {
		return 400
	}
	return 40
}

func maxOffers(s *Spec) int {
	if len(s.Hosts) > 0 {
		return 400
	}
	return 50
}

// HostSite is the domain every host of a spec must sit under: base_url's
// host without its first label, or the host itself when it has two labels
// or fewer. For google-maps-scraper.omkar.cloud that is omkar.cloud, so a
// spec can list tiktok-scraper.omkar.cloud but never another company's
// domain, and a key never leaves the provider's own site.
func HostSite(baseURL string) string {
	u, err := url.Parse(baseURL)
	if err != nil {
		return ""
	}
	h := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	if h == "" || net.ParseIP(h) != nil {
		return h
	}
	labels := strings.Split(h, ".")
	if len(labels) <= 2 {
		return h
	}
	return strings.Join(labels[1:], ".")
}

// onSite reports whether host is site or a subdomain of it.
func onSite(host, site string) bool {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	return site != "" && (host == site || strings.HasSuffix(host, "."+site))
}

func (s *Spec) validateHosts(add func(string, ...any)) {
	if len(s.Hosts) == 0 {
		return
	}
	if len(s.Hosts) > MaxHosts {
		add("hosts: at most %d", MaxHosts)
	}
	site := HostSite(s.BaseURL)
	if site == "" || net.ParseIP(site) != nil {
		add("hosts need base_url to be on a named domain")
		return
	}
	seen := map[string]bool{}
	for i, h := range s.Hosts {
		label := fmt.Sprintf("hosts[%d] %q", i, h.Name)
		if !hostNameRe.MatchString(h.Name) {
			add("%s: name must be lowercase letters, digits and hyphens (max 63)", label)
		}
		if seen[h.Name] {
			add("%s: name is listed twice", label)
		}
		seen[h.Name] = true
		u, err := url.Parse(h.URL)
		switch {
		case err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "":
			add("%s: url must be an http(s) URL", label)
		case u.User != nil || u.RawQuery != "" || u.Fragment != "":
			add("%s: url must not carry credentials, a query or a fragment", label)
		case net.ParseIP(u.Hostname()) != nil:
			add("%s: url must use a domain name, not an IP address", label)
		case !onSite(u.Hostname(), site):
			add("%s: url must be on %s, the site of base_url, so keys stay with this provider", label, site)
		}
		if len(h.Title) > 80 {
			add("%s: title must be at most 80 characters", label)
		}
		if len(h.Group) > 60 {
			add("%s: group must be at most 60 characters", label)
		}
		if len(h.Description) > 400 {
			add("%s: description must be at most 400 characters", label)
		}
		if h.DocsURL != "" {
			if du, err := url.Parse(h.DocsURL); err != nil || (du.Scheme != "http" && du.Scheme != "https") || du.Host == "" {
				add("%s: docs_url must be an http(s) URL", label)
			}
		}
	}
}

// Host returns the host with the given name.
func (s *Spec) Host(name string) (*Host, bool) {
	if s == nil {
		return nil, false
	}
	for i := range s.Hosts {
		if s.Hosts[i].Name == name {
			return &s.Hosts[i], true
		}
	}
	return nil, false
}

// ResolveHost splits a "/~name/rest" path into the named host and the rest
// of the path. path may start with a slash or not; rest keeps the same form.
// ok is false when the path does not pick a host, or the spec lists none: the
// request then goes to base_url unchanged. A "~name" the spec does not list
// is ErrUnknownHost.
func (s *Spec) ResolveHost(path string) (h *Host, rest string, ok bool, err error) {
	if s == nil || len(s.Hosts) == 0 {
		return nil, path, false, nil
	}
	lead := strings.HasPrefix(path, "/")
	p := strings.TrimPrefix(path, "/")
	if !strings.HasPrefix(p, HostMarker) {
		return nil, path, false, nil
	}
	seg, tail, _ := strings.Cut(p, "/")
	name, uerr := url.PathUnescape(strings.TrimPrefix(seg, HostMarker))
	if uerr != nil {
		return nil, path, false, ErrUnknownHost
	}
	h, found := s.Host(name)
	if !found {
		return nil, path, false, fmt.Errorf("%w %q: the provider's hosts are listed in its guide", ErrUnknownHost, HostMarker+name)
	}
	if lead {
		tail = "/" + tail
	}
	return h, tail, true, nil
}

// HostPath is the path prefix that picks host name, such as "/~tiktok-scraper".
func HostPath(name string) string { return "/" + HostMarker + name }

// HostSetChanged reports whether two specs send requests to different hosts,
// which moves where keys go just as an auth change does.
func HostSetChanged(a, b *Spec) bool {
	m := map[string]string{}
	for _, h := range a.Hosts {
		m[h.Name] = h.URL
	}
	if len(m) != len(b.Hosts) {
		return true
	}
	for _, h := range b.Hosts {
		if u, ok := m[h.Name]; !ok || u != h.URL {
			return true
		}
	}
	return false
}
