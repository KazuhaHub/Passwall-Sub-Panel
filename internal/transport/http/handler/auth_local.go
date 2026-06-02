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
	auth       *auth.Service
	user       *user.Service
	saml       *auth.SAMLService
	oidc       *auth.OIDCService
	settings   ports.SettingsRepo
	authEvents ports.AuthEventRepo
}

func NewAuthLocalHandler(authSvc *auth.Service, userSvc *user.Service, samlSvc *auth.SAMLService, oidcSvc *auth.OIDCService, settings ports.SettingsRepo, authEvents ports.AuthEventRepo) *AuthLocalHandler {
	return &AuthLocalHandler{auth: authSvc, user: userSvc, saml: samlSvc, oidc: oidcSvc, settings: settings, authEvents: authEvents}
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
	// Mirror the login path's self-service exemption: a traffic-exceeded or
	// expired user must still be able to refresh so their session survives long
	// enough to reach the self-service emergency-access page. Without this they
	// get bounced to the login screen every access-TTL. Other disable reasons
	// (admin / pending / blocked) stay hard-blocked.
	if !u.Enabled && !domain.SelfServiceDisableReason(u.AutoDisabledReason) {
		c.JSON(http.StatusUnauthorized, gin.H{
			"error":  disabledReasonMessage(u.AutoDisabledReason),
			"reason": string(u.AutoDisabledReason),
		})
		return
	}
	// TokenVersion gate — mirror middleware/auth.go's access-token check on the
	// refresh path. TokenVersion is bumped on local password change, admin
	// password reset, role change, and disable; without this check a refresh
	// token issued before the bump would keep minting valid access+refresh
	// pairs for the full refresh TTL (default 7d), defeating the documented
	// "password change revokes other sessions" guarantee. (Disable is already
	// caught by the !u.Enabled branch above; this closes the password/role
	// rotation hole.)
	if u.TokenVersion != claims.TokenVersion {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Session revoked, please sign in again"})
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
			recordAuthEvent(c, h.authEvents, domain.AuthMethodLocal, domain.AuthOutcomeFailure, 0, req.UPN, "invalid_credentials")
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid credentials"})
		case errors.Is(err, domain.ErrForbidden):
			// u is non-nil here so the message can name the actual reason —
			// otherwise the user just sees "Account disabled" and has no idea
			// whether to wait, contact the admin, or check their quota.
			reason := domain.DisabledNone
			uid := int64(0)
			if u != nil {
				reason = u.AutoDisabledReason
				uid = u.ID
			}
			recordAuthEvent(c, h.authEvents, domain.AuthMethodLocal, domain.AuthOutcomeFailure, uid, req.UPN, "disabled:"+string(reason))
			c.JSON(http.StatusForbidden, gin.H{
				"error":  disabledReasonMessage(reason),
				"reason": string(reason),
			})
		default:
			recordAuthEvent(c, h.authEvents, domain.AuthMethodLocal, domain.AuthOutcomeFailure, 0, req.UPN, "error")
			respondError(c, err)
		}
		return
	}
	// Non-admin local-login lock is controlled by DisallowUserLocalLogin.
	// /login/local itself stays reachable so admins always have a break-glass path.
	if u.Role != domain.RoleAdmin && h.localLoginDisallowedForUsers(c) {
		recordAuthEvent(c, h.authEvents, domain.AuthMethodLocal, domain.AuthOutcomeFailure, u.ID, u.UPN, "local_login_disallowed")
		c.JSON(http.StatusForbidden, gin.H{"error": "Local login is restricted to administrators; please use SSO"})
		return
	}
	access, refresh, err := h.auth.IssueTokens(u)
	if err != nil {
		recordAuthEvent(c, h.authEvents, domain.AuthMethodLocal, domain.AuthOutcomeFailure, u.ID, u.UPN, "token_error")
		respondError(c, err)
		return
	}
	recordAuthEvent(c, h.authEvents, domain.AuthMethodLocal, domain.AuthOutcomeSuccess, u.ID, u.UPN, "")
	c.JSON(http.StatusOK, loginResponse{
		AccessToken:  access,
		RefreshToken: refresh,
		User: userBrief{
			ID: u.ID, UPN: u.UPN, DisplayName: u.DisplayName, Role: u.Role,
		},
	})
}
