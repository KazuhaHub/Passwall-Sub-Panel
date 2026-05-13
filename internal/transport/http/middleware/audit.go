package middleware

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/log"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/audit"
)

func AdminAudit(auditSvc *audit.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		reqBody := captureRequestBody(c)
		c.Next()
		if auditSvc == nil || !shouldAudit(c) {
			return
		}
		claims := ClaimsFrom(c)
		actor := "admin"
		if claims != nil && claims.UPN != "" {
			actor = claims.UPN
		}
		path := c.FullPath()
		if path == "" {
			path = c.Request.URL.Path
		}
		request := map[string]any{
			"method": c.Request.Method,
			"path":   c.Request.URL.Path,
			"route":  path,
			"params": paramsMap(c),
			"query":  queryMap(c),
		}
		if reqBody != nil {
			request["body"] = reqBody
		}
		after := map[string]any{
			"status":      c.Writer.Status(),
			"duration_ms": time.Since(start).Milliseconds(),
		}
		if len(c.Errors) > 0 {
			after["errors"] = c.Errors.String()
		}
		entry := &domain.AuditEntry{
			Actor:      actor,
			Action:     actionName(c.Request.Method, path),
			Target:     path,
			BeforeJSON: auditJSON(request),
			AfterJSON:  auditJSON(after),
			IP:         c.ClientIP(),
			At:         time.Now(),
		}
		if err := auditSvc.Insert(c.Request.Context(), entry); err != nil {
			log.Warn("audit middleware insert failed", "err", err)
		}
	}
}

func shouldAudit(c *gin.Context) bool {
	switch c.Request.Method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
	default:
		return false
	}
	path := c.FullPath()
	if path == "" {
		path = c.Request.URL.Path
	}
	return strings.HasPrefix(path, "/api/admin/")
}

func captureRequestBody(c *gin.Context) any {
	if c.Request.Body == nil {
		return nil
	}
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		c.Request.Body = io.NopCloser(bytes.NewReader(nil))
		return map[string]any{"read_error": err.Error()}
	}
	c.Request.Body = io.NopCloser(bytes.NewReader(body))
	if len(bytes.TrimSpace(body)) == 0 {
		return nil
	}
	var v any
	if err := json.Unmarshal(body, &v); err == nil {
		return redact(v)
	}
	return truncateString(string(body), 8192)
}

func paramsMap(c *gin.Context) map[string]string {
	out := make(map[string]string, len(c.Params))
	for _, p := range c.Params {
		out[p.Key] = p.Value
	}
	return out
}

func queryMap(c *gin.Context) map[string][]string {
	q := c.Request.URL.Query()
	out := make(map[string][]string, len(q))
	for k, v := range q {
		out[k] = v
	}
	return out
}

func actionName(method, route string) string {
	verb := strings.ToLower(method)
	switch method {
	case http.MethodPost:
		verb = "create_or_run"
	case http.MethodPut, http.MethodPatch:
		verb = "update"
	case http.MethodDelete:
		verb = "delete"
	}
	return verb + " " + route
}

func redact(v any) any {
	switch x := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, val := range x {
			if isSensitiveKey(k) {
				out[k] = "[REDACTED]"
				continue
			}
			out[k] = redact(val)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, val := range x {
			out[i] = redact(val)
		}
		return out
	case string:
		return truncateString(x, 8192)
	default:
		return x
	}
}

func isSensitiveKey(k string) bool {
	k = strings.ToLower(k)
	sensitive := []string{
		"password", "token", "secret", "uuid", "api_token", "sub_token",
		"client_secret", "key_pem", "private_key", "refresh_token", "access_token",
	}
	for _, s := range sensitive {
		if strings.Contains(k, s) {
			return true
		}
	}
	return false
}

func truncateString(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "...[TRUNCATED]"
}

func auditJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(b)
}
