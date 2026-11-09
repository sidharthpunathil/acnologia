package api

// Catalog tools let the Assistant run a provider's catalog by chat: the many
// APIs a provider such as Omkar Cloud serves on hosts of their own. It can
// read the catalog, add or change entries (with their endpoints, so they
// become MCP tools at once), find products the provider's site lists that
// the catalog lacks, and verify entries: that each host answers, and with
// live=true that a key really works on it.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/sidharthpunathil/acnologia/server/internal/catalog"
	"github.com/sidharthpunathil/acnologia/server/internal/providers"
	"github.com/sidharthpunathil/acnologia/server/internal/store"
)

// Test seams.
var (
	catalogFetch  = fetchCatalogPage
	catalogClient = func() *http.Client { return catalog.SafeClient(10 * time.Second) }
)

const catalogVerifyMeta = "catalog_verify:"

func catalogTools() []*chatTool {
	entry := map[string]any{"type": "object", "properties": map[string]any{
		"name":        map[string]any{"type": "string", "description": "short id used in paths as /~name: lowercase letters, digits, hyphens (tiktok-scraper)"},
		"url":         map[string]any{"type": "string", "description": "the product's API base URL, on the provider's own site"},
		"title":       map[string]any{"type": "string", "description": "the product name people know"},
		"group":       map[string]any{"type": "string", "description": "category, such as Social & Video"},
		"description": map[string]any{"type": "string"},
		"docs_url":    map[string]any{"type": "string"},
		"logo_url":    map[string]any{"type": "string", "description": "logo of the product or the site it reads"},
		"endpoints": map[string]any{"type": "array", "description": "the product's calls, paths relative to its own url (no /~name prefix); each becomes an MCP tool",
			"items": map[string]any{"type": "object", "properties": map[string]any{
				"name": map[string]any{"type": "string"}, "method": map[string]any{"type": "string"}, "path": map[string]any{"type": "string"},
				"description": map[string]any{"type": "string"},
				"params": map[string]any{"type": "array", "items": map[string]any{"type": "object", "properties": map[string]any{
					"name": map[string]any{"type": "string"}, "in": map[string]any{"type": "string", "enum": []string{"query", "path", "body", "header"}},
					"type": map[string]any{"type": "string"}, "required": map[string]any{"type": "boolean"}, "description": map[string]any{"type": "string"}}}},
			}, "required": []string{"name", "path"}}},
	}, "required": []string{"name"}}
	return []*chatTool{
		{
			Name: "get_catalog", Role: store.RoleMember, Required: []string{"slug"},
			Props: map[string]any{
				"slug":  slugProp,
				"group": map[string]any{"type": "string", "description": "only this category"},
				"query": map[string]any{"type": "string", "description": "only entries whose name, title or group contains this"},
			},
			Desc: "Read only. A provider's catalog: the APIs it serves on hosts of their own, by category, with each entry's URL, docs, logo, how many endpoints it documents, and the last verification result.",
			Run:  getCatalogTool,
		},
		{
			Name: "update_catalog", Role: store.RoleAdmin, Required: []string{"slug"},
			Props: map[string]any{
				"slug":   slugProp,
				"upsert": map[string]any{"type": "array", "items": entry, "description": "entries to add, or to change by name (only the fields given change; endpoints given replace that entry's endpoints)"},
				"remove": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "entry names to remove, with their endpoints, examples and offers"},
				"note":   map[string]any{"type": "string"},
			},
			Desc: "Create or change a provider's catalog: add entries, change them, or remove them. A provider with no catalog gets one. Entries must sit on the provider's own site. " +
				"Publishes a new provider version after validation, so the new entries' endpoints become MCP tools at once. Asks the user first, since it changes where keys are sent.",
			Confirm:  true,
			Describe: describeCatalogChange,
			Run:      updateCatalogTool,
		},
		{
			Name: "find_missing_catalog", Role: store.RoleMember, Required: []string{"slug"},
			Props: map[string]any{
				"slug": slugProp,
				"urls": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "pages that list the provider's products, if the user gave any"},
			},
			Desc: "Read only. Compare a provider's catalog with what its own site lists: reads its website, docs page, sitemap and any pages given, and returns products on hosts of the provider's site that the catalog lacks, and catalog entries the site no longer mentions. " +
				"Then add the right ones with update_catalog (or improve_provider to research their endpoints), and verify them.",
			Run: findMissingCatalogTool,
		},
		{
			Name: "verify_catalog", Role: store.RoleAdmin, Required: []string{"slug"},
			Props: map[string]any{
				"slug":  slugProp,
				"names": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "entries to verify; empty for all"},
				"live":  map[string]any{"type": "boolean", "description": "also make one real call per entry with a pooled key, through the Assistant app (uses quota)"},
				"limit": map[string]any{"type": "integer", "description": "most entries to check (default 60, max 400)"},
			},
			Desc: "Verify catalog entries. Without live it checks each host answers over HTTPS. With live it also makes one safe GET per entry through the proxy (an example or an endpoint needing no params) and says whether the key works there: ok, key not accepted, plan or quota, not found, rate limited, provider error. " +
				"Results are kept and shown by get_catalog. Run it when the user asks to verify, after adding entries, or before removing ones that look dead.",
			ConfirmIf: func(in map[string]any) bool { b, _ := in["live"].(bool); return b },
			Describe: func(cs *chatSession, in map[string]any) (string, error) {
				return fmt.Sprintf("Make one real call per catalog entry of %s with a pooled key. This uses the provider's quota.", argStr(in, "slug")), nil
			},
			Run: verifyCatalogTool,
		},
	}
}

