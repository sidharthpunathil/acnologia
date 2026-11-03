//go:build !windows

package ctl

import "os"

// setPerm restricts the new config file before any token is written to it.
func setPerm(f *os.File, perm os.FileMode) error { return f.Chmod(perm) }

// replaceFile moves src over dst. rename(2) replaces dst atomically.
func replaceFile(src, dst string) error { return os.Rename(src, dst) }
