package providers

import (
	"math"
	"net/http"
	"testing"
	"time"
)

func TestParseRetryAfterClamped(t *testing.T) {
	cases := map[string]time.Duration{
		"30":                   30 * time.Second,
		"0":                    0,
		"-5":                   0,
		"9000000000":           MaxRetryAfter,
		"99999999999999999999": 0, // does not parse as int64 or a date
		"junk":                 0,
	}
	for v, want := range cases {
		h := http.Header{}
		h.Set("Retry-After", v)
		if got := ParseRetryAfter(h); got != want {
			t.Errorf("Retry-After %q = %v, want %v", v, got, want)
		}
	}
	h := http.Header{}
	h.Set("Retry-After", time.Now().Add(1000*24*time.Hour).UTC().Format(http.TimeFormat))
	if got := ParseRetryAfter(h); got != MaxRetryAfter {
		t.Errorf("far future date = %v, want cap", got)
	}
}

func TestClampSeconds(t *testing.T) {
	for f, want := range map[float64]time.Duration{
		-1: 0, 0: 0, 1.5: 1500 * time.Millisecond, 1e18: MaxRetryAfter, math.Inf(1): MaxRetryAfter, math.NaN(): 0,
	} {
		if got := ClampSeconds(f); got != want {
			t.Errorf("ClampSeconds(%v) = %v, want %v", f, got, want)
		}
	}
}

func TestAuthValue(t *testing.T) {
	if v := (Auth{Type: "bearer"}).Value("k"); v != "Bearer k" {
		t.Fatal(v)
	}
	if v := (Auth{Type: "header", Prefix: "Token "}).Value("k"); v != "Token k" {
		t.Fatal(v)
	}
	if v := (Auth{Type: "query"}).Value("k"); v != "k" {
		t.Fatal(v)
	}
}