// ---- reading ----

type catalogVerify struct {
	Status string `json:"status"`
	Detail string `json:"detail,omitempty"`
	Code   int    `json:"code,omitempty"`
	Live   bool   `json:"live,omitempty"`
	At     string `json:"at"`
}

func loadCatalogVerify(ctx context.Context, st *store.Store, slug string) map[string]catalogVerify {
	out := map[string]catalogVerify{}
	_ = json.Unmarshal([]byte(st.GetMeta(ctx, catalogVerifyMeta+slug)), &out)
	return out
}

func providerSpec(cs *chatSession, slug string) (*store.Provider, *providers.Spec, error) {
	p, err := cs.s.Store.GetProvider(cs.ctx, slug)
	if err != nil {
		return nil, nil, fmt.Errorf("no provider %s; call list_providers", slug)
	}
	var spec providers.Spec
	if err := json.Unmarshal(p.Spec, &spec); err != nil {
		return nil, nil, errors.New("the provider's spec is unreadable")
	}
	return p, &spec, nil
}

func underHost(path, name string) bool {
	pre := providers.HostPath(name)
	return path == pre || strings.HasPrefix(path, pre+"/")
}

func getCatalogTool(cs *chatSession, in map[string]any) toolOut {
	p, spec, err := providerSpec(cs, argStr(in, "slug"))
	if err != nil {
		return toolFail(err.Error())
	}
	ver := loadCatalogVerify(cs.ctx, cs.s.Store, p.Slug)
	group, q := strings.ToLower(argStr(in, "group")), strings.ToLower(argStr(in, "query"))
	groups := map[string]int{}
	rows := []map[string]any{}
	for _, h := range spec.Hosts {
		groups[orDefault(h.Group, "Other")]++
		if group != "" && strings.ToLower(h.Group) != group {
			continue
		}
		if q != "" && !strings.Contains(strings.ToLower(h.Name+" "+h.Title+" "+h.Group), q) {
			continue
		}
		eps := 0
		if spec.Guide != nil {
			for _, ep := range spec.Guide.Endpoints {
				if underHost(ep.Path, h.Name) {
					eps++
				}
			}
		}
		row := map[string]any{"name": h.Name, "title": h.Title, "group": h.Group, "url": h.URL, "endpoints": eps}
		if h.DocsURL != "" {
			row["docs_url"] = h.DocsURL
		}
		if h.LogoURL != "" {
			row["logo"] = true
		}
		if v, ok := ver[h.Name]; ok {
			row["verified"] = v
		}
		rows = append(rows, row)
	}
	shown := rows
	if len(shown) > 200 {
		shown = shown[:200]
	}
	out := map[string]any{"provider": p.Slug, "name": p.Name, "base_url": spec.BaseURL, "site": providers.HostSite(spec.BaseURL),
		"entries": len(spec.Hosts), "matching": len(rows), "groups": groups, "catalog": shown}
	if len(spec.Hosts) == 0 {
		out["note"] = "This provider has no catalog: every call goes to base_url. update_catalog creates one when its products have hosts of their own."
	}
	if len(rows) > len(shown) {
		out["note"] = fmt.Sprintf("showing 200 of %d; narrow with group or query", len(rows))
	}
	return toolOut{OK: true, Model: asJSON(out), Summary: fmt.Sprintf("Read the %s catalog (%d entries)", p.Name, len(spec.Hosts)),
		Link: "/providers/" + p.Slug, Entities: []chatEntity{{Type: "provider", Slug: p.Slug, Name: p.Name}}}
}

