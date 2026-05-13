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
		c.JSON(http.StatusUnauthorized, gin.H{"error": "no sso session"})
		return
	}
	refreshToken, _ := c.Cookie(CookieRefreshToken)

	claims, err := h.auth.Verify(accessToken)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid session"})
		return
	}

	// Clear the HttpOnly cookies — ownership transfers to the SPA's sessionStorage.
	c.SetCookie(CookieAccessToken, "", -1, "/", "", false, true)
	c.SetCookie(CookieRefreshToken, "", -1, "/", "", false, true)

	// Fetch the live user so the SPA receives the freshest display name —
	// the JWT only carries Username/Role to keep tokens compact.
	liveUser, err := h.user.Get(c.Request.Context(), claims.UserID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "account no longer exists"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "auth lookup failed"})
		return
	}

	c.JSON(http.StatusOK, loginResponse{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		User: userBrief{
			ID:          claims.UserID,
			Username:    liveUser.Username,
			DisplayName: liveUser.DisplayName,
			Role:        liveUser.Role,
		},
	})
}
