package handler

import (
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/KazuhaHub/passwall-sub-panel/internal/config"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/auth"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/user"
)

// Cookie names used by the SAML ACS handler to hand JWT tokens back to the
// SPA. Read by middleware.RequireAuth when the Authorization header is
// absent (browser-initiated SSO flow).
const (
	CookieAccessToken  = "psp_access"
	CookieRefreshToken = "psp_refresh"
	// CookieAuthPath scopes every auth cookie set by this package to the
	// /api/ tree. The old Path=/ value caused two real problems behind
	// Cloudflare: the cookie was sent on /assets/* requests, which trips
	// CF's "skip cache for requests with cookies" default and turned every
	// hashed bundle into a MISS, and it also broadcast the JWT to any
	// future non-API surface mounted at the root. Only handlers under
	// /api/auth/* read these cookies, so /api is the narrowest path that
	// still works for every auth flow.
	//
	// Cookies are keyed on (name, path, domain, secure) — if the path
	// drifts between set and clear sites the browser keeps the original
	// cookie alive until its natural TTL. Keep every SetCookie / clearing
	// SetCookie call routed through this constant.
	CookieAuthPath = "/api"
)

// AuthSAMLHandler exposes /api/auth/saml/{login,acs,metadata}.
type AuthSAMLHandler struct {
	saml *auth.SAMLService
	auth *auth.Service
	user *user.Service
}

func NewAuthSAMLHandler(samlSvc *auth.SAMLService, authSvc *auth.Service, userSvc *user.Service) *AuthSAMLHandler {
	return &AuthSAMLHandler{saml: samlSvc, auth: authSvc, user: userSvc}
}

// Login initiates SP-initiated SSO by redirecting the browser to the IdP.
// The AuthnRequest ID is embedded in RelayState ("id|returnURL") — cookies
// won't work here because the ACS POST is cross-site and SameSite=Lax blocks them.
func (h *AuthSAMLHandler) Login(c *gin.Context) {
	if !h.saml.Enabled() {
		c.JSON(http.StatusNotFound, gin.H{"error": "Sso not enabled"})
		return
	}
	returnTo := sanitizeReturnTo(c.Query("return_to"), "/user/me")
	redirectURL, err := h.saml.BuildAuthnURL(returnTo)
	if err != nil {
		respondError(c, err)
		return
	}
	c.Redirect(http.StatusFound, redirectURL)
}

// ACS handles the SAML Response POSTed back by the IdP. Validates the
// assertion, upserts the user, issues JWT tokens, and redirects the
// browser to the return URL embedded in RelayState.
func (h *AuthSAMLHandler) ACS(c *gin.Context) {
	if !h.saml.Enabled() {
		c.JSON(http.StatusNotFound, gin.H{"error": "Sso not enabled"})
		return
	}

	// RelayState format: "reqID|returnURL" (set by Login via BuildAuthnURL).
	rawRelay := c.Request.FormValue("RelayState")
	var reqID, returnTo string
	if idx := strings.IndexByte(rawRelay, '|'); idx > 0 {
		reqID = rawRelay[:idx]
		returnTo = rawRelay[idx+1:]
	} else {
		returnTo = rawRelay
	}
	var possibleIDs []string
	if reqID != "" {
		possibleIDs = []string{reqID}
	}

	assertion, err := h.saml.ParseACSResponse(c.Request, possibleIDs)
	if err != nil {
		c.Redirect(http.StatusFound, "/sso-error?error=auth_failed&description="+url.QueryEscape(err.Error()))
		return
	}

	cfg := h.saml.Config()
	var (
		groupsAttr string
		rules      []config.SSORoleRule
	)
	if cfg != nil {
		groupsAttr = cfg.AttributeMapping.Groups
		rules = cfg.RoleRules
	}
	in := user.EnsureSSOInput{
		Provider:       domain.SSOProviderSAML,
		Subject:        assertion.Subject,
		UPN:            assertion.UPN,
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
		// No description: the SPA renders a localized message for these
		// recognized codes. Passing a hardcoded string would override i18n.
		errorCode := "account_disabled"
		if u.AutoDisabledReason == domain.DisabledPendingApproval {
			errorCode = "account_pending"
		}
		c.Redirect(http.StatusFound, "/sso-error?error="+errorCode)
		return
	}
	access, refresh, err := h.auth.IssueTokens(u)
	if err != nil {
		respondError(c, err)
		return
	}

	secure := isHTTPS(c)
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie(CookieAccessToken, access, int(h.auth.AccessTTL().Seconds()), CookieAuthPath, "", secure, true)
	c.SetCookie(CookieRefreshToken, refresh, int(h.auth.RefreshTTL().Seconds()), CookieAuthPath, "", secure, true)

	if returnTo == "" {
		returnTo = "/user/me"
	}
	c.Redirect(http.StatusFound, "/sso-callback?next="+returnTo)
}

func allowDisabledEmergencyLogin(reason domain.AutoDisabledReason) bool {
	return reason == domain.DisabledTrafficExceeded || reason == domain.DisabledExpired
}

// Metadata serves the SP metadata XML for IdP-side onboarding.
func (h *AuthSAMLHandler) Metadata(c *gin.Context) {
	if !h.saml.Enabled() {
		c.JSON(http.StatusNotFound, gin.H{"error": "Sso not enabled"})
		return
	}
	xml, err := h.saml.SPMetadataXML()
	if err != nil {
		respondError(c, err)
		return
	}
	c.Data(http.StatusOK, "application/samlmetadata+xml", xml)
}
