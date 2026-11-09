package api

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	anthropic "github.com/anthropics/anthropic-sdk-go/v2"
	"github.com/go-chi/chi/v5"

	"github.com/sidharthpunathil/acnologia/server/internal/catalog"
	"github.com/sidharthpunathil/acnologia/server/internal/store"
)

// Logos in the Assistant: find_logo gathers candidate logos for a service or
// a provider and shows them to Claude, which looks at each one. A clear,
// right logo it sets at once; when it is not sure (a tiny or blurry icon, a
// generic one, the wrong brand) it asks with ask_user kind "logo", a form of
// the candidates as pictures. The user can also paste a link or drop an
// image, and set_service_logo or set_provider_logo takes it from there.

const (
	// chatMaxLogos caps the candidates a thread keeps; older ones drop.
	chatMaxLogos = 16
	// chatModelLogos is how many of the latest candidates the model sees as
	// pictures.
	chatModelLogos = 8
	findLogoMax    = 6
	findLogoSrcMax = 4
)

// Network seams, swapped in tests: the real ones refuse loopback addresses.
var (
	logoCandidates = catalog.LogoCandidates
	downloadLogo   = func(ctx context.Context, u string) ([]byte, string, error) { return catalog.DownloadImage(ctx, nil, u) }
)

// chatLogo is one candidate logo find_logo found, kept in the thread so the
// ask form can show it and a set tool can use its bytes.
type chatLogo struct {
	ID        string `json:"id"`
	For       string `json:"for"`
	Source    string `json:"source"`
	MediaType string `json:"media_type"`
	Data      string `json:"data"`
	// Size is the short side in pixels; 0 for a vector.
	Size    int    `json:"size,omitempty"`
	Quality string `json:"quality"`
}

// chatAskLogo is one picture of an ask_user kind logo form.
type chatAskLogo struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

func (cs *chatSession) logoByID(id string) *chatLogo {
	for i := range cs.data.Logos {
		if cs.data.Logos[i].ID == id {
			return &cs.data.Logos[i]
		}
	}
	return nil
}

func (cs *chatSession) addLogo(l chatLogo) chatLogo {
	cs.data.LogoSeq++
	l.ID = "logo" + strconv.Itoa(cs.data.LogoSeq)
	cs.data.Logos = append(cs.data.Logos, l)
	if n := len(cs.data.Logos); n > chatMaxLogos {
		cs.data.Logos = cs.data.Logos[n-chatMaxLogos:]
	}
	return l
}

// logoQuality grades a candidate for the model and the form: vector, clear
// (at least 128px on its short side), fair (64 to 127px) or small.
func logoQuality(mime string, score int) string {
	switch {
	case mime == "image/svg+xml":
		return "vector"
	case score >= 128:
		return "clear"
	case score >= 64:
		return "fair"
	}
	return "small"
}

func logoSize(data []byte, mime string) int {
	if mime == "image/svg+xml" {
		return 0
	}
	if b, m := icoPNG(data, mime); m != "" {
		return catalog.ImageScore(b, m)
	}
	return catalog.ImageScore(data, mime)
}

// modelImage is a candidate as Claude can see it: PNG, JPEG, GIF or WebP,
// or the largest PNG inside an ICO. SVG and BMP icons cannot be shown.
func modelImage(l *chatLogo) (string, string) {
	if _, ok := chatImageTypes[l.MediaType]; ok {
		return l.Data, l.MediaType
	}
	raw, err := base64.StdEncoding.DecodeString(l.Data)
	if err != nil {
		return "", ""
	}
	if b, m := icoPNG(raw, l.MediaType); m != "" {
		return base64.StdEncoding.EncodeToString(b), m
	}
	return "", ""
}

// icoPNG returns the largest PNG image stored in an ICO file, which most
// modern favicons carry.
func icoPNG(data []byte, mime string) ([]byte, string) {
	if mime != "image/x-icon" && mime != "image/vnd.microsoft.icon" || len(data) < 6 {
		return nil, ""
	}
	n := int(binary.LittleEndian.Uint16(data[4:6]))
	var best []byte
	bestW := -1
	for i := 0; i < n && 6+16*i+16 <= len(data); i++ {
		e := data[6+16*i : 6+16*i+16]
		w := int(e[0])
		if w == 0 {
			w = 256
		}
		size := int(binary.LittleEndian.Uint32(e[8:12]))
		off := int(binary.LittleEndian.Uint32(e[12:16]))
		if off < 0 || size <= 8 || off+size > len(data) || !bytes.HasPrefix(data[off:], []byte("\x89PNG")) {
			continue
		}
		if w > bestW {
			best, bestW = data[off:off+size], w
		}
	}
	if best == nil {
		return nil, ""
	}
	return best, "image/png"
}

