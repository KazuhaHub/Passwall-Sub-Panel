package http

import (
	"fmt"
	"io"
	stdhttp "net/http"
	"os"
	"strconv"
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
	if output == nil {
		output = os.Stdout
	}
	cfg := gin.LoggerConfig{
		Formatter: xrayAccessLogFormatter,
		Output:    output,
		Skip:      skipCredentialBearingAccessLog,
	}
	return gin.LoggerWithConfig(cfg)
}

func xrayAccessLogFormatter(params gin.LogFormatterParams) string {
	line := fmt.Sprintf("%s [Info] passwall-sub-panel: http request status=%d method=%s path=%s latency=%s client_ip=%s",
		params.TimeStamp.UTC().Format("2006/01/02 15:04:05.000000"),
		params.StatusCode,
		params.Method,
		quoteAccessLogValue(params.Path),
		quoteAccessLogValue(params.Latency.String()),
		quoteAccessLogValue(params.ClientIP),
	)
	if params.ErrorMessage != "" {
		line += " error=" + quoteAccessLogValue(sanitizeAccessLogValue(params.ErrorMessage))
	}
	return line + "\n"
}

func quoteAccessLogValue(value string) string {
	if value == "" {
		return `""`
	}
	if strings.IndexFunc(value, func(r rune) bool {
		return r == '\r' || r == '\n' || r == ' ' || r == '\t' || r == '=' || r == '"' || r == '\\'
	}) == -1 {
		return value
	}
	return strconv.Quote(value)
}

func sanitizeAccessLogValue(value string) string {
	return strings.NewReplacer("\r\n", `\n`, "\n", `\n`, "\r", `\r`).Replace(value)
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
