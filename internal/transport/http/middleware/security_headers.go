package middleware

import "github.com/gin-gonic/gin"

// SecurityHeaders sets the baseline HTTP security headers every response
// needs:
//
//   - HSTS:                 long-lived TLS pin, includes subdomains
//   - X-Frame-Options:      DENY (no iframe embedding → clickjacking off)
//   - X-Content-Type-Options nosniff (block MIME-sniff XSS)
//   - Referrer-Policy:      no-referrer (don't leak panel URLs)
//   - Content-Security-Policy: minimal self-only baseline; the SPA bundle
//     uses inline styles for MUI's emotion runtime so 'unsafe-inline'
//     is allowed for style-src only. Images: self + data: (favicons).
//     No script 'unsafe-inline' / 'unsafe-eval': React's bundled JS does
//     not need them.
//
// HSTS is harmless on HTTP-only deployments (browsers ignore it without
// TLS) and protects HTTPS deployments behind a reverse proxy.
func SecurityHeaders() gin.HandlerFunc {
	const csp = "default-src 'self'; " +
		"script-src 'self'; " +
		"style-src 'self' 'unsafe-inline'; " +
		"img-src 'self' data: blob:; " +
		"font-src 'self' data:; " +
		"connect-src 'self'; " +
		"frame-ancestors 'none'; " +
		"base-uri 'self'; " +
		"form-action 'self'"
	return func(c *gin.Context) {
		h := c.Writer.Header()
		// Setting on response gives precedence to anything a handler later
		// overrides (e.g. SAML metadata content-type); Set vs Add chosen
		// deliberately.
		h.Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		h.Set("X-Frame-Options", "DENY")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Content-Security-Policy", csp)
		c.Next()
	}
}
