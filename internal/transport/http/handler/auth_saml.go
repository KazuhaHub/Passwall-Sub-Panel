package handler

import (
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/KazuhaHub/passwall-sub-panel/internal/config"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/auth"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/user"
)

// Cookie names used by the SAML ACS handler to hand JWT tokens back to the
// SPA. Read by middleware.RequireAuth when the Authorization header is
// absent (browser-initiated SSO flow).
const (
	CookieAccessToken  = "psp_access"
	CookieRefreshToken = "psp_refresh"
	// Auth cookies are scoped at runtime to <panel_path>/api. The old Path=/
	// value caused two real problems behind
	// Cloudflare: the cookie was sent on /assets/* requests, which trips
	// CF's "skip cache for requests with cookies" default and turned every
	// hashed bundle into a MISS, and it also broadcast the JWT to any
	// future non-API surface mounted at the root. Cookies are keyed on
	// (name, path, domain, secure), so every set/clear call must use
	// cookieAuthPath(c), derived from the same dispatched external path.
)

// AuthSAMLHandler exposes /api/auth/saml/{login,acs,metadata}.
type AuthSAMLHandler struct {
	saml       *auth.SAMLService
	auth       *auth.Service
	user       *user.Service
	authEvents ports.AuthEventRepo
}

func NewAuthSAMLHandler(samlSvc *auth.SAMLService, authSvc *auth.Service, userSvc *user.Service, authEvents ports.AuthEventRepo) *AuthSAMLHandler {
	return &AuthSAMLHandler{saml: samlSvc, auth: authSvc, user: userSvc, authEvents: authEvents}
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
		recordAuthEvent(c, h.authEvents, domain.AuthMethodSAML, domain.AuthOutcomeFailure, 0, "", "saml_assertion_invalid")
		c.Redirect(http.StatusFound, panelRedirect(c, "/sso-error?error=auth_failed&description="+url.QueryEscape(err.Error())))
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
		recordAuthEvent(c, h.authEvents, domain.AuthMethodSAML, domain.AuthOutcomeFailure, 0, assertion.UPN, "sso_no_account")
		c.Redirect(http.StatusFound, panelRedirect(c, "/sso-no-account"))
		return
	}
	if errors.Is(err, domain.ErrSSOAccountConflict) {
		recordAuthEvent(c, h.authEvents, domain.AuthMethodSAML, domain.AuthOutcomeFailure, 0, assertion.UPN, "sso_conflict")
		c.Redirect(http.StatusFound, panelRedirect(c, "/sso-error?error=sso_conflict&description="+url.QueryEscape(err.Error())))
		return
	}
	if err != nil {
		recordAuthEvent(c, h.authEvents, domain.AuthMethodSAML, domain.AuthOutcomeFailure, 0, assertion.UPN, "sso_error")
		c.Redirect(http.StatusFound, panelRedirect(c, "/sso-error?error=sso_error&description="+url.QueryEscape(err.Error())))
		return
	}
	if !u.Enabled && !allowDisabledEmergencyLogin(u.AutoDisabledReason) {
		// No description: the SPA renders a localized message for these
		// recognized codes. Passing a hardcoded string would override i18n.
		errorCode := "account_disabled"
		if u.AutoDisabledReason == domain.DisabledPendingApproval {
			errorCode = "account_pending"
		}
		recordAuthEvent(c, h.authEvents, domain.AuthMethodSAML, domain.AuthOutcomeFailure, u.ID, u.UPN, "disabled:"+string(u.AutoDisabledReason))
		c.Redirect(http.StatusFound, panelRedirect(c, "/sso-error?error="+errorCode))
		return
	}
	access, refresh, err := h.auth.IssueTokens(u)
	if err != nil {
		recordAuthEvent(c, h.authEvents, domain.AuthMethodSAML, domain.AuthOutcomeFailure, u.ID, u.UPN, "token_error")
		respondError(c, err)
		return
	}
	recordAuthEvent(c, h.authEvents, domain.AuthMethodSAML, domain.AuthOutcomeSuccess, u.ID, u.UPN, "")

	secure := isHTTPS(c)
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie(CookieAccessToken, access, int(h.auth.AccessTTL().Seconds()), cookieAuthPath(c), "", secure, true)
	c.SetCookie(CookieRefreshToken, refresh, int(h.auth.RefreshTTL().Seconds()), cookieAuthPath(c), "", secure, true)

	// RelayState round-trips through the IdP and is fully attacker-controllable
	// in a crafted / IdP-initiated POST (Login sanitized only the SP-initiated
	// value). Re-sanitize here and QueryEscape into the next= param — server-side
	// hardening must not depend on the SPA's navigate() neutralizing it.
	returnTo = sanitizeReturnTo(returnTo, "/user/me")
	c.Redirect(http.StatusFound, panelRedirect(c, "/sso-callback?next="+url.QueryEscape(returnTo)))
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
