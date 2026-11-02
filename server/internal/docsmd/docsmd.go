// Package docsmd embeds the Markdown docs served at /docs/<slug>.md and read
// by the dashboard's docs pages. The Makefile `llms` target and the Dockerfile
// copy README.md and docs/*.md into files/ (README.md becomes overview.md) and
// the images they show into assets/ before building.
//
// Relative links are rewritten on load so every page works on its own: links
// to another doc point at /docs/<slug>, images at /docs/assets/<name>, and
// anything else in the repository at its GitHub URL.
package docsmd

import (
	"bytes"
	"embed"
	"io/fs"
	"path"
	"regexp"
	"strings"
)

//go:embed files/*.md
var files embed.FS

//go:embed assets/*.png
var assets embed.FS

// RepoURL is where links to repository files that are not served (source
// folders, .env.example, design specs) end up.
const RepoURL = "https://github.com/sidharthpunathil/acnologia"

// Doc is one page.
type Doc struct {
	Slug        string `json:"slug"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Size        int    `json:"size"`
	// Body is the Markdown with links rewritten.
	Body []byte `json:"-"`
}

// page lists the docs in reading order. source is the file's path in the
// repository, used to resolve its relative links.
type page struct {
	slug, file, source, title string
}

var pages = []page{
	{"overview", "overview.md", "README.md", "Overview"},
	{"architecture", "architecture.md", "docs/architecture.md", ""},
	{"flows", "flows.md", "docs/flows.md", ""},
	{"security", "security.md", "docs/security.md", ""},
	{"configuration", "configuration.md", "docs/configuration.md", ""},
	{"api", "api.md", "docs/api.md", ""},
	{"cli", "cli.md", "docs/cli.md", ""},
	{"providers", "providers.md", "docs/providers.md", ""},
	{"development", "development.md", "docs/development.md", ""},
}

var (
	docs   []Doc
	bySlug = map[string]*Doc{}
	// bySource maps a repository path to the URL that now serves it.
	bySource = map[string]string{
		"docs/README.md":     "/docs",
		"docs/llms.txt":      "/llms.txt",
		"docs/llms-full.txt": "/llms-full.txt",
	}
	assetNames = map[string]bool{}
)

func init() {
	for _, p := range pages {
		bySource[p.source] = "/docs/" + p.slug
	}
	entries, _ := fs.ReadDir(assets, "assets")
	for _, e := range entries {
		assetNames[e.Name()] = true
	}
	docs = make([]Doc, 0, len(pages))
	for _, p := range pages {
		raw, err := files.ReadFile("files/" + p.file)
		if err != nil {
			panic("docsmd: missing " + p.file + ": run make llms")
		}
		body := Rewrite(raw, p.source)
		title := p.title
		if title == "" {
			title = firstHeading(raw)
		}
		docs = append(docs, Doc{
			Slug:        p.slug,
			Title:       title,
			Description: firstParagraph(raw),
			Size:        len(body),
			Body:        body,
		})
	}
	for i := range docs {
		bySlug[docs[i].Slug] = &docs[i]
	}
}

// All returns every doc in reading order.
func All() []Doc { return docs }

// Get returns the doc for slug.
func Get(slug string) (Doc, bool) {
	d, ok := bySlug[slug]
	if !ok {
		return Doc{}, false
	}
	return *d, true
}

// Asset returns an embedded image by file name.
func Asset(name string) ([]byte, bool) {
	if !assetNames[name] {
		return nil, false
	}
	b, err := assets.ReadFile("assets/" + name)
	return b, err == nil
}

var (
	// Markdown links and images: [text](target "title") and ![alt](target).
	mdLinkRe = regexp.MustCompile(`(\]\()([^)\s]+)((?:\s+"[^"]*")?\))`)
	// Raw HTML attributes, such as the README's centered logo.
	htmlAttrRe = regexp.MustCompile(`(\s(?:src|href)=")([^"]+)(")`)
)

