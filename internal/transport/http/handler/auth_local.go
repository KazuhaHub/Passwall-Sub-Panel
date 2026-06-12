package handler

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/jwtutil"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/log"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/auth"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/captcha"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/login2fa"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/loginguard"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/passkey"
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
	settings   ports.ScopedSettings
	authEvents ports.AuthEventRepo
	guard      *loginguard.Guard
	captcha    *captcha.Service
	twofa      *twofa.Service
	passkey    *passkey.Service
	login2fa   *login2fa.Service
}

func NewAuthLocalHandler(authSvc *auth.Service, userSvc *user.Service, samlSvc *auth.SAMLService, oidcSvc *auth.OIDCService, settings ports.ScopedSettings, authEvents ports.AuthEventRepo, guard *loginguard.Guard, captchaSvc *captcha.Service, twofaSvc *twofa.Service, passkeySvc *passkey.Service, login2faSvc *login2fa.Service) *AuthLocalHandler {
	return &AuthLocalHandler{auth: authSvc, user: userSvc, saml: samlSvc, oidc: oidcSvc, settings: settings, authEvents: authEvents, guard: guard, captcha: captchaSvc, twofa: twofaSvc, passkey: passkeySvc, login2fa: login2faSvc}
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
		// Per-context captcha (v3.7.0): the register / forgot forms each gate their
		// widget on these (always-on when the admin enables that context).
		"captcha_register_required": s.CaptchaRegisterEnabled,
		"captcha_forgot_required":   s.CaptchaForgotEnabled,
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
		// Passkeys (v3.7.0): passkey_passwordless drives whether the login page
		// shows a "Sign in with a passkey" button (usernameless discoverable
		// login). passkey_enabled alone only allows enrollment as a 2nd factor.
		"passkey_enabled":      s.PasskeyEnabled,
		"passkey_passwordless": s.PasskeyEnabled && s.PasskeyPasswordless,
		// Drives the login page's "resend email code" countdown so it matches the
		// server-side per-account cooldown.
		"twofa_email_resend_cooldown_sec": s.TwoFAEmailResendCooldownSec,
	})
}

