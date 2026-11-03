package providers

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

func refusingClient(called *bool) *http.Client {
	return &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		*called = true
		return nil, errors.New("sent")
	})}
}

func TestHealthPathRefusesDotSegments(t *testing.T) {
	for _, p := range []string{
		"/../../tenant", "/v1/../admin", "/./x", "/v1/%2e%2e/admin", "/%2E%2E/x",
		"/%2e%2e%2ftenant", "/a/.%2e/b", `/a\..\b`, "/v1/..?x=1",
	} {
		h := Health{Method: "GET", Path: p}
		s := Spec{BaseURL: "https://gw.example.com/tenant-a", Auth: Auth{Type: "bearer"}, Health: &h}
		if msg := h.PathProblem(s.BaseURL); msg == "" {
			t.Errorf("path %q: PathProblem accepted it", p)
		}
		bad := false
		for _, e := range s.Validate() {
			if strings.HasPrefix(e, "health.path") {
				bad = true
			}
		}
		if !bad {
			t.Errorf("path %q: Validate accepted it", p)
		}
		called := false
		if _, err := s.Check(context.Background(), refusingClient(&called), "", "sk-secret"); err == nil || called {
			t.Errorf("path %q: Check err=%v called=%v", p, err, called)
		}
	}
	for _, p := range []string{"/v1/models", "/a..b/c", "/.well-known/x", "/v1/%2e%2ex", "/account.json?x=../y"} {
		h := Health{Method: "GET", Path: p}
		if msg := h.PathProblem("https://gw.example.com/tenant-a"); msg != "" {
			t.Errorf("path %q refused: %s", p, msg)
		}
	}
}

func TestHealthFromOriginNeedsNoBasePath(t *testing.T) {
	h := Health{Method: "GET", Path: "/tenant-b/account", FromOrigin: true}
	if msg := h.PathProblem("https://gw.example.com/tenant-a"); msg == "" {
		t.Error("from_origin under a base path prefix was accepted")
	}
	if msg := h.PathProblem("https://gw.example.com/"); msg != "" {
		t.Errorf("from_origin with no base path refused: %s", msg)
	}
	s := Spec{BaseURL: "https://gw.example.com", Auth: Auth{Type: "bearer"}, Health: &h}
	if errs := s.Validate(); len(errs) > 0 {
		t.Errorf("from_origin with no base path invalid: %v", errs)
	}
	// The service's own base URL decides at check time, not the spec's.
	called := false
	if _, err := s.Check(context.Background(), refusingClient(&called), "https://gw.example.com/tenant-a", "sk-secret"); err == nil || called {
		t.Errorf("Check err=%v called=%v", err, called)
	}
}

func TestPeerHealthFromOriginStillWorks(t *testing.T) {
	spec := peerBuiltin().Spec
	if errs := spec.Validate(); len(errs) > 0 {
		t.Fatalf("peer spec invalid: %v", errs)
	}
	var got string
	c := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		got = r.URL.String()
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{}`)), Header: http.Header{}}, nil
	})}
	res, err := spec.Check(context.Background(), c, "https://owner.example.com/proxy/serp", "share-token")
	if err != nil || !res.Valid {
		t.Fatalf("peer check: res=%+v err=%v", res, err)
	}
	if got != "https://owner.example.com/api/peer/v1/manifest" {
		t.Fatalf("peer check went to %s", got)
	}
}
