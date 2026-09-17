package http

import (
	"io"
	stdhttp "net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

type credentialBearingRoute struct {
	method  string
	pattern string
}

const (
	enrollScriptRoute = iota
	enrollCallbackRoute
	bootstrapDownloadRoute
)

// credentialBearingRoutes is the single declaration for public routes whose
// URL path contains bearer material. Route registration and access-log
// suppression both read this table so adding a delivery route cannot update
// one without making the other change visible in review.
var credentialBearingRoutes = [...]credentialBearingRoute{
	{method: stdhttp.MethodGet, pattern: "/enroll/:token"},
	{method: stdhttp.MethodPost, pattern: "/api/enroll/:token"},
	{method: stdhttp.MethodGet, pattern: "/node-bootstrap/:token"},
}

func newAccessLogger(output io.Writer) gin.HandlerFunc {
	cfg := gin.LoggerConfig{Skip: skipCredentialBearingAccessLog}
	if output != nil {
		cfg.Output = output
	}
	return gin.LoggerWithConfig(cfg)
}

func skipCredentialBearingAccessLog(c *gin.Context) bool {
	path := c.Request.URL.Path
	for _, route := range credentialBearingRoutes {
		prefix, _, ok := strings.Cut(route.pattern, ":token")
		if ok && strings.HasPrefix(path, prefix) {
			// Suppress every method, including a 404/405 request: the secret is
			// still present in the URL even when it cannot reach the handler.
			return true
		}
	}
	return false
}
