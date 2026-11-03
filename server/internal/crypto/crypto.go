// Package crypto encrypts key secrets at rest and hashes tokens.
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

type Box struct {
	aead cipher.AEAD
	mac  []byte
}

// LoadOrCreateKey returns the 32 byte master key. It prefers the env value,
// then dataDir/master.key, and generates the file only if it does not exist.
// Any other read error is returned so a transient failure never replaces a
// working key.
func LoadOrCreateKey(envValue, dataDir string) ([]byte, error) {
	if envValue != "" {
		return decodeKey(envValue)
	}
	path := filepath.Join(dataDir, "master.key")
	b, err := os.ReadFile(path)
	if err == nil {
		return decodeKey(strings.TrimSpace(strings.TrimPrefix(string(b), "\ufeff")))
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("read master key: %w", err)
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, []byte(base64.StdEncoding.EncodeToString(key)+"\n"), 0o600); err != nil {
		return nil, fmt.Errorf("write master key: %w", err)
	}
	slog.Warn("generated a new master key; back it up or set ACNOLOGIA_MASTER_KEY, losing it makes stored keys unreadable", "path", path)
	return key, nil
}

func decodeKey(s string) ([]byte, error) {
	if b, err := base64.StdEncoding.DecodeString(s); err == nil && len(b) == 32 {
		return b, nil
	}
	if b, err := hex.DecodeString(s); err == nil && len(b) == 32 {
		return b, nil
	}
	return nil, errors.New("master key must be 32 bytes, base64 or hex encoded")
}

func New(key []byte) (*Box, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	m := hmac.New(sha256.New, key)
	// The label keeps the product's old name on purpose: it derives the key behind
	// every stored fingerprint and the master key check, so changing it would
	// make existing databases unreadable.
	m.Write([]byte("cogito-secret-fingerprint"))
	return &Box{aead: aead, mac: m.Sum(nil)}, nil
}

func (b *Box) Encrypt(plain string) ([]byte, error) {
	nonce := make([]byte, b.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return b.aead.Seal(nonce, nonce, []byte(plain), nil), nil
}

func (b *Box) Decrypt(blob []byte) (string, error) {
	n := b.aead.NonceSize()
	if len(blob) < n {
		return "", errors.New("ciphertext too short")
	}
	out, err := b.aead.Open(nil, blob[:n], blob[n:], nil)
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// CheckValue is a keyed constant stored next to the data so a boot with a
// different master key is detected before anything is encrypted with it.
func (b *Box) CheckValue() string {
	m := hmac.New(sha256.New, b.mac)
	// Old product name kept on purpose, see New.
	m.Write([]byte("cogito-master-key-check"))
	return hex.EncodeToString(m.Sum(nil))
}

// Fingerprint is a keyed hash used to detect duplicate secrets without decrypting.
func (b *Box) Fingerprint(secret string) string {
	m := hmac.New(sha256.New, b.mac)
	m.Write([]byte(secret))
	return hex.EncodeToString(m.Sum(nil))
}

func SHA256(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

func RandomToken(prefix string, nbytes int) string {
	b := make([]byte, nbytes)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return prefix + base64.RawURLEncoding.EncodeToString(b)
}

func Hint(secret string) string {
	if len(secret) <= 4 {
		return "..."
	}
	return "..." + secret[len(secret)-4:]
}

// Scrub replaces the secret, raw and URL encoded, with [key] so upstream
// errors and messages can be stored or shown without leaking it.
func Scrub(msg, secret string) string {
	if secret == "" || msg == "" {
		return msg
	}
	msg = strings.ReplaceAll(msg, secret, "[key]")
	msg = strings.ReplaceAll(msg, url.QueryEscape(secret), "[key]")
	return strings.ReplaceAll(msg, url.PathEscape(secret), "[key]")
}

// Truncate cuts s to at most n bytes without splitting a UTF-8 character.
func Truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}
