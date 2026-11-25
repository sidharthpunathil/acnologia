package providers

import (
	"strings"
	"testing"
)

// TestOAuthBuiltins: every built in OAuth provider is valid, has a logo,
// and its block reads as the provider documents it.
func TestOAuthBuiltins(t *testing.T) {
	specs := map[string]Spec{}
	for _, b := range Builtins() {
		if b.Spec.Auth.Type != AuthOAuth2 {
			continue
		}
		specs[b.Slug] = b.Spec
		if errs := b.Spec.Validate(); len(errs) > 0 {
			t.Errorf("%s: %v", b.Slug, errs)
		}
		if logo, mime := SeedLogo(b.Slug); logo == nil || mime == "" {
			t.Errorf("%s has no seed logo", b.Slug)
		}
		if !strings.HasPrefix(b.Spec.Auth.OAuth.TokenURL, "https://") || !strings.HasPrefix(b.Spec.Auth.OAuth.AuthorizeURL, "https://") {
			t.Errorf("%s: OAuth URLs must be https", b.Slug)
		}
	}
	for _, slug := range []string{"google", "google-ads", "microsoft-graph", "slack", "hubspot", "linkedin"} {
		if _, ok := specs[slug]; !ok {
			t.Errorf("no built in OAuth provider %s", slug)
		}
	}

	// Microsoft signs in through the service's tenant, common by default.
	ms := specs["microsoft-graph"].Auth.OAuth
	if !ms.HasTenant() || ms.Tenant != "common" ||
		ms.ForTenant("").TokenURL != "https://login.microsoftonline.com/common/oauth2/v2.0/token" ||
		ms.ForTenant("contoso.onmicrosoft.com").AuthorizeURL != "https://login.microsoftonline.com/contoso.onmicrosoft.com/oauth2/v2.0/authorize" ||
		ms.ForTenant("../x").TokenURL != "https://login.microsoftonline.com/common/oauth2/v2.0/token" {
		t.Fatalf("microsoft tenant: %+v", ms)
	}

	// Google Ads paths use the version set in one place.
	for _, e := range specs["google-ads"].Examples {
		if !strings.HasPrefix(e.Path, "/"+GoogleAdsAPIVersion+"/") {
			t.Errorf("google ads example %s uses %s", e.Name, e.Path)
		}
	}

	// Slack: comma separated scopes, the bot token at the top of the
	// answer, and a refused token on HTTP 200 marks the key.
	sl := specs["slack"]
	if sl.Auth.OAuth.Separator() != "," || sl.Auth.OAuth.TokenPath != "access_token" {
		t.Fatalf("slack oauth %+v", sl.Auth.OAuth)
	}
	if r := sl.Classify(200, nil, []byte(`{"ok":false,"error":"token_revoked"}`)); r.Outcome != Invalid {
		t.Fatalf("slack revoked token: %v", r.Outcome)
	}
	if r := sl.Classify(200, nil, []byte(`{"ok":true,"messages":[{"text":"we saw invalid_auth earlier"}]}`)); r.Outcome != OK {
		t.Fatalf("slack message text: %v", r.Outcome)
	}
}

func TestOAuthBlockValidation(t *testing.T) {
	base := func(c OAuthConfig) []string {
		s := Spec{BaseURL: "https://api.example.com", Auth: Auth{Type: AuthOAuth2, Name: "Authorization", OAuth: &c}}
		var errs []string
		s.Auth.validateOAuth(func(f string, a ...any) { errs = append(errs, f) })
		return errs
	}
	ok := OAuthConfig{AuthorizeURL: "https://idp.example/{tenant}/authorize", TokenURL: "https://idp.example/{tenant}/token", Tenant: "common", TokenPath: "authed_user.access_token"}
	if errs := base(ok); len(errs) > 0 {
		t.Fatalf("valid block refused: %v", errs)
	}
	noDefault := ok
	noDefault.Tenant = ""
	stray := OAuthConfig{AuthorizeURL: "https://idp.example/a", TokenURL: "https://idp.example/t", Tenant: "common"}
	badPath := ok
	badPath.TokenPath = "a..b"
	for name, c := range map[string]OAuthConfig{"no default tenant": noDefault, "tenant without placeholder": stray, "bad token path": badPath} {
		if errs := base(c); len(errs) == 0 {
			t.Errorf("%s: accepted", name)
		}
	}
	for tenant, want := range map[string]bool{"common": true, "72f988bf-86f1-41af-91ab-2d7cd011db47": true, "contoso.onmicrosoft.com": true,
		"": false, "../x": false, "a/b": false, "a..b": false, "x?y": false} {
		if ValidTenant(tenant) != want {
			t.Errorf("ValidTenant(%q) != %v", tenant, want)
		}
	}
}
