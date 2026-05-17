package handler

import (
	"errors"
	"net/http"
	"net/url"

	"github.com/gin-gonic/gin"

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
	cookieOIDCState = "psp_oidc_state"
	cookieOIDCNonce = "psp_oidc_nonce"
	cookieOIDCRet   = "psp_oidc_return"
	oidcCookieTTL   = 300 // seconds
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
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	nonce, err := auth.RandomState()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	url, err := h.oidc.AuthCodeURL(state, nonce)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	returnTo := sanitizeReturnTo(c.Query("return_to"), "/user/me")
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie(cookieOIDCState, state, oidcCookieTTL, "/", "", false, true)
	c.SetCookie(cookieOIDCNonce, nonce, oidcCookieTTL, "/", "", false, true)
	c.SetCookie(cookieOIDCRet, returnTo, oidcCookieTTL, "/", "", false, true)
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
	// Clear the one-time cookies regardless of outcome.
	c.SetCookie(cookieOIDCState, "", -1, "/", "", false, true)
	c.SetCookie(cookieOIDCNonce, "", -1, "/", "", false, true)

	assertion, err := h.oidc.Exchange(c.Request.Context(), code, nonce)
	if err != nil {
		c.Redirect(http.StatusFound, "/sso-error?error=auth_failed&description="+url.QueryEscape(err.Error()))
		return
	}

	isAdmin := h.oidc.IsAdmin(assertion.Groups)

	upn := assertion.Username
	cfg := h.oidc.Config()
	in := user.EnsureSSOInput{
		Provider:    domain.SSOProviderOIDC,
		Subject:     assertion.Subject,
		UPN:         upn,
		Email:       assertion.Email,
		DisplayName: assertion.DisplayName,
		Groups:      assertion.Groups,
		IsAdmin:     isAdmin,
	}
	if cfg != nil {
		in.AllowAutoCreate = cfg.AllowAutoCreate
		in.KeepAdminOnLogin = cfg.KeepAdminWhenNotInGroup
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
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	secure := isHTTPS(c)
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie(CookieAccessToken, access, int(h.auth.AccessTTL().Seconds()), "/", "", secure, true)
	c.SetCookie(CookieRefreshToken, refresh, int(h.auth.RefreshTTL().Seconds()), "/", "", secure, true)

	returnTo, _ := c.Cookie(cookieOIDCRet)
	c.SetCookie(cookieOIDCRet, "", -1, "/", "", false, true)
	if returnTo == "" {
		returnTo = "/user/me"
	}
	c.Redirect(http.StatusFound, "/sso-callback?next="+returnTo)
}
