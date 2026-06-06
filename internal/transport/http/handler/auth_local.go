package handler

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/log"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/auth"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/captcha"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/loginguard"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/twofa"
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
	guard      *loginguard.Guard
	captcha    *captcha.Service
	twofa      *twofa.Service
}

func NewAuthLocalHandler(authSvc *auth.Service, userSvc *user.Service, samlSvc *auth.SAMLService, oidcSvc *auth.OIDCService, settings ports.SettingsRepo, authEvents ports.AuthEventRepo, guard *loginguard.Guard, captchaSvc *captcha.Service, twofaSvc *twofa.Service) *AuthLocalHandler {
	return &AuthLocalHandler{auth: authSvc, user: userSvc, saml: samlSvc, oidc: oidcSvc, settings: settings, authEvents: authEvents, guard: guard, captcha: captchaSvc, twofa: twofaSvc}
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
	// Captcha config for the login form. captcha_required is the upfront
	// requirement (always-mode); after_failures mode flips on later via the
	// captcha_required flag returned on a failed login. site_key is public;
	// the secret never leaves the server.
	captchaProvider := s.CaptchaProvider
	if captchaProvider == "" {
		captchaProvider = captcha.ProviderImage
	}
	c.JSON(http.StatusOK, gin.H{
		"local":            localShown,
		"sso":              ssoShown,
		"saml":             ssoShown && samlEnabled,
		"oidc":             ssoShown && oidcEnabled,
		"login_mode":       mode,
		"site_title":       s.SiteTitle,
		"app_title":        s.AppTitle,
		"icon_url":         s.IconURL,
		"logo_url":         s.LogoURL,
		"logo_url_dark":    s.LogoURLDark,
		"footer_text":      s.FooterText,
		"theme_color":      s.ThemeColor,
		"timezone":         s.Timezone,
		"captcha_enabled":  s.CaptchaEnabled,
		"captcha_provider": captchaProvider,
		"captcha_site_key": s.CaptchaSiteKey,
		"captcha_required": s.CaptchaEnabled && s.CaptchaTrigger == "always",
		// Self-service password recovery (v3.7.0): drives the "Forgot password?"
		// link + which reset form (link vs OTP) the reset page renders.
		"password_recovery_enabled":  s.PasswordRecoveryEnabled,
		"password_recovery_delivery": s.PasswordRecoveryDelivery,
		// Self-service registration (v3.7.0): the "Create account" link + whether
		// the register page should expect an email-verify step. The email-domain
		// allow-list is deliberately NOT exposed (server-side only).
		"registration_enabled":                   s.RegistrationEnabled,
		"registration_require_email_verification": !s.RegistrationAllowUnverified,
		"registration_delivery":                   s.RegistrationDelivery,
	})
}

// Captcha issues a fresh image-captcha challenge for the login form. Only
// meaningful for the self-hosted "image" provider; token providers render
// client-side from the site key (so this returns enabled:false for them and
// when captcha is off). Shares the login rate limiter.
func (h *AuthLocalHandler) Captcha(c *gin.Context) {
	s, err := h.settings.Load(c.Request.Context(), ports.UISettings{})
	if err != nil || !s.CaptchaEnabled {
		c.JSON(http.StatusOK, gin.H{"enabled": false})
		return
	}
	ch, err := h.captcha.Issue(c.Request.Context(), s)
	if err != nil {
		respondError(c, err)
		return
	}
	if ch == nil {
		// Token provider — nothing to issue server-side.
		c.JSON(http.StatusOK, gin.H{"enabled": false})
		return
	}
	c.JSON(http.StatusOK, gin.H{"enabled": true, "captcha_id": ch.ID, "image": ch.Image})
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
	case domain.DisabledPendingEmailVerify:
		return "请先验证邮箱后再登录，验证邮件已发送到你的邮箱。"
	case domain.DisabledPendingDelete:
		return "账号已被标记为待删除，请联系管理员。"
	default:
		return "账号已被停用，请联系管理员。"
	}
}

