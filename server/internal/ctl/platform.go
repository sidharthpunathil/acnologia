package ctl

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// configDirFor is os.UserConfigDir for a given GOOS and environment, split
// out so every platform's answer can be tested anywhere:
//
//	windows  %AppData%                      (C:\Users\me\AppData\Roaming)
//	darwin   $HOME/Library/Application Support
//	others   $XDG_CONFIG_HOME, else $HOME/.config
//
// The client keeps its files in an "acnologia" folder below it.
func configDirFor(goos string, getenv func(string) string) (string, error) {
	switch goos {
	case "windows":
		if d := getenv("APPDATA"); d != "" {
			return d, nil
		}
		return "", errors.New("%AppData% is not set")
	case "darwin", "ios":
		if h := getenv("HOME"); h != "" {
			return h + "/Library/Application Support", nil
		}
		return "", errors.New("$HOME is not set")
	case "plan9":
		if h := getenv("home"); h != "" {
			return h + "/lib", nil
		}
		return "", errors.New("$home is not set")
	default:
		// Like os.UserConfigDir, a relative XDG_CONFIG_HOME is ignored.
		if d := getenv("XDG_CONFIG_HOME"); strings.HasPrefix(d, "/") {
			return d, nil
		}
		if h := getenv("HOME"); h != "" {
			return h + "/.config", nil
		}
		return "", errors.New("neither $XDG_CONFIG_HOME nor $HOME is set")
	}
}

// browserCommand is the command that opens link in the default browser on
// goos. Windows uses rundll32 rather than `cmd /c start`, because cmd treats
// the & in a query string as a command separator. A Linux or BSD box without
// a display (a server over ssh) has no browser to open.
func browserCommand(goos, link string, getenv func(string) string) ([]string, error) {
	switch goos {
	case "darwin":
		return []string{"open", link}, nil
	case "windows":
		return []string{"rundll32", "url.dll,FileProtocolHandler", link}, nil
	default:
		if getenv("DISPLAY") == "" && getenv("WAYLAND_DISPLAY") == "" {
			return nil, errors.New("no display")
		}
		return []string{"xdg-open", link}, nil
	}
}

// writeFileAtomic writes data to a temporary file next to path and moves it
// into place, so a crash or a full disk never leaves a half written config
// holding tokens. perm applies where the OS has Unix permissions; on Windows
// the file inherits the folder's ACL, which under %AppData% is the user's.
func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	f, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	ok := false
	defer func() {
		if !ok {
			_ = os.Remove(tmp)
		}
	}()
	if err := setPerm(f, perm); err != nil {
		f.Close()
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := replaceFile(tmp, path); err != nil {
		return err
	}
	ok = true
	return nil
}
