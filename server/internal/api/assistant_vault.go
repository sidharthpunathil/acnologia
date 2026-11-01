package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/cogito-keys/cogito/internal/crypto"
	"github.com/cogito-keys/cogito/internal/keyparse"
	"github.com/cogito-keys/cogito/internal/redact"
)

// The key vault keeps pasted secrets away from Claude.
//
// Before a user message reaches the model it goes through keyparse. Every
// key found is encrypted on its own with the server's Box and kept in the
// thread's data (itself encrypted at rest) as a vault entry with a ref (k1,
// k2, ...) and a batch id per message (b1, b2, ...). In the text Claude gets,
// each secret becomes a placeholder such as [key k3 ...c21e]; labels around
// it stay. A big paste is condensed to one line naming the batch, so 400 keys
// cost Claude a few tokens and one add_keys call with batch "latest".
// add_keys and start_provider_build resolve refs server side; an entry is
// wiped once its key is stored. Deleting the thread deletes the vault with
// it. The transcript keeps the prose and a key_list of labels and hints.
// The redact pattern detector runs after keyparse, so a key in a URL, after
// Bearer or glued to text is vaulted too; the model client scrubs whatever
// is left (see internal/redact).

// vaultEntry is one pasted key.
type vaultEntry struct {
	Ref   string `json:"ref"`
	Batch string `json:"batch"`
	Label string `json:"label,omitempty"`
	Hint  string `json:"hint"`
	Enc   []byte `json:"enc"`
}

const (
	// vaultMax bounds the entries a thread keeps; the oldest go first.
	vaultMax = 2 * keyparse.MaxKeys
	// vaultInline is the most keys a message keeps inline as placeholders;
	// more are condensed into one batch line.
	vaultInline = 40
	// vaultModelText is the longest placeholder text sent inline.
	vaultModelText = 8000
)

// chatKeyRef is one key of a user message as the transcript shows it.
type chatKeyRef struct {
	Label string `json:"label"`
	Hint  string `json:"hint"`
}

// findKeys is keyparse, then the redact pattern detector for what keyparse
// misses: a key inside a URL (?key=AIza...), after "Bearer", in a JSON field
// or glued to other text.
func findKeys(text string) keyparse.Result {
	res := keyparse.Parse(text)
	if res.Overflow > 0 {
		return res
	}
	seen := map[string]bool{}
	for _, k := range res.Keys {
		seen[k.Secret] = true
	}
	for _, m := range redact.Find(text) {
		sec := text[m.Start:m.End]
		if seen[sec] || strings.HasPrefix(sec, "[") {
			continue
		}
		inside := false
		for _, k := range res.Keys {
			inside = inside || strings.Contains(k.Secret, sec)
		}
		if inside {
			continue
		}
		if len(res.Keys) >= keyparse.MaxKeys {
			res.Overflow++
			continue
		}
		seen[sec] = true
		res.Keys = append(res.Keys, keyparse.Key{Secret: sec})
	}
	return res
}

// vaultMessage moves the keys in a user message into the vault. It returns
// the text for the model (placeholders), the text for the transcript (the
// prose without the keys), the keys as label and hint, and how many keys it
// found.
func (cs *chatSession) vaultMessage(text string) (model, shown string, list []chatKeyRef, n int, err error) {
	res := findKeys(text)
	if len(res.Keys) == 0 {
		return text, text, nil, 0, nil
	}
	if res.Overflow > 0 {
		return "", "", nil, 0, fmt.Errorf("send at most %d keys per message", keyparse.MaxKeys)
	}
	list = make([]chatKeyRef, 0, len(res.Keys))
	for _, k := range res.Keys {
		list = append(list, chatKeyRef{Label: k.Label, Hint: crypto.Hint(k.Secret)})
	}
	shown = proseOnly(text, res.Keys)
	d := cs.data
	d.BatchSeq++
	batch := "b" + strconv.Itoa(d.BatchSeq)
	type pair struct{ secret, ph string }
	pairs := make([]pair, 0, len(res.Keys))
	literal := true
	for _, k := range res.Keys {
		enc, err := cs.s.Pool.Box.Encrypt(k.Secret)
		if err != nil {
			return "", "", nil, 0, err
		}
		d.VaultSeq++
		ref := "k" + strconv.Itoa(d.VaultSeq)
		d.Vault = append(d.Vault, vaultEntry{Ref: ref, Batch: batch, Label: k.Label, Hint: crypto.Hint(k.Secret), Enc: enc})
		pairs = append(pairs, pair{k.Secret, "[key " + ref + " " + crypto.Hint(k.Secret) + "]"})
		if !strings.Contains(text, k.Secret) {
			literal = false // found only in an escaped form, such as JSON
		}
	}
	if over := len(d.Vault) - vaultMax; over > 0 {
		d.Vault = append([]vaultEntry(nil), d.Vault[over:]...)
	}
	// Longest first, so a secret inside another never splits it.
	sort.SliceStable(pairs, func(i, j int) bool { return len(pairs[i].secret) > len(pairs[j].secret) })
	mp := make([]string, 0, 2*len(pairs))
	for _, p := range pairs {
		mp = append(mp, p.secret, p.ph)
	}
	model = strings.NewReplacer(mp...).Replace(text)
	if literal && len(res.Keys) <= vaultInline && len(model) <= vaultModelText {
		return model, shown, list, len(res.Keys), nil
	}
	first, last := d.Vault[len(d.Vault)-len(res.Keys)].Ref, d.Vault[len(d.Vault)-1].Ref
	prose := proseLines(model, literal)
	var b strings.Builder
	if prose != "" {
		b.WriteString(prose + "\n\n")
	}
	fmt.Fprintf(&b, "[Pasted %d keys, stored as batch %s (refs %s to %s).", len(res.Keys), batch, first, last)
	labeled := []string{}
	for _, e := range d.Vault[len(d.Vault)-len(res.Keys):] {
		if e.Label != "" {
			labeled = append(labeled, e.Label+" = "+e.Ref)
		}
	}
	switch {
	case len(labeled) == 0:
		b.WriteString(" No labels were given.]")
	case len(labeled) <= 20:
		fmt.Fprintf(&b, " Labels: %s.]", strings.Join(labeled, ", "))
	default:
		fmt.Fprintf(&b, " %d have labels, for example %s.]", len(labeled), strings.Join(labeled[:20], ", "))
	}
	return b.String(), shown, list, len(res.Keys), nil
}