type loginRequest struct {
	UPN      string `json:"upn" binding:"required"`
	Password string `json:"password" binding:"required"`
	// Captcha response (optional; required only when the guard demands it).
	// Image provider fills captcha_id+captcha_answer; token providers
	// (turnstile/recaptcha/hcaptcha) fill captcha_token.
	CaptchaID     string `json:"captcha_id,omitempty"`
	CaptchaAnswer string `json:"captcha_answer,omitempty"`
	CaptchaToken  string `json:"captcha_token,omitempty"`
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
	ip := c.ClientIP()
	// Live config, loaded once and shared by the guard + captcha checks below.
	s, _ := h.settings.Load(c.Request.Context(), ports.UISettings{})

	// Pre-password guard: account lockout + captcha requirement. Runs BEFORE
	// the bcrypt check so brute-force automation is stopped before it can even
	// probe a password.
	decision, gerr := h.guard.Evaluate(c.Request.Context(), s, ip, req.UPN)
	if gerr != nil {
		// Fail open: a failed history read must never lock everyone out.
		log.Warn("login guard evaluate failed", "err", gerr)
		decision = loginguard.Decision{}
	}
	if decision.Locked {
		recordAuthEvent(c, h.authEvents, domain.AuthMethodLocal, domain.AuthOutcomeFailure, 0, req.UPN, domain.AuthReasonLockedOut)
		retry := int(decision.RetryAfter.Seconds()) + 1
		c.Header("Retry-After", strconv.Itoa(retry))
		c.JSON(http.StatusTooManyRequests, gin.H{
			"error":       "Too many failed attempts, please try again later",
			"locked":      true,
			"retry_after": retry,
		})
		return
	}
	if decision.CaptchaRequired {
		ok, cerr := h.captcha.Verify(c.Request.Context(), s, captcha.Response{
			ChallengeID: req.CaptchaID, Answer: req.CaptchaAnswer, Token: req.CaptchaToken, RemoteIP: ip,
		})
		if cerr != nil {
			// Fail CLOSED: a captcha the panel can't verify (misconfigured
			// provider, or a transient siteverify outage on a token provider)
			// must not silently disable the gate and let automation through.
			// The image provider is the network-free default; admins who pick a
			// token provider accept its reachability requirement. Log for ops.
			log.Warn("captcha verify error", "err", cerr)
		}
		if cerr != nil || !ok {
			// Reject WITHOUT recording an invalid_credentials failure — a wrong
			// or unverifiable captcha isn't a password guess and must not feed
			// the lockout count.
			c.JSON(http.StatusBadRequest, gin.H{
				"error":            "Captcha is required or incorrect",
				"captcha_required": true,
			})
			return
		}
	}

	u, err := h.user.VerifyLocalPassword(c.Request.Context(), req.UPN, req.Password)
	if err != nil {
		switch {
		case errors.Is(err, domain.ErrNotFound), errors.Is(err, domain.ErrUnauthorized):
			recordAuthEvent(c, h.authEvents, domain.AuthMethodLocal, domain.AuthOutcomeFailure, 0, req.UPN, domain.AuthReasonInvalidCredentials)
			body := gin.H{"error": "Invalid credentials"}
			// Re-evaluate after recording this failure so the response can tell
			// the client whether the retry now needs a captcha (after_failures
			// mode flips on here).
			if d, e := h.guard.Evaluate(c.Request.Context(), s, ip, req.UPN); e == nil && d.CaptchaRequired {
				body["captcha_required"] = true
			}
			c.JSON(http.StatusUnauthorized, body)
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
	// 2FA gate: when the account has TOTP enabled, the password check is only the
	// first factor. Hand back a short-lived pending token (not a real session) and
	// require /auth/2fa/verify to complete the login.
	if u.TOTPEnabled {
		pending, perr := h.auth.IssuePending(u)
		if perr != nil {
			recordAuthEvent(c, h.authEvents, domain.AuthMethodLocal, domain.AuthOutcomeFailure, u.ID, u.UPN, "token_error")
			respondError(c, perr)
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "2fa_required", "pending_token": pending})
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

// TwoFAVerify completes a 2FA login: it exchanges a pending token + a TOTP (or
// recovery) code for a real session. It sits behind the same login rate-limiter
// as /login so the code can't be brute-forced.
func (h *AuthLocalHandler) TwoFAVerify(c *gin.Context) {
	var req struct {
		PendingToken string `json:"pending_token"`
		Code         string `json:"code"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request"})
		return
	}
	claims, err := h.auth.VerifyPending(req.PendingToken)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid or expired 2FA session; please log in again"})
		return
	}
	u, err := h.user.Get(c.Request.Context(), claims.UserID)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid or expired 2FA session; please log in again"})
		return
	}
	// Re-gate against the live account before completing the login — the pending
	// token lives up to 5 minutes, during which an admin may disable the account,
	// reset its password, or change its role. Mirror the Refresh path so /2fa/verify
	// is not the one login-completion path that mints a session for an account the
	// system has already revoked. (Disable is the security-relevant case; the
	// self-service-disable reasons stay loginable, matching Login/Refresh.)
	if !u.Enabled && !domain.SelfServiceDisableReason(u.AutoDisabledReason) {
		recordAuthEvent(c, h.authEvents, domain.AuthMethodLocal, domain.AuthOutcomeFailure, u.ID, u.UPN, "disabled:"+string(u.AutoDisabledReason))
		c.JSON(http.StatusForbidden, gin.H{
			"error":  disabledReasonMessage(u.AutoDisabledReason),
			"reason": string(u.AutoDisabledReason),
		})
		return
	}
	if u.TokenVersion != claims.TokenVersion {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid or expired 2FA session; please log in again"})
		return
	}
	ok, err := h.twofa.VerifyLogin(c.Request.Context(), u.ID, req.Code)
	if err != nil {
		respondError(c, err)
		return
	}
	if !ok {
		recordAuthEvent(c, h.authEvents, domain.AuthMethodLocal, domain.AuthOutcomeFailure, u.ID, u.UPN, "2fa_invalid")
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid 2FA code"})
		return
	}
	access, refresh, err := h.auth.IssueTokens(u)
	if err != nil {
		recordAuthEvent(c, h.authEvents, domain.AuthMethodLocal, domain.AuthOutcomeFailure, u.ID, u.UPN, "token_error")
		respondError(c, err)
		return
	}
	recordAuthEvent(c, h.authEvents, domain.AuthMethodLocal, domain.AuthOutcomeSuccess, u.ID, u.UPN, "2fa")
	c.JSON(http.StatusOK, loginResponse{
		AccessToken:  access,
		RefreshToken: refresh,
		User: userBrief{
			ID: u.ID, UPN: u.UPN, DisplayName: u.DisplayName, Role: u.Role,
		},
	})
}
