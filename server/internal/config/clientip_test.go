package config

import "testing"

func TestClientIPSettings(t *testing.T) {
	t.Setenv("ACNOLOGIA_TRUSTED_PROXY_HOPS", "")
	t.Setenv("ACNOLOGIA_CLIENT_IP_HEADER", "")
	c := Load("test")
	if c.TrustedProxyHops != 1 || c.ClientIPHeader != "" {
		t.Fatalf("defaults: hops %d, header %q", c.TrustedProxyHops, c.ClientIPHeader)
	}
	t.Setenv("ACNOLOGIA_TRUSTED_PROXY_HOPS", "2")
	t.Setenv("ACNOLOGIA_CLIENT_IP_HEADER", " Fly-Client-IP ")
	c = Load("test")
	if c.TrustedProxyHops != 2 || c.ClientIPHeader != "Fly-Client-IP" {
		t.Fatalf("set: hops %d, header %q", c.TrustedProxyHops, c.ClientIPHeader)
	}
	// Fewer than one hop would trust nothing a proxy wrote; it reads as one.
	t.Setenv("ACNOLOGIA_TRUSTED_PROXY_HOPS", "0")
	if c = Load("test"); c.TrustedProxyHops != 1 {
		t.Fatalf("hops 0 should read as 1: %d", c.TrustedProxyHops)
	}
}
