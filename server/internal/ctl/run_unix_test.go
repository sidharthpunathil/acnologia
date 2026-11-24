//go:build !windows

package ctl

import (
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestRunForwardsSignals(t *testing.T) {
	srv := runTestServer(t)
	withRoots(t)
	c, out, errb := runCLI(t, srv.URL, "")
	t.Setenv("ACN_APP_TOKEN", "acn_app")
	done := make(chan int, 1)
	go func() { done <- c.exec(helperArgs("wait")) }()
	deadline := time.Now().Add(15 * time.Second)
	for !strings.Contains(out.String(), "ready") {
		if time.Now().After(deadline) {
			t.Fatalf("the child never started: %q %q", out, errb)
		}
		time.Sleep(10 * time.Millisecond)
	}
	// A SIGTERM aimed at acn reaches the command, which exits 7 on it.
	if err := syscall.Kill(syscall.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	select {
	case code := <-done:
		if code != 7 {
			t.Fatalf("exit code %d, want 7: %q", code, errb)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("the signal was not forwarded")
	}
}