// keyWords are words that only describe a paste: CSV headers, env exports
// and the joins between keys. A line left with nothing else once its keys
// are gone is not prose.
var keyWords = map[string]bool{
	"label": true, "name": true, "key": true, "keys": true, "secret": true, "api_key": true, "apikey": true,
	"token": true, "account": true, "export": true, "and": true, "or": true, "also": true, "plus": true,
	"bearer": true, "basic": true,
}

// proseOnly is the user's message without its keys: lines that held only
// keys (with their labels) go, keys inside a sentence are cut out with the
// label right before them, and blank runs collapse to one blank line.
func proseOnly(text string, keys []keyparse.Key) string {
	if t := strings.TrimSpace(text); strings.HasPrefix(t, "[") && json.Valid([]byte(t)) {
		return "" // a JSON array of keys is all data
	}
	cuts := make([]*regexp.Regexp, 0, len(keys))
	labels := map[string]bool{}
	sorted := append([]keyparse.Key(nil), keys...)
	sort.SliceStable(sorted, func(i, j int) bool { return len(sorted[i].Secret) > len(sorted[j].Secret) })
	for _, k := range sorted {
		q := regexp.QuoteMeta(k.Secret)
		if k.Label != "" {
			labels[strings.ToLower(k.Label)] = true
			q = `(?:` + regexp.QuoteMeta(k.Label) + `["'\x60]?\s*[:=,;|]?\s*)?["'\x60]?` + q + `["'\x60]?`
		}
		cuts = append(cuts, regexp.MustCompile(q))
	}
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	out := make([]string, 0, len(lines))
	header := true
	for _, l := range lines {
		t := strings.TrimSpace(l)
		if header && t != "" {
			header = false
			if _, _, _, ok := csvHeaderLine(t); ok {
				continue
			}
		}
		had := false
		for _, re := range cuts {
			if re.MatchString(t) {
				had = true
				t = re.ReplaceAllString(t, "")
			}
		}
		if !had {
			out = append(out, strings.TrimRight(l, " \t"))
			continue
		}
		t = emptyPairs.Replace(t)
		t = strings.Join(strings.Fields(t), " ")
		t = strings.TrimLeft(t, ",;|")
		t = strings.TrimSpace(t)
		if onlyKeyWords(t, labels) {
			continue
		}
		out = append(out, t)
	}
	// Collapse runs of blank lines to one and trim.
	res := make([]string, 0, len(out))
	for _, l := range out {
		if strings.TrimSpace(l) == "" && (len(res) == 0 || strings.TrimSpace(res[len(res)-1]) == "") {
			continue
		}
		res = append(res, l)
	}
	return strings.TrimSpace(strings.Join(res, "\n"))
}

var emptyPairs = strings.NewReplacer(`""`, "", "''", "", "``", "", "()", "", "[]", "", "{}", "", "<>", "")

func onlyKeyWords(t string, labels map[string]bool) bool {
	for _, w := range strings.FieldsFunc(t, func(r rune) bool {
		return !(r == '_' || r == '-' || r == '.' || r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r > 127)
	}) {
		lw := strings.ToLower(strings.Trim(w, ".-_"))
		if lw == "" || keyWords[lw] || labels[strings.ToLower(w)] || labels[lw] {
			continue
		}
		return false
	}
	return true
}

