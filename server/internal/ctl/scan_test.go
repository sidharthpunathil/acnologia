package ctl

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	scanOpenAI    = "sk-proj-AbCdEfGhIjKlMnOpQrStUvWx1234wxyz"
	scanAnthropic = "sk-ant-api03-AAAAbbbbCCCCddddEEEEffff9876"
	scanTavily    = "tvly-dev-QwErTyUiOpAsDfGhJkLz5555"
	scanSerper    = "0123456789abcdef0123456789abcdef01234567"
)

// scanFixture lays out a home and a project with keys in the usual places.
func scanFixture(t *testing.T) (home, project string) {
	t.Helper()
	root := t.TempDir()
	home, project = filepath.Join(root, "home"), filepath.Join(root, "project")
	files := map[string]string{
		filepath.Join(home, ".zshrc"): "# keys\nexport OPENAI_API_KEY=\"" + scanOpenAI + "\"\nexport PATH=/usr/bin\n",
		filepath.Join(home, ".claude.json"): `{
  "mcpServers": {
    "tavily": {
      "command": "npx",
      "args": ["-y", "tavily-mcp", "--api-key", "` + scanTavily + `"],
      "env": {
        "ANTHROPIC_API_KEY": "` + scanAnthropic + `"
      }
    }
  }
}`,
		filepath.Join(home, ".codex", "config.toml"): "[mcp_servers.s.env]\nSERPER_API_KEY = \"" + scanSerper + "\"\n",
		// The same key again, and values that only stand in for one.
		filepath.Join(project, ".env"):         "OPENAI_API_KEY=" + scanOpenAI + "\nGROQ_API_KEY=your-groq-key-goes-here-123\nTAVILY_API_KEY=${TAVILY}\n",
		filepath.Join(project, ".env.example"): "ANTHROPIC_API_KEY=sk-ant-xxxxxxxxxxxxxxxxxxxxxxxxxxxx\n",
		filepath.Join(project, "README.md"):    "OPENAI_API_KEY=" + scanOpenAI + "\n",
	}
	for p, body := range files {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	oldHome, oldCwd := scanHome, scanCwd
	scanHome = func() (string, error) { return home, nil }
	scanCwd = func() (string, error) { return project, nil }
	t.Cleanup(func() { scanHome, scanCwd = oldHome, oldCwd })
	return home, project
}

func TestScanPreviewNeverPrintsKeys(t *testing.T) {
	scanFixture(t)
	c, out, errb, _ := newTestCLI(t, "")
	if code := c.exec([]string{"scan"}); code != 0 {
		t.Fatalf("scan: %d %s", code, errb)
	}
	s := out.String()
	for _, k := range []string{scanOpenAI, scanAnthropic, scanTavily, scanSerper} {
		if strings.Contains(s, k) {
			t.Fatalf("printed a whole key: %s", s)
		}
	}
	for _, want := range []string{"sk-p...wxyz", "openai", "anthropic", "tavily", "serper", "tvly...5555", "OPENAI_API_KEY", "Nothing was changed"} {
		if !strings.Contains(s, want) {
			t.Fatalf("missing %q in:\n%s", want, s)
		}
	}
	if strings.Contains(s, "groq") || strings.Contains(s, "README") {
		t.Fatalf("placeholder or non config file reported:\n%s", s)
	}
	// One row per key, though the OpenAI key sits in two files.
	if n := strings.Count(s, "sk-p...wxyz"); n != 1 {
		t.Fatalf("openai key listed %d times:\n%s", n, s)
	}
}

func TestScanPathsAndJSON(t *testing.T) {
	_, project := scanFixture(t)
	c, out, errb, _ := newTestCLI(t, "")
	if code := c.exec([]string{"scan", "--json", project}); code != 0 {
		t.Fatalf("scan: %d %s", code, errb)
	}
	var got struct {
		Findings []finding `json:"findings"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("json: %v %s", err, out)
	}
	if len(got.Findings) != 1 || got.Findings[0].Provider != "openai" || got.Findings[0].Line != 1 ||
		!strings.HasSuffix(got.Findings[0].File, ".env") || strings.Contains(out.String(), scanOpenAI) {
		t.Fatalf("findings: %s", out)
	}
}

type scanServer struct {
	created []string
	uploads map[string][]map[string]any
}

func (f *scanServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.URL.Path == "/api/admin/providers":
		_, _ = io.WriteString(w, `{"providers":[{"slug":"openai"},{"slug":"anthropic"},{"slug":"tavily"}]}`)
	case r.URL.Path == "/api/admin/services" && r.Method == "GET":
		_, _ = io.WriteString(w, `{"services":[{"id":3,"slug":"openai","provider":"openai"}]}`)
	case r.URL.Path == "/api/admin/services" && r.Method == "POST":
		var in struct{ Provider string }
		_ = json.NewDecoder(r.Body).Decode(&in)
		if in.Provider != "anthropic" && in.Provider != "tavily" {
			w.WriteHeader(400)
			_, _ = io.WriteString(w, `{"error":{"code":"unknown_provider","message":"no provider"}}`)
			return
		}
		f.created = append(f.created, in.Provider)
		id := map[string]string{"anthropic": "8", "tavily": "9"}[in.Provider]
		w.WriteHeader(201)
		_, _ = io.WriteString(w, `{"id":`+id+`,"slug":"`+in.Provider+`","provider":"`+in.Provider+`"}`)
	case r.URL.Path == "/api/admin/keys":
		_, _ = io.WriteString(w, `{"keys":[{"hint":"...wxyz","service_slug":"openai"}]}`)
	case strings.HasSuffix(r.URL.Path, "/keys") && r.Method == "POST":
		var body struct{ Keys []map[string]any }
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.uploads[r.URL.Path] = body.Keys
		_, _ = io.WriteString(w, `{"keys":[{}],"skipped":[]}`)
	default:
		w.WriteHeader(404)
	}
}

func TestScanImport(t *testing.T) {
	scanFixture(t)
	f := &scanServer{uploads: map[string][]map[string]any{}}
	srv := httptest.NewServer(f)
	defer srv.Close()

	// Preview first: the OpenAI key is likely on the server already.
	c, out, errb, _ := newTestCLI(t, "")
	t.Setenv("ACNOLOGIA_URL", srv.URL)
	t.Setenv("ACNOLOGIA_TOKEN", "acnadm_x")
	if code := c.exec([]string{"scan"}); code != 0 || !strings.Contains(out.String(), "likely") {
		t.Fatalf("preview: %d %s %s", code, out, errb)
	}
	if len(f.uploads) != 0 || len(f.created) != 0 {
		t.Fatal("a preview changed the server")
	}

	// Saying no at the confirm uploads nothing.
	c, out, _, _ = newTestCLI(t, "all\nn\n")
	t.Setenv("ACNOLOGIA_URL", srv.URL)
	t.Setenv("ACNOLOGIA_TOKEN", "acnadm_x")
	if code := c.exec([]string{"scan", "--import"}); code != 0 || len(f.uploads) != 0 {
		t.Fatalf("declined: %d %s %v", code, out, f.uploads)
	}

	// Rows 2 and 3 (the Anthropic and Tavily keys in ~/.claude.json),
	// confirmed: the services are made from the catalog.
	c, out, errb, _ = newTestCLI(t, "2,3\ny\n")
	t.Setenv("ACNOLOGIA_URL", srv.URL)
	t.Setenv("ACNOLOGIA_TOKEN", "acnadm_x")
	if code := c.exec([]string{"scan", "--import"}); code != 0 {
		t.Fatalf("import: %d %s %s", code, out, errb)
	}
	if strings.Join(f.created, ",") != "anthropic,tavily" {
		t.Fatalf("created %v\n%s", f.created, out)
	}
	if k := f.uploads["/api/admin/services/8/keys"]; len(k) != 1 || k[0]["secret"] != scanAnthropic || k[0]["label"] != "ANTHROPIC_API_KEY (acn scan)" {
		t.Fatalf("anthropic upload: %v", k)
	}
	if k := f.uploads["/api/admin/services/9/keys"]; len(k) != 1 || k[0]["secret"] != scanTavily {
		t.Fatalf("tavily upload: %v", k)
	}
	if strings.Contains(out.String(), scanAnthropic) || !strings.Contains(out.String(), "No file was changed") {
		t.Fatalf("output: %s", out)
	}

	// --yes imports everything without asking; serper is not in this
	// server's catalog, so it is skipped and the command fails.
	f.uploads = map[string][]map[string]any{}
	c, out, _, _ = newTestCLI(t, "")
	t.Setenv("ACNOLOGIA_URL", srv.URL)
	t.Setenv("ACNOLOGIA_TOKEN", "acnadm_x")
	if code := c.exec([]string{"scan", "--import", "--yes"}); code != 1 || !strings.Contains(out.String(), `no provider "serper"`) {
		t.Fatalf("yes: %d %s", code, out)
	}
	if k := f.uploads["/api/admin/services/3/keys"]; len(k) != 1 || k[0]["secret"] != scanOpenAI {
		t.Fatalf("openai upload: %v", f.uploads)
	}
}

func TestScanTextFormats(t *testing.T) {
	text := strings.Join([]string{
		`set -gx GROQ_API_KEY gsk_` + strings.Repeat("a1", 25),
		`$env:XAI_API_KEY = "xai-` + strings.Repeat("Zz9", 15) + `"`,
		`MY_SECRET=` + strings.Repeat("q", 30),
		`// OPENAI_API_KEY=` + scanOpenAI,
		`EXA_API_KEY: 12345678-aaaa-bbbb-cccc-1234567890ab`,
	}, "\n")
	got := scanText("f", []byte(text), map[string]bool{})
	var provs []string
	for _, f := range got {
		provs = append(provs, f.Provider+":"+f.Var)
	}
	if strings.Join(provs, " ") != "groq:GROQ_API_KEY xai:XAI_API_KEY exa:EXA_API_KEY" {
		t.Fatalf("found %v", provs)
	}
	if maskKey("short") != "****" {
		t.Fatal("short keys are fully masked")
	}
}
