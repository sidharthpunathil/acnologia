package catalog

import (
	"bytes"
	"context"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"

	_ "golang.org/x/image/webp"

	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"syscall"
	"time"
)

const MaxLogoBytes = 512 << 10

var AllowedLogoMimes = map[string]bool{
	"image/png": true, "image/jpeg": true, "image/webp": true, "image/svg+xml": true,
	"image/x-icon": true, "image/vnd.microsoft.icon": true, "image/gif": true,
}

// blockedPrefixes are address ranges that are never public internet hosts:
// private, loopback, link-local, carrier-grade NAT (also Tailscale and the
// Alibaba metadata service), benchmarking, IETF protocol assignments,
// documentation, multicast and reserved space.
var blockedPrefixes = func() []netip.Prefix {
	var out []netip.Prefix
	for _, p := range []string{
		"0.0.0.0/8", "10.0.0.0/8", "100.64.0.0/10", "127.0.0.0/8", "169.254.0.0/16",
		"172.16.0.0/12", "192.0.0.0/24", "192.0.2.0/24", "192.168.0.0/16", "198.18.0.0/15",
		"198.51.100.0/24", "203.0.113.0/24", "224.0.0.0/4", "240.0.0.0/4",
		"::/128", "::1/128", "fc00::/7", "fe80::/10", "ff00::/8", "2001:db8::/32",
	} {
		out = append(out, netip.MustParsePrefix(p))
	}
	return out
}()

var (
	nat64Prefix = netip.MustParsePrefix("64:ff9b::/96")
	sixToFour   = netip.MustParsePrefix("2002::/16")
)

// BlockedIP reports whether ip is not a public internet address. IPv4 that is
// embedded in IPv6 (mapped, NAT64 or 6to4) is checked as the IPv4 address.
func BlockedIP(ip netip.Addr) bool {
	if !ip.IsValid() {
		return true
	}
	ip = ip.Unmap()
	if ip.Is6() {
		b := ip.As16()
		switch {
		case nat64Prefix.Contains(ip):
			return BlockedIP(netip.AddrFrom4([4]byte{b[12], b[13], b[14], b[15]}))
		case sixToFour.Contains(ip):
			return BlockedIP(netip.AddrFrom4([4]byte{b[2], b[3], b[4], b[5]}))
		}
	}
	for _, p := range blockedPrefixes {
		if p.Contains(ip) {
			return true
		}
	}
	return false
}

// PublicOnly is a net.Dialer Control hook that refuses any non-public
// address. It sees the resolved address of every connection, so a name that
// resolves somewhere else later (DNS rebinding) is still refused.
func PublicOnly(_, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return err
	}
	ip, err := netip.ParseAddr(host)
	if err != nil || BlockedIP(ip) {
		return fmt.Errorf("refusing to connect to non-public address %s", host)
	}
	return nil
}

// SafeClient refuses to connect to non-public addresses, so URLs chosen by
// users or an AI cannot reach internal services. The check runs on the
// resolved address at dial time, which also covers redirects and DNS rebinding.
func SafeClient(timeout time.Duration) *http.Client {
	d := &net.Dialer{Timeout: 10 * time.Second, Control: PublicOnly}
	return &http.Client{
		Timeout:   timeout,
		Transport: &http.Transport{DialContext: d.DialContext, TLSHandshakeTimeout: 10 * time.Second, Proxy: nil},
	}
}

var iconLinkRe = regexp.MustCompile(`(?is)<link[^>]+rel=["']?([^"'>]*icon[^"'>]*)["']?[^>]*>`)
var hrefRe = regexp.MustCompile(`(?is)href=["']([^"']+)["']`)
var sizesRe = regexp.MustCompile(`(?is)sizes=["'](\d+)x\d+["']`)

