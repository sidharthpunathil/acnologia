package api

import (
	"errors"
	"testing"

	"github.com/sidharthpunathil/acnologia/server/internal/providers"
	"github.com/sidharthpunathil/acnologia/server/internal/store"
)

func TestServiceURLPicksProviderHost(t *testing.T) {
	providers.Set("hosts-test", &providers.Spec{
		BaseURL: "https://google-maps-scraper.omkar.cloud",
		Hosts:   []providers.Host{{Name: "tiktok-scraper", URL: "https://tiktok-scraper.omkar.cloud"}},
	})
	sv := &store.Service{Provider: "hosts-test", BaseURL: "https://google-maps-scraper.omkar.cloud"}

	got, err := serviceURL(sv, "~tiktok-scraper/videos/details", nil)
	if err != nil || got != "https://tiktok-scraper.omkar.cloud/videos/details" {
		t.Fatalf("host path: %q %v", got, err)
	}
	got, err = serviceURL(sv, "search", nil)
	if err != nil || got != "https://google-maps-scraper.omkar.cloud/search" {
		t.Fatalf("plain path: %q %v", got, err)
	}
	if _, err = serviceURL(sv, "~nope/x", nil); !errors.Is(err, providers.ErrUnknownHost) {
		t.Fatalf("unknown host: %v", err)
	}
	// Dot segments stay refused after the host prefix.
	if _, err = serviceURL(sv, "~tiktok-scraper/../admin", nil); err == nil {
		t.Fatal("a dot segment passed after a host prefix")
	}
}