// ---- changing ----

type catalogEntryIn struct {
	Name        string                    `json:"name"`
	URL         *string                   `json:"url"`
	Title       *string                   `json:"title"`
	Group       *string                   `json:"group"`
	Description *string                   `json:"description"`
	DocsURL     *string                   `json:"docs_url"`
	LogoURL     *string                   `json:"logo_url"`
	Endpoints   []providers.GuideEndpoint `json:"endpoints"`
}

func catalogInput(in map[string]any) ([]catalogEntryIn, []string, error) {
	var ups []catalogEntryIn
	if v, ok := in["upsert"]; ok && v != nil {
		b, _ := json.Marshal(v)
		if err := json.Unmarshal(b, &ups); err != nil {
			return nil, nil, fmt.Errorf("upsert: %v", err)
		}
	}
	rm := argStrings(in, "remove")
	for i := range ups {
		ups[i].Name = strings.ToLower(strings.TrimSpace(ups[i].Name))
		if ups[i].Name == "" {
			return nil, nil, errors.New("every entry needs a name")
		}
	}
	return ups, rm, nil
}

func describeCatalogChange(cs *chatSession, in map[string]any) (string, error) {
	p, spec, err := providerSpec(cs, argStr(in, "slug"))
	if err != nil {
		return "", err
	}
	ups, rm, err := catalogInput(in)
	if err != nil {
		return "", err
	}
	have := map[string]bool{}
	for _, h := range spec.Hosts {
		have[h.Name] = true
	}
	var add, change []string
	for _, u := range ups {
		if have[u.Name] {
			change = append(change, u.Name)
		} else {
			add = append(add, u.Name)
		}
	}
	parts := []string{}
	list := func(verb string, names []string) {
		if len(names) == 0 {
			return
		}
		shown := names
		if len(shown) > 12 {
			shown = append(append([]string{}, shown[:12]...), fmt.Sprintf("and %d more", len(names)-12))
		}
		parts = append(parts, fmt.Sprintf("%s %d (%s)", verb, len(names), strings.Join(shown, ", ")))
	}
	list("add", add)
	list("change", change)
	list("remove", rm)
	return fmt.Sprintf("Change the %s catalog: %s. Keys of %s will be sent to the hosts listed.", p.Name, strings.Join(parts, "; "), p.Name), nil
}

