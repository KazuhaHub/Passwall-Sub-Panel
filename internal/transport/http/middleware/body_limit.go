package middleware

import (
	"errors"
	"net/http"
	"sort"
	"strings"

	"github.com/gin-gonic/gin"
)

// BodyLimit caps the request body to maxBytes for every request that
// reaches it. Pre-emptive than reactive: we wrap r.Body in a
// http.MaxBytesReader so any io.ReadAll downstream returns an error the
// moment the limit is exceeded, before the bytes ever accumulate in
// memory.
//
// 1 MiB is plenty for every admin write the panel exposes (user CRUD,
// settings JSON, SAML config). The only intentionally-large endpoint
// is /api/auth/saml/acs which carries a SAMLResponse — its expected
// upper bound is typically ~80 KiB, so 1 MiB covers it with margin.
func BodyLimit(maxBytes int64) gin.HandlerFunc {
	return BodyLimitByPath(maxBytes, nil)
}

// BodyLimitByPath applies a default cap and exact-path overrides. The map is
// copied at construction so startup wiring cannot race request handling by
// mutating it later.
func BodyLimitByPath(defaultMaxBytes int64, pathLimits map[string]int64) gin.HandlerFunc {
	return BodyLimitByPathPrefix(defaultMaxBytes, pathLimits, nil)
}

// BodyLimitByPathPrefix is BodyLimitByPath plus prefix overrides, for route
// families whose paths carry a parameter (PUT /api/admin/rules/:slug) and so
// cannot be named exactly. Precedence: an exact path wins, then the longest
// matching prefix, then the default.
//
// The middleware sees the path AFTER panelDispatch has stripped the runtime
// panel prefix (it runs inside the Gin engine), so prefixes are written without
// panel_path and a deployment under /mypanel gets the same limits.
//
// A prefix must start and end with "/": the trailing slash stops
// "/api/admin/rules/" from also widening a sibling such as "/api/admin/rulesets".
func BodyLimitByPathPrefix(defaultMaxBytes int64, pathLimits, prefixLimits map[string]int64) gin.HandlerFunc {
	if defaultMaxBytes <= 0 {
		panic("body limit must be positive")
	}
	limits := make(map[string]int64, len(pathLimits))
	for path, limit := range pathLimits {
		if path == "" || limit <= 0 {
			panic("body-limit path overrides require a path and positive limit")
		}
		limits[path] = limit
	}
	type prefixLimit struct {
		prefix string
		limit  int64
	}
	prefixes := make([]prefixLimit, 0, len(prefixLimits))
	for prefix, limit := range prefixLimits {
		if !strings.HasPrefix(prefix, "/") || !strings.HasSuffix(prefix, "/") || limit <= 0 {
			panic("body-limit prefix overrides require a /-delimited prefix and positive limit")
		}
		prefixes = append(prefixes, prefixLimit{prefix, limit})
	}
	// Longest first, so the first match is the most specific.
	sort.Slice(prefixes, func(i, j int) bool { return len(prefixes[i].prefix) > len(prefixes[j].prefix) })
	return func(c *gin.Context) {
		if c.Request.Body != nil {
			path := c.Request.URL.Path
			maxBytes := defaultMaxBytes
			if override, ok := limits[path]; ok {
				maxBytes = override
			} else {
				for _, p := range prefixes {
					if strings.HasPrefix(path, p.prefix) {
						maxBytes = p.limit
						break
					}
				}
			}
			c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxBytes)
		}
		c.Next()
	}
}

// BodyTooLargeCode is the stable machine-readable code of a 413 produced by
// the body cap; the SPA keys its message on it.
const BodyTooLargeCode = "body_too_large"

// AbortBodyTooLarge answers 413 {error, code, limit_bytes} and aborts when err
// is (or wraps) the *http.MaxBytesError the cap raises, reporting whether it
// did. Anything else is left to the caller, so a malformed body is still a 400.
func AbortBodyTooLarge(c *gin.Context, err error) bool {
	var tooLarge *http.MaxBytesError
	if err == nil || !errors.As(err, &tooLarge) {
		return false
	}
	c.AbortWithStatusJSON(http.StatusRequestEntityTooLarge, gin.H{
		"error":       "Request body too large",
		"code":        BodyTooLargeCode,
		"limit_bytes": tooLarge.Limit,
	})
	return true
}
