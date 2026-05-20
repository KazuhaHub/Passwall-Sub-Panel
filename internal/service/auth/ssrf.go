package auth

import (
	"fmt"
	"net"
	"net/http"
	"syscall"
	"time"
)

// blockNonPublicDial rejects connections to loopback, link-local (including
// the 169.254.169.254 cloud-metadata endpoint), and unspecified addresses.
// It runs per-connection AFTER DNS resolution, so a hostname that resolves
// to one of these (DNS rebinding) is caught too.
//
// General private LAN ranges (10/8, 172.16/12, 192.168/16) are intentionally
// allowed: internal corporate IdPs legitimately live there, and these fetches
// are admin-only. The goal is to kill the classic metadata-endpoint /
// localhost SSRF pivots without breaking valid private-network deployments.
func blockNonPublicDial(_, address string, _ syscall.RawConn) error {
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

// newSafeHTTPClient builds an http.Client whose dialer blocks the SSRF-prone
// address ranges above. Used for admin-supplied IdP metadata (SAML) and
// issuer-discovery (OIDC) fetches.
func newSafeHTTPClient(timeout time.Duration) *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DialContext = (&net.Dialer{
		Timeout:   10 * time.Second,
		KeepAlive: 30 * time.Second,
		Control:   blockNonPublicDial,
	}).DialContext
	return &http.Client{Timeout: timeout, Transport: transport}
}
