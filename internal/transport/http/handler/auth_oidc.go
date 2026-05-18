package handler

import (
	"errors"
	"net/http"
	"net/url"

	"github.com/gin-gonic/gin"

	"github.com/KazuhaHub/passwall-sub-panel/internal/config"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/auth"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/user"
)

// AuthOIDCHandler exposes /api/auth/oidc/{login,callback}.
//
// State and nonce are kept in short-lived HttpOnly cookies (5 minutes)
// and validated on the callback to defeat CSRF / replay. Once the IdP
// returns a verified ID token, the panel upserts a domain.User and
// hands back JWT cookies the same way SAML does.
type AuthOIDCHandler struct {
	oidc *auth.OIDCService
	auth *auth.Service
	user *user.Service
}

const (
	cookieOIDCState    = "psp_oidc_state"
	cookieOIDCNonce    = "psp_oidc_nonce"
	cookieOIDCRet      = "psp_oidc_return"
	cookieOIDCVerifier = "psp_oidc_pkce"
	oidcCookieTTL      = 300 // seconds
)

func NewAuthOIDCHandler(oidcSvc *auth.OIDCService, authSvc *auth.Service, userSvc *user.Service) *AuthOIDCHandler {
	return &AuthOIDCHandler{oidc: oidcSvc, auth: authSvc, user: userSvc}
}

func (h *AuthOIDCHandler) Login(c *gin.Context) {
	if !h.oidc.Enabled() {
		c.JSON(http.StatusNotFound, gin.H{"error": "Oidc not enabled"})
		return
	}
	state, err := auth.RandomState()
	if err != nil {
		respondError(c, err)
		return
	}
	nonce, err := auth.RandomState()
	if err != nil {
		respondError(c, err)
		return
	}
	// PKCE verifier — RFC 7636 S256 path. Defence-in-depth on top of
	// state+nonce against an attacker who can read the redirect-URI's
	// `code` parameter but not our HttpOnly cookie.
	verifier, err := auth.RandomState()
	if err != nil {
		respondError(c, err)
		return
	}
	url, err := h.oidc.AuthCodeURL(state, nonce, verifier)
	if err != nil {
		respondError(c, err)
		return
	}
	returnTo := sanitizeReturnTo(c.Query("return_to"), "/user/me")
	// Match the Secure-flag policy used for the JWT session cookies below:
	// when the panel is reached over HTTPS (directly or behind a TLS-
	// terminating proxy), the one-time OIDC cookies should also be Secure
	// so they can't leak over a downgrade. HttpOnly+SameSite=Lax already
	// give CSRF protection; Secure closes the network-layer hole.
	secure := isHTTPS(c)
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie(cookieOIDCState, state, oidcCookieTTL, "/", "", secure, true)
	c.SetCookie(cookieOIDCNonce, nonce, oidcCookieTTL, "/", "", secure, true)
	c.SetCookie(cookieOIDCVerifier, verifier, oidcCookieTTL, "/", "", secure, true)
	c.SetCookie(cookieOIDCRet, returnTo, oidcCookieTTL, "/", "", secure, true)
	c.Redirect(http.StatusFound, url)
}

