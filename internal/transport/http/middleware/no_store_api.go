package middleware

import (
	"strings"

	"github.com/gin-gonic/gin"
)

// NoStoreAPI marks every /api response as uncacheable.
//
// WHY THIS EXISTS. API responses shipped with NO cache directives at all —
// SecurityHeaders sets HSTS, X-Frame-Options, X-Content-Type-Options,
// Referrer-Policy and CSP, and stops there; the bundled nginx config
// deliberately does not override headers for /api; and the SPA's axios client
// sends none either. Per RFC 9111 §4.2.2 a 200 GET with no explicit freshness
// information MAY be stored and served under HEURISTIC freshness, so the
// absence of a directive is not the same as "do not cache" — it is a decision
// handed to whatever sits between us and the browser.
//
// That gap has a matching symptom: an admin saves an entity, reopens the
// editor immediately and sees the value from BEFORE the save, then the same
// page is correct a few seconds later. It reproduces across browser engines
// and heals on its own, which fits a cache TTL better than it fits anything
// else. PR #30 removed the redundant list read that made it visible on the
// editing admin's own screen; this closes the hole underneath, which a second
// admin or a plain page reload would still have fallen into.
//
// This project ships behind a reverse proxy by design, and its own CSP allows
// static.cloudflareinsights.com — so an intermediary cache is the expected
// deployment, not an exotic one. panel_dispatch.go already reaches for
// no-store on the root redirect for exactly this reason ("must never become a
// sticky browser/CDN mapping").
//
// no-store rather than no-cache. These responses carry no validator (no ETag,
// no Last-Modified), so no-cache would permit storing them and then ask for a
// revalidation there is nothing to revalidate with. They also carry account
// data and configuration, which is not something to leave in a shared cache on
// a technicality.
//
// Set BEFORE c.Next() so a handler that has thought about its own caching
// wins: /api/i18n/:lang deliberately serves no-cache with an ETag so bundles
// revalidate instead of re-downloading, and c.Header there overwrites this.
//
// Scoped to /api. Subscriptions are served from NoRoute outside this prefix
// and set their own private, no-cache with an ETag; static assets set
// immutable far-future caching. Neither is touched.
//
// The prefix test is safe under a custom panel_path: panelDispatch strips that
// prefix before gin sees the request, so the path here is always /api/... no
// matter what the panel is mounted as.
func NoStoreAPI() gin.HandlerFunc {
	return func(c *gin.Context) {
		if strings.HasPrefix(c.Request.URL.Path, "/api/") {
			c.Header("Cache-Control", "no-store")
		}
		c.Next()
	}
}