// Rewrite resolves the relative links in a Markdown file whose repository
// path is source. Code blocks and inline code are left alone.
func Rewrite(md []byte, source string) []byte {
	var out bytes.Buffer
	fence := ""
	lines := strings.SplitAfter(string(md), "\n")
	for _, line := range lines {
		trimmed := strings.TrimLeft(line, " ")
		if f := fenceMarker(trimmed); f != "" {
			if fence == "" {
				fence = f
			} else if strings.HasPrefix(trimmed, fence) {
				fence = ""
			}
			out.WriteString(line)
			continue
		}
		if fence != "" {
			out.WriteString(line)
			continue
		}
		// Odd segments between backticks are inline code.
		parts := strings.Split(line, "`")
		for i := range parts {
			if i%2 == 0 {
				parts[i] = rewriteSegment(parts[i], source)
			}
		}
		out.WriteString(strings.Join(parts, "`"))
	}
	return out.Bytes()
}

func fenceMarker(s string) string {
	for _, f := range []string{"```", "~~~"} {
		if strings.HasPrefix(s, f) {
			return f
		}
	}
	return ""
}

func rewriteSegment(s, source string) string {
	fix := func(re *regexp.Regexp) {
		s = re.ReplaceAllStringFunc(s, func(m string) string {
			g := re.FindStringSubmatch(m)
			return g[1] + resolve(g[2], source) + g[3]
		})
	}
	fix(mdLinkRe)
	fix(htmlAttrRe)
	return s
}

// resolve maps one link target found in source to its served URL.
func resolve(target, source string) string {
	if target == "" || strings.HasPrefix(target, "#") || strings.HasPrefix(target, "/") ||
		strings.Contains(target, "://") || strings.HasPrefix(target, "mailto:") {
		return target
	}
	p, anchor := target, ""
	if i := strings.IndexByte(target, '#'); i >= 0 {
		p, anchor = target[:i], target[i:]
	}
	p = path.Clean(path.Join(path.Dir(source), p))
	if strings.HasPrefix(p, "../") || p == ".." {
		return target
	}
	if u, ok := bySource[p]; ok {
		return u + anchor
	}
	if name := path.Base(p); assetNames[name] && (strings.HasPrefix(p, "docs/screenshots/") || p == "docs/brand/logo.png") {
		return "/docs/assets/" + name
	}
	return RepoURL + "/blob/main/" + p + anchor
}

func firstHeading(md []byte) string {
	for _, line := range strings.Split(string(md), "\n") {
		if strings.HasPrefix(line, "# ") {
			return strings.TrimSpace(line[2:])
		}
	}
	return ""
}

var (
	inlineLinkRe = regexp.MustCompile(`!?\[([^\]]*)\]\([^)]*\)`)
	emphasisRe   = regexp.MustCompile(`\*\*|__|\*|` + "`")
)

// firstParagraph returns the first prose paragraph after the title, as plain
// text. A paragraph that is only an emphasized tagline is skipped.
func firstParagraph(md []byte) string {
	var para []string
	fence := ""
	flush := func() string {
		text := strings.Join(para, " ")
		para = nil
		t := strings.TrimSpace(text)
		if t == "" {
			return ""
		}
		if (strings.HasPrefix(t, "**") && strings.HasSuffix(t, "**")) ||
			(strings.HasPrefix(t, "_") && strings.HasSuffix(t, "_")) {
			return ""
		}
		t = inlineLinkRe.ReplaceAllString(t, "$1")
		t = emphasisRe.ReplaceAllString(t, "")
		return strings.Join(strings.Fields(t), " ")
	}
	for _, line := range strings.Split(string(md), "\n") {
		trimmed := strings.TrimSpace(line)
		if f := fenceMarker(trimmed); f != "" {
			if fence == "" {
				fence = f
			} else {
				fence = ""
			}
			continue
		}
		if fence != "" {
			continue
		}
		prose := trimmed != "" && !strings.HasPrefix(trimmed, "#") && !strings.HasPrefix(trimmed, "<") &&
			!strings.HasPrefix(trimmed, "|") && !strings.HasPrefix(trimmed, "!") &&
			!strings.HasPrefix(trimmed, "- ") && !strings.HasPrefix(trimmed, ">")
		if prose {
			para = append(para, trimmed)
			continue
		}
		if s := flush(); s != "" {
			return s
		}
	}
	return flush()
}
