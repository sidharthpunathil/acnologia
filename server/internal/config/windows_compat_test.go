package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadDotEnvCRLF(t *testing.T) {
	names := []string{"ACNTEST_PLAIN", "ACNTEST_QUOTED", "ACNTEST_EXPORTED", "ACNTEST_SINGLE", "ACNTEST_LAST"}
	for _, n := range names {
		t.Setenv(n, "")
		os.Unsetenv(n)
	}
	body := "\ufeffACNTEST_PLAIN=one\r\n" +
		"# a comment\r\n" +
		"\r\n" +
		"ACNTEST_QUOTED=\"two words\"\r\n" +
		"export ACNTEST_EXPORTED = three\r\n" +
		"ACNTEST_SINGLE='C:\\Users\\me\\acnologia data'\r\n" +
		"ACNTEST_LAST=four"
	p := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	LoadDotEnv(p)
	want := map[string]string{
		"ACNTEST_PLAIN":    "one",
		"ACNTEST_QUOTED":   "two words",
		"ACNTEST_EXPORTED": "three",
		"ACNTEST_SINGLE":   `C:\Users\me\acnologia data`,
		"ACNTEST_LAST":     "four",
	}
	for k, v := range want {
		if got := os.Getenv(k); got != v {
			t.Errorf("%s = %q, want %q", k, got, v)
		}
	}
}

func TestDefaultDataDirFor(t *testing.T) {
	cases := []struct {
		goos string
		env  map[string]string
		want string
	}{
		{"windows", map[string]string{"LOCALAPPDATA": `C:\Users\me\AppData\Local`}, `C:\Users\me\AppData\Local\Acnologia`},
		{"windows", map[string]string{"LOCALAPPDATA": `C:\Users\me\AppData\Local\`}, `C:\Users\me\AppData\Local\Acnologia`},
		{"windows", map[string]string{}, "./data"},
		{"linux", map[string]string{"LOCALAPPDATA": `C:\x`}, "./data"},
		{"darwin", map[string]string{"HOME": "/Users/me"}, "./data"},
	}
	for _, tc := range cases {
		got := defaultDataDirFor(tc.goos, func(k string) string { return tc.env[k] })
		if got != tc.want {
			t.Errorf("%s %v: got %q, want %q", tc.goos, tc.env, got, tc.want)
		}
	}
}

func TestDataDirEnvWins(t *testing.T) {
	t.Setenv("ACNOLOGIA_DATA_DIR", `D:\acnologia`)
	if got := Load("test").DataDir; got != `D:\acnologia` {
		t.Fatalf("DataDir = %q", got)
	}
}
