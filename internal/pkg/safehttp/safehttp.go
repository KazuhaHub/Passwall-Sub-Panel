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
	"fmt"
	"net"
	"net/http"
	"syscall"
	"time"
)

// BlockNonPublicDial is the net.Dialer.Control hook that rejects
// loopback / link-local / unspecified addresses post-DNS-resolution.
// Exported so callers wiring their own dialer with extra knobs (DSCP,
// SO_BINDTODEVICE, etc.) can reuse the policy without duplicating
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
	return nil
}

// NewClient returns an *http.Client whose dialer enforces
// BlockNonPublicDial. `timeout` caps the full request lifetime
// (Connect + TLS + read + write); the internal dial+keepalive
// settings are reasonable defaults that mirror http.DefaultTransport.
func NewClient(timeout time.Duration) *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DialContext = (&net.Dialer{
		Timeout:   10 * time.Second,
		KeepAlive: 30 * time.Second,
		Control:   BlockNonPublicDial,
	}).DialContext
	return &http.Client{Timeout: timeout, Transport: transport}
}
