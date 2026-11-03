package api

import (
	"fmt"
	"net/http"

	"github.com/sidharthpunathil/acnologia/server/internal/providers"
	"github.com/sidharthpunathil/acnologia/server/internal/store"
)

// guideTools read and write providers' usage guides from the Assistant.
func guideTools() []*chatTool {
	slugProp := map[string]any{"type": "string", "description": "provider slug from list_providers"}
	return []*chatTool{
		{
			Name: "provider_guide", Role: store.RoleMember, Required: []string{"provider"},
			Props: map[string]any{"provider": slugProp},
			Desc:  "Read only. A provider's usage guide as Markdown: how apps and AI agents call it through Acnologia's proxy, its main endpoints with params, examples, limits and notes. generated is true when the provider has no written guide and this one was made from its examples.",
			Run:   providerGuideTool,
		},
		{
			Name: "write_provider_guide", Role: store.RoleAdmin, Required: []string{"provider"},
			Props: map[string]any{
				"provider":    slugProp,
				"instruction": map[string]any{"type": "string", "description": "optional focus from the user, such as which endpoints matter"},
			},
			Desc: "Write or rewrite a provider's usage guide with AI: it reads the provider's docs and saves the guide as a new provider version. Takes a minute or two and costs a little AI spend. Offer it when provider_guide says generated is true, or after a provider build or service creation for a provider without a guide; run it when the user agrees.",
			Run:  writeProviderGuideTool,
		},
	}
}

// guideReq is the chat's request with the turn's context, for the public
// URL; nil when there is none.
func (cs *chatSession) guideReq() *http.Request {
	if cs.r == nil {
		return nil
	}
	return cs.r.WithContext(cs.ctx)
}

func providerGuideTool(cs *chatSession, in map[string]any) toolOut {
	slug := argStr(in, "provider")
	p, err := cs.s.Store.GetProvider(cs.ctx, slug)
	if err != nil {
		return toolFail("no provider named " + slug)
	}
	spec, err := providers.DecodeSpec(p.Spec)
	if err != nil {
		return toolFail("the provider's spec is unreadable")
	}
	md := providers.RenderGuide(providers.GuideInput{
		Meta: providers.GuideMeta{Slug: p.Slug, Name: p.Name, Description: p.Description, Category: p.Category, DocsURL: p.DocsURL, Website: p.Website},
		Spec: spec, Ctx: providers.GuideContext{PublicURL: cs.s.guidePublicURL(cs.guideReq()), Admin: true},
	})
	ent := chatEntity{Type: "provider", Slug: p.Slug, Name: p.Name}
	return toolOut{OK: true, Summary: "Read the " + p.Name + " guide", Link: "/providers/" + p.Slug,
		Model: asJSON(map[string]any{"provider": p.Slug, "generated": spec.Guide == nil, "markdown": md}), Entities: []chatEntity{ent}}
}

func writeProviderGuideTool(cs *chatSession, in map[string]any) toolOut {
	slug := argStr(in, "provider")
	out, err := cs.s.writeProviderGuide(cs.ctx, cs.guideReq(), cs.user, slug, argStr(in, "instruction"))
	if err != nil {
		return toolFail("could not write the guide: " + err.Error())
	}
	ent := chatEntity{Type: "provider", Slug: out.Provider.Slug, Name: out.Provider.Name}
	model := map[string]any{"provider": out.Provider.Slug, "updated": out.Updated, "summary": out.Summary}
	if !out.Updated {
		model["note"] = "The model could not read the provider's docs, so the guide was not changed."
		return toolOut{OK: true, Summary: "Could not write the " + out.Provider.Name + " guide", Model: asJSON(model), Entities: []chatEntity{ent}}
	}
	n := 0
	if out.Guide != nil {
		n = len(out.Guide.Endpoints)
	}
	model["version"] = out.Provider.Version
	model["endpoints"] = n
	return toolOut{OK: true, Summary: fmt.Sprintf("Wrote the %s guide (%d endpoints, v%d)", out.Provider.Name, n, out.Provider.Version),
		Link: "/providers/" + out.Provider.Slug, Model: asJSON(model), Entities: []chatEntity{ent}}
}
