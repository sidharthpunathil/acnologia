package config

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// AccessPasswordFile is the file in the data dir that holds a generated
// access password.
const AccessPasswordFile = "access-password"

// LoadOrCreateAccessPassword returns the access password stored in
// dataDir/access-password, generating a random one (and the file, mode 0600)
// when there is none. A host that requires the gate but was never given
// ACNOLOGIA_ACCESS_PASSWORD then comes up locked instead of crashing, and the
// operator reads the password off the volume. The password is never logged.
func LoadOrCreateAccessPassword(dataDir string) (password string, created bool, err error) {
	path := filepath.Join(dataDir, AccessPasswordFile)
	b, err := os.ReadFile(path)
	if err == nil {
		// TrimSpace takes the CRLF Notepad adds; a byte order mark goes too.
		p := strings.TrimSpace(strings.TrimPrefix(string(b), "\ufeff"))
		if len(p) < MinAccessPasswordLen {
			return "", false, fmt.Errorf("%s is shorter than %d characters: delete it to get a new one, or set ACNOLOGIA_ACCESS_PASSWORD", path, MinAccessPasswordLen)
		}
		return p, false, nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return "", false, err
	}
	raw := make([]byte, 24)
	if _, err := rand.Read(raw); err != nil {
		return "", false, err
	}
	p := base64.RawURLEncoding.EncodeToString(raw)
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", false, err
	}
	if _, err := f.WriteString(p + "\n"); err != nil {
		f.Close()
		return "", false, err
	}
	if err := f.Close(); err != nil {
		return "", false, err
	}
	return p, true, nil
}
