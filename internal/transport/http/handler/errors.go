package handler

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/log"
)

// respondError writes a JSON error using one of the domain sentinels to
// pick the HTTP status, with a stable user-facing message that never
// leaks the underlying error string. The raw err is logged so admins
// can still debug from server logs.
//
// Mapping (covers the domain.Err* sentinels used across the codebase):
//
//	ErrValidation        → 400 (re-uses err.Error() because validation
//	                       messages are intentionally user-facing —
//	                       "panel name required", "invalid recipient")
//	ErrUnauthorized      → 401, "Unauthorized"
//	ErrForbidden         → 403, "Forbidden"
//	ErrNotFound          → 404, "Not found"
//	ErrConflict          → 409, "Conflict"
//	ErrSSONoAccount      → 404, "No SSO-linked account for this identity"
//	ErrSSOAccountConflict→ 409, "SSO identity conflicts with an existing account"
//	default              → 500, "Internal server error"
//
// Use this in every handler's "unexpected error" branch instead of
// returning err.Error() raw — that path used to leak GORM / SMTP /
// 3X-UI internals to the browser.
func respondError(c *gin.Context, err error) {
	respondErrorDetail(c, err, true)
}

// respondPublicError is respondError for PUBLIC, unauthenticated endpoints
// (register / verify-email / reset-password). It maps the user-facing domain
// sentinels identically, but its default (non-sentinel) branch returns a
// generic 500 instead of echoing err.Error(): an anonymous caller must never
// receive raw DB / SMTP / 3X-UI internals (driver, table/constraint names,
// file paths). The full error is still logged server-side for debugging.
func respondPublicError(c *gin.Context, err error) {
	respondErrorDetail(c, err, false)
}

// respondErrorDetail is the shared sentinel→status mapping. leakDetail controls
// ONLY the default (non-sentinel internal-error) branch: true for staff/admin
// handlers that want the diagnostic string in the response, false for public
// handlers that must keep it generic. The domain-sentinel branches are safe to
// surface to anyone (author-controlled validation text, stable status labels).
func respondErrorDetail(c *gin.Context, err error, leakDetail bool) {
	if err == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Internal server error"})
		return
	}
	switch {
	case errors.Is(err, domain.ErrValidation):
		// Validation messages are author-controlled (no GORM/SMTP leakage)
		// and the caller wants the user to see them.
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
	case errors.Is(err, domain.ErrUnauthorized):
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
	case errors.Is(err, domain.ErrForbidden):
		c.JSON(http.StatusForbidden, gin.H{"error": "Forbidden"})
	case errors.Is(err, domain.ErrNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": "Not found"})
	case errors.Is(err, domain.ErrConflict):
		c.JSON(http.StatusConflict, gin.H{"error": "Conflict"})
	case errors.Is(err, domain.ErrAlreadyExists):
		c.JSON(http.StatusConflict, gin.H{"error": "Already exists"})
	case errors.Is(err, domain.ErrSSONoAccount):
		c.JSON(http.StatusNotFound, gin.H{"error": "No SSO-linked account for this identity"})
	case errors.Is(err, domain.ErrSSOAccountConflict):
		c.JSON(http.StatusConflict, gin.H{"error": "SSO identity conflicts with an existing account"})
	default:
		// Internal error — always log full detail server-side. Whether the
		// raw string also goes to the caller depends on leakDetail: staff/admin
		// handlers (respondError) surface it for useful bug reports; public
		// unauthenticated handlers (respondPublicError) must NOT, or they leak
		// GORM/SMTP/3X-UI internals (driver, table/constraint names, paths) to
		// anonymous callers.
		path := c.FullPath()
		if path == "" && c.Request != nil {
			path = c.Request.URL.Path
		}
		log.Warn("handler internal error", "path", path, "err", err.Error())
		if leakDetail {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Internal server error"})
		}
	}
}
