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
	return func(c *gin.Context) {
		if c.Request.Body != nil {
			c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxBytes)
		}
		c.Next()
	}
}
