package catalog

import (
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"syscall"
	"time"
)

// metadataAddrs are cloud metadata endpoints. 169.254.169.254 is link local
// anyway; AWS also answers on fd00:ec2::254 and Alibaba on 100.100.100.200.
var metadataAddrs = []netip.Addr{
	netip.MustParseAddr("169.254.169.254"),
	netip.MustParseAddr("fd00:ec2::254"),
	netip.MustParseAddr("100.100.100.200"),
}

// MetadataIP reports whether ip is an address no upstream may ever be: link
// local (169.254.0.0/16, fe80::/10), a cloud metadata endpoint, unspecified
// or invalid. IPv4 embedded in IPv6 (mapped, NAT64 or 6to4) is checked as
// the IPv4 address. Loopback and private ranges are not refused: self
// hosters proxy to model servers on their own machine or network.
func MetadataIP(ip netip.Addr) bool {
	if !ip.IsValid() {
		return true
	}
	ip = ip.Unmap()
	if ip.Is6() {
		b := ip.As16()
		switch {
		case nat64Prefix.Contains(ip):
			return MetadataIP(netip.AddrFrom4([4]byte{b[12], b[13], b[14], b[15]}))
		case sixToFour.Contains(ip):
			return MetadataIP(netip.AddrFrom4([4]byte{b[2], b[3], b[4], b[5]}))
		}
	}
	if ip.IsUnspecified() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
		return true
	}
	for _, m := range metadataAddrs {
		if ip == m {
			return true
		}
	}
	return false
}

// ErrMetadataAddr is the message a refused connection carries.
const ErrMetadataAddr = "refusing to connect to a link-local or metadata address"

// NoMetadata is a net.Dialer Control hook that refuses MetadataIP addresses.
// It runs on the resolved address of every connection, so a host name that
// resolves there (or is rebound there later) is refused too.
func NoMetadata(_, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return err
	}
	ip, err := netip.ParseAddr(host)
	if err != nil || MetadataIP(ip) {
		return fmt.Errorf("%s %s", ErrMetadataAddr, host)
	}
	return nil
}

// UpstreamDialer is the dialer for connections to a service's upstream (or
// its egress proxy): any address but link local and metadata ones.
func UpstreamDialer() *net.Dialer {
	return &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second, Control: NoMetadata}
}

// GuardProxy wraps a transport's Proxy func. Behind a proxy the dialer sees
// only the proxy's address, so a request whose URL names a link local or
// metadata address literally is refused here. A host name the proxy resolves
// is out of reach: a proxy connects wherever it can.
func GuardProxy(next func(*http.Request) (*url.URL, error)) func(*http.Request) (*url.URL, error) {
	return func(req *http.Request) (*url.URL, error) {
		if ip, err := netip.ParseAddr(req.URL.Hostname()); err == nil && MetadataIP(ip) {
			return nil, fmt.Errorf("%s %s", ErrMetadataAddr, ip)
		}
		if next == nil {
			return nil, nil
		}
		return next(req)
	}
}
