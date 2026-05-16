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

// AuditWrites records every write request (POST/PUT/PATCH/DELETE) that lands
// on an audited path. Attached at the engine level so it covers admin
// endpoints AND user-side self-service AND the local login endpoint —
// shouldAuditPath is the gate.
//
// Path filter runs BEFORE the request body is read, so static asset / sub
// fetch / health probe traffic doesn't pay the io.ReadAll cost.
func AuditWrites(auditSvc *audit.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		if auditSvc == nil || !shouldAuditPath(c.Request.URL.Path, c.Request.Method) {
			c.Next()
			return
		}
		start := time.Now()
		reqBody := captureRequestBody(c)
		c.Next()
		path := c.FullPath()
		if path == "" {
			path = c.Request.URL.Path
		}
		// Login attempts arrive before claims are set; pull the upn from
		// the (redacted) request body so failed-login rows still name the
		// attempting account. Other paths use JWT claims as usual.
		actor := resolveAuditActor(c, path, reqBody)
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

// shouldAuditPath decides — purely from the URL path + HTTP method, without
// needing a matched route — whether this request should be audited. Kept as
// a pure function so it can be unit-tested without standing up gin.
func shouldAuditPath(path, method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
	default:
		return false
	}
	switch {
	case strings.HasPrefix(path, "/api/admin/"):
		// Every admin write (settings, users, nodes, …) was already
		// covered by the previous AdminAudit version; preserve that.
		return true
	case path == "/api/auth/local/login":
		// Login attempts (success AND failure) — security log.
		return true
	case strings.HasPrefix(path, "/api/user/me"):
		// User self-service writes: password change, sub-token reset,
		// personal rules edit, emergency-access request.
		return true
	}
	return false
}

// resolveAuditActor picks the best identity string for the audit entry.
//   - JWT claims (set by RequireAuth) win when present.
//   - For local login specifically we fall back to the upn the user typed,
//     so failed-login rows still tell the admin which account was being
//     targeted.
//   - Otherwise "anonymous" — better than the previous "admin" default.
func resolveAuditActor(c *gin.Context, path string, body any) string {
	if claims := ClaimsFrom(c); claims != nil && claims.UPN != "" {
		return claims.UPN
	}
	if path == "/api/auth/local/login" {
		if m, ok := body.(map[string]any); ok {
			if v, ok := m["upn"].(string); ok && v != "" {
				return v
			}
		}
	}
	return "anonymous"
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
