package api

import (
	"bufio"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

// A read-only token shares its app's leases with the app's other tokens. It
// must not renew, report or rotate one: a rotation hands back a raw key, and
// a refused renewal used to end the sibling's lease.
func TestReadOnlyTokenCannotTouchSiblingLease(t *testing.T) {
	e := newEnv(t)
	e.addKeys(map[string]any{"secret": "k1"}, map[string]any{"secret": "k2"})
	id, full := e.app(map[string]any{"name": "site"})
	ro := e.newAppToken(id, map[string]any{"name": "viewer", "read_only": true}).Token
	if ro == "" {
		t.Fatal("no read-only token")
	}
	fullH := []string{"Authorization", "Bearer " + full}
	roH := []string{"Authorization", "Bearer " + ro}

	var l leaseDTO
	e.do(http.DefaultClient, "POST", "/api/v1/lease", map[string]any{"service": "serpapi"}, 200, &l, fullH...)
	if l.LeaseID == "" || l.Secret == "" {
		t.Fatalf("full token should lease: %+v", l)
	}

	// Report with rotate: refused, and no secret comes back.
	var rep map[string]any
	e.do(http.DefaultClient, "POST", "/api/v1/leases/"+l.LeaseID+"/report",
		map[string]any{"outcome": "rate_limited", "rotate": true}, 403, &rep, roH...)
	if errCode(rep) != "read_only_token" || rep["next"] != nil {
		t.Fatalf("read-only report should be refused with read_only_token: %v", rep)
	}

	// Renew: refused, and the sibling's lease is not revoked.
	var ren map[string]any
	e.do(http.DefaultClient, "POST", "/api/v1/leases/"+l.LeaseID+"/renew", map[string]any{"ttl_sec": 60}, 403, &ren, roH...)
	if errCode(ren) != "read_only_token" {
		t.Fatalf("read-only renew should be refused with read_only_token: %v", ren)
	}
	var outcome string
	if err := e.st.DB.QueryRow(`SELECT COALESCE(outcome,'') FROM leases WHERE id=?`, l.LeaseID).Scan(&outcome); err != nil {
		t.Fatal(err)
	}
	if outcome != "" {
		t.Fatalf("a refused read-only call must leave the lease open, got outcome %q", outcome)
	}

	// The full token still owns its lease.
	e.do(http.DefaultClient, "POST", "/api/v1/leases/"+l.LeaseID+"/renew", map[string]any{"ttl_sec": 60}, 200, nil, fullH...)
	e.do(http.DefaultClient, "POST", "/api/v1/leases/"+l.LeaseID+"/report", map[string]any{"outcome": "ok"}, 200, nil, fullH...)
}

// connectStatus sends a raw CONNECT through the forward proxy and returns
// the status code from the answer's first line.
func (e *env) connectStatus(tok, authority string) string {
	e.t.Helper()
	conn, err := net.Dial("tcp", strings.TrimPrefix(e.srv.URL, "http://"))
	if err != nil {
		e.t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(15 * time.Second))
	_, _ = io.WriteString(conn, "CONNECT "+authority+" HTTP/1.1\r\nHost: "+authority+"\r\nProxy-Authorization: Bearer "+tok+"\r\n\r\n")
	line, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil {
		e.t.Fatalf("CONNECT %s: %v", authority, err)
	}
	f := strings.Fields(line)
	if len(f) < 2 {
		e.t.Fatalf("CONNECT %s: %q", authority, line)
	}
	return f[1]
}

// Passthrough is a general relay to hosts that are no service. A read-only
// token is for reads through known services, so it is refused like a
// share's token.
func TestReadOnlyTokenGetsNoPassthrough(t *testing.T) {
	e := newEnv(t)
	e.addKeys(map[string]any{"secret": "k1"})
	e.api.Cfg.ForwardPassthrough = true
	id, full := e.app(map[string]any{"name": "site"})
	ro := e.newAppToken(id, map[string]any{"name": "viewer", "read_only": true}).Token

	// 127.0.0.1:443 is no service. The full token's tunnel is attempted (and
	// then refused by the public address check, 502); the read-only token
	// is refused before that.
	if got := e.connectStatus(full, "127.0.0.1:443"); got != "502" {
		t.Fatalf("full token CONNECT: want 502 from the tunnel, got %s", got)
	}
	if got := e.connectStatus(ro, "127.0.0.1:443"); got != "403" {
		t.Fatalf("read-only token CONNECT: want 403, got %s", got)
	}

	// The setup says so too, and suggests NO_PROXY and a CA bundle.
	var setup struct {
		Passthrough bool              `json:"passthrough"`
		Env         map[string]string `json:"env"`
		CABundle    map[string]string `json:"ca_bundle"`
	}
	e.do(http.DefaultClient, "GET", "/api/v1/proxy", nil, 200, &setup, "Authorization", "Bearer "+ro)
	if setup.Passthrough {
		t.Fatal("a read-only token's setup should say passthrough is off")
	}
	e.do(http.DefaultClient, "GET", "/api/v1/proxy", nil, 200, &setup, "Authorization", "Bearer "+full)
	if !setup.Passthrough {
		t.Fatal("a full token's setup should say passthrough is on")
	}
	if setup.Env["NO_PROXY"] != "localhost,127.0.0.1,::1" || setup.Env["no_proxy"] != setup.Env["NO_PROXY"] {
		t.Fatalf("setup env should carry NO_PROXY: %v", setup.Env)
	}
	if setup.Env["NODE_EXTRA_CA_CERTS"] != "acnologia-ca.pem" || setup.Env["SSL_CERT_FILE"] != setup.CABundle["file"] ||
		setup.Env["REQUESTS_CA_BUNDLE"] != setup.CABundle["file"] || setup.CABundle["file"] == "acnologia-ca.pem" {
		t.Fatalf("SSL_CERT_FILE and REQUESTS_CA_BUNDLE should name the bundle, not the CA alone: %v %v", setup.Env, setup.CABundle)
	}
}
