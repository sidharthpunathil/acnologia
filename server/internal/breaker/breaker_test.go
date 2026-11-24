package breaker

import (
	"testing"
	"time"
)

type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

func newTest() (*Set, *clock, *[]bool) {
	c := &clock{t: time.Unix(1_000_000, 0)}
	s := New()
	s.now = c.now
	// These tests fail at one instant on purpose; TestBurstIsOneStrike
	// covers the spacing.
	s.Spacing = 0
	var changes []bool
	s.OnChange = func(_ int64, down bool, _ string) { changes = append(changes, down) }
	return s, c, &changes
}

var cfg = Settings{Threshold: 3, Window: time.Minute, Reset: 30 * time.Second}

func TestOpensAfterThresholdInsideWindow(t *testing.T) {
	s, c, changes := newTest()
	s.Failure(1, cfg, "503")
	s.Failure(1, cfg, "503")
	if ok, _ := s.Allow(1, cfg); !ok {
		t.Fatal("should still allow below the threshold")
	}
	s.Failure(1, cfg, "503")
	ok, ra := s.Allow(1, cfg)
	if ok || ra != 30*time.Second {
		t.Fatalf("want open with 30s retry, got ok=%v retry=%v", ok, ra)
	}
	if len(*changes) != 1 || !(*changes)[0] {
		t.Fatalf("want one down change, got %v", *changes)
	}
	c.t = c.t.Add(10 * time.Second)
	if _, ra := s.Allow(1, cfg); ra != 20*time.Second {
		t.Fatalf("retry should count down, got %v", ra)
	}
}

func TestOldFailuresFallOutOfWindow(t *testing.T) {
	s, c, _ := newTest()
	s.Failure(1, cfg, "x")
	s.Failure(1, cfg, "x")
	c.t = c.t.Add(2 * time.Minute)
	s.Failure(1, cfg, "x")
	if ok, _ := s.Allow(1, cfg); !ok {
		t.Fatal("failures outside the window must not count")
	}
}

func TestHalfOpenLetsOneProbeThrough(t *testing.T) {
	s, c, changes := newTest()
	for range 3 {
		s.Failure(1, cfg, "x")
	}
	c.t = c.t.Add(31 * time.Second)
	if ok, _ := s.Allow(1, cfg); !ok {
		t.Fatal("first request after reset should be the probe")
	}
	if ok, _ := s.Allow(1, cfg); ok {
		t.Fatal("only one probe at a time")
	}
	s.Success(1)
	if ok, _ := s.Allow(1, cfg); !ok {
		t.Fatal("a good probe closes the breaker")
	}
	if got := *changes; len(got) != 2 || got[1] {
		t.Fatalf("want down then up, got %v", got)
	}
}

func TestFailedProbeReopens(t *testing.T) {
	s, c, _ := newTest()
	for range 3 {
		s.Failure(1, cfg, "x")
	}
	c.t = c.t.Add(31 * time.Second)
	s.Allow(1, cfg)
	s.Failure(1, cfg, "still down")
	ok, ra := s.Allow(1, cfg)
	if ok || ra != 30*time.Second {
		t.Fatalf("failed probe should reopen for a full reset, got ok=%v retry=%v", ok, ra)
	}
}

func TestAbortFreesTheProbe(t *testing.T) {
	s, c, _ := newTest()
	for range 3 {
		s.Failure(1, cfg, "x")
	}
	c.t = c.t.Add(31 * time.Second)
	s.Allow(1, cfg)
	s.Abort(1)
	if ok, _ := s.Allow(1, cfg); !ok {
		t.Fatal("an aborted probe should let the next request probe")
	}
}

func TestDisabled(t *testing.T) {
	s, _, _ := newTest()
	off := Settings{}
	for range 100 {
		s.Failure(1, off, "x")
	}
	if ok, _ := s.Allow(1, off); !ok {
		t.Fatal("threshold 0 disables the breaker")
	}
}

func TestServicesAreIndependent(t *testing.T) {
	s, _, _ := newTest()
	for range 3 {
		s.Failure(1, cfg, "x")
	}
	if ok, _ := s.Allow(2, cfg); !ok {
		t.Fatal("service 2 is unaffected")
	}
}

func TestBurstIsOneStrike(t *testing.T) {
	s, c, _ := newTest()
	s.Spacing = DefaultSpacing
	// Ten concurrent requests failing together are one strike.
	for range 10 {
		s.Failure(1, cfg, "503")
		c.t = c.t.Add(50 * time.Millisecond)
	}
	if ok, _ := s.Allow(1, cfg); !ok {
		t.Fatal("one burst of failures must not open the breaker")
	}
	if st := s.Status(1, cfg); st.Failures != 1 {
		t.Fatalf("want 1 strike, got %d", st.Failures)
	}
	// Failures a second or more apart are separate strikes.
	c.t = c.t.Add(time.Second)
	s.Failure(1, cfg, "503")
	c.t = c.t.Add(time.Second)
	s.Failure(1, cfg, "503")
	if ok, _ := s.Allow(1, cfg); ok {
		t.Fatal("three spaced failures should open the breaker")
	}
}
