//go:build windows

package ctl

import (
	"errors"
	"os"
	"syscall"
	"time"
)

// setPerm does nothing on Windows: Unix modes do not apply, and the file
// inherits the ACL of the folder it is created in (%AppData% is private to
// the user).
func setPerm(*os.File, os.FileMode) error { return nil }

// Windows error codes returned while another process (an editor, a virus
// scanner, a backup tool) briefly holds the target open.
const (
	errorAccessDenied     syscall.Errno = 5
	errorSharingViolation syscall.Errno = 32
)

// replaceFile moves src over dst. os.Rename uses MoveFileEx with
// MOVEFILE_REPLACE_EXISTING, which fails while dst is open elsewhere, so a
// short retry rides out scanners and indexers that hold it for a moment.
func replaceFile(src, dst string) error {
	var err error
	for i := 0; i < 10; i++ {
		if err = os.Rename(src, dst); err == nil {
			return nil
		}
		if !errors.Is(err, errorAccessDenied) && !errors.Is(err, errorSharingViolation) {
			return err
		}
		time.Sleep(25 * time.Millisecond * time.Duration(i+1))
	}
	return err
}