// recentLogos names the candidates of the latest tool results that carry
// them, up to n, so old find_logo pictures stop costing tokens.
func recentLogos(msgs []chatMsg, n int) map[string]bool {
	keep := map[string]bool{}
	for i := len(msgs) - 1; i >= 0 && len(keep) < n; i-- {
		for _, b := range msgs[i].Blocks {
			for _, id := range b.LogoIDs {
				if len(keep) < n {
					keep[id] = true
				}
			}
		}
	}
	return keep
}

// ---- find_logo ----

func findLogoTool(cs *chatSession, in map[string]any) toolOut {
	sources := strList(in["sources"])
	subject := ""
	switch {
	case argStr(in, "service") != "":
		sv, err := cs.resolveService(argStr(in, "service"))
		if err != nil {
			return toolFail(err.Error())
		}
		subject = sv.Name
		if h := catalog.HostLogoFor(sv); h != "" {
			sources = append(sources, h)
		}
	case argStr(in, "provider") != "":
		slug := argStr(in, "provider")
		code, out, msg := cs.api("GET", "/api/admin/providers/"+url.PathEscape(slug), nil)
		if code >= 400 {
			return toolFail(msg)
		}
		subject = argStr(out, "name")
		if len(sources) == 0 && argStr(out, "website") != "" {
			sources = append(sources, argStr(out, "website"))
		}
	default:
		return toolFail("pass service or provider")
	}
	if len(sources) == 0 {
		return toolFail("pass sources: the website of the site or brand whose logo it should be (linkedin.com for a LinkedIn scraper), or a direct image link")
	}
	if len(sources) > findLogoSrcMax {
		sources = sources[:findLogoSrcMax]
	}
	cs.turn.setStep("Looking for the " + subject + " logo")
	ctx, cancel := context.WithTimeout(cs.ctx, 45*time.Second)
	defer cancel()
	type found struct {
		src  string
		c    catalog.LogoCandidate
		from string
	}
	var all []found
	problems := []string{}
	dup := func(data []byte) bool {
		for _, f := range all {
			if bytes.Equal(f.c.Data, data) {
				return true
			}
		}
		return false
	}
	for _, src := range sources {
		src = strings.TrimSpace(src)
		if catalog.SniffImageURL(src) || looksLikeImageLink(src) {
			if data, mime, err := downloadLogo(ctx, src); err == nil {
				if !dup(data) {
					all = append(all, found{src, catalog.LogoCandidate{URL: src, Data: data, Mime: mime, Score: catalog.ImageScore(data, mime)}, src})
				}
				continue
			} else if catalog.SniffImageURL(src) {
				problems = append(problems, src+": "+err.Error())
				continue
			}
		}
		cands, err := logoCandidates(ctx, src, 3)
		if err != nil {
			problems = append(problems, src+": "+err.Error())
			continue
		}
		for _, c := range cands {
			if !dup(c.Data) {
				all = append(all, found{src, c, c.URL})
			}
		}
	}
	if len(all) == 0 {
		return toolFail("no logo found. " + strings.Join(problems, "; ") + ". Try the brand's main website, or ask the user for a link or an image")
	}
	if len(all) > findLogoMax {
		all = all[:findLogoMax]
	}
	list := []map[string]any{}
	ids := []string{}
	for _, f := range all {
		w := logoSize(f.c.Data, f.c.Mime)
		l := cs.addLogo(chatLogo{For: subject, Source: f.from, MediaType: f.c.Mime,
			Data: base64.StdEncoding.EncodeToString(f.c.Data), Size: w, Quality: logoQuality(f.c.Mime, max(f.c.Score, w))})
		row := map[string]any{"logo": l.ID, "searched": f.src, "source": l.Source, "format": strings.TrimPrefix(l.MediaType, "image/"), "quality": l.Quality}
		if w > 0 {
			row["size"] = fmt.Sprintf("%dpx", w)
		}
		if d, _ := modelImage(&l); d != "" {
			row["shown"] = "as the picture below, in this order"
		} else {
			row["shown"] = "no (vector or BMP icon): judge it by its source"
		}
		list = append(list, row)
		ids = append(ids, l.ID)
	}
	res := map[string]any{"for": subject, "logos": list,
		"next": "Look at each picture. If one is clearly the right brand's logo and its quality is clear or vector, set it with set_service_logo or set_provider_logo and logo=<id>. " +
			"If none is clearly right and sharp (small, blurry, generic, cropped, a wordmark too wide to read small, or another brand), ask the user with ask_user kind logo and these ids: they can pick one, or send a link or an image instead."}
	if len(problems) > 0 {
		res["not_found"] = problems
	}
	return toolOut{OK: true, Model: asJSON(res), Logos: ids,
		Summary: fmt.Sprintf("Found %d %s for %s", len(ids), plural(len(ids), "logo", "logos"), subject)}
}

