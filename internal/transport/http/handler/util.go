package handler

import (
	"strings"

	"github.com/gin-gonic/gin"
)

// isHTTPS detects whether the request is over HTTPS, considering
// common proxy headers like X-Forwarded-Proto.
//
// Trust model: we ONLY honour X-Forwarded-Proto / X-Forwarded-Ssl
// when Gin's trust-list resolution says the request came from a
// trusted proxy. gin.RemoteIP() returns the leftmost address of the
// X-Forwarded-For chain that the trust list accepts; if no proxy is
// trusted (e.g. trusted_proxies = "none") it falls back to the raw
// TCP peer. In either case c.Request.RemoteAddr is the actual TCP
// peer, and we compare that to RemoteIP() — a mismatch means an
// intermediary added the XFP header, so we trust it; an equal match
// (no proxy in the chain) means the header is attacker-controlled
// and must be ignored.
//
// Falls through to the request's TLS state as the safe default.
func isHTTPS(c *gin.Context) bool {
	if c.Request.TLS != nil {
		return true
	}
	if !proxyHeadersTrustworthy(c) {
		return false
	}
	if proto := c.GetHeader("X-Forwarded-Proto"); proto != "" {
		return strings.EqualFold(proto, "https")
	}
	if ssl := c.GetHeader("X-Forwarded-Ssl"); ssl != "" {
		return strings.EqualFold(ssl, "on")
	}
	return false
}

// proxyHeadersTrustworthy reports whether the proxy headers on this
// request are signed off by a trusted proxy. Uses Gin's own trust list
// (set from http.trusted_proxies in config). gin.ClientIP() is the
// resolved client; RemoteAddr is the raw TCP peer. If they differ, at
// least one trusted hop is in front; if they're equal, no trusted
// proxy is involved and the headers are attacker-supplied.
func proxyHeadersTrustworthy(c *gin.Context) bool {
	if c.Request == nil {
		return false
	}
	raw := c.Request.RemoteAddr
	if i := strings.LastIndex(raw, ":"); i > 0 {
		raw = raw[:i]
	}
	raw = strings.Trim(raw, "[]")
	return raw != c.ClientIP()
}

// sanitizeReturnTo validates and sanitizes the return_to parameter
// to prevent open redirect attacks. Only allows relative paths.
func sanitizeReturnTo(returnTo string, fallback string) string {
	if returnTo == "" {
		return fallback
	}
	// Must start with /
	if !strings.HasPrefix(returnTo, "/") {
		return fallback
	}
	// Must not contain protocol:// (prevents redirects to external sites)
	if strings.Contains(returnTo, "://") {
		return fallback
	}
	// Must not contain double slashes (prevents protocol-relative URLs)
	if strings.HasPrefix(returnTo, "//") {
		return fallback
	}
	// Reject backslashes — some browsers normalize "\" to "/", so "/\evil.com"
	// or "\\evil.com" could slip past the "//" check and resolve off-origin.
	if strings.Contains(returnTo, "\\") {
		return fallback
	}
	return returnTo
}
