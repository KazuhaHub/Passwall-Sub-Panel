package handler

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/auth"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/user"
)

// AuthSSOCompleteHandler exposes GET /api/auth/sso-complete.
//
// The SSO ACS / OIDC callback handlers set HttpOnly cookies then redirect
// the browser to /sso-callback (a public SPA page). That page calls this
// endpoint to bridge the HttpOnly cookie session into the frontend's
// sessionStorage-based auth model: we read and validate the cookie, clear
// it, and return the tokens in the JSON body so the SPA can store them
// exactly as it would after a local login.
type AuthSSOCompleteHandler struct {
	auth *auth.Service
	user *user.Service
}

func NewAuthSSOCompleteHandler(authSvc *auth.Service, userSvc *user.Service) *AuthSSOCompleteHandler {
	return &AuthSSOCompleteHandler{auth: authSvc, user: userSvc}
}

func (h *AuthSSOCompleteHandler) Complete(c *gin.Context) {
	accessToken, err := c.Cookie(CookieAccessToken)
	if err != nil || accessToken == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "No sso session"})
		return
	}
	refreshToken, _ := c.Cookie(CookieRefreshToken)

	claims, err := h.auth.Verify(accessToken)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid session"})
		return
	}

	// Clear the HttpOnly cookies — ownership transfers to the SPA's
	// sessionStorage.
	//
	// secure MUST match the value used when the cookie was set
	// (auth_saml.ACS / auth_oidc.Callback use isHTTPS(c)). Cookies are
	// keyed on (name, path, domain, secure); if secure flips between
	// set and delete the browser keeps the old cookie alive until its
	// natural TTL, so a logged-out user is still authenticated under
	// the HttpOnly cookie path for the remaining 120 min access TTL.
	secure := isHTTPS(c)
	c.SetCookie(CookieAccessToken, "", -1, CookieAuthPath, "", secure, true)
	c.SetCookie(CookieRefreshToken, "", -1, CookieAuthPath, "", secure, true)

	// Fetch the live user so the SPA receives the freshest display name.
	liveUser, err := h.user.Get(c.Request.Context(), claims.UserID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Account no longer exists"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Auth lookup failed"})
		return
	}

	c.JSON(http.StatusOK, loginResponse{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		User: userBrief{
			ID:          claims.UserID,
			UPN:         liveUser.UPN,
			DisplayName: liveUser.DisplayName,
			Role:        liveUser.Role,
		},
	})
}
