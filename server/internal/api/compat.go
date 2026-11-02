package api

import "net/http"

// Names kept from before the product was renamed from Cogito to Acnologia.
// Requests accept both spellings and responses carry both, so existing
// clients, scripts and browser sessions keep working through the upgrade.
const (
	headerPrefix        = "X-Acnologia-"
	legacyHeaderPrefix  = "X-Cogito-"
	tokenParam          = "acn_token"
	legacyTokenParam    = "cogito_token"
	legacySessionCookie = "cogito_session"
)

// reqHeader reads X-Acnologia-<name>, falling back to X-Cogito-<name>.
func reqHeader(r *http.Request, name string) string {
	if v := r.Header.Get(headerPrefix + name); v != "" {
		return v
	}
	return r.Header.Get(legacyHeaderPrefix + name)
}

// setHeader sets X-Acnologia-<name> and, for existing clients that still read
// it, the old X-Cogito-<name>.
func setHeader(h http.Header, name, value string) {
	h.Set(headerPrefix+name, value)
	h.Set(legacyHeaderPrefix+name, value) // kept for existing clients
}

// delTokenParams removes the app token query parameters, new and old.
func delTokenParams(q interface{ Del(string) }) {
	q.Del(tokenParam)
	q.Del(legacyTokenParam)
}

// sessionFrom returns the session cookie value, accepting the old cookie
// name so nobody is signed out by the upgrade. New sessions only set the new one.
func sessionFrom(r *http.Request) string {
	if c, err := r.Cookie(sessionCookie); err == nil && c.Value != "" {
		return c.Value
	}
	if c, err := r.Cookie(legacySessionCookie); err == nil {
		return c.Value
	}
	return ""
}
