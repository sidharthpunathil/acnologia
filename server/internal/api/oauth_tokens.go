package api

// OAuth credentials, the token side: trading a code or a refresh token at
// the provider's token endpoint, keeping each key's access token current,
// and the pool's Resolver, which hands the proxy, leases and health checks
// an access token in place of the key's stored secret (oauth_connect.go has
// the sign in). Tokens are never logged: errors quote the provider's error
// code and description only, and everything stored goes through
// Store.Scrub, which knows every token (store/redact.go).

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/sidharthpunathil/acnologia/server/internal/crypto"
	"github.com/sidharthpunathil/acnologia/server/internal/keyscrub"
	"github.com/sidharthpunathil/acnologia/server/internal/pool"
	"github.com/sidharthpunathil/acnologia/server/internal/providers"
	"github.com/sidharthpunathil/acnologia/server/internal/store"
)

// oauthRefreshEarly refreshes an access token this long before it expires.
const oauthRefreshEarly = 60 * time.Second

// oauthTokenTimeout bounds one call to a token or userinfo endpoint.
const oauthTokenTimeout = 20 * time.Second

// oauthFailCooldown keeps a key out of rotation for a while after its
// refresh failed for a reason that may pass (the token endpoint was down).
const oauthFailCooldown = 30 * time.Second

// errOAuthReconnect means the provider refused the refresh token: the
// account must sign in again. The key is marked invalid.
var errOAuthReconnect = errors.New("the provider refused the refresh token: reconnect the account")

// oauthTokens is a token endpoint's answer.
type oauthTokens struct {
	AccessToken      string      `json:"access_token"`
	RefreshToken     string      `json:"refresh_token"`
	IDToken          string      `json:"id_token"`
	TokenType        string      `json:"token_type"`
	Scope            string      `json:"scope"`
	ExpiresIn        json.Number `json:"expires_in"`
	Error            string      `json:"error"`
	ErrorDescription string      `json:"error_description"`
}

// expiry is when the access token expires, zero when the provider gave no
// lifetime (the token lasts until it is refused).
func (t *oauthTokens) expiry(now time.Time) time.Time {
	n, err := strconv.ParseFloat(string(t.ExpiresIn), 64)
	if err != nil || n <= 0 {
		return time.Time{}
	}
	return now.Add(time.Duration(n) * time.Second)
}

// oauthTokenError is a refusal from the token endpoint, without the tokens
// or the request.
type oauthTokenError struct {
	Status      int
	Code        string
	Description string
}

func (e *oauthTokenError) Error() string {
	msg := "token endpoint answered " + strconv.Itoa(e.Status)
	if e.Code != "" {
		msg += ": " + e.Code
	}
	if e.Description != "" {
		msg += " (" + crypto.Truncate(e.Description, 200) + ")"
	}
	return msg
}

// invalidGrant reports whether the provider refused the grant itself: the
// refresh token was revoked, expired or rotated away.
func (e *oauthTokenError) invalidGrant() bool {
	return e.Code == "invalid_grant" || (e.Code == "" && e.Status == http.StatusUnauthorized)
}

// oauthClientSecret is the service's OAuth client with its secret.
type oauthClientSecret struct {
	ID, Secret string
	Scopes     []string
}

func (s *Server) loadOAuthClient(ctx context.Context, serviceID int64) (*oauthClientSecret, error) {
	c, err := s.Store.GetServiceOAuthClient(ctx, serviceID)
	if err != nil {
		return nil, err
	}
	out := &oauthClientSecret{ID: c.ClientID, Scopes: c.Scopes}
	if len(c.ClientSecretEnc) > 0 {
		if out.Secret, err = s.Pool.Box.Decrypt(c.ClientSecretEnc); err != nil {
			return nil, errors.New("cannot decrypt the OAuth client secret, is ACNOLOGIA_MASTER_KEY correct?")
		}
	}
	return out, nil
}

