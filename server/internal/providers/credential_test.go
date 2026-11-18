package providers

import (
	"encoding/base64"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"
)

func TestBasicCredentialAcceptsEveryPasteShape(t *testing.T) {
	a := Auth{Type: "basic"}
	want := "user1:pass-2345678"
	b64 := base64.StdEncoding.EncodeToString([]byte(want))
	for _, raw := range []string{want, " " + want + " ", b64, "Basic " + b64, "user1 pass-2345678", `{"login":"user1","password":"pass-2345678"}`, `{"username":"user1","api_password":"pass-2345678"}`} {
		got, err := a.NormalizeSecret(raw, nil)
		if err != nil || got != want {
			t.Errorf("%q: got %q, %v", raw, got, err)
		}
	}
	got, err := a.NormalizeSecret("", map[string]string{"login": " user1 ", "password": "pass-2345678"})
	if err != nil || got != want {
		t.Fatalf("parts: %q %v", got, err)
	}
	if _, err := a.NormalizeSecret("", map[string]string{"login": "user1"}); err == nil || !strings.Contains(err.Error(), "Password") {
		t.Fatalf("missing password: %v", err)
	}
	if _, err := a.NormalizeSecret("onlyonepart", nil); err == nil {
		t.Fatal("a single value is not a login and password")
	}
	if h := a.SecretHint(want); h != "...5678" {
		t.Fatalf("hint %q", h)
	}

	req, _ := http.NewRequest("GET", "https://api.example.com/v3", nil)
	q := url.Values{}
	a.Apply(req, q, want)
	if req.Header.Get("Authorization") != "Basic "+b64 || len(q) != 0 {
		t.Fatalf("applied: %v %v", req.Header, q)
	}
	if a.Value(want) != "Basic "+b64 {
		t.Fatalf("value %q", a.Value(want))
	}
	v := SecretVariants(want)
	if !slices.Contains(v, b64) || !slices.Contains(v, "pass-2345678") || slices.Contains(v, "user1") {
		t.Fatalf("variants %v", v)
	}
}

func TestFieldsCredentialPlacesEachPart(t *testing.T) {
	a := Auth{Type: "fields", Fields: []AuthField{
		{Key: "app_id", Label: "App ID", In: "query", Name: "app_id", Public: true},
		{Key: "app_key", Label: "App key", In: "header", Name: "X-App-Key", Prefix: "Key "},
	}}
	var errs []string
	a.validate(func(f string, args ...any) { errs = append(errs, f) })
	if len(errs) != 0 {
		t.Fatalf("valid auth refused: %v", errs)
	}
	sec, err := a.NormalizeSecret("", map[string]string{"app_key": "k-abcdefgh", "app_id": "id1"})
	if err != nil || sec != `{"app_id":"id1","app_key":"k-abcdefgh"}` {
		t.Fatalf("canonical %q %v", sec, err)
	}
	// Joined parts and a JSON object give the same stored text.
	for _, raw := range []string{"id1:k-abcdefgh", `{"app_key":"k-abcdefgh","app_id":"id1"}`} {
		if got, err := a.NormalizeSecret(raw, nil); err != nil || got != sec {
			t.Errorf("%q: %q %v", raw, got, err)
		}
	}
	req, _ := http.NewRequest("GET", "https://api.example.com/x", nil)
	q := url.Values{}
	a.Apply(req, q, sec)
	if q.Get("app_id") != "id1" || req.Header.Get("X-App-Key") != "Key k-abcdefgh" {
		t.Fatalf("applied: %v %v", q, req.Header)
	}
	if a.SecretHint(sec) != "...efgh" {
		t.Fatalf("hint %q", a.SecretHint(sec))
	}
	if !slices.Equal(a.HeaderNames(), []string{"X-App-Key"}) || !slices.Equal(a.QueryNames(), []string{"app_id"}) {
		t.Fatal("names")
	}
	if v := SecretVariants(sec); !slices.Contains(v, "k-abcdefgh") {
		t.Fatalf("variants %v", v)
	}

	bad := Auth{Type: "fields", Fields: []AuthField{{Key: "A", In: "body", Name: ""}, {Key: "A", In: "query", Name: "x"}}}
	errs = nil
	bad.validate(func(f string, args ...any) { errs = append(errs, f) })
	if len(errs) < 3 {
		t.Fatalf("bad fields accepted: %v", errs)
	}
}

func TestSingleKeyAuthUnchanged(t *testing.T) {
	a := Auth{Type: "header", Name: "X-Key"}
	if got, _ := a.NormalizeSecret("  abc:def  ", nil); got != "abc:def" {
		t.Fatalf("single key changed: %q", got)
	}
	req, _ := http.NewRequest("GET", "https://x.io", nil)
	a.Apply(req, url.Values{}, "k1")
	if req.Header.Get("X-Key") != "k1" {
		t.Fatal("header auth")
	}
	b := Auth{Type: "bearer"}
	req, _ = http.NewRequest("GET", "https://x.io", nil)
	b.Apply(req, url.Values{}, "k1")
	if req.Header.Get("Authorization") != "Bearer k1" {
		t.Fatal("bearer auth")
	}
}
