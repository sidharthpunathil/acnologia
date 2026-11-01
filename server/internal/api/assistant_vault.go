package api

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/cogito-keys/cogito/internal/crypto"
	"github.com/cogito-keys/cogito/internal/keyparse"
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
// it. The transcript keeps only hints.

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

// mask is how the transcript shows a secret: its last four characters.
func mask(secret string) string {
	if len(secret) <= 4 {
		return "…"
	}
	return "…" + secret[len(secret)-4:]
}

// vaultMessage moves the keys in a user message into the vault. It returns
// the text for the model (placeholders), the text for the transcript (hints)
// and how many keys it found.
func (cs *chatSession) vaultMessage(text string) (model, shown string, n int, err error) {
	res := keyparse.Parse(text)
	if len(res.Keys) == 0 {
		return text, text, 0, nil
	}
	if res.Overflow > 0 {
		return "", "", 0, fmt.Errorf("send at most %d keys per message", keyparse.MaxKeys)
	}
	d := cs.data
	d.BatchSeq++
	batch := "b" + strconv.Itoa(d.BatchSeq)
	type pair struct{ secret, ph, hint string }
	pairs := make([]pair, 0, len(res.Keys))
	literal := true
	for _, k := range res.Keys {
		enc, err := cs.s.Pool.Box.Encrypt(k.Secret)
		if err != nil {
			return "", "", 0, err
		}
		d.VaultSeq++
		ref := "k" + strconv.Itoa(d.VaultSeq)
		d.Vault = append(d.Vault, vaultEntry{Ref: ref, Batch: batch, Label: k.Label, Hint: crypto.Hint(k.Secret), Enc: enc})
		pairs = append(pairs, pair{k.Secret, "[key " + ref + " " + crypto.Hint(k.Secret) + "]", mask(k.Secret)})
		if !strings.Contains(text, k.Secret) {
			literal = false // found only in an escaped form, such as JSON
		}
	}
	if over := len(d.Vault) - vaultMax; over > 0 {
		d.Vault = append([]vaultEntry(nil), d.Vault[over:]...)
	}
	// Longest first, so a secret inside another never splits it.
	sort.SliceStable(pairs, func(i, j int) bool { return len(pairs[i].secret) > len(pairs[j].secret) })
	mp, sp := make([]string, 0, 2*len(pairs)), make([]string, 0, 2*len(pairs))
	for _, p := range pairs {
		mp = append(mp, p.secret, p.ph)
		sp = append(sp, p.secret, p.hint)
	}
	model = strings.NewReplacer(mp...).Replace(text)
	shown = strings.NewReplacer(sp...).Replace(text)
	if literal && len(res.Keys) <= vaultInline && len(model) <= vaultModelText {
		return model, shown, len(res.Keys), nil
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
	model = b.String()
	hints := []string{}
	for i, k := range res.Keys {
		if i == 3 {
			break
		}
		hints = append(hints, mask(k.Secret))
	}
	more := ""
	if len(res.Keys) > 3 {
		more = fmt.Sprintf(" and %d more", len(res.Keys)-3)
	}
	sprose := proseLines(shown, literal)
	if sprose != "" {
		sprose += "\n"
	}
	shown = fmt.Sprintf("%s%d keys: %s%s", sprose, len(res.Keys), strings.Join(hints, ", "), more)
	return model, shown, len(res.Keys), nil
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
