// Package boundary holds only a test: the import rules that keep the key
// handling packages small enough to review on their own.
package boundary

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"
)

const module = "github.com/sidharthpunathil/acnologia/server"

// leaves are the packages on the key handling hot path, and the leaf
// packages taken out of internal/api. Neither they nor anything they import
// may reach a forbidden package.
var leaves = []string{
	// Taken out of internal/api.
	"internal/apierr",
	"internal/clientip",
	"internal/keyscrub",
	"internal/tokenmeter",
	// Low level.
	"internal/breaker",
	"internal/catalog",
	"internal/crypto",
	"internal/egress",
	"internal/pool",
	"internal/providers",
	"internal/redact",
	"internal/respcache",
	"internal/store",
}

// forbidden: the HTTP handlers, the AI features (the Assistant, the
// provider builder) and test helpers.
var forbidden = map[string]bool{
	module + "/internal/api":   true,
	module + "/internal/agent": true,
	"net/http/httptest":        true,
}

// imports reads the imports of a package's non-test files, every build
// tag included, so a file for another OS cannot slip one in.
func imports(t *testing.T, root, pkg string) []string {
	t.Helper()
	dir := filepath.Join(root, filepath.FromSlash(pkg))
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("%s: %v", pkg, err)
	}
	seen := map[string]bool{}
	fset := token.NewFileSet()
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, filepath.Join(dir, name), nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("%s/%s: %v", pkg, name, err)
		}
		for _, im := range f.Imports {
			p, err := strconv.Unquote(im.Path.Value)
			if err != nil {
				t.Fatalf("%s/%s: %v", pkg, name, err)
			}
			seen[p] = true
		}
	}
	out := make([]string, 0, len(seen))
	for p := range seen {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// violations walks start's imports inside the module, transitively, and
// returns every path from start to a forbidden package.
func violations(t *testing.T, start string) []string {
	t.Helper()
	// via maps a module package to the package that first imported it, so
	// a violation names the whole chain.
	via := map[string]string{start: ""}
	queue := []string{start}
	var out []string
	for len(queue) > 0 {
		pkg := queue[0]
		queue = queue[1:]
		for _, im := range imports(t, moduleRoot, pkg) {
			if forbidden[im] {
				chain := []string{strings.TrimPrefix(im, module+"/")}
				for p := pkg; p != ""; p = via[p] {
					chain = append(chain, p)
				}
				slices.Reverse(chain)
				out = append(out, strings.Join(chain, " -> "))
				continue
			}
			rel, ok := strings.CutPrefix(im, module+"/")
			if !ok {
				continue
			}
			if _, done := via[rel]; !done {
				via[rel] = pkg
				queue = append(queue, rel)
			}
		}
	}
	return out
}

// moduleRoot is server/, relative to this directory.
var moduleRoot = filepath.Join("..", "..")

// TestLeafPackagesStayLeaves: no leaf reaches internal/api, internal/agent
// or net/http/httptest outside its tests, directly or through another
// package.
func TestLeafPackagesStayLeaves(t *testing.T) {
	for _, leaf := range leaves {
		for _, v := range violations(t, leaf) {
			t.Errorf("%s must not reach a handler, AI or test package: %s", leaf, v)
		}
	}
}

// TestBoundarySeesImports runs the walk on a package that does reach the
// handlers, so a broken walk cannot pass the test above by finding nothing.
func TestBoundarySeesImports(t *testing.T) {
	got := violations(t, "cmd/acnologia")
	want := "cmd/acnologia -> internal/api"
	if !slices.Contains(got, want) {
		t.Fatalf("the walk missed %q: %q", want, got)
	}
}