func updateCatalogTool(cs *chatSession, in map[string]any) toolOut {
	p, spec, err := providerSpec(cs, argStr(in, "slug"))
	if err != nil {
		return toolFail(err.Error())
	}
	ups, rm, err := catalogInput(in)
	if err != nil {
		return toolFail(err.Error())
	}
	if len(ups) == 0 && len(rm) == 0 {
		return toolFail("nothing to change: pass upsert or remove")
	}
	hosts := append([]providers.Host(nil), spec.Hosts...)
	idx := map[string]int{}
	for i, h := range hosts {
		idx[h.Name] = i
	}
	var guide providers.Guide
	if spec.Guide != nil {
		guide = *spec.Guide
	}
	eps := append([]providers.GuideEndpoint(nil), guide.Endpoints...)
	examples := spec.Examples
	offers := spec.Offers
	touchedGuide, touchedEx, touchedOffers := false, false, false
	removed := map[string]bool{}
	for _, n := range rm {
		n = strings.ToLower(strings.TrimSpace(n))
		if _, ok := idx[n]; !ok {
			return toolFail("the catalog has no entry " + n)
		}
		removed[n] = true
	}
	added, changed := 0, 0
	for _, u := range ups {
		if removed[u.Name] {
			return toolFail(u.Name + " is both added and removed")
		}
		i, ok := idx[u.Name]
		if !ok {
			if u.URL == nil || strings.TrimSpace(*u.URL) == "" {
				return toolFail("new entry " + u.Name + " needs a url")
			}
			hosts = append(hosts, providers.Host{Name: u.Name})
			i = len(hosts) - 1
			idx[u.Name] = i
			added++
		} else {
			changed++
		}
		h := &hosts[i]
		set := func(dst *string, v *string) {
			if v != nil {
				*dst = strings.TrimSpace(*v)
			}
		}
		set(&h.URL, u.URL)
		set(&h.Title, u.Title)
		set(&h.Group, u.Group)
		set(&h.Description, u.Description)
		set(&h.DocsURL, u.DocsURL)
		set(&h.LogoURL, u.LogoURL)
		if u.Endpoints != nil {
			keep := eps[:0:0]
			for _, ep := range eps {
				if !underHost(ep.Path, u.Name) {
					keep = append(keep, ep)
				}
			}
			for _, ep := range u.Endpoints {
				ep.Path = providers.HostPath(u.Name) + "/" + strings.TrimPrefix(strings.TrimSpace(ep.Path), "/")
				ep.Path = strings.TrimSuffix(ep.Path, "/")
				ep.Method = strings.ToUpper(orDefault(ep.Method, "GET"))
				if ep.Group == "" {
					ep.Group = orDefault(h.Title, u.Name)
				}
				keep = append(keep, ep)
			}
			eps = keep
			touchedGuide = true
		}
	}
	if len(removed) > 0 {
		kept := hosts[:0]
		for _, h := range hosts {
			if !removed[h.Name] {
				kept = append(kept, h)
			}
		}
		hosts = kept
		gone := func(path string) bool {
			for n := range removed {
				if underHost(path, n) {
					return true
				}
			}
			return false
		}
		keepEps := eps[:0:0]
		for _, ep := range eps {
			if !gone(ep.Path) {
				keepEps = append(keepEps, ep)
			}
		}
		touchedGuide = touchedGuide || len(keepEps) != len(eps)
		eps = keepEps
		var keepEx []providers.Example
		for _, ex := range examples {
			if !gone(ex.Path) {
				keepEx = append(keepEx, ex)
			}
		}
		touchedEx = len(keepEx) != len(examples)
		examples = keepEx
		var keepOff []providers.Offer
		for _, o := range offers {
			if o.Match == nil || o.Match.Path == "" || !gone(o.Match.Path) {
				keepOff = append(keepOff, o)
			}
		}
		touchedOffers = len(keepOff) != len(offers)
		offers = keepOff
	}
	patch := map[string]any{"hosts": toJSONValue(hosts)}
	if touchedGuide {
		guide.Endpoints = eps
		if strings.TrimSpace(guide.Summary) == "" {
			// A guide needs a summary; a provider getting its first one
			// takes its description.
			guide.Summary = orDefault(strings.TrimSpace(p.Description), p.Name+" serves several APIs with one key, each on a host of its own; pick one with a /~name first path segment.")
		}
		patch["guide"] = toJSONValue(guide)
	}
	if touchedEx {
		patch["examples"] = toJSONValue(examples)
	}
	if touchedOffers {
		patch["offers"] = toJSONValue(offers)
	}
	note := argStr(in, "note")
	if note == "" {
		note = fmt.Sprintf("Assistant: catalog %d added, %d changed, %d removed", added, changed, len(removed))
	}
	out := updateProviderTool(cs, map[string]any{"slug": p.Slug, "spec_patch": patch, "note": note})
	if out.OK {
		out.Summary = fmt.Sprintf("Updated the %s catalog: %d added, %d changed, %d removed (%d entries)", p.Name, added, changed, len(removed), len(hosts))
		if added > 0 {
			out.Model = strings.TrimSpace(out.Model + "\nNew entries are not verified yet: offer verify_catalog for them.")
		}
	}
	return out
}

