// Package safehttp builds HTTP clients whose dialer refuses the
// SSRF-prone destinations: loopback, link-local (including the
// 169.254.169.254 cloud-metadata endpoint), and the unspecified
// address. The check runs per-connection AFTER DNS resolution, so a
// hostname that resolves to one of those (DNS rebinding) is caught
// at dial time too.
//
// Used by any code path whose URL is admin-supplied or pulled from
// the DB:
//   - the 3X-UI adapter (panel URL is stored config, can point anywhere)
//   - the GitHub release/compat fetchers (URL override is admin-only
//     today, but the constructor accepts a urlOverride parameter)
//   - SAML metadata refresh + OIDC issuer discovery (admin enters them)
//
// General private LAN ranges (10/8, 172.16/12, 192.168/16) are
// deliberately ALLOWED: legitimate internal IdPs / 3X-UI panels often
// live there, and the network reachability is itself a credential. The
// blocklist focuses on the addresses every cloud's "host as router"
// magic gets confused by — localhost-only metadata services, instance
// metadata endpoint, and the all-zeroes "self" address.
package safehttp

import (
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"syscall"
	"time"
)

// specialUseRanges are IANA special-use blocks that are never a legitimate
// panel / IdP host yet aren't caught by IsLoopback / IsLinkLocal /
// IsUnspecified: TEST-NET documentation ranges, the benchmarking range, the
// IETF protocol-assignment block, the reserved/future class-E space, and the
// IPv6 documentation prefix. Blocking them narrows the SSRF surface as
// defense-in-depth.
//
// Deliberately NOT included: 100.64.0.0/10 (RFC 6598 CGNAT). Tailscale /
// Headscale overlay networks address nodes in that range, so a self-hosted
// panel reachable over Tailscale legitimately lives there — blocking it would
// break a real deployment. RFC1918 / ULA private ranges are likewise allowed
// (see the package doc).
var specialUseRanges = parseCIDRs(
	"192.0.0.0/24",    // RFC 6890 IETF protocol assignments
	"192.0.2.0/24",    // RFC 5737 TEST-NET-1
	"198.18.0.0/15",   // RFC 2544 benchmarking
	"198.51.100.0/24", // RFC 5737 TEST-NET-2
	"203.0.113.0/24",  // RFC 5737 TEST-NET-3
	"240.0.0.0/4",     // RFC 1112 reserved / future use (includes 255.255.255.255)
	"2001:db8::/32",   // RFC 3849 IPv6 documentation
)

func parseCIDRs(cidrs ...string) []*net.IPNet {
	out := make([]*net.IPNet, 0, len(cidrs))
	for _, c := range cidrs {
		if _, n, err := net.ParseCIDR(c); err == nil {
			out = append(out, n)
		}
	}
	return out
}

// BlockNonPublicDial is the net.Dialer.Control hook that rejects
// loopback / link-local / unspecified / IANA-special-use addresses
// post-DNS-resolution. Exported so callers wiring their own dialer with extra
// knobs (DSCP, SO_BINDTODEVICE, etc.) can reuse the policy without duplicating
// the rule set.
func BlockNonPublicDial(_, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return err
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return fmt.Errorf("refusing connection to non-IP host %q", host)
	}
	if ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() {
		return fmt.Errorf("refusing connection to non-public address %s", ip)
	}
	for _, n := range specialUseRanges {
		if n.Contains(ip) {
			return fmt.Errorf("refusing connection to special-use address %s", ip)
		}
	}
	return nil
}

// NewClient returns an *http.Client whose dialer enforces
// BlockNonPublicDial. `timeout` caps the full request lifetime
// (Connect + TLS + read + write); the internal dial+keepalive
// settings are reasonable defaults that mirror http.DefaultTransport.
func NewClient(timeout time.Duration) *http.Client {
	return NewClientTLS(timeout, false)
}

// NewClientTLS is NewClient with an explicit TLS-verification choice. When
// insecureSkipVerify is true the client accepts any server certificate — for a
// 3X-UI panel behind a self-signed / hostname-mismatched cert. The SSRF dial
// guard is unchanged either way; this only relaxes certificate validation.
func NewClientTLS(timeout time.Duration, insecureSkipVerify bool) *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DialContext = (&net.Dialer{
		Timeout:   10 * time.Second,
		KeepAlive: 30 * time.Second,
		Control:   BlockNonPublicDial,
	}).DialContext
	if insecureSkipVerify {
		if transport.TLSClientConfig == nil {
			transport.TLSClientConfig = &tls.Config{}
		}
		transport.TLSClientConfig.InsecureSkipVerify = true
	}
	return &http.Client{Timeout: timeout, Transport: transport}
}