// oauthHTTP is the client for token and userinfo calls: the upstream
// client's dialer checks (no cloud metadata addresses) and no redirects.
func (s *Server) oauthHTTP() *http.Client {
	if s.UpClient != nil {
		return s.UpClient
	}
	return newUpstreamClient()
}

// oauthTokenCall posts a grant to the provider's token endpoint.
func (s *Server) oauthTokenCall(ctx context.Context, cfg *providers.OAuthConfig, cl *oauthClientSecret, form url.Values) (*oauthTokens, error) {
	ctx, cancel := context.WithTimeout(ctx, oauthTokenTimeout)
	defer cancel()
	if cfg.ClientAuth == "basic" {
		form.Del("client_secret")
	} else {
		form.Set("client_id", cl.ID)
		if cl.Secret != "" {
			form.Set("client_secret", cl.Secret)
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	if cfg.ClientAuth == "basic" {
		req.SetBasicAuth(url.QueryEscape(cl.ID), url.QueryEscape(cl.Secret))
	}
	resp, err := s.oauthHTTP().Do(req)
	if err != nil {
		// The transport error quotes the URL only; the form is not in it.
		return nil, fmt.Errorf("token endpoint unreachable: %s", keyscrub.Text(err.Error(), cl.Secret))
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var t oauthTokens
	ct, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if ct == "application/x-www-form-urlencoded" || ct == "text/plain" {
		if v, err := url.ParseQuery(string(body)); err == nil {
			t = oauthTokens{AccessToken: v.Get("access_token"), RefreshToken: v.Get("refresh_token"), IDToken: v.Get("id_token"),
				TokenType: v.Get("token_type"), Scope: v.Get("scope"), ExpiresIn: json.Number(v.Get("expires_in")),
				Error: v.Get("error"), ErrorDescription: v.Get("error_description")}
		}
	} else {
		_ = json.Unmarshal(body, &t)
	}
	if resp.StatusCode >= 400 || t.Error != "" || t.AccessToken == "" {
		e := &oauthTokenError{Status: resp.StatusCode, Code: t.Error, Description: t.ErrorDescription}
		// A description may quote what was sent; never let a secret through.
		e.Description = keyscrub.Text(keyscrub.Text(keyscrub.Text(e.Description, cl.Secret), form.Get("refresh_token")), form.Get("code"))
		if e.Code == "" && resp.StatusCode < 400 {
			e.Code = "no_access_token"
		}
		return nil, e
	}
	return &t, nil
}

// oauthLocks serialize the refresh of one key, so concurrent requests that
// find its token expiring refresh it once: the rest wait, then read the
// new token. Keyed by key id.
var oauthLocks sync.Map

func oauthKeyLock(keyID int64) *sync.Mutex {
	v, _ := oauthLocks.LoadOrStore(keyID, &sync.Mutex{})
	return v.(*sync.Mutex)
}

// oauthAccess returns a current access token for an OAuth key. It
// refreshes the token when it expires within a minute, or, with stale set,
// when the stored token is still the one the provider just refused. A
// refused refresh token marks the key invalid (it leaves rotation and the
// dashboard asks for a reconnect).
func (s *Server) oauthAccess(ctx context.Context, sv *store.Service, k *store.Key, stale string) (string, error) {
	mu := oauthKeyLock(k.ID)
	mu.Lock()
	defer mu.Unlock()
	cred, err := s.Store.GetOAuthCredential(ctx, k.ID)
	if err != nil {
		return "", fmt.Errorf("this key has no OAuth connection: %w", err)
	}
	access := ""
	if len(cred.AccessEnc) > 0 {
		if access, err = s.Pool.Box.Decrypt(cred.AccessEnc); err != nil {
			return "", errors.New("cannot decrypt the access token, is ACNOLOGIA_MASTER_KEY correct?")
		}
	}
	now := time.Now()
	fresh := access != "" && (cred.ExpiresAt.IsZero() || cred.ExpiresAt.Sub(now) > oauthRefreshEarly)
	if fresh && (stale == "" || access != stale) {
		return access, nil // current, or another request already refreshed it
	}
	refresh := ""
	if len(cred.RefreshEnc) > 0 {
		if refresh, err = s.Pool.Box.Decrypt(cred.RefreshEnc); err != nil {
			return "", errors.New("cannot decrypt the refresh token, is ACNOLOGIA_MASTER_KEY correct?")
		}
	}
	if refresh == "" {
		if access != "" && stale == "" && (cred.ExpiresAt.IsZero() || cred.ExpiresAt.After(now)) {
			return access, nil
		}
		s.oauthMarkReconnect(ctx, sv, k, "the provider gave no refresh token and the access token expired")
		return "", errOAuthReconnect
	}
	spec := providers.Get(sv.Provider)
	if spec.Auth.OAuth == nil {
		return "", errors.New("the provider has no oauth block")
	}
	cl, err := s.loadOAuthClient(ctx, sv.ID)
	if err != nil {
		return "", fmt.Errorf("the service's OAuth client is not set: %w", err)
	}
	form := url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refresh}}
	t, err := s.oauthTokenCall(ctx, spec.Auth.OAuth, cl, form)
	if err != nil {
		var te *oauthTokenError
		if errors.As(err, &te) && te.invalidGrant() {
			s.oauthMarkReconnect(ctx, sv, k, err.Error())
			return "", errOAuthReconnect
		}
		wctx := context.WithoutCancel(ctx)
		_ = s.Store.SetOAuthError(wctx, k.ID, "refresh failed: "+err.Error())
		if access != "" && stale == "" && (cred.ExpiresAt.IsZero() || cred.ExpiresAt.After(now)) {
			return access, nil // still good for a few seconds
		}
		until := now.Add(oauthFailCooldown)
		_ = s.Store.ApplyEffect(wctx, k.ID, store.KeyEffect{CooldownUntil: &until, LastError: ptrStr("token refresh failed: " + err.Error())})
		return "", err
	}
	accessEnc, err := s.Pool.Box.Encrypt(t.AccessToken)
	if err != nil {
		return "", err
	}
	var refreshEnc []byte
	if t.RefreshToken != "" && t.RefreshToken != refresh {
		// The provider rotated the refresh token; the old one may already
		// be dead, so the new one must be kept.
		if refreshEnc, err = s.Pool.Box.Encrypt(t.RefreshToken); err != nil {
			return "", err
		}
	}
	if err := s.Store.UpdateOAuthTokens(context.WithoutCancel(ctx), k.ID, accessEnc, refreshEnc, t.expiry(now)); err != nil {
		return "", err
	}
	return t.AccessToken, nil
}

func ptrStr(s string) *string { return &s }

// oauthMarkReconnect takes a key whose grant is gone out of rotation.
func (s *Server) oauthMarkReconnect(ctx context.Context, sv *store.Service, k *store.Key, why string) {
	wctx := context.WithoutCancel(ctx)
	_ = s.Store.SetOAuthError(wctx, k.ID, "reconnect needed: "+why)
	_ = s.Pool.Apply(wctx, sv, k, providers.Result{Outcome: providers.Invalid, Message: "reconnect the account: " + why})
	s.Store.AddEvent(wctx, store.NewEvent{Kind: "invalid", ServiceID: sv.ID, KeyID: k.ID, Detail: "oauth: reconnect needed: " + why})
}

// oauthResolve is the pool's Resolver: the wire secret of an OAuth key, its
// current access token with the key's extra fields.
func (s *Server) oauthResolve(ctx context.Context, sv *store.Service, k *store.Key, stored string) (string, error) {
	access, err := s.oauthAccess(ctx, sv, k, "")
	if err != nil {
		return "", err
	}
	return providers.OAuthWireSecret(access, providers.OAuthFields(stored)), nil
}

// oauthRetryUnauthorized answers a provider's 401 to an OAuth key: it
// refreshes the access token once and sends the request again. It returns
// the answer to use, and updates picked.Secret to the credential that got
// it, so the scrub covers the token actually sent. When no fresh token can
// be had, the 401 stands and the rules mark the key.
func (s *Server) oauthRetryUnauthorized(ctx context.Context, r *http.Request, sv *store.Service, picked *pool.Picked,
	path string, body []byte, client *http.Client, resp *http.Response) *http.Response {
	if sv.AuthType != providers.AuthOAuth2 || resp.StatusCode != http.StatusUnauthorized {
		return resp
	}
	wire, ok := s.oauthFreshWire(ctx, sv, picked)
	if !ok {
		return resp
	}
	req, err := buildUpstream(ctx, r, sv, path, body, wire)
	if err != nil {
		return resp
	}
	again, err := client.Do(req)
	if err != nil {
		return resp
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
	resp.Body.Close()
	picked.Secret = wire
	return again
}

// oauthFreshWire is the refresh half of the 401 retry, shared by the proxy
// and upstream MCP calls (mcp_upstream.go). The provider refused the access
// token in picked.Secret: it refreshes the key's token once and returns the
// new wire secret. ok is false for a key that is not an OAuth key, or when
// no fresh token could be had; a refused refresh token (invalid_grant) has
// then already marked the key invalid (oauthAccess).
func (s *Server) oauthFreshWire(ctx context.Context, sv *store.Service, picked *pool.Picked) (string, bool) {
	if sv.AuthType != providers.AuthOAuth2 || picked == nil || picked.Key == nil {
		return "", false
	}
	stored, err := s.Pool.Box.Decrypt(picked.Key.SecretEnc)
	if err != nil {
		return "", false
	}
	stale := providers.OAuthWireParts(picked.Secret)["access_token"]
	access, err := s.oauthAccess(ctx, sv, picked.Key, stale)
	if err != nil || access == "" || access == stale {
		return "", false
	}
	return providers.OAuthWireSecret(access, providers.OAuthFields(stored)), true
}

// idTokenClaim reads a claim from an id_token's payload. The token came
// straight from the provider's token endpoint over TLS, which OpenID
// Connect accepts in place of checking its signature; it only names the
// account, and grants nothing.
func idTokenClaim(idToken, claim string) string {
	parts := strings.Split(idToken, ".")
	if len(parts) != 3 {
		return ""
	}
	b, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(parts[1], "="))
	if err != nil {
		return ""
	}
	var m map[string]any
	if json.Unmarshal(b, &m) != nil {
		return ""
	}
	v, _ := providers.JSONPath(m, claim)
	return accountString(v)
}

func accountString(v any) string {
	switch x := v.(type) {
	case string:
		return crypto.Truncate(strings.TrimSpace(x), 200)
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	}
	return ""
}

// oauthAccount names the account that signed in: the id_token claim, or
// the userinfo endpoint's field when the spec has one.
func (s *Server) oauthAccount(ctx context.Context, cfg *providers.OAuthConfig, t *oauthTokens) string {
	if t.IDToken != "" {
		if a := idTokenClaim(t.IDToken, cfg.Account()); a != "" {
			return a
		}
	}
	if cfg.UserinfoURL == "" {
		return ""
	}
	ctx, cancel := context.WithTimeout(ctx, oauthTokenTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, cfg.UserinfoURL, nil)
	if err != nil {
		return ""
	}
	req.Header.Set("Authorization", "Bearer "+t.AccessToken)
	req.Header.Set("Accept", "application/json")
	resp, err := s.oauthHTTP().Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return ""
	}
	var m any
	if json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&m) != nil {
		return ""
	}
	v, _ := providers.JSONPath(m, cfg.Account())
	return accountString(v)
}
