package middleware

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/log"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/audit"
)

// AsyncDispatch is the minimal subset of httptransport.AsyncDispatcher
// this middleware uses to defer the audit INSERT off the request thread.
// Stripped to a single method to avoid importing the transport package
// (which would create a cycle).
type AsyncDispatch func(name string, fn func(ctx context.Context))

// AuditWrites records every write request (POST/PUT/PATCH/DELETE) that lands
// on an audited path. Attached at the engine level so it covers admin
// endpoints AND user-side self-service AND the local login endpoint —
// shouldAuditPath is the gate.
//
// Path filter runs BEFORE the request body is read, so static asset / sub
// fetch / health probe traffic doesn't pay the io.ReadAll cost.
//
// The audit INSERT is dispatched through async (best-effort fire-and-forget,
// shielded + WaitGroup-tracked) so the request thread doesn't block on
// the fsync. Pre-fix every admin write blocked on a synchronous INSERT
// before flushing the response — ~5-50ms per write on SQLite, worse under
// contention. dispatch=nil keeps the legacy synchronous path (test harness
// + any wiring path that doesn't have an AsyncDispatcher) so tests stay
// deterministic.
func AuditWrites(auditSvc *audit.Service, dispatch AsyncDispatch) gin.HandlerFunc {
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
		// Capture the IP from the request thread (c.ClientIP is gin-context
		// bound — touching it from the dispatched goroutine after the
		// request returns is unsafe).
		entry := &domain.AuditEntry{
			Actor:      actor,
			Action:     actionName(c.Request.Method, path),
			Target:     path,
			BeforeJSON: auditJSON(request),
			AfterJSON:  auditJSON(after),
			IP:         c.ClientIP(),
			At:         time.Now(),
		}
		if dispatch != nil {
			dispatch("audit.insert", func(ctx context.Context) {
				if err := auditSvc.Insert(ctx, entry); err != nil {
					log.Warn("audit middleware insert failed", "err", err)
				}
			})
			return
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
	case path == "/api/auth/refresh":
		// Token refresh mints a fresh access+refresh pair — the
		// credential-re-issuance event a post-incident review needs
		// alongside login (e.g. a stolen refresh token kept alive). The
		// refresh_token in the body is already redacted by isSensitiveKey.
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
	// Non-JSON body. It may still carry credentials — e.g. a non-SPA client or a
	// login-form probe POSTing `upn=x&password=hunter2` to the unauthenticated
	// /api/auth/local/login, or a JSON body with a BOM/trailing comma the handler
	// later 400s. Capture runs at engine level BEFORE the handler, and the audit
	// log is operator-readable, so we must NOT store the raw body verbatim.
	// Form-encoded bodies are parsed and run through the same key redaction;
	// anything else is recorded as shape only.
	if isFormContentType(c.ContentType()) {
		if vals, err := url.ParseQuery(string(body)); err == nil && len(vals) > 0 {
			out := make(map[string]any, len(vals))
			for k, vs := range vals {
				if isSensitiveKey(k) {
					out[k] = "[REDACTED]"
					continue
				}
				if len(vs) == 1 {
					out[k] = vs[0]
				} else {
					out[k] = vs
				}
			}
			return out
		}
	}
	return map[string]any{"unparsed_body": true, "len": len(body)}
}

func isFormContentType(ct string) bool {
	return ct == "application/x-www-form-urlencoded"
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
