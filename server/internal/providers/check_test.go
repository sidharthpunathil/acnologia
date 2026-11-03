package providers

import (
	"context"
	"errors"
	"net/http"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestCheckRefusesPathsThatLeaveTheHost(t *testing.T) {
	for _, h := range []Health{
		{Method: "GET", Path: "@attacker.test/c"},
		{Method: "GET", Path: ".attacker.test/c"},
		{Method: "GET", Path: "@attacker.test/c", FromOrigin: true},
	} {
		s := Spec{BaseURL: "https://api.example.com", Auth: Auth{Type: "bearer"}, Health: &h}
		called := false
		c := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			called = true
			return nil, errors.New("sent")
		})}
		if _, err := s.Check(context.Background(), c, "", "sk-secret"); err == nil || called {
			t.Errorf("path %q: err=%v called=%v", h.Path, err, called)
		}
	}
}
