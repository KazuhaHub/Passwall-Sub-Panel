// Package middleware holds Gin middlewares used by the HTTP transport layer.
package middleware

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/jwtutil"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/auth"
)

// Keys under which middleware stores values in *gin.Context.
const (
	CtxClaims = "psp.claims"
	CtxUserID = "psp.user_id"
)

// CookieAccessToken is the cookie name set by the SAML ACS handler. We
// duplicate the constant here (instead of importing transport/http/handler)
// to keep middleware free of upstream package dependencies.
const CookieAccessToken = "psp_access"

// RequireAuth verifies a token (Authorization Bearer header OR HttpOnly
// cookie set by SAML ACS) and stores the parsed Claims in the context.
func RequireAuth(svc *auth.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		raw := bearerToken(c.GetHeader("Authorization"))
		if raw == "" {
			if cookie, err := c.Cookie(CookieAccessToken); err == nil {
				raw = cookie
			}
		}
		if raw == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "missing token"})
			return
		}
		claims, err := svc.Verify(raw)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid token"})
			return
		}
		c.Set(CtxClaims, claims)
		c.Set(CtxUserID, claims.UserID)
		c.Next()
	}
}

// RequireRole short-circuits with 403 unless the claims carry one of the
// allowed roles. Must run after RequireAuth.
func RequireRole(roles ...domain.Role) gin.HandlerFunc {
	allowed := make(map[domain.Role]struct{}, len(roles))
	for _, r := range roles {
		allowed[r] = struct{}{}
	}
	return func(c *gin.Context) {
		v, ok := c.Get(CtxClaims)
		if !ok {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "no auth context"})
			return
		}
		claims, ok := v.(*jwtutil.Claims)
		if !ok {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "bad auth context"})
			return
		}
		if _, allow := allowed[claims.Role]; !allow {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "role not permitted"})
			return
		}
		c.Next()
	}
}

func bearerToken(h string) string {
	const prefix = "Bearer "
	if len(h) > len(prefix) && strings.EqualFold(h[:len(prefix)], prefix) {
		return h[len(prefix):]
	}
	return ""
}

// ClaimsFrom retrieves the parsed JWT claims; nil if none.
func ClaimsFrom(c *gin.Context) *jwtutil.Claims {
	v, ok := c.Get(CtxClaims)
	if !ok {
		return nil
	}
	claims, _ := v.(*jwtutil.Claims)
	return claims
}
