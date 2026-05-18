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
// rejected when they POST /api/auth/local/login. The /login/local route stays
// reachable so admins always have a break-glass path.
func (h *AuthLocalHandler) localLoginDisallowedForUsers(c *gin.Context) bool {
	s, err := h.settings.Load(c.Request.Context(), ports.UISettings{})
	if err != nil {
		return false
	}
	return s.DisallowUserLocalLogin
}

func (h *AuthLocalHandler) Methods(c *gin.Context) {
	defaults := ports.UISettings{
		LoginMode: "dual",
		SiteTitle: "Kazuha Hub Passwall",
		AppTitle:  "Passwall",
		// IconURL deliberately blank — frontend has a built-in fallback.
	}
	s, err := h.settings.Load(c.Request.Context(), defaults)
	if err != nil {
		s = defaults
	}
	mode := s.LoginMode
	samlEnabled := h.saml != nil && h.saml.Enabled()
	oidcEnabled := h.oidc != nil && h.oidc.Enabled()
	ssoEnabled := samlEnabled || oidcEnabled
	if !ssoEnabled && (mode == "sso_redirect" || mode == "sso_first" || mode == "dual") {
		mode = "local_only"
	}
	// Reflect the active mode in the booleans so the frontend can render off
	// a single source of truth without re-implementing the mode rules.
	//   local_only   → hide SSO buttons even if providers are configured
	//   sso_redirect → still expose SSO so the redirect target is reachable;
	//                  frontend bypasses the form entirely via login_mode
	localShown := mode != "sso_redirect"
	ssoShown := ssoEnabled && mode != "local_only"
	c.JSON(http.StatusOK, gin.H{
		"local":         localShown,
		"sso":           ssoShown,
		"saml":          ssoShown && samlEnabled,
		"oidc":          ssoShown && oidcEnabled,
		"login_mode":    mode,
		"site_title":    s.SiteTitle,
		"app_title":     s.AppTitle,
		"icon_url":      s.IconURL,
		"logo_url":      s.LogoURL,
		"logo_url_dark": s.LogoURLDark,
		"footer_text":   s.FooterText,
		"theme_color":   s.ThemeColor,
		"timezone":      s.Timezone,
	})
}

// disabledReasonMessage produces a user-facing explanation for why a login
// attempt was rejected on a disabled account. Reasons that the panel exempts
// (traffic_exceeded / expired) never reach this code path — they're allowed
// through VerifyLocalPassword — so they intentionally aren't listed.
func disabledReasonMessage(reason domain.AutoDisabledReason) string {
	switch reason {
	case domain.DisabledManual:
		return "账号已被管理员停用，请联系管理员。"
	case domain.DisabledBlockedClient:
		return "账号因使用了被禁的客户端而停用，请联系管理员。"
	case domain.DisabledPendingApproval:
		return "账号正在等待管理员审核，请稍后再试。"
	case domain.DisabledPendingDelete:
		return "账号已被标记为待删除，请联系管理员。"
	default:
		return "账号已被停用，请联系管理员。"
	}
}

type loginRequest struct {
	UPN      string `json:"upn" binding:"required"`
	Password string `json:"password" binding:"required"`
}

type loginResponse struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	User         userBrief `json:"user"`
}

type userBrief struct {
	ID          int64       `json:"id"`
	UPN         string      `json:"upn"`
	DisplayName string      `json:"display_name,omitempty"`
	Role        domain.Role `json:"role"`
}

// Refresh issues a new (access, refresh) pair from a still-valid
// refresh JWT. Used by the SPA's 401 interceptor so a user who left a
// tab open past the access TTL doesn't lose their session — and any
// half-typed form — to a hard bounce back to /login.
//
// Reads the live user row again so a role change / disable that
// happened during the access-token lifetime takes effect immediately
// on the next refresh (a 7-day refresh window otherwise outruns admin
// actions). Disabled accounts get a 401 — same shape as Login() so
// the interceptor can fall through to the logout path.
type refreshRequest struct {
	RefreshToken string `json:"refresh_token" binding:"required"`
}

func (h *AuthLocalHandler) Refresh(c *gin.Context) {
	var req refreshRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing refresh token"})
		return
	}
	claims, err := h.auth.VerifyRefresh(req.RefreshToken)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid or expired refresh token"})
		return
	}
	u, err := h.user.Get(c.Request.Context(), claims.UserID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Account no longer exists"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "auth lookup failed"})
		return
	}
	if !u.Enabled {
		c.JSON(http.StatusUnauthorized, gin.H{
			"error":  disabledReasonMessage(u.AutoDisabledReason),
			"reason": string(u.AutoDisabledReason),
		})
		return
	}
	access, refresh, err := h.auth.IssueTokens(u)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "token reissue failed"})
		return
	}
	c.JSON(http.StatusOK, loginResponse{
		AccessToken:  access,
		RefreshToken: refresh,
		User: userBrief{
			ID: u.ID, UPN: u.UPN, DisplayName: u.DisplayName, Role: u.Role,
		},
	})
}

func (h *AuthLocalHandler) Login(c *gin.Context) {
	var req loginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	u, err := h.user.VerifyLocalPassword(c.Request.Context(), req.UPN, req.Password)
	if err != nil {
		switch {
		case errors.Is(err, domain.ErrNotFound), errors.Is(err, domain.ErrUnauthorized):
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid credentials"})
		case errors.Is(err, domain.ErrForbidden):
			// u is non-nil here so the message can name the actual reason —
			// otherwise the user just sees "Account disabled" and has no idea
			// whether to wait, contact the admin, or check their quota.
			reason := domain.DisabledNone
			if u != nil {
				reason = u.AutoDisabledReason
			}
			c.JSON(http.StatusForbidden, gin.H{
				"error":  disabledReasonMessage(reason),
				"reason": string(reason),
			})
		default:
			respondError(c, err)
		}
		return
	}
	// Non-admin local-login lock is controlled by DisallowUserLocalLogin.
	// /login/local itself stays reachable so admins always have a break-glass path.
	if u.Role != domain.RoleAdmin && h.localLoginDisallowedForUsers(c) {
		c.JSON(http.StatusForbidden, gin.H{"error": "Local login is restricted to administrators; please use SSO"})
		return
	}
	access, refresh, err := h.auth.IssueTokens(u)
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, loginResponse{
		AccessToken:  access,
		RefreshToken: refresh,
		User: userBrief{
			ID: u.ID, UPN: u.UPN, DisplayName: u.DisplayName, Role: u.Role,
		},
	})
}