func (h *AuthOIDCHandler) Callback(c *gin.Context) {
	if !h.oidc.Enabled() {
		c.Redirect(http.StatusFound, "/sso-error?error=sso_error&description=OIDC+not+enabled")
		return
	}
	if errParam := c.Query("error"); errParam != "" {
		desc := c.Query("error_description")
		c.Redirect(http.StatusFound, "/sso-error?error=auth_failed&description="+url.QueryEscape(desc))
		return
	}
	state := c.Query("state")
	wantState, _ := c.Cookie(cookieOIDCState)
	if state == "" || state != wantState {
		c.Redirect(http.StatusFound, "/sso-error?error=auth_failed&description=State+mismatch")
		return
	}
	nonce, _ := c.Cookie(cookieOIDCNonce)
	code := c.Query("code")
	if code == "" {
		c.Redirect(http.StatusFound, "/sso-error?error=auth_failed&description=Missing+authorization+code")
		return
	}
	// Clear the one-time cookies regardless of outcome. Secure flag must
	// match the one used when setting them; otherwise some browsers
	// won't recognize the deletion and the stale cookie lingers. Declared
	// once at this point so the later JWT cookie sets reuse it.
	secure := isHTTPS(c)
	c.SetCookie(cookieOIDCState, "", -1, "/", "", secure, true)
	c.SetCookie(cookieOIDCNonce, "", -1, "/", "", secure, true)
	pkceVerifier, _ := c.Cookie(cookieOIDCVerifier)
	c.SetCookie(cookieOIDCVerifier, "", -1, "/", "", secure, true)

	assertion, err := h.oidc.Exchange(c.Request.Context(), code, nonce, pkceVerifier)
	if err != nil {
		c.Redirect(http.StatusFound, "/sso-error?error=auth_failed&description="+url.QueryEscape(err.Error()))
		return
	}

	upn := assertion.Username
	cfg := h.oidc.Config()
	var (
		groupsAttr string
		rules      []config.SSORoleRule
	)
	if cfg != nil {
		groupsAttr = cfg.AttributeMapping.Groups
		rules = cfg.RoleRules
	}
	in := user.EnsureSSOInput{
		Provider:       domain.SSOProviderOIDC,
		Subject:        assertion.Subject,
		UPN:            upn,
		Email:          assertion.Email,
		DisplayName:    assertion.DisplayName,
		Groups:         assertion.Groups,
		Attributes:     assertion.Attributes,
		Rules:          rules,
		GroupsAttrName: groupsAttr,
	}
	if cfg != nil {
		in.AllowAutoCreate = cfg.AllowAutoCreate
		in.DefaultGroupSlug = cfg.DefaultGroupSlug
		in.DefaultExpireDays = cfg.NewUserDefaults.ExpireDays
		in.DefaultLimitBytes = cfg.NewUserDefaults.TrafficLimitBytes
		in.DefaultResetPeriod = domain.ResetPeriod(cfg.NewUserDefaults.TrafficResetPeriod)
	}
	u, err := h.user.EnsureSSO(c.Request.Context(), in)
	if errors.Is(err, domain.ErrSSONoAccount) {
		c.Redirect(http.StatusFound, "/sso-no-account")
		return
	}
	if errors.Is(err, domain.ErrSSOAccountConflict) {
		c.Redirect(http.StatusFound, "/sso-error?error=sso_conflict&description="+url.QueryEscape(err.Error()))
		return
	}
	if err != nil {
		c.Redirect(http.StatusFound, "/sso-error?error=sso_error&description="+url.QueryEscape(err.Error()))
		return
	}
	if !u.Enabled && !allowDisabledEmergencyLogin(u.AutoDisabledReason) {
		errorCode := "account_disabled"
		errorDesc := "您的账号已被停用，请联系管理员。"
		if u.AutoDisabledReason == domain.DisabledPendingApproval {
			errorCode = "account_pending"
			errorDesc = "您的账号正在等待管理员审核，请稍后再试。"
		}
		c.Redirect(http.StatusFound, "/sso-error?error="+errorCode+"&description="+url.QueryEscape(errorDesc))
		return
	}
	access, refresh, err := h.auth.IssueTokens(u)
	if err != nil {
		respondError(c, err)
		return
	}

	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie(CookieAccessToken, access, int(h.auth.AccessTTL().Seconds()), "/", "", secure, true)
	c.SetCookie(CookieRefreshToken, refresh, int(h.auth.RefreshTTL().Seconds()), "/", "", secure, true)

	returnTo, _ := c.Cookie(cookieOIDCRet)
	c.SetCookie(cookieOIDCRet, "", -1, "/", "", secure, true)
	if returnTo == "" {
		returnTo = "/user/me"
	}
	c.Redirect(http.StatusFound, "/sso-callback?next="+returnTo)
}
