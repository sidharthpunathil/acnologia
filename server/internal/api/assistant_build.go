package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/cogito-keys/cogito/internal/store"
)

// chatBuild is a provider build the assistant follows, shown as a card.
type chatBuild struct {
	RunID        int64      `json:"run_id"`
	Title        string     `json:"title"`
	Status       string     `json:"status"`
	Phase        int        `json:"phase"` // 0 research, 1 spec, 2 tests, 3 live check, 4 publish
	Step         string     `json:"step"`
	StartedAt    time.Time  `json:"started_at"`
	FinishedAt   *time.Time `json:"finished_at,omitempty"`
	ProviderSlug string     `json:"provider_slug,omitempty"`
	ServiceID    *int64     `json:"service_id,omitempty"`
	Error        string     `json:"error,omitempty"`
}

// chatAsk is a question the model asked with ask_user.
type chatAsk struct {
	ID       string       `json:"id"`
	Kind     string       `json:"kind"` // names, choice
	Question string       `json:"question"`
	Rows     []chatAskRow `json:"rows,omitempty"`
	Choices  []string     `json:"choices,omitempty"`
	Status   string       `json:"status"` // waiting, answered, expired
	Answer   string       `json:"answer,omitempty"`
}

type chatAskRow struct {
	Ref        string `json:"ref"`
	Hint       string `json:"hint"`
	Suggestion string `json:"suggestion"`
}

var buildTerminal = map[string]bool{"published": true, "needs_review": true, "failed": true, "cancelled": true}

func strList(v any) []string {
	out := []string{}
	arr, _ := v.([]any)
	for _, x := range arr {
		if s, ok := x.(string); ok && strings.TrimSpace(s) != "" {
			out = append(out, strings.TrimSpace(s))
		}
	}
	return out
}

// ---- add_keys ----

