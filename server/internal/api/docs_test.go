package api

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/sidharthpunathil/acnologia/server/internal/docsmd"
)

var docSlugs = []string{"overview", "architecture", "flows", "security", "configuration", "deploy-railway", "api", "cli", "sdks", "providers", "development"}

func getDoc(t *testing.T, url string) (*http.Response, string) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp, string(b)
}

func TestDocsMarkdownServesEverySlug(t *testing.T) {
	e := newEnv(t)
	for _, slug := range docSlugs {
		resp, body := getDoc(t, e.srv.URL+"/docs/"+slug+".md")
		if resp.StatusCode != 200 {
			t.Fatalf("%s: status %d", slug, resp.StatusCode)
		}
		if ct := resp.Header.Get("Content-Type"); ct != "text/markdown; charset=utf-8" {
			t.Fatalf("%s: content type %q", slug, ct)
		}
		if !strings.Contains(body, "\n# ") && !strings.HasPrefix(body, "# ") {
			t.Fatalf("%s: no title heading", slug)
		}
		d, _ := docsmd.Get(slug)
		if body != string(d.Body) {
			t.Fatalf("%s: body differs from the embedded doc", slug)
		}
	}
}

func TestDocsLinksAreRewritten(t *testing.T) {
	e := newEnv(t)
	_, overview := getDoc(t, e.srv.URL+"/docs/overview.md")
	for _, want := range []string{
		"(/docs/security)",
		"(/docs/flows#assistant-chat)",
		"(/docs/assets/catalog.png)",
		`src="/docs/assets/logo.png"`,
		"(/docs/sdks)",
		"(" + docsmd.RepoURL + "/blob/main/.env.example)",
	} {
		if !strings.Contains(overview, want) {
			t.Errorf("overview: missing %s", want)
		}
	}
	if strings.Contains(overview, "](docs/") {
		t.Error("overview: relative docs/ link left")
	}
	_, arch := getDoc(t, e.srv.URL+"/docs/architecture.md")
	for _, want := range []string{"(/docs/configuration)", "(/docs/flows)", "(/docs/providers)"} {
		if !strings.Contains(arch, want) {
			t.Errorf("architecture: missing %s", want)
		}
	}
	_, dev := getDoc(t, e.srv.URL+"/docs/development.md")
	if !strings.Contains(dev, "(/llms.txt)") || !strings.Contains(dev, "(/llms-full.txt)") {
		t.Error("development: llms links not rewritten")
	}
	_, sdks := getDoc(t, e.srv.URL+"/docs/sdks.md")
	for _, want := range []string{"(" + docsmd.RepoURL + "/blob/main/sdk/go/README.md)", "(" + docsmd.RepoURL + "/blob/main/sdk/cpp)"} {
		if !strings.Contains(sdks, want) {
			t.Errorf("sdks: missing %s", want)
		}
	}
	// In page anchors stay as they are.
	_, flows := getDoc(t, e.srv.URL+"/docs/flows.md")
	if !strings.Contains(flows, "(#proxy-request)") {
		t.Error("flows: in page anchor changed")
	}
}

func TestDocsRewriteLeavesCodeAlone(t *testing.T) {
	in := "See [a](flows.md#x).\n\n```md\n[b](flows.md)\n```\n\nInline `[c](flows.md)` and [d](../README.md).\n"
	got := string(docsmd.Rewrite([]byte(in), "docs/security.md"))
	want := "See [a](/docs/flows#x).\n\n```md\n[b](flows.md)\n```\n\nInline `[c](flows.md)` and [d](/docs/overview).\n"
	if got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
}

func TestDocsUnknownSlug(t *testing.T) {
	e := newEnv(t)
	for _, p := range []string{"/docs/nope.md", "/docs/README.md", "/docs/assets/nope.png"} {
		resp, _ := getDoc(t, e.srv.URL+p)
		if resp.StatusCode != 404 {
			t.Errorf("%s: status %d, want 404", p, resp.StatusCode)
		}
	}
	// The reader pages themselves stay with the SPA.
	resp, body := getDoc(t, e.srv.URL+"/docs/flows")
	if resp.StatusCode != 200 || body != "ui" {
		t.Fatalf("/docs/flows: %d %q", resp.StatusCode, body)
	}
}

func TestDocsIndexAndAssets(t *testing.T) {
	e := newEnv(t)
	resp, body := getDoc(t, e.srv.URL+"/api/docs")
	if resp.StatusCode != 200 {
		t.Fatalf("status %d", resp.StatusCode)
	}
	var out struct {
		Docs []docsmd.Doc `json:"docs"`
	}
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Docs) != len(docSlugs) {
		t.Fatalf("got %d docs", len(out.Docs))
	}
	for i, d := range out.Docs {
		if d.Slug != docSlugs[i] || d.Title == "" || d.Description == "" || d.Size == 0 {
			t.Errorf("doc %d: %+v", i, d)
		}
	}
	if out.Docs[0].Title != "Overview" || !strings.HasPrefix(out.Docs[0].Description, "A self-hosted pool") {
		t.Errorf("overview entry: %+v", out.Docs[0])
	}
	resp, _ = getDoc(t, e.srv.URL+"/docs/assets/catalog.png")
	if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "image/png" {
		t.Fatalf("asset: %d %s", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
}