// looksLikeImageLink is a link with a path, which may be an image served
// without an extension (a CDN or an image service).
func looksLikeImageLink(src string) bool {
	u, err := url.Parse(src)
	return err == nil && (u.Scheme == "https" || u.Scheme == "http") && u.Path != "" && u.Path != "/"
}

// logoBytes resolves a set tool's picture: a find_logo candidate or an
// image the user sent in this chat. ok is false when neither was named.
func (cs *chatSession) logoBytes(in map[string]any) (data string, source string, ok bool, err error) {
	if id := argStr(in, "logo"); id != "" {
		l := cs.logoByID(id)
		if l == nil {
			return "", "", true, fmt.Errorf("no logo %s in this chat; call find_logo again", id)
		}
		return l.Data, l.Source, true, nil
	}
	if id := argStr(in, "image_id"); id != "" {
		for _, m := range cs.data.Messages {
			for _, b := range m.Blocks {
				if b.Type == "image" && b.ImageID == id && b.Data != "" {
					return b.Data, "an image the user sent", true, nil
				}
			}
		}
		return "", "", true, fmt.Errorf("no image %s in this chat", id)
	}
	return "", "", false, nil
}

var logoPickProps = map[string]any{
	"logo":     map[string]any{"type": "string", "description": "a logo id from find_logo"},
	"image_id": map[string]any{"type": "string", "description": "the id of an image the user sent in this chat (shown as [image img1])"},
}

func withLogoPick(p map[string]any) map[string]any {
	for k, v := range logoPickProps {
		p[k] = v
	}
	return p
}

// ---- set_service_logo ----

func setServiceLogoTool(cs *chatSession, in map[string]any) toolOut {
	sv, err := cs.resolveService(argStr(in, "service"))
	if err != nil {
		return toolFail(err.Error())
	}
	ent := []chatEntity{{Type: "service", ID: sv.ID, Slug: sv.Slug, Name: sv.Name}}
	link := fmt.Sprintf("/services/%d", sv.ID)
	data, src, picked, err := cs.logoBytes(in)
	if err != nil {
		return toolFail(err.Error())
	}
	var code int
	var msg string
	if picked {
		code, _, msg = cs.api("PUT", fmt.Sprintf("/api/admin/services/%d/logo", sv.ID), map[string]any{"data_base64": data})
	} else {
		src = argStr(in, "url")
		code, _, msg = cs.api("POST", fmt.Sprintf("/api/admin/services/%d/logo/fetch", sv.ID), map[string]any{"url": src})
	}
	if code >= 400 {
		return toolFail("could not set the logo: " + msg + ". Try find_logo, or ask the user for a direct image link or an image")
	}
	return toolOut{OK: true, Summary: "Set the " + sv.Name + " logo", Link: link, Entities: ent,
		Model: asJSON(map[string]any{"service": sv.Slug, "logo": "updated", "from": src})}
}

// ---- set_provider_logo ----

func setProviderLogoTool(cs *chatSession, in map[string]any) toolOut {
	slug := argStr(in, "slug")
	data, src, picked, err := cs.logoBytes(in)
	if err != nil {
		return toolFail(err.Error())
	}
	var code int
	var out map[string]any
	var msg string
	if picked {
		code, out, msg = cs.api("PUT", "/api/admin/providers/"+url.PathEscape(slug)+"/logo", map[string]any{"data_base64": data})
	} else {
		src = argStr(in, "url")
		code, out, msg = cs.api("POST", "/api/admin/providers/"+url.PathEscape(slug)+"/logo/fetch", map[string]any{"url": src})
	}
	if code >= 400 {
		return toolFail("could not set the logo: " + msg + ". Try find_logo, or ask the user for a direct image link or an image")
	}
	name := argStr(out, "name")
	if name == "" {
		name = slug
	}
	return toolOut{OK: true, Summary: "Set the " + name + " logo", Link: "/providers/" + slug,
		Entities: []chatEntity{{Type: "provider", Slug: slug, Name: name}}, Model: asJSON(map[string]any{"slug": slug, "logo": "updated", "from": src})}
}

