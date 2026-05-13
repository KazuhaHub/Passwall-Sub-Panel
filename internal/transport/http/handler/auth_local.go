package handler

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/auth"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/user"
)

// AuthLocalHandler exposes /api/auth/local/login and the public /methods
// metadata endpoint the login page consults.
type AuthLocalHandler struct {
	auth     *auth.Service
	user     *user.Service
	saml     *auth.SAMLService
	oidc     *auth.OIDCService
	settings ports.SettingsRepo
}

func NewAuthLocalHandler(authSvc *auth.Service, userSvc *user.Service, samlSvc *auth.SAMLService, oidcSvc *auth.OIDCService, settings ports.SettingsRepo) *AuthLocalHandler {
	return &AuthLocalHandler{auth: authSvc, user: userSvc, saml: samlSvc, oidc: oidcSvc, settings: settings}
}

// Methods reports which login methods are configured and which UI mode the
// login page should render. Public endpoint. Reads the runtime-editable
// login_mode from the DB on every call so admin edits propagate
// to the next visitor without a restart. "sso" is true when EITHER SAML
// or OIDC is enabled; the frontend further distinguishes via "saml" /
// "oidc" booleans when rendering provider-specific buttons.
func (h *AuthLocalHandler) activeLoginMode(c *gin.Context) string {
	s, err := h.settings.Load(c.Request.Context(), ports.UISettings{LoginMode: "dual"})
	if err != nil {
		return "dual"
	}
	return s.LoginMode
}

// localLoginDisallowedForUsers reports whether non-admin accounts should be
// rejected when they POST /api/auth/local/login. Two paths trigger it:
//   - LoginMode == sso_strict (legacy single-knob mode that implies this)
//   - DisallowUserLocalLogin == true (explicit toggle that works under any
//     login mode, e.g. dual mode where a local form is still rendered)
func (h *AuthLocalHandler) localLoginDisallowedForUsers(c *gin.Context) bool {
	s, err := h.settings.Load(c.Request.Context(), ports.UISettings{})
	if err != nil {
		return false
	}
	return s.LoginMode == "sso_strict" || s.DisallowUserLocalLogin
}

func (h *AuthLocalHandler) Methods(c *gin.Context) {
	defaults := ports.UISettings{LoginMode: "dual", SiteTitle: "Passwall"}
	s, err := h.settings.Load(c.Request.Context(), defaults)
	if err != nil {
		s = defaults
	}
	mode := s.LoginMode
	samlEnabled := h.saml != nil && h.saml.Enabled()
	oidcEnabled := h.oidc != nil && h.oidc.Enabled()
	ssoEnabled := samlEnabled || oidcEnabled
	if !ssoEnabled && (mode == "sso_first" || mode == "sso_strict" || mode == "dual") {
		mode = "local_only"
	}
	c.JSON(http.StatusOK, gin.H{
		"local":         true,
		"sso":           ssoEnabled,
		"saml":          samlEnabled,
		"oidc":          oidcEnabled,
		"login_mode":    mode,
		"site_title":    s.SiteTitle,
		"logo_url":      s.LogoURL,
		"logo_url_dark": s.LogoURLDark,
	})
}

type loginRequest struct {
	Username string `json:"username" binding:"required"`
	Password string `json:"password" binding:"required"`
}

type loginResponse struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	User         userBrief `json:"user"`
}

type userBrief struct {
	ID          int64       `json:"id"`
	Username    string      `json:"username"`
	DisplayName string      `json:"display_name,omitempty"`
	Role        domain.Role `json:"role"`
}

func (h *AuthLocalHandler) Login(c *gin.Context) {
	var req loginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	u, err := h.user.VerifyLocalPassword(c.Request.Context(), req.Username, req.Password)
	if err != nil {
		switch {
		case errors.Is(err, domain.ErrNotFound), errors.Is(err, domain.ErrUnauthorized):
			c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid credentials"})
		case errors.Is(err, domain.ErrForbidden):
			c.JSON(http.StatusForbidden, gin.H{"error": "account disabled"})
		default:
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		}
		return
	}
	// Non-admin local-login lock: either implied by sso_strict, or set
	// explicitly via the DisallowUserLocalLogin setting. /login/local
	// itself stays reachable so admins always have a break-glass path.
	if u.Role != domain.RoleAdmin && h.localLoginDisallowedForUsers(c) {
		c.JSON(http.StatusForbidden, gin.H{"error": "local login is restricted to administrators; please use SSO"})
		return
	}
	access, refresh, err := h.auth.IssueTokens(u)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, loginResponse{
		AccessToken:  access,
		RefreshToken: refresh,
		User: userBrief{
			ID: u.ID, Username: u.Username, DisplayName: u.DisplayName, Role: u.Role,
		},
	})
}
