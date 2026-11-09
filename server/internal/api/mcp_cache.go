package api

// The MCP tool catalog is built once and shared by every app and request
// until something it is made of changes: the services, the capabilities, or
// a provider spec. With thousands of providers and hundreds of endpoints
// each, building it per request would cost more than the request, so a
// request only checks a cheap key, an app's view is a list of indexes
// computed once per access list, a tool is found by name in a map, and
// search_tools walks an inverted index instead of every tool.

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/sidharthpunathil/acnologia/server/internal/providers"
	"github.com/sidharthpunathil/acnologia/server/internal/store"
)

// toolCatalogRecheck is how long a built catalog is trusted without looking
// at the services again. Admin writes bump catGen, which skips the wait;
// this only bounds how late a change made elsewhere (another server's
// share, a direct store write) shows up.
var toolCatalogRecheck = 2 * time.Second

type toolCatalog struct {
	key     string
	gen     int64
	checked time.Time

	tools  []mcpTool
	byName map[string]int

	idxOnce sync.Once
	idx     *toolIndex

	mu    sync.Mutex
	views map[string]*toolView
}

// toolView is what one access list sees: the tools' indexes in order, and
// the same as a lookup table.
type toolView struct {
	ids     []int
	allowed []bool
}

func catalogKey(svcs []*store.Service, caps []*store.Capability) string {
	h := sha256.New()
	var b [8]byte
	binary.LittleEndian.PutUint64(b[:], uint64(providers.Version()))
	h.Write(b[:])
	for _, sv := range svcs {
		peer := ""
		if sv.PeerLinkID != nil {
			peer = "p"
		}
		h.Write([]byte(strings.Join([]string{"s", sv.Slug, sv.Name, sv.Provider, sv.PeerProvider, sv.Category, sv.Description, peer}, "\x00")))
		binary.LittleEndian.PutUint64(b[:], uint64(sv.ID))
		h.Write(b[:])
	}
	cb, _ := json.Marshal(caps)
	h.Write(cb)
	return hex.EncodeToString(h.Sum(nil)[:12])
}

// toolCatalog returns the current catalog, rebuilding it when its parts
// changed.
func (s *Server) toolCatalog(ctx context.Context) *toolCatalog {
	gen := s.catGen.Load()
	s.toolCatMu.Lock()
	c := s.toolCat
	s.toolCatMu.Unlock()
	if c != nil && c.gen == gen && time.Since(c.checked) < toolCatalogRecheck {
		return c
	}
	s.toolBuildMu.Lock()
	defer s.toolBuildMu.Unlock()
	s.toolCatMu.Lock()
	c = s.toolCat
	s.toolCatMu.Unlock()
	if c != nil && c.gen == gen && time.Since(c.checked) < toolCatalogRecheck {
		return c // another request rebuilt it meanwhile
	}
	svcs, err := s.Store.ListServices(ctx)
	if err != nil {
		if c != nil {
			return c
		}
		return &toolCatalog{byName: map[string]int{}, views: map[string]*toolView{}}
	}
	caps, _ := s.Store.ListCapabilities(ctx)
	key := catalogKey(svcs, caps)
	if c != nil && c.key == key {
		// Nothing changed: trust it again. A fresh value, so readers of the
		// old pointer never race with this write.
		next := &toolCatalog{key: c.key, gen: gen, checked: time.Now(), tools: c.tools, byName: c.byName, idx: c.idx, views: map[string]*toolView{}}
		if c.idx != nil {
			next.idxOnce.Do(func() {})
		}
		c.mu.Lock()
		for k, v := range c.views {
			next.views[k] = v
		}
		c.mu.Unlock()
		s.toolCatMu.Lock()
		s.toolCat = next
		s.toolCatMu.Unlock()
		return next
	}
	tools := buildServiceTools(svcs, caps)
	byName := make(map[string]int, len(tools))
	for i, t := range tools {
		byName[t.Name] = i
	}
	next := &toolCatalog{key: key, gen: gen, checked: time.Now(), tools: tools, byName: byName, views: map[string]*toolView{}}
	s.toolCatMu.Lock()
	s.toolCat = next
	s.toolCatMu.Unlock()
	return next
}

func accessKey(a *store.App) string {
	k := "a"
	if a.ShareID != nil {
		k = "s"
	}
	if len(a.AllowedServices) == 0 {
		return k + "*"
	}
	list := append([]string(nil), a.AllowedServices...)
	sort.Strings(list)
	return k + strings.Join(list, ",")
}