func toJSONValue(v any) any {
	b, _ := json.Marshal(v)
	var out any
	_ = json.Unmarshal(b, &out)
	return out
}

// ---- finding what is missing ----

// Hosts a company runs for itself rather than as products.
var catalogInfraLabels = map[string]bool{"www": true, "docs": true, "doc": true, "app": true, "blog": true, "status": true, "help": true,
	"support": true, "dashboard": true, "cdn": true, "static": true, "assets": true, "mail": true, "auth": true, "login": true, "console": true,
	"accounts": true, "account": true, "community": true, "forum": true, "careers": true, "jobs": true, "shop": true, "store": true, "media": true,
	"images": true, "img": true, "files": true, "dev": true, "staging": true, "beta": true, "portal": true, "my": true, "go": true, "links": true, "news": true}

var (
	catalogLinkRe = regexp.MustCompile(`(?is)<a\b[^>]*href\s*=\s*["']([^"']+)["'][^>]*>(.*?)</a>`)
	catalogURLRe  = regexp.MustCompile(`https?://[a-zA-Z0-9.-]+[^\s"'<>)]*`)
	catalogTagRe  = regexp.MustCompile(`<[^>]+>`)
	catalogLocRe  = regexp.MustCompile(`(?i)<loc>\s*([^<\s]+)\s*</loc>`)
)

func fetchCatalogPage(ctx context.Context, u string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "Acnologia catalog check")
	resp, err := catalogClient().Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("%s answered %d", u, resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 3<<20))
	return string(b), err
}

type catalogCandidate struct {
	Name   string `json:"name"`
	URL    string `json:"url"`
	Title  string `json:"title,omitempty"`
	Source string `json:"source"`
}

