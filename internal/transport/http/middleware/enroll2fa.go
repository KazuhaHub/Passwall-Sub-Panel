package middleware

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/log"
)

// EnrollChecker reports whether the user must set up a second factor before
// using the panel (authpolicy.Service satisfies it).
type EnrollChecker interface {
	MustEnroll(ctx context.Context, u *domain.User) (bool, error)
}

// enroll2FAAllowlist is the set of authenticated routes a not-yet-enrolled user
// may still reach: read their own profile (to drive the gate UI) and the 2FA /
// passkey enrollment ceremonies themselves. Everything else under the
// authenticated API is blocked until they enroll. Matched against c.FullPath().
var enroll2FAAllowlist = map[string]struct{}{
	"/api/user/me":                 {},
	"/api/user/me/2fa/begin":       {},
	"/api/user/me/2fa/enable":      {},
	"/api/user/me/passkeys":        {},
	"/api/user/me/passkeys/begin":  {},
	"/api/user/me/passkeys/finish": {},
}

// Require2FAEnrollment is the server-side hard gate for the "require 2FA" policy:
// a user who must enroll a second factor but hasn't is refused everything except
// the allowlisted enrollment + self-read routes, with a machine-readable code so
// the SPA can route to its enrollment screen. Must run AFTER RequireAuth.
//
// The frontend has its own gate for UX; this backstops direct API calls. It
// loads the user fresh (no cache) so enrolling takes effect on the next request
// with no staleness — acceptable for a small panel's request volume.
func Require2FAEnrollment(checker EnrollChecker, users UserLookup) gin.HandlerFunc {
	return func(c *gin.Context) {
		if _, ok := enroll2FAAllowlist[c.FullPath()]; ok {
			c.Next()
			return
		}
		claims := ClaimsFrom(c)
		if claims == nil { // RequireAuth runs first; be defensive.
			c.Next()
			return
		}
		// Deliberate fail-OPEN on a transient lookup/eval error: this is an
		// enrollment nudge, not the auth boundary (RequireAuth already failed
		// closed on the token). A required-but-unenrolled user slipping through
		// for the duration of a DB/settings blip is a brief policy lapse, not a
		// breach — far better than 403-ing every user on any settings hiccup.
		// Logged so the lapse is observable.
		u, err := users.Get(c.Request.Context(), claims.UserID)
		if err != nil {
			log.Warn("2fa enrollment gate: user lookup failed, allowing through", "user_id", claims.UserID, "err", err)
			c.Next()
			return
		}
		must, err := checker.MustEnroll(c.Request.Context(), u)
		if err != nil {
			log.Warn("2fa enrollment gate: policy eval failed, allowing through", "user_id", claims.UserID, "err", err)
			c.Next()
			return
		}
		if must {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
				"error": "Two-factor authentication setup is required before you can continue",
				"code":  "2fa_enrollment_required",
			})
			return
		}
		c.Next()
	}
}
