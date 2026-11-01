package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go/v2"

	"github.com/cogito-keys/cogito/internal/store"
)

// Builder runs get a readable name in three steps: an instant fallback from
// the input, then a cheap Haiku call that names the run from its input, then
// the accepted draft's provider name and description, which win.

const (
	namingModel    = "claude-haiku-5-5"
	maxTitle       = 60
	maxDescription = 120
	namingInput    = 3000
)

var (
	tagRe   = regexp.MustCompile(`<[^>]*>`)
	spaceRe = regexp.MustCompile(`\s+`)
	// Dash punctuation is replaced with a comma: the em dash, horizontal
	// bar, figure dash, minus sign, two and three em dashes, small and
	// fullwidth hyphen minus, a spaced en dash and a doubled hyphen.
	dashRe = regexp.MustCompile("\\s*[\u2012\u2014\u2015\u2212\u2e3a\u2e3b\ufe58\ufe63\uff0d]\\s*|\\s+\u2013\\s+|\\s*--+\\s*")
)

// cleanText strips HTML tags and entities, dash punctuation and extra space.
func cleanText(s string) string {
	s = tagRe.ReplaceAllString(s, " ")
	s = html.UnescapeString(s)
	s = tagRe.ReplaceAllString(s, " ") // tags that were escaped
	s = dashRe.ReplaceAllString(s, ", ")
	s = spaceRe.ReplaceAllString(s, " ")
	return strings.Trim(strings.TrimSpace(s), ",;: ")
}

// cutWords cuts s to at most n bytes at a word boundary.
func cutWords(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := store.TruncateUTF8(s, n)
	if i := strings.LastIndex(cut, " "); i > n/2 {
		cut = cut[:i]
	}
	return strings.TrimRight(cut, ",;:. ")
}

func siteName(raw string) string {
	raw = strings.TrimSpace(raw)
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" || !strings.Contains(u.Hostname(), ".") {
		return ""
	}
	return strings.TrimPrefix(u.Hostname(), "www.")
}

// fallbackTitle names a run from its input without calling Claude.
func (a *Agent) fallbackTitle(ctx context.Context, req RunRequest) string {
	if req.Kind == "update_provider" {
		if p, err := a.Store.GetProvider(ctx, req.ProviderSlug); err == nil && p.Name != "" {
			return cutWords(cleanText(p.Name), maxTitle)
		}
		return cutWords(req.ProviderSlug, maxTitle)
	}
	for _, u := range req.URLs {
		if h := siteName(u); h != "" {
			return cutWords(h, maxTitle)
		}
	}
	if g := strings.TrimSpace(req.GitHub); g != "" {
		g = strings.TrimPrefix(strings.TrimPrefix(g, "https://"), "github.com/")
		return cutWords(cleanText(g), maxTitle)
	}
	if t := cleanText(req.Context); t != "" {
		return cutWords(t, maxTitle)
	}
	return "New provider"
}

type runName struct {
	Title       string `json:"title"`
	Description string `json:"description"`
}

// nameRun asks Haiku for a short title and description and saves them while
// the run still has its fallback title. Failures keep the fallback.
func (a *Agent) nameRun(id int64, by Caller, req RunRequest, fallback string) {
	if !a.enabled {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var in strings.Builder
	fmt.Fprintf(&in, "Kind: %s\n", req.Kind)
	if req.ProviderSlug != "" {
		fmt.Fprintf(&in, "Provider slug: %s\n", req.ProviderSlug)
	}
	for _, u := range req.URLs {
		fmt.Fprintf(&in, "URL: %s\n", strings.TrimSpace(u))
	}
	if req.GitHub != "" {
		fmt.Fprintf(&in, "GitHub: %s\n", req.GitHub)
	}
	if c := cleanText(req.Context); c != "" {
		fmt.Fprintf(&in, "Context: %s\n", store.TruncateUTF8(c, namingInput))
	}
	schema := map[string]any{
		"type": "object", "additionalProperties": false, "required": []string{"title", "description"},
		"properties": map[string]any{
			"title":       map[string]any{"type": "string", "description": "2 to 5 words"},
			"description": map[string]any{"type": "string", "description": "one sentence under 120 characters"},
		},
	}
	resp, err := a.client.Beta.Messages.New(ctx, anthropic.BetaMessageNewParams{
		Model:     anthropic.Model(namingModel),
		MaxTokens: 120,
		System: []anthropic.BetaTextBlockParam{{Text: "You name jobs of an AI that adds API providers to Acnologia, an API key pool. " +
			"Create jobs build a provider for an API; update jobs fix an existing one. Return JSON: title, the provider or job in 2 to 5 words " +
			"(for example \"ElevenLabs provider\" or \"Fix SerpAPI quota rules\"), and description, one plain sentence under 120 characters " +
			"on what the job will build. No HTML, no quotes, no dashes as punctuation."}},
		Messages: []anthropic.BetaMessageParam{anthropic.NewBetaUserMessage(anthropic.NewBetaTextBlock(in.String()))},
		OutputConfig: anthropic.BetaOutputConfigParam{
			Format: anthropic.BetaJSONOutputFormatParam{Schema: schema},
		},
	})
	if err != nil {
		return
	}
	a.Record(ctx, store.AIFeatureBuilder, by, runRef(id), resp)
	var n runName
	for _, b := range resp.Content {
		if t, ok := b.AsAny().(anthropic.BetaTextBlock); ok {
			raw := strings.TrimSpace(t.Text)
			raw = strings.TrimSuffix(strings.TrimPrefix(strings.TrimPrefix(raw, "```json"), "```"), "```")
			_ = json.Unmarshal([]byte(strings.TrimSpace(raw)), &n)
		}
	}
	title := cutWords(strings.Trim(cleanText(n.Title), `"'.`), maxTitle)
	if title == "" {
		return
	}
	desc := cutWords(cleanText(n.Description), maxDescription)
	_, _ = a.Store.RenameRun(ctx, id, title, desc, fallback)
}

// nameFromDraft names the run after the provider its accepted draft builds.
func (a *Agent) nameFromDraft(ctx context.Context, s *runState, d *draft) {
	name := cutWords(cleanText(d.Name), maxTitle-len(" provider"))
	if name == "" {
		return
	}
	title := name
	if s.run.Kind == "create_provider" {
		title += " provider"
	}
	p := store.RunPatch{Title: &title}
	if desc := cutWords(cleanText(d.Description), 200); desc != "" {
		p.Description = &desc
	}
	_ = a.Store.UpdateRun(ctx, s.run.ID, p)
}
