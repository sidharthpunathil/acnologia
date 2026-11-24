//go:build !windows

package ctl

import (
	"os"
	"os/exec"
	"syscall"
)

// forwardSignals are the signals acn run passes on to the command. A
// terminal's Ctrl+C reaches the whole foreground process group, the command
// included, so SIGINT also arrives directly; passing it on as well covers a
// kill aimed at acn alone.
var forwardSignals = []os.Signal{syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP, syscall.SIGQUIT, syscall.SIGUSR1, syscall.SIGUSR2}

func forwardSignal(p *os.Process, sig os.Signal) { _ = p.Signal(sig) }

// childExitCode is the command's exit code, or 128 plus the signal number
// when a signal ended it, as shells report it.
func childExitCode(ee *exec.ExitError) int {
	if ws, ok := ee.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
		return 128 + int(ws.Signal())
	}
	return ee.ExitCode()
}
