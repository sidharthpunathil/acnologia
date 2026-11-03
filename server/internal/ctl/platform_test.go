package ctl

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func envMap(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestConfigDirFor(t *testing.T) {
	cases := []struct {
		name, goos string
		env        map[string]string
		want       string
		fails      bool
	}{
		{"windows uses AppData", "windows", map[string]string{"APPDATA": `C:\Users\me\AppData\Roaming`, "HOME": `C:\Users\me`}, `C:\Users\me\AppData\Roaming`, false},
		{"windows ignores XDG", "windows", map[string]string{"APPDATA": `D:\profiles\me`, "XDG_CONFIG_HOME": "/x"}, `D:\profiles\me`, false},
		{"windows without AppData", "windows", map[string]string{"HOME": `C:\Users\me`}, "", true},
		{"darwin", "darwin", map[string]string{"HOME": "/Users/me"}, "/Users/me/Library/Application Support", false},
		{"linux XDG", "linux", map[string]string{"HOME": "/home/me", "XDG_CONFIG_HOME": "/cfg"}, "/cfg", false},
		{"linux relative XDG is ignored", "linux", map[string]string{"HOME": "/home/me", "XDG_CONFIG_HOME": "cfg"}, "/home/me/.config", false},
		{"linux HOME", "linux", map[string]string{"HOME": "/home/me"}, "/home/me/.config", false},
		{"freebsd HOME", "freebsd", map[string]string{"HOME": "/home/me"}, "/home/me/.config", false},
		{"linux nothing set", "linux", map[string]string{}, "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := configDirFor(tc.goos, envMap(tc.env))
			if tc.fails != (err != nil) || got != tc.want {
				t.Fatalf("got %q, %v; want %q (fails=%v)", got, err, tc.want, tc.fails)
			}
		})
	}
}

// configDirFor must agree with the standard library on the machine running
// the tests, whichever that is.
func TestConfigDirForMatchesOS(t *testing.T) {
	want, err := os.UserConfigDir()
	if err != nil {
		t.Skip(err)
	}
	got, err := configDirFor(runtime.GOOS, os.Getenv)
	if err != nil || filepath.Clean(got) != filepath.Clean(want) {
		t.Fatalf("configDirFor = %q, %v; os.UserConfigDir = %q", got, err, want)
	}
}

func TestBrowserCommand(t *testing.T) {
	link := "https://key.example.com/cli/device?code=AB-CD&next=/x"
	cases := []struct {
		goos string
		env  map[string]string
		want []string
	}{
		{"windows", nil, []string{"rundll32", "url.dll,FileProtocolHandler", link}},
		{"darwin", nil, []string{"open", link}},
		{"linux", map[string]string{"DISPLAY": ":0"}, []string{"xdg-open", link}},
		{"linux", map[string]string{"WAYLAND_DISPLAY": "wayland-0"}, []string{"xdg-open", link}},
		{"freebsd", map[string]string{"DISPLAY": ":0"}, []string{"xdg-open", link}},
		{"linux", nil, nil},
	}
	for _, tc := range cases {
		got, err := browserCommand(tc.goos, link, envMap(tc.env))
		if tc.want == nil {
			if err == nil {
				t.Errorf("%s %v: want an error without a display, got %v", tc.goos, tc.env, got)
			}
			continue
		}
		if err != nil || !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s %v: got %v, %v; want %v", tc.goos, tc.env, got, err, tc.want)
		}
		// The link is one argument, never parsed by a shell, so & survives.
		if got[len(got)-1] != link || strings.Contains(strings.Join(got[:len(got)-1], " "), "cmd") {
			t.Errorf("%s: link must be passed as is, without cmd: %v", tc.goos, got)
		}
	}
}

func TestWriteFileAtomicReplaces(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "ctl.json")
	if err := writeFileAtomic(p, []byte("one"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := writeFileAtomic(p, []byte("two"), 0o600); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(p)
	if err != nil || string(b) != "two" {
		t.Fatalf("got %q, %v", b, err)
	}
	ents, _ := os.ReadDir(dir)
	if len(ents) != 1 {
		t.Fatalf("temporary files left behind: %v", ents)
	}
	if runtime.GOOS != "windows" {
		if st, _ := os.Stat(p); st.Mode().Perm() != 0o600 {
			t.Fatalf("mode %v", st.Mode().Perm())
		}
	}
}
