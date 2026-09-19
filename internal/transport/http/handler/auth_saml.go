package handler

import (
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/KazuhaHub/passwall-sub-panel/internal/config"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/metrics"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/samlguard"
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

// Reason codes recorded for a refused SAML ACS request, on both the auth event
// and the metric label. A CLOSED set — adding one is a deliberate act, and the
// classifier below never interpolates error text, so the label space cannot
// grow with attacker input.
//
// The separation that earns its keep is replay vs. replay-store failure. Those
// are an attack to investigate and an outage to fix; giving them one code, which
// is what the single "saml_assertion_invalid" they used to share amounted to,
// means an operator eventually stops trusting the word "replay"
// (ADR 0036 §6.5.1). The guard's three rejections get their own codes for the
// same reason: they point at an IdP configuration to correct rather than at a
// credential to distrust.
const (
	samlReasonAssertionInvalid = "saml_assertion_invalid"
	samlReasonReplayed         = "saml_assertion_replayed"
	samlReasonStoreError       = "saml_replay_store_error"
	samlReasonWeakSignature    = "saml_weak_signature"
	samlReasonMultiAssertion   = "saml_multiple_assertions"
	samlReasonDestination      = "saml_destination"
	samlReasonRequestInvalid   = "saml_request_invalid"
)

// samlFailureReason classifies an ACS failure for observability. Classification
// only: it changes no control flow, and a nil error maps to the default rather
// than panicking, because a caller misusing it should not take down the ACS.
func samlFailureReason(err error) string {
	switch {
	case errors.Is(err, auth.ErrSAMLAssertionReplayed):
		return samlReasonReplayed
	case errors.Is(err, domain.ErrUnavailable):
		return samlReasonStoreError
	case errors.Is(err, samlguard.ErrWeakSignatureAlgorithm):
		return samlReasonWeakSignature
	case errors.Is(err, samlguard.ErrTooManyAssertions):
		return samlReasonMultiAssertion
	case errors.Is(err, samlguard.ErrMissingDestination), errors.Is(err, samlguard.ErrDestinationMismatch):
		return samlReasonDestination
	case errors.Is(err, domain.ErrSAMLRequestInvalid):
		return samlReasonRequestInvalid
	default:
		return samlReasonAssertionInvalid
	}
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
		// Classify once, then use the same code for the metric and the audit row
		// so the two can never disagree about why a login was refused.
		reason := samlFailureReason(err)
		metrics.SAMLACSFailureTotal.With(reason).Inc()
		recordAuthEvent(c, h.authEvents, domain.AuthMethodSAML, domain.AuthOutcomeFailure, 0, "", reason)
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
		in.GroupRules = cfg.GroupRules
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
	if !domain.AccountLoginAllowed(u.Enabled, u.AutoDisabledReason) {
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