func findMissingCatalogTool(cs *chatSession, in map[string]any) toolOut {
	p, spec, err := providerSpec(cs, argStr(in, "slug"))
	if err != nil {
		return toolFail(err.Error())
	}
	site := providers.HostSite(spec.BaseURL)
	if site == "" {
		return toolFail("the provider's base_url has no site")
	}
	pages := []string{}
	add := func(u string) {
		u = strings.TrimSpace(u)
		if u == "" {
			return
		}
		if !strings.Contains(u, "://") {
			u = "https://" + u
		}
		for _, x := range pages {
			if x == u {
				return
			}
		}
		pages = append(pages, u)
	}
	for _, u := range argStrings(in, "urls") {
		add(u)
	}
	add(p.Website)
	add(p.DocsURL)
	add("https://" + site)
	add("https://" + site + "/sitemap.xml")
	if len(pages) > 12 {
		pages = pages[:12]
	}
	known := map[string]bool{}
	for _, h := range spec.Hosts {
		known[h.Name] = true
		if u, err := url.Parse(h.URL); err == nil {
			known[strings.ToLower(u.Hostname())] = true
		}
	}
	if u, err := url.Parse(spec.BaseURL); err == nil {
		known[strings.ToLower(u.Hostname())] = true
	}
	ctx, cancel := context.WithTimeout(cs.ctx, 60*time.Second)
	defer cancel()
	found := map[string]*catalogCandidate{}
	seenHosts := map[string]bool{}
	read, failed := []string{}, []string{}
	var visit func(page string, depth int)
	visit = func(page string, depth int) {
		body, err := catalogFetch(ctx, page)
		if err != nil {
			failed = append(failed, page+": "+err.Error())
			return
		}
		read = append(read, page)
		titles := map[string]string{}
		for _, m := range catalogLinkRe.FindAllStringSubmatch(body, -1) {
			t := strings.Join(strings.Fields(catalogTagRe.ReplaceAllString(m[2], " ")), " ")
			if len(t) > 80 {
				t = t[:80]
			}
			titles[m[1]] = t
		}
		for _, raw := range catalogURLRe.FindAllString(body, -1) {
			u, err := url.Parse(raw)
			if err != nil {
				continue
			}
			host := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
			if host == site || !strings.HasSuffix(host, "."+site) {
				continue
			}
			seenHosts[host] = true
			label := strings.TrimSuffix(host, "."+site)
			if strings.Contains(label, ".") || catalogInfraLabels[label] || known[host] || known[label] {
				continue
			}
			if !regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`).MatchString(label) {
				continue
			}
			if found[label] == nil {
				found[label] = &catalogCandidate{Name: label, URL: "https://" + host, Source: page}
			}
			if t := titles[raw]; t != "" && found[label].Title == "" {
				found[label].Title = t
			}
		}
		// A sitemap index points at more sitemaps: read a few.
		if depth == 0 && strings.Contains(body, "<sitemapindex") {
			n := 0
			for _, m := range catalogLocRe.FindAllStringSubmatch(body, -1) {
				if n == 5 {
					break
				}
				if strings.HasSuffix(strings.ToLower(m[1]), ".xml") {
					visit(m[1], 1)
					n++
				}
			}
		}
	}
	for _, pg := range pages {
		if ctx.Err() != nil {
			break
		}
		visit(pg, 0)
	}
	cands := make([]catalogCandidate, 0, len(found))
	for _, c := range found {
		if c.Title == "" {
			c.Title = humanizeName(c.Name)
		}
		cands = append(cands, *c)
	}
	sort.Slice(cands, func(i, j int) bool { return cands[i].Name < cands[j].Name })
	// Entries the pages never mention may be retired, or only listed
	// elsewhere: verify before removing.
	unseen := []string{}
	if len(read) > 0 {
		for _, h := range spec.Hosts {
			u, err := url.Parse(h.URL)
			if err == nil && !seenHosts[strings.ToLower(u.Hostname())] {
				unseen = append(unseen, h.Name)
			}
		}
	}
	out := map[string]any{"provider": p.Slug, "site": site, "catalog_entries": len(spec.Hosts), "pages_read": read,
		"missing": cands, "not_seen_on_pages": unseen}
	if len(failed) > 0 {
		out["pages_failed"] = failed
	}
	if len(cands) == 0 {
		out["note"] = "No product hosts on " + site + " that the catalog lacks. Products listed only by path, or on pages not read, need improve_provider or the page's URL."
	} else {
		out["note"] = "Candidates come from links on the provider's own site: check the titles, then add the real products with update_catalog (endpoints from their docs, or improve_provider to research them) and verify_catalog them."
	}
	if len(unseen) > 0 && len(unseen) == len(spec.Hosts) {
		delete(out, "not_seen_on_pages") // the pages read list no products at all
	}
	return toolOut{OK: true, Model: asJSON(out), Summary: fmt.Sprintf("Compared the %s catalog with %d pages: %d missing", p.Name, len(read), len(cands)),
		Entities: []chatEntity{{Type: "provider", Slug: p.Slug, Name: p.Name}}}
}

func humanizeName(n string) string {
	w := strings.Fields(strings.ReplaceAll(n, "-", " "))
	for i, x := range w {
		w[i] = strings.ToUpper(x[:1]) + x[1:]
	}
	return strings.Join(w, " ")
}

// ---- verifying ----

func verifyCatalogTool(cs *chatSession, in map[string]any) toolOut {
	p, spec, err := providerSpec(cs, argStr(in, "slug"))
	if err != nil {
		return toolFail(err.Error())
	}
	if len(spec.Hosts) == 0 {
		return toolFail(p.Name + " has no catalog entries to verify")
	}
	limit, _ := argNum(in, "limit")
	if limit <= 0 {
		limit = 60
	}
	if limit > providers.MaxHosts {
		limit = providers.MaxHosts
	}
	want := map[string]bool{}
	for _, n := range argStrings(in, "names") {
		want[strings.ToLower(strings.TrimSpace(n))] = true
	}
	var hosts []providers.Host
	for _, h := range spec.Hosts {
		if len(want) == 0 || want[h.Name] {
			hosts = append(hosts, h)
		}
	}
	if len(hosts) == 0 {
		return toolFail("none of those names are in the catalog")
	}
	skipped := 0
	if len(hosts) > int(limit) {
		skipped = len(hosts) - int(limit)
		hosts = hosts[:limit]
	}
	live, _ := in["live"].(bool)
	var app *store.App
	var svc *store.Service
	if live {
		if app, err = cs.assistantApp(); err != nil {
			return toolFail(err.Error())
		}
		if !app.Active {
			return toolFail("the Assistant app is turned off; turn it on in Apps to make live calls")
		}
		svcs, _ := cs.s.Store.ListServices(cs.ctx)
		_ = cs.s.Store.FillStats(cs.ctx, svcs, false, nil)
		for _, sv := range svcs {
			if sv.Provider == p.Slug && sv.PeerLinkID == nil && (svc == nil || sv.KeyCounts.Total > svc.KeyCounts.Total) {
				svc = sv
			}
		}
		if svc == nil {
			return toolFail("live calls need a service of " + p.Name + " with keys; create one, or verify without live")
		}
	}
	ctx, cancel := context.WithTimeout(cs.ctx, 3*time.Minute)
	defer cancel()
	results := make([]catalogVerify, len(hosts))
	workers := 8
	if live {
		workers = 4
	}
	var wg sync.WaitGroup
	next := make(chan int)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range next {
				results[i] = reachHost(ctx, hosts[i])
				if live && results[i].Status == "reachable" {
					results[i] = cs.liveCheckHost(ctx, app, svc, spec, hosts[i])
				}
				results[i].At = time.Now().UTC().Format(time.RFC3339)
			}
		}()
	}
	for i := range hosts {
		next <- i
	}
	close(next)
	wg.Wait()

	stored := loadCatalogVerify(cs.ctx, cs.s.Store, p.Slug)
	counts := map[string]int{}
	problems := []map[string]any{}
	for i, h := range hosts {
		r := results[i]
		stored[h.Name] = r
		counts[r.Status]++
		if r.Status != "ok" && r.Status != "reachable" && r.Status != "rate_limited" {
			problems = append(problems, map[string]any{"name": h.Name, "status": r.Status, "detail": r.Detail, "code": r.Code})
		}
	}
	keep := map[string]bool{}
	for _, h := range spec.Hosts {
		keep[h.Name] = true
	}
	for n := range stored {
		if !keep[n] {
			delete(stored, n)
		}
	}
	b, _ := json.Marshal(stored)
	_ = cs.s.Store.SetMeta(cs.ctx, catalogVerifyMeta+p.Slug, string(b))
	out := map[string]any{"provider": p.Slug, "checked": len(hosts), "live": live, "counts": counts, "problems": problems}
	if skipped > 0 {
		out["not_checked"] = skipped
		out["note"] = fmt.Sprintf("%d more entries not checked: raise limit or pass names", skipped)
	}
	if svc != nil {
		out["service"] = svc.Slug
	}
	parts := []string{}
	for _, k := range []string{"ok", "reachable", "rate_limited", "key_rejected", "plan_or_quota", "not_found", "no_key", "no_test_call", "provider_error", "unreachable", "error"} {
		if counts[k] > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", counts[k], strings.ReplaceAll(k, "_", " ")))
		}
	}
	return toolOut{OK: true, Model: cs.s.Store.Scrub(cs.ctx, asJSON(out)),
		Summary: fmt.Sprintf("Verified %d %s entries: %s", len(hosts), p.Name, strings.Join(parts, ", ")),
		Link:    "/providers/" + p.Slug, Entities: []chatEntity{{Type: "provider", Slug: p.Slug, Name: p.Name}}}
}

// reachHost checks a host answers HTTPS at all. Any HTTP answer counts: a
// product root often says 404 or 401 without a key.
func reachHost(ctx context.Context, h providers.Host) catalogVerify {
	u, err := url.Parse(h.URL)
	if err != nil || u.Host == "" {
		return catalogVerify{Status: "error", Detail: "bad url"}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.Scheme+"://"+u.Host+"/", nil)
	if err != nil {
		return catalogVerify{Status: "error", Detail: err.Error()}
	}
	req.Header.Set("User-Agent", "Acnologia catalog check")
	c := catalogClient()
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := c.Do(req)
	if err != nil {
		var dns *net.DNSError
		detail := err.Error()
		if errors.As(err, &dns) {
			detail = "the host does not resolve"
		}
		return catalogVerify{Status: "unreachable", Detail: detail}
	}
	resp.Body.Close()
	return catalogVerify{Status: "reachable", Code: resp.StatusCode}
}

// liveCheckHost makes one safe GET on a host through the proxy: an example
// on it, or a guide endpoint that needs no params.
func (cs *chatSession) liveCheckHost(ctx context.Context, app *store.App, svc *store.Service, spec *providers.Spec, h providers.Host) catalogVerify {
	path, query := "", map[string]string{}
	for _, ex := range spec.Examples {
		if underHost(ex.Path, h.Name) && strings.EqualFold(orDefault(ex.Method, "GET"), "GET") && !pathParamRe.MatchString(ex.Path) {
			path = ex.Path
			for k, v := range ex.Query {
				query[k] = v
			}
			break
		}
	}
	if path == "" && spec.Guide != nil {
	eps:
		for _, ep := range spec.Guide.Endpoints {
			if !underHost(ep.Path, h.Name) || !strings.EqualFold(orDefault(ep.Method, "GET"), "GET") || pathParamRe.MatchString(ep.Path) {
				continue
			}
			for _, prm := range ep.Params {
				if prm.Required {
					continue eps
				}
			}
			path = ep.Path
			break
		}
	}
	if path == "" {
		return catalogVerify{Status: "no_test_call", Detail: "no example or parameterless GET endpoint on this entry; add one to test it live"}
	}
	actx := store.WithAppToken(context.WithValue(ctx, appKey, app), app.TokenID)
	rec, err := cs.s.proxyCapture(actx, cs.r, svc.Slug, http.MethodGet, path, query, nil, nil, 4<<10)
	if err != nil {
		return catalogVerify{Status: "error", Detail: err.Error(), Live: true}
	}
	code := rec.Code
	detail := strings.TrimSpace(string(truncate(rec.Body.Bytes(), 200)))
	v := catalogVerify{Code: code, Live: true, Detail: "GET " + path}
	switch {
	case code >= 200 && code < 300:
		v.Status = "ok"
	case code == 429:
		v.Status = "rate_limited"
	case code == 402:
		v.Status, v.Detail = "plan_or_quota", detail
	case code == 401 || code == 403:
		v.Status, v.Detail = "key_rejected", detail
	case code == 404:
		v.Status, v.Detail = "not_found", "GET "+path+": "+detail
	case code == 503 && (strings.Contains(detail, "no_key_available") || strings.Contains(detail, "all_keys_busy")):
		v.Status, v.Detail = "no_key", detail
	case code >= 500:
		v.Status, v.Detail = "provider_error", detail
	default:
		v.Status, v.Detail = "error", detail
	}
	return v
}