// Captcha issues a fresh image-captcha challenge for any form that needs one
// (login / register / forgot). Only meaningful for the self-hosted "image"
// provider; token providers render client-side from the site key (so this returns
// enabled:false for them and when no context uses captcha). Shares the login
// rate limiter. The image store is shared, so a challenge issued here verifies on
// any of the three forms.
func (h *AuthLocalHandler) Captcha(c *gin.Context) {
	s, err := h.settings.Load(c.Request.Context(), ports.UISettings{})
	if err != nil || !(s.CaptchaEnabled || s.CaptchaRegisterEnabled || s.CaptchaForgotEnabled) {
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
	// 2FA gate: the password check is only the first factor when the account has a
	// SECOND factor enrolled — TOTP, OR a passkey (enrolling a passkey opts the
	// account into 2FA; there is no separate "allow passkey as 2FA" toggle). Hand
	// back a short-lived pending token (not a real session) and require
	// /auth/2fa/verify (or a passkey/email alternative) to complete the login.
	//
	// The user is identified now, so the METHOD decision uses the EFFECTIVE
	// settings (global ⊕ this user's group overrides) — a group can enable email
	// or passkey as a 2FA method. The pre-password guard/captcha above stay on the
	// global `s` (they run before the user, hence the group, is known).
	su, _ := h.settings.LoadForUser(c.Request.Context(), u, ports.UISettings{})
	hasPasskey := h.userHasPasskey(c.Request.Context(), u.ID, su)
	if u.TOTPEnabled || hasPasskey {
		pending, perr := h.auth.IssuePending(u, jwtutil.FirstFactorPassword)
		if perr != nil {
			recordAuthEvent(c, h.authEvents, domain.AuthMethodLocal, domain.AuthOutcomeFailure, u.ID, u.UPN, "token_error")
			respondError(c, perr)
			return
		}
		hasRecovery := false
		if h.twofa != nil {
			if n, rerr := h.twofa.RecoveryRemaining(c.Request.Context(), u.ID); rerr == nil {
				hasRecovery = n > 0
			}
		}
		methods := availableTwoFAMethods(u, jwtutil.FirstFactorPassword, su, hasPasskey, hasRecovery)
		c.JSON(http.StatusOK, gin.H{"status": "2fa_required", "pending_token": pending, "methods": methods})
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

// resolvePendingUser verifies a 2fa_pending token and returns the live user,
// re-gating against the account the same way Refresh does — the token lives up to
// 5 minutes, during which an admin may disable/demote it. On any failure it writes
// the response and returns ok=false.
func (h *AuthLocalHandler) resolvePendingUser(c *gin.Context, pendingToken string) (*domain.User, *jwtutil.Claims, bool) {
	claims, err := h.auth.VerifyPending(pendingToken)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid or expired 2FA session; please log in again"})
		return nil, nil, false
	}
	u, err := h.user.Get(c.Request.Context(), claims.UserID)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid or expired 2FA session; please log in again"})
		return nil, nil, false
	}
	if !u.Enabled && !domain.SelfServiceDisableReason(u.AutoDisabledReason) {
		recordAuthEvent(c, h.authEvents, domain.AuthMethodLocal, domain.AuthOutcomeFailure, u.ID, u.UPN, "disabled:"+string(u.AutoDisabledReason))
		c.JSON(http.StatusForbidden, gin.H{"error": disabledReasonMessage(u.AutoDisabledReason), "reason": string(u.AutoDisabledReason)})
		return nil, nil, false
	}
	if u.TokenVersion != claims.TokenVersion {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid or expired 2FA session; please log in again"})
		return nil, nil, false
	}
	return u, claims, true
}

// completeLogin issues the real session pair for a user who has cleared 2FA.
func (h *AuthLocalHandler) completeLogin(c *gin.Context, u *domain.User, via string) {
	access, refresh, err := h.auth.IssueTokens(u)
	if err != nil {
		recordAuthEvent(c, h.authEvents, domain.AuthMethodLocal, domain.AuthOutcomeFailure, u.ID, u.UPN, "token_error")
		respondError(c, err)
		return
	}
	recordAuthEvent(c, h.authEvents, domain.AuthMethodLocal, domain.AuthOutcomeSuccess, u.ID, u.UPN, via)
	c.JSON(http.StatusOK, loginResponse{
		AccessToken:  access,
		RefreshToken: refresh,
		User: userBrief{
			ID: u.ID, UPN: u.UPN, DisplayName: u.DisplayName, Role: u.Role,
		},
	})
}

// TwoFAVerify completes a 2FA login: it exchanges a pending token + a code for a
// real session. The code may be a TOTP, a one-time recovery code, or (if the
// admin enabled it) an emailed login code — the user picks which on the screen.
// Behind the same login rate-limiter as /login so codes can't be brute-forced.
func (h *AuthLocalHandler) TwoFAVerify(c *gin.Context) {
	var req struct {
		PendingToken string `json:"pending_token"`
		Code         string `json:"code"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request"})
		return
	}
	u, _, ok := h.resolvePendingUser(c, req.PendingToken)
	if !ok {
		return
	}
	// Global `s` for the per-account lockout (thresholds are panel-wide, §10-1);
	// the user's EFFECTIVE settings `su` for the email-method gate (a group can
	// enable email-as-2FA).
	s, _ := h.settings.Load(c.Request.Context(), ports.UISettings{})
	su, _ := h.settings.LoadForUser(c.Request.Context(), u, ports.UISettings{})
	// Per-account 2FA lockout: a distributed attacker who already passed the
	// password can otherwise grind TOTP codes from many IPs (the per-IP login
	// limiter can't see that). Checked BEFORE verifying so a locked account can't
	// keep guessing. A passkey assertion (TwoFAPasskeyFinish) is unaffected — it's
	// cryptographic, not brute-forceable — so a user locked out of codes can still
	// finish with their passkey.
	if h.guard != nil {
		if dec, derr := h.guard.Evaluate2FA(c.Request.Context(), s, u.ID); derr == nil && dec.Locked {
			recordAuthEvent(c, h.authEvents, domain.AuthMethodLocal, domain.AuthOutcomeFailure, u.ID, u.UPN, domain.AuthReason2FALockedOut)
			retry := int(dec.RetryAfter.Seconds()) + 1
			c.Header("Retry-After", strconv.Itoa(retry))
			c.JSON(http.StatusTooManyRequests, gin.H{
				"error":       "Too many invalid codes, please try again later",
				"locked":      true,
				"retry_after": retry,
			})
			return
		}
	}
	verified, err := h.twofa.VerifyLogin(c.Request.Context(), u.ID, req.Code)
	if err != nil {
		respondError(c, err)
		return
	}
	if !verified && h.login2fa != nil {
		// Only accept an emailed code while email-as-2FA is enabled, so flipping
		// the admin toggle off invalidates outstanding codes immediately.
		if su.TwoFAAllowEmail {
			if emailOK, eerr := h.login2fa.VerifyCode(c.Request.Context(), u.ID, req.Code); eerr == nil && emailOK {
				verified = true
			}
		}
	}
	if !verified {
		recordAuthEvent(c, h.authEvents, domain.AuthMethodLocal, domain.AuthOutcomeFailure, u.ID, u.UPN, domain.AuthReason2FAInvalid)
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid 2FA code"})
		return
	}
	h.completeLogin(c, u, "2fa")
}

// TwoFAEmailSend emails a one-time code for the email-as-2FA alternative. Requires
// a valid pending token; rate-limited like the other 2FA endpoints.
func (h *AuthLocalHandler) TwoFAEmailSend(c *gin.Context) {
	var req struct {
		PendingToken string `json:"pending_token"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request"})
		return
	}
	u, _, ok := h.resolvePendingUser(c, req.PendingToken)
	if !ok {
		return
	}
	if h.login2fa == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Email verification is not available"})
		return
	}
	if err := h.login2fa.SendCode(c.Request.Context(), u); err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"sent": true})
}

