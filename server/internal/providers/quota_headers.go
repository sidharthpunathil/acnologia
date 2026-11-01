package providers

import (
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// ReadQuota reads the quota headers the spec names. It returns an empty
// reading when the spec names none or the response carries none.
func (s *Spec) ReadQuota(h http.Header) QuotaReading {
	qh := s.Quota.Headers
	if qh == nil || h == nil {
		return QuotaReading{}
	}
	var q QuotaReading
	if v, ok := headerNumber(h, qh.Limit, qh.Index); ok {
		n := int64(math.Round(v))
		q.Limit = &n
	}
	if v, ok := headerNumber(h, qh.Remaining, qh.Index); ok {
		n := int64(math.Round(v))
		q.Remaining = &n
	}
	if qh.Reset != "" {
		v := h.Get(qh.Reset)
		if qh.ResetFormat != "http_date" { // HTTP dates contain a comma themselves
			v = headerPart(v, qh.Index)
		}
		q.ResetAt = parseResetHeader(v, qh.ResetFormat)
	}
	return q
}

// headerPart picks entry i of a comma separated header value. A value with
// fewer entries falls back to its last entry.
func headerPart(v string, i int) string {
	v = strings.TrimSpace(v)
	if v == "" || i == 0 && !strings.Contains(v, ",") {
		return v
	}
	parts := strings.Split(v, ",")
	if i >= len(parts) {
		i = len(parts) - 1
	}
	return strings.TrimSpace(parts[i])
}

func headerNumber(h http.Header, name string, i int) (float64, bool) {
	if name == "" {
		return 0, false
	}
	p := headerPart(h.Get(name), i)
	if p == "" {
		return 0, false
	}
	// Some providers append a window, as in "100;w=60".
	if j := strings.IndexByte(p, ';'); j >= 0 {
		p = p[:j]
	}
	f, err := strconv.ParseFloat(strings.TrimSpace(p), 64)
	if err != nil || f < 0 || math.IsInf(f, 0) || math.IsNaN(f) {
		return 0, false
	}
	return f, true
}

// parseResetHeader turns a reset header into a time. "seconds" (the default)
// is a delay from now; a large number in seconds format is read as a unix
// time, since several providers send that under the same header name.
func parseResetHeader(v, format string) *time.Time {
	if v == "" {
		return nil
	}
	if j := strings.IndexByte(v, ';'); j >= 0 {
		v = v[:j]
	}
	v = strings.TrimSpace(v)
	now := time.Now()
	var t time.Time
	switch format {
	case "iso8601":
		p, err := time.Parse(time.RFC3339, v)
		if err != nil {
			return nil
		}
		t = p
	case "http_date":
		p, err := http.ParseTime(v)
		if err != nil {
			return nil
		}
		t = p
	case "unix", "unix_ms":
		f, err := strconv.ParseFloat(v, 64)
		if err != nil || f <= 0 {
			return nil
		}
		if format == "unix_ms" {
			f /= 1000
		}
		t = time.Unix(int64(f), 0)
	default:
		f, err := strconv.ParseFloat(v, 64)
		if err != nil || f < 0 {
			return nil
		}
		if f > 1e9 {
			t = time.Unix(int64(f), 0)
		} else {
			t = now.Add(ClampSeconds(f))
		}
	}
	// A reset in the past or absurdly far away is noise.
	if t.Before(now.Add(-time.Minute)) || t.After(now.Add(400*24*time.Hour)) {
		return nil
	}
	t = t.UTC()
	return &t
}

// BreakerSettings returns the circuit breaker settings with defaults filled:
// 10 provider errors within 60 seconds open it for 30 seconds.
func (s *Spec) BreakerSettings() (threshold int, window, reset time.Duration) {
	d := s.Defaults
	threshold, ws, rs := d.BreakerThreshold, d.BreakerWindowSec, d.BreakerResetSec
	if threshold == 0 {
		threshold = 10
	}
	if threshold < 0 {
		threshold = 0
	}
	if ws <= 0 {
		ws = 60
	}
	if rs <= 0 {
		rs = 30
	}
	return threshold, time.Duration(ws) * time.Second, time.Duration(rs) * time.Second
}
