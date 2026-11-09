package api

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"math/big"
	"net"
	"strings"
	"sync"
	"time"
)

// The forward proxy's certificate authority. An app that sends HTTPS through
// Acnologia as its HTTPS_PROXY trusts this CA (NODE_EXTRA_CA_CERTS,
// REQUESTS_CA_BUNDLE, SSL_CERT_FILE, curl --cacert), so Acnologia can open
// the request to a service's host, put a pool key on it and send it on.
// Each server makes its own CA on first use: the certificate is public
// (GET /api/v1/proxy/ca.pem), the private key is stored encrypted with the
// master key and never leaves the server.

const (
	metaForwardCACert = "forward_ca_cert"
	metaForwardCAKey  = "forward_ca_key"
	// leafTTL is how long a minted host certificate lasts; it is renewed a
	// week before it ends.
	leafTTL      = 30 * 24 * time.Hour
	leafRenew    = 7 * 24 * time.Hour
	maxLeafCache = 2000
)

type forwardCA struct {
	mu     sync.Mutex
	cert   *x509.Certificate
	der    []byte
	key    *ecdsa.PrivateKey
	leaves map[string]*tls.Certificate
}

// ca loads the CA, making and storing one on first use.
func (s *Server) ca() (*forwardCA, error) {
	s.caMu.Lock()
	defer s.caMu.Unlock()
	if s.fwdCA != nil {
		return s.fwdCA, nil
	}
	ctx := s.bgCtx()
	if certPEM, keyEnc := s.Store.GetMeta(ctx, metaForwardCACert), s.Store.GetMeta(ctx, metaForwardCAKey); certPEM != "" && keyEnc != "" {
		ca, err := s.loadCA(certPEM, keyEnc)
		if err != nil {
			return nil, err
		}
		s.fwdCA = ca
		return ca, nil
	}
	ca, err := s.newCA()
	if err != nil {
		return nil, err
	}
	s.fwdCA = ca
	return ca, nil
}

func (s *Server) loadCA(certPEM, keyEnc string) (*forwardCA, error) {
	b, _ := pem.Decode([]byte(certPEM))
	if b == nil {
		return nil, errors.New("stored proxy CA certificate is not PEM")
	}
	cert, err := x509.ParseCertificate(b.Bytes)
	if err != nil {
		return nil, err
	}
	raw, err := hex.DecodeString(keyEnc)
	if err != nil {
		return nil, err
	}
	plain, err := s.Pool.Box.Decrypt(raw)
	if err != nil {
		return nil, err
	}
	key, err := x509.ParseECPrivateKey([]byte(plain))
	if err != nil {
		return nil, err
	}
	return &forwardCA{cert: cert, der: b.Bytes, key: key, leaves: map[string]*tls.Certificate{}}, nil
}

// newCA makes a fresh CA and stores it, replacing any older one.
func (s *Server) newCA() (*forwardCA, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	name := "Acnologia proxy CA"
	if h := hostOf(s.Cfg.PublicURL); h != "" {
		name += " (" + h + ")"
	}
	tmpl := &x509.Certificate{
		SerialNumber:          randSerial(),
		Subject:               pkix.Name{CommonName: name, Organization: []string{"Acnologia"}},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().AddDate(10, 0, 0),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLenZero:        true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, err
	}
	cert, _ := x509.ParseCertificate(der)
	kb, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, err
	}
	enc, err := s.Pool.Box.Encrypt(string(kb))
	if err != nil {
		return nil, err
	}
	ctx := s.bgCtx()
	if err := s.Store.SetMeta(ctx, metaForwardCAKey, hex.EncodeToString(enc)); err != nil {
		return nil, err
	}
	if err := s.Store.SetMeta(ctx, metaForwardCACert, string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))); err != nil {
		return nil, err
	}
	return &forwardCA{cert: cert, der: der, key: key, leaves: map[string]*tls.Certificate{}}, nil
}

// rotateCA replaces the CA. Apps must download and trust the new one.
func (s *Server) rotateCA() (*forwardCA, error) {
	s.caMu.Lock()
	defer s.caMu.Unlock()
	ca, err := s.newCA()
	if err != nil {
		return nil, err
	}
	s.fwdCA = ca
	return ca, nil
}

// PEM is the CA certificate apps trust.
func (c *forwardCA) PEM() []byte {
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: c.der})
}

// Fingerprint is the CA's SHA-256 fingerprint, colon separated.
func (c *forwardCA) Fingerprint() string {
	sum := sha256.Sum256(c.der)
	h := strings.ToUpper(hex.EncodeToString(sum[:]))
	parts := make([]string, 0, len(sum))
	for i := 0; i < len(h); i += 2 {
		parts = append(parts, h[i:i+2])
	}
	return strings.Join(parts, ":")
}

// leaf returns a certificate for host signed by the CA, minted on first use.
func (c *forwardCA) leaf(host string) (*tls.Certificate, error) {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	if host == "" {
		host = "acnologia-proxy"
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if l, ok := c.leaves[host]; ok && time.Until(l.Leaf.NotAfter) > leafRenew {
		return l, nil
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	tmpl := &x509.Certificate{
		SerialNumber: randSerial(),
		Subject:      pkix.Name{CommonName: host},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(leafTTL),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	if ip := net.ParseIP(host); ip != nil {
		tmpl.IPAddresses = []net.IP{ip}
	} else {
		tmpl.DNSNames = []string{host}
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, c.cert, &key.PublicKey, c.key)
	if err != nil {
		return nil, err
	}
	leaf, _ := x509.ParseCertificate(der)
	tc := &tls.Certificate{Certificate: [][]byte{der, c.der}, PrivateKey: key, Leaf: leaf}
	if len(c.leaves) >= maxLeafCache {
		c.leaves = map[string]*tls.Certificate{}
	}
	c.leaves[host] = tc
	return tc, nil
}

func randSerial() *big.Int {
	n, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 127))
	return n
}
