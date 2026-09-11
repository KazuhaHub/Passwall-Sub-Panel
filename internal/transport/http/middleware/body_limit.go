package middleware

import (
	"net/http"

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
	return func(c *gin.Context) {
		if c.Request.Body != nil {
			maxBytes := defaultMaxBytes
			if override, ok := limits[c.Request.URL.Path]; ok {
				maxBytes = override
			}
			c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxBytes)
		}
		c.Next()
	}
}