// FetchLogo finds and downloads a site's logo: the largest declared icon,
// then apple-touch-icon, then favicon.
func FetchLogo(ctx context.Context, site string) ([]byte, string, string, error) {
	if !strings.Contains(site, "://") {
		site = "https://" + site
	}
	base, err := url.Parse(site)
	if err != nil || base.Host == "" || (base.Scheme != "https" && base.Scheme != "http") {
		return nil, "", "", errors.New("website must be an http(s) URL")
	}
	c := SafeClient(15 * time.Second)
	candidates := []string{}
	if body, err := get(ctx, c, base.String(), 1<<20); err == nil {
		type cand struct {
			href string
			size int
		}
		var found []cand
		for _, m := range iconLinkRe.FindAllStringSubmatch(string(body), -1) {
			h := hrefRe.FindStringSubmatch(m[0])
			if h == nil {
				continue
			}
			size := 0
			if s := sizesRe.FindStringSubmatch(m[0]); s != nil {
				fmt.Sscan(s[1], &size)
			}
			if strings.Contains(strings.ToLower(m[1]), "apple-touch") && size == 0 {
				size = 180
			}
			if strings.Contains(strings.ToLower(m[0]), ".svg") && size == 0 {
				size = 512
			}
			found = append(found, cand{h[1], size})
			if len(found) >= 64 {
				break
			}
		}
		// Largest first.
		sort.SliceStable(found, func(i, j int) bool { return found[i].size > found[j].size })
		for _, f := range found {
			if u, err := base.Parse(strings.TrimSpace(f.href)); err == nil {
				candidates = append(candidates, u.String())
			}
		}
	}
	root := base.Scheme + "://" + base.Host
	candidates = append(candidates, root+"/apple-touch-icon.png", root+"/favicon.svg", root+"/favicon.png", root+"/favicon.ico")
	// Download every candidate and keep the sharpest: SVG first, then the
	// largest raster. Tiny favicons only win when nothing else exists.
	seen := map[string]bool{}
	var best []byte
	var bestMime, bestURL string
	bestScore := 0
	for _, u := range candidates {
		if seen[u] || len(seen) >= 8 {
			continue
		}
		seen[u] = true
		data, mime, err := DownloadImage(ctx, c, u)
		if err != nil {
			continue
		}
		if sc := ImageScore(data, mime); sc > bestScore {
			best, bestMime, bestURL, bestScore = data, mime, u, sc
		}
	}
	if bestScore < 128 {
		// A public icon service renders most sites' icons at 256px.
		gs := "https://www.google.com/s2/favicons?sz=256&domain=" + url.QueryEscape(base.Hostname())
		if data, mime, err := DownloadImage(ctx, c, gs); err == nil {
			if sc := ImageScore(data, mime); sc > bestScore && sc > 16 {
				best, bestMime, bestURL, bestScore = data, mime, gs, sc
			}
		}
	}
	if best == nil {
		return nil, "", "", fmt.Errorf("no usable logo found on %s", base.Host)
	}
	return best, bestMime, bestURL, nil
}

// ImageScore ranks logo quality by size: SVGs are best, then pixel width.
func ImageScore(data []byte, mime string) int {
	switch mime {
	case "image/svg+xml":
		return 10000
	case "image/x-icon", "image/vnd.microsoft.icon":
		// ICONDIR: entries of 16 bytes after a 6 byte header; width 0 means 256.
		if len(data) < 6 {
			return 0
		}
		n := int(data[4]) | int(data[5])<<8
		bestW := 0
		for i := 0; i < n && 6+16*i+16 <= len(data); i++ {
			w := int(data[6+16*i])
			if w == 0 {
				w = 256
			}
			bestW = max(bestW, w)
		}
		return bestW
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return 1
	}
	return min(cfg.Width, cfg.Height)
}

// DownloadImage fetches an image URL and checks its type and size.
func DownloadImage(ctx context.Context, c *http.Client, u string) ([]byte, string, error) {
	if c == nil {
		c = SafeClient(15 * time.Second)
	}
	pu, err := url.Parse(u)
	if err != nil || (pu.Scheme != "https" && pu.Scheme != "http") {
		return nil, "", errors.New("image URL must be http(s)")
	}
	data, err := get(ctx, c, u, MaxLogoBytes+1)
	if err != nil {
		return nil, "", err
	}
	if len(data) > MaxLogoBytes {
		return nil, "", errors.New("image is larger than 512 KB")
	}
	mime := SniffImage(data)
	if mime == "" {
		return nil, "", errors.New("not an image")
	}
	return data, mime, nil
}

// SniffImage returns the image mime type, or "" when the bytes are not an allowed image.
func SniffImage(data []byte) string {
	if len(data) < 8 {
		return ""
	}
	head := strings.ToLower(strings.TrimSpace(string(data[:min(len(data), 512)])))
	switch {
	case strings.HasPrefix(head, "<svg") || (strings.HasPrefix(head, "<?xml") && strings.Contains(head, "<svg")):
		return "image/svg+xml"
	case data[0] == 0 && data[1] == 0 && data[2] == 1 && data[3] == 0:
		return "image/x-icon"
	}
	m := http.DetectContentType(data)
	if AllowedLogoMimes[m] && m != "image/svg+xml" {
		return m
	}
	return ""
}

func get(ctx context.Context, c *http.Client, u string, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; Acnologia/1.0; +https://github.com/sidharthpunathil/acnologia)")
	resp, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("%s returned %d", u, resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, limit))
}

// SniffImageURL reports whether a URL points straight at an image file.
func SniffImageURL(u string) bool {
	pu, err := url.Parse(u)
	if err != nil {
		return false
	}
	p := strings.ToLower(pu.Path)
	for _, ext := range []string{".png", ".jpg", ".jpeg", ".webp", ".svg", ".ico", ".gif"} {
		if strings.HasSuffix(p, ext) {
			return true
		}
	}
	return false
}
