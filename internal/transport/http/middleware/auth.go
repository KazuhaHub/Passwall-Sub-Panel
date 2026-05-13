// Package middleware holds Gin middlewares used by the HTTP transport layer.
package middleware

import (
	"container/list"
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

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

// UserLookup is the narrow user-store surface RequireAuth needs to
// re-validate that the JWT subject still exists and is allowed in.
// *user.Service satisfies it.
type UserLookup interface {
	Get(ctx context.Context, id int64) (*domain.User, error)
}

// RequireAuth verifies a token (Authorization Bearer header OR HttpOnly
// cookie set by SAML ACS) and stores the parsed Claims in the context.
//
// Authenticated requests also re-check the live user through a short in-memory
// LRU so deletes/disables/role changes take effect quickly without hitting the
// DB on every request.
func RequireAuth(svc *auth.Service, users UserLookup) gin.HandlerFunc {
	userCache := newAuthUserLRU(4096, 5*time.Second)
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
		u, ok := userCache.Get(claims.UserID)
		if !ok {
			liveUser, err := users.Get(c.Request.Context(), claims.UserID)
			if err != nil {
				if errors.Is(err, domain.ErrNotFound) {
					c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "account no longer exists"})
					return
				}
				c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "auth lookup failed"})
				return
			}
			u = authUserFromDomain(liveUser)
			userCache.Put(u)
		}
		if !u.Enabled {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "account disabled"})
			return
		}
		// Re-bind claims to the live DB role so demotions take effect for
		// RequireRole checks downstream.
		claims.Role = u.Role
		claims.UPN = u.UPN
		c.Set(CtxClaims, claims)
		c.Set(CtxUserID, claims.UserID)
		c.Next()
	}
}

type authUserSnapshot struct {
	ID      int64
	UPN     string
	Role    domain.Role
	Enabled bool
}

type authUserEntry struct {
	key     int64
	value   authUserSnapshot
	expires time.Time
}

type authUserLRU struct {
	mu    sync.Mutex
	max   int
	ttl   time.Duration
	ll    *list.List
	items map[int64]*list.Element
}

func newAuthUserLRU(max int, ttl time.Duration) *authUserLRU {
	return &authUserLRU{
		max:   max,
		ttl:   ttl,
		ll:    list.New(),
		items: make(map[int64]*list.Element, max),
	}
}

func authUserFromDomain(u *domain.User) authUserSnapshot {
	return authUserSnapshot{
		ID:      u.ID,
		UPN:     u.UPN,
		Role:    u.Role,
		Enabled: u.Enabled,
	}
}

func (c *authUserLRU) Get(id int64) (authUserSnapshot, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	el := c.items[id]
	if el == nil {
		return authUserSnapshot{}, false
	}
	entry := el.Value.(*authUserEntry)
	if time.Now().After(entry.expires) {
		c.remove(el)
		return authUserSnapshot{}, false
	}
	c.ll.MoveToFront(el)
	return entry.value, true
}

func (c *authUserLRU) Put(u authUserSnapshot) {
	if c.max <= 0 || c.ttl <= 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	if el := c.items[u.ID]; el != nil {
		entry := el.Value.(*authUserEntry)
		entry.value = u
		entry.expires = time.Now().Add(c.ttl)
		c.ll.MoveToFront(el)
		return
	}
	el := c.ll.PushFront(&authUserEntry{
		key:     u.ID,
		value:   u,
		expires: time.Now().Add(c.ttl),
	})
	c.items[u.ID] = el
	for c.ll.Len() > c.max {
		c.remove(c.ll.Back())
	}
}

func (c *authUserLRU) remove(el *list.Element) {
	if el == nil {
		return
	}
	entry := el.Value.(*authUserEntry)
	delete(c.items, entry.key)
	c.ll.Remove(el)
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
