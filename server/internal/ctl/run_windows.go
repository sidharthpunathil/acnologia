//go:build windows

package ctl

import (
	"os"
	"os/exec"
)

// forwardSignals: Windows sends Ctrl+C to every process on the console, the
// command included, so acn run only has to stay alive while the command
// handles it. Go cannot deliver an interrupt to another process there.
var forwardSignals = []os.Signal{os.Interrupt}

func forwardSignal(*os.Process, os.Signal) {}

func childExitCode(ee *exec.ExitError) int { return ee.ExitCode() }
