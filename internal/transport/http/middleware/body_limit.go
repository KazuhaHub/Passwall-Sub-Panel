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
// Ordinary admin writes retain a one-MiB cap. Native sync and destination-list
// writes have separate route overrides for their bounded larger payloads.
func BodyLimit(maxBytes int64) gin.HandlerFunc {
	return BodyLimitByPath(maxBytes, nil)
}

// BodyLimitByPath applies a default cap and exact-path or matched-route overrides. The map is
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
			} else if override, ok := limits[c.FullPath()]; ok {
				maxBytes = override
			}
			c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxBytes)
		}
		c.Next()
	}
}
