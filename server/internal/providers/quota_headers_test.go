package providers

import (
	"net/http"
	"strconv"
	"testing"
	"time"
)

func TestReadQuotaBraveStyleLists(t *testing.T) {
	s := &Spec{Quota: QuotaInfo{Headers: &QuotaHeaders{
		Limit: "X-RateLimit-Limit", Remaining: "X-RateLimit-Remaining", Reset: "X-RateLimit-Reset", Index: 1,
	}}}
	h := http.Header{}
	h.Set("X-RateLimit-Limit", "1, 2000")
	h.Set("X-RateLimit-Remaining", "0, 1999")
	h.Set("X-RateLimit-Reset", "1, 3600")
	q := s.ReadQuota(h)
	if q.Limit == nil || *q.Limit != 2000 || q.Remaining == nil || *q.Remaining != 1999 {
		t.Fatalf("want the monthly entry, got %+v", q)
	}
	if q.ResetAt == nil || q.ResetAt.Sub(time.Now()) < 59*time.Minute {
		t.Fatalf("reset should be about an hour away, got %v", q.ResetAt)
	}
}

func TestReadQuotaFormats(t *testing.T) {
	future := time.Now().Add(2 * time.Hour).Truncate(time.Second).UTC()
	cases := []struct {
		format, value string
	}{
		{"unix", itoa64(future.Unix())},
		{"unix_ms", itoa64(future.UnixMilli())},
		{"iso8601", future.Format(time.RFC3339)},
		{"http_date", future.Format(http.TimeFormat)},
		{"", itoa64(future.Unix())}, // seconds format, but clearly a unix time
	}
	for _, c := range cases {
		s := &Spec{Quota: QuotaInfo{Headers: &QuotaHeaders{Reset: "R", ResetFormat: c.format}}}
		h := http.Header{}
		h.Set("R", c.value)
		q := s.ReadQuota(h)
		if q.ResetAt == nil || !q.ResetAt.Equal(future) {
			t.Fatalf("format %q value %q: got %v want %v", c.format, c.value, q.ResetAt, future)
		}
	}
}

func TestReadQuotaIgnoresJunk(t *testing.T) {
	s := &Spec{Quota: QuotaInfo{Headers: &QuotaHeaders{Remaining: "Rem", Reset: "R"}}}
	h := http.Header{}
	h.Set("Rem", "lots")
	h.Set("R", "-5")
	if q := s.ReadQuota(h); !q.Empty() {
		t.Fatalf("junk headers must be ignored, got %+v", q)
	}
	if q := (&Spec{}).ReadQuota(h); !q.Empty() {
		t.Fatal("specs without quota headers read nothing")
	}
	h.Set("Rem", "42;w=60")
	if q := s.ReadQuota(h); q.Remaining == nil || *q.Remaining != 42 {
		t.Fatalf("window suffix should be stripped, got %+v", q)
	}
}

func TestClassifyCarriesQuota(t *testing.T) {
	s := &Spec{UseStandardRules: true, Quota: QuotaInfo{Headers: &QuotaHeaders{Remaining: "X-Left"}}}
	h := http.Header{}
	h.Set("X-Left", "7")
	for _, status := range []int{200, 429, 503} {
		r := s.Classify(status, h, nil)
		if r.Quota.Remaining == nil || *r.Quota.Remaining != 7 {
			t.Fatalf("status %d: quota not read", status)
		}
	}
}

func TestBreakerSettingsDefaults(t *testing.T) {
	th, w, r := (&Spec{}).BreakerSettings()
	if th != 10 || w != time.Minute || r != 30*time.Second {
		t.Fatalf("defaults: %d %v %v", th, w, r)
	}
	th, _, _ = (&Spec{Defaults: Defaults{BreakerThreshold: -1}}).BreakerSettings()
	if th != 0 {
		t.Fatal("negative threshold turns the breaker off")
	}
}

func TestNewOutcomes(t *testing.T) {
	for _, o := range []Outcome{Busy, Banned} {
		if _, ok := ParseOutcome(string(o)); !ok || !o.Rotates() {
			t.Fatalf("%s should parse and rotate", o)
		}
	}
	if Busy.ProviderFault() || !Error.ProviderFault() {
		t.Fatal("only error is a provider fault")
	}
	for _, b := range Builtins() {
		rep := b.Spec.RunTests()
		if rep.Failed > 0 {
			t.Fatalf("%s builtin fixtures fail: %+v", b.Slug, rep.Results)
		}
		if errs := b.Spec.Validate(); len(errs) > 0 {
			t.Fatalf("%s builtin invalid: %v", b.Slug, errs)
		}
	}
}

func itoa64(n int64) string { return strconv.FormatInt(n, 10) }