// csvHeaderLine mirrors keyparse's CSV header rule: a line naming a label
// and a secret column.
func csvHeaderLine(line string) (string, int, int, bool) {
	sep := "\t"
	if strings.Contains(line, ",") {
		sep = ","
	} else if !strings.Contains(line, "\t") {
		return "", 0, 0, false
	}
	lab, sec := -1, -1
	for i, c := range strings.Split(line, sep) {
		switch strings.ToLower(strings.Trim(strings.TrimSpace(c), "\"'`")) {
		case "label", "name":
			if lab < 0 {
				lab = i
			}
		case "key", "secret", "api_key":
			if sec < 0 {
				sec = i
			}
		}
	}
	return sep, lab, sec, lab >= 0 && sec >= 0
}

// proseLines keeps the lines of a message that hold no key, so the
// instruction around a big paste survives condensing.
func proseLines(text string, literal bool) string {
	if !literal {
		return ""
	}
	out := []string{}
	size := 0
	for _, l := range strings.Split(text, "\n") {
		l = strings.TrimSpace(l)
		if l == "" || strings.Contains(l, "[key k") || strings.Contains(l, "…") && hasHint(l) {
			continue
		}
		if len(out) == 30 || size+len(l) > 3000 {
			break
		}
		out = append(out, l)
		size += len(l)
	}
	return strings.Join(out, "\n")
}

func hasHint(l string) bool {
	for _, f := range strings.Fields(l) {
		if strings.HasPrefix(strings.Trim(f, ",;|\"'`"), "…") {
			return true
		}
	}
	return false
}

// vaultPick returns the entries named by refs and by batch ("latest" or an
// id such as b2), in vault order without repeats.
func (cs *chatSession) vaultPick(refs []string, batch string) ([]vaultEntry, []string) {
	want := map[string]bool{}
	for _, r := range refs {
		want[strings.TrimSpace(r)] = true
	}
	batch = strings.TrimSpace(batch)
	if batch == "latest" || batch == "all" {
		batch = "-" // matches nothing when the vault is empty
		if n := len(cs.data.Vault); n > 0 {
			batch = cs.data.Vault[n-1].Batch
		}
	}
	out := []vaultEntry{}
	found := map[string]bool{}
	for _, e := range cs.data.Vault {
		if want[e.Ref] || (batch != "" && e.Batch == batch) {
			out = append(out, e)
			found[e.Ref] = true
		}
	}
	missing := []string{}
	for r := range want {
		if r != "" && !found[r] {
			missing = append(missing, r)
		}
	}
	sort.Strings(missing)
	return out, missing
}

func (cs *chatSession) vaultSecret(e vaultEntry) (string, error) {
	s, err := cs.s.Pool.Box.Decrypt(e.Enc)
	if err != nil {
		return "", errors.New("a pasted key could not be decrypted")
	}
	cs.secrets[s] = true
	return s, nil
}

// vaultDrop wipes used entries.
func (cs *chatSession) vaultDrop(refs map[string]bool) {
	if len(refs) == 0 {
		return
	}
	kept := cs.data.Vault[:0]
	for _, e := range cs.data.Vault {
		if !refs[e.Ref] {
			kept = append(kept, e)
		}
	}
	cs.data.Vault = kept
}

func (cs *chatSession) vaultEntry(ref string) *vaultEntry {
	for i := range cs.data.Vault {
		if cs.data.Vault[i].Ref == ref {
			return &cs.data.Vault[i]
		}
	}
	return nil
}

// splitSuggestions takes a trailing "Suggestions: a | b | c" line off a
// reply and returns the reply and up to three short follow ups.
func splitSuggestions(text string) (string, []string) {
	t := strings.TrimSpace(text)
	i := strings.LastIndex(t, "\n")
	last := t[i+1:]
	if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(last)), "suggestions:") {
		return t, nil
	}
	body := strings.TrimSpace(last)[len("suggestions:"):]
	out := []string{}
	for _, s := range strings.Split(body, "|") {
		s = strings.Trim(strings.TrimSpace(s), "\"'`")
		if s == "" || len(s) > 80 {
			continue
		}
		out = append(out, s)
		if len(out) == 3 {
			break
		}
	}
	if i < 0 {
		return "", out
	}
	return strings.TrimSpace(t[:i]), out
}

// stepText is the live progress line while a tool runs.
func stepText(tool string) string {
	switch tool {
	case "list_providers", "search_providers", "get_provider":
		return "Searching the catalog"
	case "list_services":
		return "Looking at your services"
	case "list_keys", "key_details":
		return "Reading keys"
	case "start_provider_build":
		return "Starting the provider builder"
	case "wait_for_build", "get_ai_run":
		return "Following the provider build"
	case "create_service":
		return "Creating the service"
	case "add_keys":
		return "Adding keys"
	case "check_keys":
		return "Checking keys"
	case "ask_user":
		return "Asking you"
	}
	return humanTool(tool)
}

func humanTool(t string) string {
	s := strings.ReplaceAll(t, "_", " ")
	if s == "" {
		return "Working"
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
