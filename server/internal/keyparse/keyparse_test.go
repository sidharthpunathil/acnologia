package keyparse

import (
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
)

// TestSharedCases runs testdata/cases.json, which the web parser
// (web/src/lib/keyparse.ts) must pass too.
func TestSharedCases(t *testing.T) {
	b, err := os.ReadFile("testdata/cases.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Name    string   `json:"name"`
		Input   string   `json:"input"`
		Keys    []Key    `json:"keys"`
		Ignored []string `json:"ignored"`
	}
	if err := json.Unmarshal(b, &cases); err != nil {
		t.Fatal(err)
	}
	if len(cases) < 15 {
		t.Fatalf("only %d cases", len(cases))
	}
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			got := Parse(c.Input)
			if !reflect.DeepEqual(got.Keys, c.Keys) {
				t.Errorf("keys\n got %+v\nwant %+v", got.Keys, c.Keys)
			}
			if !reflect.DeepEqual(got.Ignored, c.Ignored) {
				t.Errorf("ignored\n got %q\nwant %q", got.Ignored, c.Ignored)
			}
		})
	}
}

func TestKeyLike(t *testing.T) {
	for s, want := range map[string]bool{
		"sk_live_0123456789":           true,
		"short1":                       false,
		"abcdefghijklmnopqrstuvw":      false, // 23 letters, no digit
		"abcdefghijklmnopqrstuvwx":     true,
		"https://example.com/12345678": false,
		"has space 1234567890":         false,
		"bad*char0123456789":           false,
		strings.Repeat("a1", 256):      true,
		strings.Repeat("a1", 257):      false,
	} {
		if KeyLike(s) != want {
			t.Errorf("KeyLike(%q) = %v", s, !want)
		}
	}
}

func TestMixedBatchOf300(t *testing.T) {
	var b strings.Builder
	seps := []string{"\n", ", ", "; ", " | ", "\t", " "}
	want := []Key{}
	for i := 0; i < 300; i++ {
		sec := fmt.Sprintf("sk_test_%016d", i)
		if i%3 == 0 {
			lbl := fmt.Sprintf("acct-%d", i)
			fmt.Fprintf(&b, "%s %s", lbl, sec)
			want = append(want, Key{Label: lbl, Secret: sec})
		} else {
			b.WriteString(sec)
			want = append(want, Key{Secret: sec})
		}
		b.WriteString(seps[i%len(seps)])
	}
	got := Parse(b.String())
	if !reflect.DeepEqual(got.Keys, want) || len(got.Ignored) != 0 {
		t.Fatalf("got %d keys, %d ignored", len(got.Keys), len(got.Ignored))
	}
}

func TestCapAndOverflow(t *testing.T) {
	var b strings.Builder
	for i := 0; i < MaxKeys+7; i++ {
		fmt.Fprintf(&b, "key_%016d\n", i)
	}
	b.WriteString("key_0000000000000000\n") // a duplicate does not count as overflow
	got := Parse(b.String())
	if len(got.Keys) != MaxKeys || got.Overflow != 7 {
		t.Fatalf("keys %d overflow %d", len(got.Keys), got.Overflow)
	}
}

func TestIgnoredIsCapped(t *testing.T) {
	got := Parse(strings.Repeat("word ", MaxIgnored+50))
	if len(got.Ignored) != MaxIgnored {
		t.Fatalf("ignored %d", len(got.Ignored))
	}
}
