package providers

import (
	"math"
	"regexp"
	"strconv"
	"testing"
)

var modelNote = regexp.MustCompile(`^Model (\S+): \$([0-9.]+) input and \$([0-9.]+) output per million tokens\.`)

// TestBuiltinTokenPrices: every built-in model offer whose notes state its
// per million token prices carries them as a structured price, and a
// sample call's token cost comes out of it.
func TestBuiltinTokenPrices(t *testing.T) {
	n := 0
	for _, b := range Builtins() {
		spec := b.Spec
		if errs := spec.Validate(); len(errs) > 0 {
			t.Errorf("%s: %v", b.Slug, errs)
		}
		for _, o := range spec.Offers {
			m := modelNote.FindStringSubmatch(o.Notes)
			if m == nil {
				continue
			}
			n++
			in, _ := strconv.ParseFloat(m[2], 64)
			out, _ := strconv.ParseFloat(m[3], 64)
			if o.Model != m[1] || o.Price == nil || o.Price.Unit != "token" ||
				math.Abs(o.Price.Amount*1e6-in) > 1e-9 || math.Abs(o.Price.OutputAmount*1e6-out) > 1e-9 {
				t.Errorf("%s %s: model %q price %+v, notes say %s", b.Slug, o.Name, o.Model, o.Price, m[0])
				continue
			}
			// 2,000 input and 1,000 output tokens of this model.
			cost, ok := spec.TokenCost(o.Match.Method, o.Match.Path, o.Model, 2000, 1000)
			want := (2000*in + 1000*out) / 1e6
			if !ok || math.Abs(cost-want) > 1e-12 {
				t.Errorf("%s %s: token cost %v %v, want %v", b.Slug, o.Name, cost, ok, want)
			}
		}
	}
	if n < 20 {
		t.Fatalf("only %d model offers state prices; the notes format changed?", n)
	}
	// A dated model id is priced at its family's rate; an unknown model is
	// not priced at all.
	var anthropic Spec
	for _, b := range Builtins() {
		if b.Slug == "anthropic" {
			anthropic = b.Spec
		}
	}
	if c, ok := anthropic.TokenCost("POST", "/v1/messages", "claude-haiku-5-5-20261101", 1_000_000, 0); !ok || math.Abs(c-0.10) > 1e-12 {
		t.Fatalf("dated model cost %v %v", c, ok)
	}
	if _, ok := anthropic.TokenCost("POST", "/v1/messages", "some-other-model", 10, 10); ok {
		t.Fatal("an unknown model was priced")
	}
	o := anthropic.Offers[0]
	if got := o.Summary(); got != "Claude Opus 5.5 [model claude-opus-5-5] (POST /v1/messages): $4 input and $20 output per million tokens" {
		t.Fatalf("summary %q", got)
	}
}