// view returns the tools an app may use: the services and capabilities its
// access list allows, and no capabilities for a share's token.
func (c *toolCatalog) view(a *store.App) *toolView {
	key := accessKey(a)
	c.mu.Lock()
	defer c.mu.Unlock()
	if v, ok := c.views[key]; ok {
		return v
	}
	v := &toolView{allowed: make([]bool, len(c.tools))}
	for i, t := range c.tools {
		ok := false
		if t.capability != "" {
			ok = a.ShareID == nil && a.CanUse(t.capability)
		} else {
			ok = a.CanUse(t.service)
		}
		if ok {
			v.ids = append(v.ids, i)
			v.allowed[i] = true
		}
	}
	if len(c.views) > 512 {
		c.views = map[string]*toolView{}
	}
	c.views[key] = v
	return v
}

// ---- search index ----

// fieldWeights weigh where a word is found: the service (slug, name,
// provider, category, product group, host), the tool's name and title, its
// description, its parameter names.
var fieldWeights = []float64{4, 3, 1, 1.5}

type posting struct {
	doc   int32
	field uint8
}

type toolIndex struct {
	postings map[string][]posting
	vocab    []string // sorted, for prefix and typo matches
}

func (c *toolCatalog) index() *toolIndex {
	c.idxOnce.Do(func() {
		ix := &toolIndex{postings: map[string][]posting{}}
		for i := range c.tools {
			t := &c.tools[i]
			params := []string{}
			if p, ok := t.InputSchema["properties"].(map[string]any); ok {
				for k := range p {
					if k != "extra_query" && k != "body" {
						params = append(params, k)
					}
				}
			}
			for f, text := range []string{t.keywords, t.Name + " " + t.Title, t.Description, strings.Join(params, " ")} {
				seen := map[string]bool{}
				for _, w := range tokens(text) {
					if !seen[w] {
						seen[w] = true
						ix.postings[w] = append(ix.postings[w], posting{int32(i), uint8(f)})
					}
				}
			}
		}
		ix.vocab = make([]string, 0, len(ix.postings))
		for w := range ix.postings {
			ix.vocab = append(ix.vocab, w)
		}
		sort.Strings(ix.vocab)
		c.idx = ix
	})
	return c.idx
}

// termScores gives each tool's best weight for one query word: an exact word
// in a heavy field counts most, a longer word it starts or one typo less.
func (ix *toolIndex) termScores(q string, allowed []bool) map[int32]float64 {
	out := map[int32]float64{}
	add := func(w string, factor float64) {
		for _, p := range ix.postings[w] {
			if allowed != nil && !allowed[p.doc] {
				continue
			}
			if v := fieldWeights[p.field] * factor; v > out[p.doc] {
				out[p.doc] = v
			}
		}
	}
	add(q, 1)
	if len(q) >= 3 {
		i := sort.SearchStrings(ix.vocab, q)
		for ; i < len(ix.vocab) && strings.HasPrefix(ix.vocab[i], q); i++ {
			if ix.vocab[i] != q {
				add(ix.vocab[i], 0.7)
			}
		}
	}
	if len(q) >= 5 {
		for _, w := range ix.vocab {
			if d := len(w) - len(q); d >= -1 && d <= 1 && w != q && within1(q, w) {
				add(w, 0.6)
			}
		}
	}
	return out
}

type toolHit struct {
	id    int
	score float64
}

// rank orders the tools an app may use by how well they match the query.
// Each word counts by how rare it is (fewer tools match it, more it says),
// a synonym counts half, and a tool matching every word gets a bonus.
func (ix *toolIndex) rank(query string, allowed []bool, n int) []toolHit {
	scores := map[int32]float64{}
	matched := map[int32]int{}
	terms := tokens(query)
	idf := func(m map[int32]float64) float64 {
		return math.Log(1 + float64(n+1)/float64(len(m)+1))
	}
	for _, q := range terms {
		best := map[int32]float64{}
		m := ix.termScores(q, allowed)
		w := idf(m)
		for d, v := range m {
			best[d] = v * w
		}
		for _, syn := range toolSynonyms[q] {
			ms := ix.termScores(stem(syn), allowed)
			ws := idf(ms)
			for d, v := range ms {
				if x := 0.5 * v * ws; x > best[d] {
					best[d] = x
				}
			}
		}
		for d, v := range best {
			scores[d] += v
			matched[d]++
		}
	}
	hits := make([]toolHit, 0, len(scores))
	for d, v := range scores {
		if matched[d] == len(terms) && len(terms) > 1 {
			v *= 1.5
		}
		hits = append(hits, toolHit{int(d), v})
	}
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].score != hits[j].score {
			return hits[i].score > hits[j].score
		}
		return hits[i].id < hits[j].id
	})
	return hits
}