func logoTools() []*chatTool {
	return []*chatTool{
		{
			Name: "find_logo", Role: store.RoleAdmin,
			Props: map[string]any{
				"service":  map[string]any{"type": "string", "description": "the service the logo is for"},
				"provider": map[string]any{"type": "string", "description": "or the provider slug the logo is for"},
				"sources": map[string]any{"type": "array", "items": map[string]any{"type": "string"},
					"description": "up to 4 websites or direct image links to look on: the site or brand the logo should show (linkedin.com for a LinkedIn scraper, the provider's own site for a provider)"},
			},
			Desc: "Find candidate logos and see them: each website's declared icons, apple-touch-icon and favicon, or the image at a link, graded vector, clear, fair or small, with the pictures shown to you. " +
				"Then set the right one with set_service_logo or set_provider_logo (logo=<id>), or ask the user with ask_user kind logo when you are not sure.",
			Run: findLogoTool,
		},
		{
			Name: "set_service_logo", Role: store.RoleAdmin, Required: []string{"service"},
			Props: withLogoPick(map[string]any{
				"service": map[string]any{"type": "string"},
				"url":     map[string]any{"type": "string", "description": "a direct image link or a website to take the icon from; with no url, logo or image_id, the logo of the provider host the service calls"},
			}),
			Desc: "Set a service's own logo from a find_logo candidate (logo), an image the user sent (image_id), or a link (url) it downloads. For a scraper or an API that reads another site, that site's logo (LinkedIn for a LinkedIn Jobs scraper); the provider's logo shows small beside it.",
			Run:  setServiceLogoTool,
		},
		{
			Name: "set_provider_logo", Role: store.RoleAdmin, Required: []string{"slug"},
			Props: withLogoPick(map[string]any{
				"slug": slugProp,
				"url":  map[string]any{"type": "string", "description": "a direct image link, or a website to take the icon from; with no url, logo or image_id, the provider's website"},
			}),
			Desc: "Set a provider's logo from a find_logo candidate (logo), an image the user sent (image_id), or a link (url) it downloads.",
			Run:  setProviderLogoTool,
		},
	}
}

// chatLogoGet serves one candidate logo of the user's own thread, for the
// ask form. SVGs are served sandboxed, like every stored logo.
func (s *Server) chatLogoGet(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(w, r)
	if !ok {
		return
	}
	_, d, err := s.loadChat(r.Context(), id, userFrom(r).ID)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	want := chi.URLParam(r, "logo")
	for _, l := range d.Logos {
		if l.ID != want {
			continue
		}
		raw, err := base64.StdEncoding.DecodeString(l.Data)
		if err != nil {
			break
		}
		w.Header().Set("Content-Type", l.MediaType)
		w.Header().Set("Cache-Control", "private, max-age=86400")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; sandbox")
		_, _ = w.Write(raw)
		return
	}
	writeErr(w, 404, "not_found", "no such logo in this chat")
}

// logoLabel is the caption of a candidate in the ask form: its site, size
// and grade.
func logoLabel(l *chatLogo) string {
	host := l.Source
	if u, err := url.Parse(l.Source); err == nil && u.Host != "" {
		host = strings.TrimPrefix(u.Hostname(), "www.")
	}
	size := "vector"
	if l.Size > 0 {
		size = fmt.Sprintf("%dpx", l.Size)
	}
	return host + " · " + size
}

// askLogos is the form pictures for candidate ids still in the thread.
func (cs *chatSession) askLogos(ids []string) []chatAskLogo {
	out := []chatAskLogo{}
	for _, id := range ids {
		if l := cs.logoByID(id); l != nil {
			out = append(out, chatAskLogo{ID: l.ID, Label: logoLabel(l)})
		}
	}
	return out
}

// toolResultParam rebuilds a tool result, with the find_logo candidates
// it names as pictures while they are among the latest.
func toolResultParam(b chatBlock, keep map[string]bool, logos map[string]*chatLogo) anthropic.BetaContentBlockParamUnion {
	if len(b.LogoIDs) == 0 {
		return anthropic.NewBetaToolResultBlock(b.ToolUseID, b.Content, b.IsError)
	}
	tb := anthropic.BetaToolResultBlockParam{ToolUseID: b.ToolUseID, IsError: anthropic.Bool(b.IsError)}
	tb.Content = []anthropic.BetaToolResultBlockParamContentUnion{{OfText: &anthropic.BetaTextBlockParam{Text: b.Content}}}
	for _, id := range b.LogoIDs {
		l := logos[id]
		if l == nil || !keep[id] {
			tb.Content = append(tb.Content, anthropic.BetaToolResultBlockParamContentUnion{
				OfText: &anthropic.BetaTextBlockParam{Text: "[" + id + ": no longer shown]"}})
			continue
		}
		data, mt := modelImage(l)
		if data == "" {
			continue
		}
		tb.Content = append(tb.Content,
			anthropic.BetaToolResultBlockParamContentUnion{OfText: &anthropic.BetaTextBlockParam{Text: "[" + id + "]"}},
			anthropic.BetaToolResultBlockParamContentUnion{OfImage: &anthropic.BetaImageBlockParam{
				Source: anthropic.BetaImageBlockParamSourceUnion{OfBase64: &anthropic.BetaBase64ImageSourceParam{
					Data: data, MediaType: anthropic.BetaBase64ImageSourceMediaType(mt)}}}})
	}
	return anthropic.BetaContentBlockParamUnion{OfToolResult: &tb}
}
