package api

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/sidharthpunathil/acnologia/server/internal/keyparse"
)

// proseOnlyByRegexp is the earlier proseOnly, one regexp per key, kept as
// the reference for what the linear version must produce.
func proseOnlyByRegexp(text string, keys []keyparse.Key) string {
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
	out := []string{}
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
	res := []string{}
	for _, l := range out {
		if strings.TrimSpace(l) == "" && (len(res) == 0 || strings.TrimSpace(res[len(res)-1]) == "") {
			continue
		}
		res = append(res, l)
	}
	return strings.TrimSpace(strings.Join(res, "\n"))
}

func TestProseOnlyMatchesReference(t *testing.T) {
	cases := []struct {
		text string
		keys []keyparse.Key
	}{
		{"add these to serpapi\nacct-1: sk_live_aaaaaaaa11\nacct-2 = \"sk_live_bbbbbbbb22\"\nthanks",
			[]keyparse.Key{{Label: "acct-1", Secret: "sk_live_aaaaaaaa11"}, {Label: "acct-2", Secret: "sk_live_bbbbbbbb22"}}},
		{"please use sk_live_cccccccc33 for the search app, and 'sk_live_dddddddd44' too",
			[]keyparse.Key{{Secret: "sk_live_cccccccc33"}, {Secret: "sk_live_dddddddd44"}}},
		{"label,secret\nalice,AIzaSyA0000000000000000\nbob,AIzaSyB0000000000000000\n\n\nbye",
			[]keyparse.Key{{Label: "alice", Secret: "AIzaSyA0000000000000000"}, {Label: "bob", Secret: "AIzaSyB0000000000000000"}}},
		{"main key | `tok_123456789abc` is for prod; backup:tok_123456789abcdef",
			[]keyparse.Key{{Label: "main key", Secret: "tok_123456789abc"}, {Label: "backup", Secret: "tok_123456789abcdef"}}},
		{"no keys here\r\njust text  ", nil},
		{`[{"secret":"abcdefgh12345"}]`, []keyparse.Key{{Secret: "abcdefgh12345"}}},
	}
	for i, c := range cases {
		got, want := proseOnly(c.text, c.keys), proseOnlyByRegexp(c.text, c.keys)
		if strings.HasPrefix(c.text, "[") {
			want = ""
		}
		if got != want {
			t.Errorf("case %d:\n got %q\nwant %q", i, got, want)
		}
	}
}

// A 1 MB message of 5000 labeled keys and one long prose line must not
// cost keys times bytes.
func TestProseOnlyIsLinear(t *testing.T) {
	var b strings.Builder
	keys := make([]keyparse.Key, 0, 5000)
	for i := 0; i < 5000; i++ {
		k := keyparse.Key{Label: fmt.Sprintf("acct%d", i), Secret: fmt.Sprintf("sk_test_%016d", i)}
		keys = append(keys, k)
		fmt.Fprintf(&b, "%s %s\n", k.Label, k.Secret)
	}
	b.WriteString(strings.Repeat("plain words of prose ", 40000))
	text := b.String()
	start := time.Now()
	got := proseOnly(text, keys)
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("proseOnly took %s on a 1 MB message", d)
	}
	if strings.Contains(got, "sk_test_") || !strings.HasPrefix(got, "plain words") {
		t.Fatalf("keys left in the prose: %.80q", got)
	}
}
