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
//
// TTL = 60s. Pre-v3.6.1-beta.6 this was 5s, which meant any authenticated
// client polling faster than 5s (admin dashboards, /traffic/top, the user
// portal's auto-refresh) bypassed the cache on every request — at active-
// session scale that was ~one DB user-lookup per request per logged-in
// admin. Bumping to 60s caps the worst-case "revoked JWT still works"
// window to 60s for changes made outside user.Service. Normal service writes
// actively invalidate the shared cache, so revocation takes effect on the
// next authenticated request.
func RequireAuth(svc *auth.Service, users UserLookup, userCache *AuthUserCache) gin.HandlerFunc {
	if userCache == nil {
		userCache = NewAuthUserCache(4096, 60*time.Second)
	}
	return func(c *gin.Context) {
		raw := bearerToken(c.GetHeader("Authorization"))
		if raw == "" {
			if cookie, err := c.Cookie(CookieAccessToken); err == nil {
				raw = cookie
			}
		}
		if raw == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "Missing token"})
			return
		}
		claims, err := svc.Verify(raw)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "Invalid token"})
			return
		}
		u, ok := userCache.Get(claims.UserID)
		if !ok {
			liveUser, err := users.Get(c.Request.Context(), claims.UserID)
			if err != nil {
				if errors.Is(err, domain.ErrNotFound) {
					c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "Account no longer exists"})
					return
				}
				c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "Auth lookup failed"})
				return
			}
			u = authUserFromDomain(liveUser)
			userCache.Put(u)
		}
		if !u.Enabled && (!domain.AccountLoginAllowed(u.Enabled, u.AutoDisabledReason) || !allowSelfServiceForDisabledUser(c, u.AutoDisabledReason)) {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "Account disabled"})
			return
		}
		// TokenVersion gate: a JWT whose tv claim is older than the live
		// row's value has been revoked (admin disable / role demote /
		// password change all bump the version). The cache TTL caps how
		// long a stale token can survive past an out-of-band bump (see the
		// TTL note above). Service writes invalidate the entry immediately.
		if u.TokenVersion != claims.TokenVersion {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "Session revoked, please sign in again"})
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
	ID                 int64
	UPN                string
	Role               domain.Role
	Enabled            bool
	AutoDisabledReason domain.AutoDisabledReason
	TokenVersion       int
}

type authUserEntry struct {
	key     int64
	value   authUserSnapshot
	expires time.Time
}

// AuthUserCache is a bounded, expiring cache of the live account fields used
// to authorize a JWT. One instance should be shared by all protected routes.
type AuthUserCache struct {
	mu    sync.Mutex
	max   int
	ttl   time.Duration
	ll    *list.List
	items map[int64]*list.Element
}

func NewAuthUserCache(max int, ttl time.Duration) *AuthUserCache {
	return &AuthUserCache{
		max:   max,
		ttl:   ttl,
		ll:    list.New(),
		items: make(map[int64]*list.Element, max),
	}
}

func authUserFromDomain(u *domain.User) authUserSnapshot {
	return authUserSnapshot{
		ID:                 u.ID,
		UPN:                u.UPN,
		Role:               u.Role,
		Enabled:            u.Enabled,
		AutoDisabledReason: u.AutoDisabledReason,
		TokenVersion:       u.TokenVersion,
	}
}

// allowSelfServiceForDisabledUser keeps the user portal fully usable for
// accounts auto-disabled by traffic-exceeded or expiry: they can log in, view
// their profile/traffic/rules, change password, reset credentials, request
// emergency access, etc. The disable still bites at the 3X-UI side (proxy
// connections are refused), but the panel itself must stay reachable so the
// user can see WHY they're cut off and take action.
//
// Other disable reasons (block_violation, pending_delete, admin manual) keep
// the previous "401 on everything" behavior — those are punitive states, not
// quota states.
func allowSelfServiceForDisabledUser(c *gin.Context, reason domain.AutoDisabledReason) bool {
	if !domain.AccountLoginAllowed(false, reason) {
		return false
	}
	path := c.FullPath()
	if path == "" {
		path = c.Request.URL.Path
	}
	// Any route registered under the userGroup (`/api/user/me/...`) is the
	// authenticated self-service surface. Allowing the whole prefix keeps this
	// from regressing whenever a new self-service endpoint is added.
	return strings.HasPrefix(path, "/api/user/me")
}

func (c *AuthUserCache) Get(id int64) (authUserSnapshot, bool) {
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

func (c *AuthUserCache) Put(u authUserSnapshot) {
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

// Invalidate removes an account snapshot so the next request must read the
// updated user row. It is safe to call for an ID that is not cached.
func (c *AuthUserCache) Invalidate(id int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if el := c.items[id]; el != nil {
		c.remove(el)
	}
}

func (c *AuthUserCache) remove(el *list.Element) {
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
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "No auth context"})
			return
		}
		claims, ok := v.(*jwtutil.Claims)
		if !ok {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "Bad auth context"})
			return
		}
		if _, allow := allowed[claims.Role]; !allow {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "Role not permitted"})
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