// userHasPasskey reports whether the account has at least one passkey enrolled
// AND passkeys are enabled panel-wide. Gating on PasskeyEnabled is the fail-safe:
// if the admin turns passkeys off panel-wide the assertion ceremony would be
// blocked, so the account must NOT be locked behind a passkey it can no longer
// use — disabling passkeys cleanly drops the passkey 2FA requirement.
func (h *AuthLocalHandler) userHasPasskey(ctx context.Context, userID int64, s ports.UISettings) bool {
	if h.passkey == nil || !s.PasskeyEnabled {
		return false
	}
	creds, err := h.passkey.List(ctx, userID)
	return err == nil && len(creds) > 0
}

// passkeyTwoFAAllowed enforces the passkey-as-2FA preconditions, writing the
// response and returning false if not met: passkeys enabled panel-wide, and the
// first factor was a password (a passwordless passkey login can't re-use a passkey
// as 2FA). Enrolling a passkey is itself the opt-in — there is no separate toggle.
func (h *AuthLocalHandler) passkeyTwoFAAllowed(c *gin.Context, u *domain.User, claims *jwtutil.Claims) bool {
	if h.passkey == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Passkey verification is not available"})
		return false
	}
	if claims.FirstFactor != jwtutil.FirstFactorPassword {
		c.JSON(http.StatusForbidden, gin.H{"error": "A passkey can't be the second factor for a passkey login"})
		return false
	}
	// Effective (per-group) passkey toggle — a group can enable/disable passkey as
	// a 2FA method for its members.
	s, err := h.settings.LoadForUser(c.Request.Context(), u, ports.UISettings{})
	if err != nil || !s.PasskeyEnabled {
		c.JSON(http.StatusForbidden, gin.H{"error": "Passkey is not an enabled verification method"})
		return false
	}
	return true
}

// TwoFAPasskeyBegin starts a passkey assertion as the 2FA factor (allow-listed to
// the pending user's credentials).
func (h *AuthLocalHandler) TwoFAPasskeyBegin(c *gin.Context) {
	var req struct {
		PendingToken string `json:"pending_token"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request"})
		return
	}
	u, claims, ok := h.resolvePendingUser(c, req.PendingToken)
	if !ok {
		return
	}
	if !h.passkeyTwoFAAllowed(c, u, claims) {
		return
	}
	opts, sessionID, err := h.passkey.BeginLoginForUser(c.Request.Context(), u.ID)
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"session_id": sessionID, "publicKey": opts})
}

// TwoFAPasskeyFinish verifies the passkey assertion and completes the login. The
// assertion JSON is in the body (read by the webauthn library), so the pending
// token + session id come via the query string.
func (h *AuthLocalHandler) TwoFAPasskeyFinish(c *gin.Context) {
	u, claims, ok := h.resolvePendingUser(c, c.Query("pending_token"))
	if !ok {
		return
	}
	if !h.passkeyTwoFAAllowed(c, u, claims) {
		return
	}
	if err := h.passkey.FinishLoginForUser(c.Request.Context(), u.ID, c.Query("session"), c.Request); err != nil {
		recordAuthEvent(c, h.authEvents, domain.AuthMethodPasskey, domain.AuthOutcomeFailure, u.ID, u.UPN, "2fa_passkey_invalid")
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Passkey verification failed"})
		return
	}
	h.completeLogin(c, u, "2fa_passkey")
}