func addKeysTool(cs *chatSession, in map[string]any) toolOut {
	sv, err := cs.resolveService(argStr(in, "service"))
	if err != nil {
		return toolFail(err.Error())
	}
	inputs := []map[string]any{}
	refOf := []string{}
	used := map[string]bool{}
	missing := []string{}
	for _, k := range func() []any { ks, _ := in["keys"].([]any); return ks }() {
		m, ok := k.(map[string]any)
		if !ok {
			continue
		}
		body := pick(m, "secret", "label", "account_name", "account_email", "account_phone", "account_ref", "tier", "notes")
		ref := argStr(m, "ref")
		if ref != "" {
			e := cs.vaultEntry(ref)
			if e == nil {
				missing = append(missing, ref)
				continue
			}
			sec, err := cs.vaultSecret(*e)
			if err != nil {
				return toolFail(err.Error())
			}
			body["secret"] = sec
			if argStr(body, "label") == "" && e.Label != "" {
				body["label"] = e.Label
			}
			used[ref] = true
		}
		if argStr(body, "secret") == "" {
			continue
		}
		inputs = append(inputs, body)
		refOf = append(refOf, ref)
	}
	refs, batch := strList(in["key_refs"]), argStr(in, "batch")
	if len(refs) > 0 || batch != "" {
		entries, miss := cs.vaultPick(refs, batch)
		missing = append(missing, miss...)
		for _, e := range entries {
			if used[e.Ref] {
				continue
			}
			sec, err := cs.vaultSecret(e)
			if err != nil {
				return toolFail(err.Error())
			}
			body := map[string]any{"secret": sec}
			if e.Label != "" {
				body["label"] = e.Label
			}
			inputs = append(inputs, body)
			refOf = append(refOf, e.Ref)
			used[e.Ref] = true
		}
	}
	if len(inputs) == 0 {
		if len(missing) > 0 {
			return toolFail("no pasted keys with refs " + strings.Join(missing, ", ") + "; they were already added or never pasted")
		}
		return toolFail(`no keys given: pass batch "latest", key_refs, or keys with a ref each`)
	}
	cs.turn.setStep(fmt.Sprintf("Adding %d %s to %s", len(inputs), plural(len(inputs), "key", "keys"), sv.Name))
	code, out, msg := cs.api("POST", fmt.Sprintf("/api/admin/services/%d/keys", sv.ID), map[string]any{"keys": inputs})
	if code >= 400 {
		return toolFail(msg)
	}
	// Stored or already there: either way the vault no longer needs them.
	drop := map[string]bool{}
	for _, r := range refOf {
		if r != "" {
			drop[r] = true
		}
	}
	cs.vaultDrop(drop)
	created := rows(out["keys"], "id", "label", "hint", "effective_status", "account_name", "account_email", "account_phone", "account_ref")
	skipped, _ := out["skipped"].([]any)
	sum := fmt.Sprintf("Added %d key(s) to %s", len(created), sv.Slug)
	if len(skipped) > 0 {
		sum += fmt.Sprintf(", skipped %d", len(skipped))
	}
	svcEnt := chatEntity{Type: "service", ID: sv.ID, Slug: sv.Slug, Name: sv.Name}
	card := &chatCard{Type: "keys", Service: &svcEnt, KeyTotal: len(created), Skipped: len(skipped)}
	ents := []chatEntity{svcEnt}
	for i, k := range created {
		id, _ := argNum(k, "id")
		if i < 50 {
			card.Keys = append(card.Keys, chatCardKey{ID: id, Label: argStr(k, "label"), Hint: argStr(k, "hint"), Status: argStr(k, "effective_status")})
		}
		if i < 20 {
			ents = append(ents, chatEntity{Type: "key", ID: id, Name: argStr(k, "label")})
		}
	}
	res := map[string]any{"service": sv.Slug, "created_count": len(created), "skipped_count": len(skipped)}
	if len(created) <= 20 {
		res["created"] = created
	} else {
		res["first_created"] = created[:10]
	}
	if len(skipped) <= 20 {
		res["skipped"] = skipped
	} else {
		res["first_skipped"] = skipped[:10]
	}
	if len(missing) > 0 {
		res["unknown_refs"] = missing
	}
	if wv, ok := out["warning"]; ok {
		res["warning"] = wv
	}
	return toolOut{OK: true, Summary: sum, Model: asJSON(res), Link: fmt.Sprintf("/services/%d", sv.ID), Card: card, Entities: ents}
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// ---- search_providers ----

func hostOf(s string) string {
	s = strings.TrimSpace(strings.ToLower(s))
	if !strings.Contains(s, "://") {
		if strings.ContainsAny(s, " ") || !strings.Contains(s, ".") {
			return ""
		}
		s = "https://" + s
	}
	u, err := url.Parse(s)
	if err != nil {
		return ""
	}
	return strings.TrimPrefix(u.Hostname(), "www.")
}

// sameSite reports whether two hosts share a registrable looking suffix:
// ai.google.dev and google.dev match, docs.x.io and api.x.io match.
func sameSite(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	if a == b || strings.HasSuffix(a, "."+b) || strings.HasSuffix(b, "."+a) {
		return true
	}
	base := func(h string) string {
		p := strings.Split(h, ".")
		if len(p) < 2 {
			return h
		}
		return strings.Join(p[len(p)-2:], ".")
	}
	return base(a) == base(b)
}

func searchProvidersTool(cs *chatSession, in map[string]any) toolOut {
	q := strings.ToLower(argStr(in, "query"))
	if q == "" {
		return toolFail("give a query: a name, slug, website or docs URL")
	}
	ps, err := cs.s.Store.ListProviders(cs.ctx)
	if err != nil {
		return toolFail(err.Error())
	}
	qh := hostOf(q)
	words := strings.Fields(strings.NewReplacer("-", " ", "_", " ", ".", " ").Replace(q))
	type hit struct {
		p     *store.Provider
		score int
	}
	hits := []hit{}
	for _, p := range ps {
		name, slug := strings.ToLower(p.Name), strings.ToLower(p.Slug)
		score := 0
		switch {
		case q == slug || q == name:
			score = 100
		case qh != "" && (sameSite(qh, hostOf(p.Website)) || sameSite(qh, hostOf(p.DocsURL))):
			score = 80
		case strings.Contains(name, q) || strings.Contains(slug, q):
			score = 60
		default:
			for _, w := range words {
				if len(w) >= 3 && (strings.Contains(name, w) || strings.Contains(slug, w) || strings.Contains(strings.ToLower(p.Website), w)) {
					score += 20
				}
			}
		}
		if score > 0 {
			hits = append(hits, hit{p, score})
		}
	}
	for i := 1; i < len(hits); i++ {
		for j := i; j > 0 && hits[j].score > hits[j-1].score; j-- {
			hits[j], hits[j-1] = hits[j-1], hits[j]
		}
	}
	if len(hits) > 10 {
		hits = hits[:10]
	}
	outRows := []map[string]any{}
	ents := []chatEntity{}
	for _, h := range hits {
		outRows = append(outRows, map[string]any{"slug": h.p.Slug, "name": h.p.Name, "website": h.p.Website, "docs_url": h.p.DocsURL,
			"builtin": h.p.Builtin, "status": h.p.Status, "services_count": h.p.ServicesCount, "match": h.score})
		ents = append(ents, chatEntity{Type: "provider", Slug: h.p.Slug, Name: h.p.Name})
	}
	sum := fmt.Sprintf("Found %d provider(s) for %q", len(hits), argStr(in, "query"))
	return toolOut{OK: true, Summary: sum, Model: asJSON(map[string]any{"matches": outRows}), Entities: ents}
}

// ---- start_provider_build and wait_for_build ----

func startBuildTool(cs *chatSession, in map[string]any) toolOut {
	name := argStr(in, "name")
	ctxText := name
	if c := argStr(in, "context"); c != "" {
		ctxText += ". " + c
	}
	body := map[string]any{"kind": "create_provider", "context": ctxText}
	if u, ok := in["urls"].([]any); ok {
		body["urls"] = u
	}
	if ref := argStr(in, "test_key_ref"); ref != "" {
		e := cs.vaultEntry(ref)
		if e == nil {
			return toolFail("no pasted key " + ref)
		}
		sec, err := cs.vaultSecret(*e)
		if err != nil {
			return toolFail(err.Error())
		}
		body["test_key"] = sec
	}
	code, out, msg := cs.api("POST", "/api/admin/ai/runs", body)
	if code >= 400 {
		return toolFail(msg)
	}
	id, _ := argNum(out, "id")
	title := argStr(out, "title")
	if title == "" {
		title = name
	}
	cs.addItem(chatItem{Kind: "build", OK: true, Link: fmt.Sprintf("/providers/ai/%d", id),
		Build: &chatBuild{RunID: id, Title: title, Status: "queued", Step: "Queued", StartedAt: time.Now().UTC()}})
	return toolOut{OK: true, Summary: fmt.Sprintf("Started AI provider build %d", id), Link: fmt.Sprintf("/providers/ai/%d", id),
		Model: asJSON(map[string]any{"run_id": id, "status": out["status"], "title": title,
			"next": "call wait_for_build with this run_id now, then finish the setup"})}
}

// buildItem finds the card of a run, or adds one.
func (cs *chatSession) buildItem(run *store.AgentRun) *chatItem {
	for i := len(cs.data.Items) - 1; i >= 0; i-- {
		if b := cs.data.Items[i].Build; b != nil && b.RunID == run.ID {
			return &cs.data.Items[i]
		}
	}
	cs.addItem(chatItem{Kind: "build", OK: true, Link: fmt.Sprintf("/providers/ai/%d", run.ID),
		Build: &chatBuild{RunID: run.ID, Title: run.Title, Status: run.Status, Step: "Following the build", StartedAt: run.CreatedAt}})
	return &cs.data.Items[len(cs.data.Items)-1]
}

// buildStep turns a builder log line into a short progress line and phase.
func buildStep(st store.AgentStep) (string, int) {
	t := st.Title
	switch {
	case st.Type == "fetch" && strings.HasPrefix(t, "Reading "):
		if h := hostOf(strings.TrimPrefix(t, "Reading ")); h != "" {
			return "Researching " + h, 0
		}
		return "Reading the docs", 0
	case st.Type == "fetch":
		return "Reading " + strings.TrimPrefix(t, "GitHub: "), 0
	case st.Type == "search":
		return "Searching the web for " + truncateWords(strings.TrimPrefix(t, "Searching: "), 48), 0
	case strings.HasPrefix(t, "Validated draft"):
		return "Writing the spec", 1
	case strings.HasPrefix(t, "Submitted"):
		return "Running the tests", 2
	case t == "Submission rejected":
		return "Fixing what the tests found", 2
	case strings.HasPrefix(t, "Live check"):
		if strings.Contains(t, "ok") || strings.Contains(t, "passed") {
			return "Live check passed", 3
		}
		return "Live check failed, fixing", 3
	case strings.HasPrefix(t, "Accepted"):
		return strings.TrimPrefix(t, "Accepted: "), 3
	case strings.HasPrefix(t, "Logo"):
		return "Fetching the logo", 4
	case strings.HasPrefix(t, "Published"):
		return t, 4
	case t == "Ready for review":
		return "Ready for your review", 4
	case strings.HasPrefix(t, "Added"):
		return t, 4
	case t == "Run failed":
		return "Build failed", -1
	case st.Type == "thinking" || st.Type == "result":
		return "", 0
	}
	return truncateWords(t, 80), 0
}

func truncateWords(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := store.TruncateUTF8(s, n)
	if i := strings.LastIndex(cut, " "); i > n/2 {
		cut = cut[:i]
	}
	return cut + "..."
}

func (s *Server) pollEvery() time.Duration {
	if s.buildPoll > 0 {
		return s.buildPoll
	}
	return 2 * time.Second
}

func waitBuildTool(cs *chatSession, in map[string]any) toolOut {
	id, ok := argNum(in, "run_id")
	if !ok {
		return toolFail("give run_id")
	}
	run, err := cs.s.Store.GetRun(cs.ctx, id)
	if err != nil {
		return toolFail(fmt.Sprintf("no AI run %d", id))
	}
	link := fmt.Sprintf("/providers/ai/%d", id)
	left := chatWaitBudget - cs.waitUsed
	if left <= 0 {
		return toolOut{OK: true, Silent: true, Model: asJSON(map[string]any{"status": run.Status, "link": link,
			"note": "This turn already waited the most it can. Tell the user the build is still running and that the card above updates when it ends; finish the rest yourself in your next turn."})}
	}
	timeout := 600 * time.Second
	if t, ok := argNum(in, "timeout_sec"); ok && t > 0 && t < 600 {
		timeout = time.Duration(t) * time.Second
	}
	if timeout > left {
		timeout = left
	}
	it := cs.buildItem(run)
	itemID := it.ID
	b := it.Build
	start := time.Now()
	deadline := start.Add(timeout)
	after := 0
	for {
		run, err = cs.s.Store.GetRun(cs.ctx, id)
		if err != nil {
			return toolFail(err.Error())
		}
		steps, _ := cs.s.Store.ListSteps(cs.ctx, id, after)
		changed := b.Status != run.Status
		for _, st := range steps {
			after = st.Seq
			text, phase := buildStep(st)
			if text != "" && text != b.Step {
				b.Step, changed = text, true
			}
			if phase > b.Phase {
				b.Phase, changed = phase, true
			}
		}
		if run.Status == "testing" && b.Phase < 2 {
			b.Phase, changed = 2, true
		}
		b.Status = run.Status
		cs.turn.setStep(b.Step)
		if changed {
			cs.newIDs[itemID] = true
			_ = cs.save()
		}
		if buildTerminal[run.Status] || time.Now().After(deadline) {
			break
		}
		select {
		case <-cs.ctx.Done():
			cs.waitUsed += time.Since(start)
			return toolFail("the turn was stopped while waiting for the build")
		case <-time.After(cs.s.pollEvery()):
		}
	}
	cs.waitUsed += time.Since(start)
	res := map[string]any{"status": run.Status, "run_id": id, "link": link}
	it = cs.buildItemByID(itemID)
	b = it.Build
	if buildTerminal[run.Status] {
		now := time.Now().UTC()
		b.FinishedAt = &now
	}
	switch run.Status {
	case "published":
		b.Phase = 4
		b.ProviderSlug = run.ProviderSlug
		b.ServiceID = run.ServiceID
		res["provider"] = run.ProviderSlug
		pc := &chatCardProv{Slug: run.ProviderSlug, Name: run.ProviderSlug}
		if p, err := cs.s.Store.GetProvider(cs.ctx, run.ProviderSlug); err == nil {
			pc.Name, pc.Version = p.Name, p.Version
			res["provider_name"], res["version"] = p.Name, p.Version
		}
		var rep struct{ Passed, Failed int }
		_ = json.Unmarshal(run.Report, &rep)
		pc.Passed, pc.Failed = rep.Passed, rep.Failed
		it.Card = &chatCard{Type: "provider", Provider: pc}
		it.Entities = []chatEntity{{Type: "provider", Slug: pc.Slug, Name: pc.Name}}
		b.Step = fmt.Sprintf("Published %s", pc.Name)
		if run.ServiceID != nil {
			if sv, err := cs.s.Store.GetService(cs.ctx, *run.ServiceID); err == nil {
				res["service_id"], res["service"] = sv.ID, sv.Slug
				ent := chatEntity{Type: "service", ID: sv.ID, Slug: sv.Slug, Name: sv.Name}
				it.Card.Service = &ent
				it.Entities = append(it.Entities, ent)
			}
			res["next"] = "add the keys the user pasted to this service with add_keys, then report what was set up"
		} else {
			res["next"] = "create_service from this provider, add the keys the user pasted with add_keys, then report what was set up"
		}
	case "needs_review":
		res["note"] = "The provider waits for the user's review at the link. Tell them so with the link and stop."
	case "failed", "cancelled":
		b.Error = run.Error
		res["error"] = run.Error
		res["note"] = "Tell the user plainly why it failed, with the link, and stop."
	default:
		res["note"] = "Still running. Call wait_for_build again to keep following it."
	}
	it.OK = run.Status != "failed" && run.Status != "cancelled"
	cs.newIDs[itemID] = true
	_ = cs.save()
	return toolOut{OK: true, Silent: true, Model: asJSON(res)}
}

func (cs *chatSession) buildItemByID(id string) *chatItem {
	for i := range cs.data.Items {
		if cs.data.Items[i].ID == id {
			return &cs.data.Items[i]
		}
	}
	return nil
}

// ---- ask_user ----

func askUserTool(cs *chatSession, in map[string]any) toolOut {
	q := argStr(in, "question")
	if q == "" {
		return toolFail("give the question")
	}
	ask := &chatAsk{ID: "ask_" + strconv.Itoa(len(cs.data.Items)) + "_" + strconv.FormatInt(time.Now().UnixNano()%1e6, 36),
		Kind: argStr(in, "kind"), Question: crypto80(q), Status: "waiting"}
	switch ask.Kind {
	case "names":
		entries, missing := cs.vaultPick(strList(in["key_refs"]), argStr(in, "batch"))
		if len(entries) == 0 {
			return toolFail("names needs key_refs or batch naming keys still in the vault" + missingNote(missing))
		}
		if len(entries) > 200 {
			return toolFail("too many keys to name one by one; add them with their parsed labels or default labels instead")
		}
		sugg, _ := in["suggestions"].(map[string]any)
		for _, e := range entries {
			s, _ := sugg[e.Ref].(string)
			if s == "" {
				s = e.Label
			}
			ask.Rows = append(ask.Rows, chatAskRow{Ref: e.Ref, Hint: e.Hint, Suggestion: strings.TrimSpace(s)})
		}
	case "choice":
		ask.Choices = strList(in["choices"])
		if len(ask.Choices) < 2 || len(ask.Choices) > 8 {
			return toolFail("choice needs 2 to 8 choices")
		}
	default:
		return toolFail("kind must be names or choice")
	}
	cs.addItem(chatItem{Kind: "ask", Text: ask.Question, OK: true, Ask: ask})
	cs.halt = true
	return toolOut{OK: true, Silent: true, Model: "The user now sees this question as a form. Their answer arrives as the next user message."}
}

func crypto80(s string) string { return truncateWords(s, 300) }

func missingNote(m []string) string {
	if len(m) == 0 {
		return ""
	}
	return " (not in the vault: " + strings.Join(m, ", ") + ")"
}

func (cs *chatSession) expireAsks() {
	for i := range cs.data.Items {
		if a := cs.data.Items[i].Ask; a != nil && a.Status == "waiting" {
			a.Status = "expired"
			cs.newIDs[cs.data.Items[i].ID] = true
		}
	}
}

// chatAnswer posts the user's answer to an ask_user form and continues the turn.
func (s *Server) chatAnswer(w http.ResponseWriter, r *http.Request) {
	var body struct {
		AskID  string            `json:"ask_id"`
		Names  map[string]string `json:"names"`
		Choice string            `json:"choice"`
		Auto   bool              `json:"auto"`
	}
	if !decode(w, r, &body) {
		return
	}
	s.chatTurn(w, r, func(cs *chatSession) error {
		var it *chatItem
		for i := range cs.data.Items {
			if a := cs.data.Items[i].Ask; a != nil && a.ID == body.AskID && a.Status == "waiting" {
				it = &cs.data.Items[i]
			}
		}
		if it == nil {
			cs.addItem(chatItem{Kind: "error", Text: "That question is no longer open. Send a message instead."})
			cs.halt = true
			return nil
		}
		a := it.Ask
		var model, shown string
		switch {
		case a.Kind == "names" && body.Auto:
			model = fmt.Sprintf("[Answer to %q: the user asked you to pick the names yourself. Pick short, useful labels and go on.]", a.Question)
			shown = "Pick names for me"
		case a.Kind == "names":
			pairs, show := []string{}, []string{}
			for _, row := range a.Rows {
				n := strings.TrimSpace(body.Names[row.Ref])
				if n == "" {
					n = row.Suggestion
				}
				if len(n) > 64 {
					n = store.TruncateUTF8(n, 64)
				}
				if n == "" {
					continue
				}
				if e := cs.vaultEntry(row.Ref); e != nil {
					e.Label = n
				}
				pairs = append(pairs, row.Ref+" = "+n)
				show = append(show, n+" ("+strings.Replace(row.Hint, "...", "…", 1)+")")
			}
			if len(pairs) == 0 {
				return errors.New("give at least one name")
			}
			model = fmt.Sprintf("[Answer to %q: %s. These labels are now on the pasted keys; add_keys uses them.]", a.Question, strings.Join(pairs, ", "))
			shown = "Names: " + strings.Join(show, ", ")
		default:
			ok := false
			for _, c := range a.Choices {
				ok = ok || c == body.Choice
			}
			if !ok {
				return errors.New("pick one of the choices")
			}
			model = fmt.Sprintf("[Answer to %q: %s]", a.Question, body.Choice)
			shown = body.Choice
		}
		a.Status, a.Answer = "answered", shown
		cs.newIDs[it.ID] = true
		cs.addItem(chatItem{Kind: "user", Text: shown, OK: true})
		cs.appendUser(chatBlock{Type: "text", Text: model})
		return nil
	})
}
