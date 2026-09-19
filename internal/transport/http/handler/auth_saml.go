package handler

import (
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/KazuhaHub/passwall-sub-panel/internal/config"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/log"
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
	samlReasonEntryPoint       = "saml_entry_point"
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
	case errors.Is(err, auth.ErrSAMLEntryPoint):
		return samlReasonEntryPoint
	default:
		return samlReasonAssertionInvalid
	}
}

// The codes the FAILURE PAGE understands. They are coarser than the observability
// reasons on purpose: what a user can act on is "try again", "an administrator has
// an IdP setting to fix", or "that did not work" — and every code here needs copy
// in two languages.
const (
	samlPageAuthFailed  = "auth_failed"
	samlPageConfig      = "saml_config"
	samlPageUnavailable = "saml_unavailable"
)

// samlFailurePageCode maps the observability reason onto the page code. The fine
// reason still reaches the metric and the audit row; only the page collapses it.
func samlFailurePageCode(err error) string {
	switch samlFailureReason(err) {
	case samlReasonStoreError:
		return samlPageUnavailable
	case samlReasonWeakSignature, samlReasonMultiAssertion, samlReasonDestination:
		return samlPageConfig
	default:
		return samlPageAuthFailed
	}
}

// samlFailureRedirect is the failure-page target for an ACS refusal.
//
// It carries the page code and nothing else. A description would be rendered by
// the SPA — so anything a caller or an IdP chose to say would be reflected into
// the page — it would survive in browser history and in Referer headers, and it
// would override the localized copy. The full error goes to the process log and
// the classified reason to the metric, which is where an operator reads it.
func samlFailureRedirect(err error) string {
	return "/sso-error?error=" + samlFailurePageCode(err)
}

// Login initiates SP-initiated SSO.
//
// The browser is bound to the login by a per-request cookie rather than by
// RelayState alone: RelayState round-trips through the IdP and is therefore
// attacker-controlled, so it identifies nothing on its own (ADR 0036 D2).
func (h *AuthSAMLHandler) Login(c *gin.Context) {
	if !h.saml.Enabled() {
		c.JSON(http.StatusNotFound, gin.H{"error": "Sso not enabled"})
		return
	}
	returnTo := sanitizeReturnTo(c.Query("return_to"), "/user/me")
	res, err := h.saml.BeginLogin(c.Request.Context(), auth.BeginLoginOptions{
		// The origin comes from the trusted-proxy-aware helper, never from a raw
		// Host header: an untrusted Host would otherwise let a caller talk the
		// panel into a login whose binding cookie can never come back.
		RequestOrigin: inferRequestBaseURL(c.Request),
		ReturnTo:      returnTo,
		Now:           time.Now(),
	})
	if err != nil {
		reason := samlFailureReason(err)
		metrics.SAMLACSFailureTotal.With(reason).Inc()
		recordAuthEvent(c, h.authEvents, domain.AuthMethodSAML, domain.AuthOutcomeFailure, 0, "", reason)
		log.Warn("saml: login could not be started", "err", err)
		c.Redirect(http.StatusFound, panelRedirect(c, samlFailureRedirect(err)))
		return
	}

	// Host-only (no Domain), `Secure` and SameSite=None because the ACS POST
	// arrives from the IdP's site and a Lax cookie is not sent with it. The Path
	// is the panel's EXTERNAL ACS path: the internal route has the panel prefix
	// stripped before dispatch, so using that would scope the cookie to a path
	// the browser never requests.
	c.SetSameSite(http.SameSiteNoneMode)
	c.SetCookie(res.CookieName, res.CookieValue, int(auth.SAMLLoginTTL.Seconds()), samlACSCookiePath(c), "", true, true)
	c.Redirect(http.StatusFound, res.RedirectURL)
}

// samlACSCookiePath is the externally visible path of the ACS endpoint, which is
// where the binding cookie has to be sent.
func samlACSCookiePath(c *gin.Context) string {
	return cookieAuthPath(c) + "/auth/saml/acs"
}

// samlFailureDescription decides what the SSO failure page may show.
//
// Validation failures carry the panel's own actionable text ("missing UPN claim
// ... — add the matching attribute on the IdP side"), which is what an admin
// needs and has always seen here. Infrastructure failures do not: their text is
// about databases and connections, and it belongs in the log rather than in a
// URL the browser keeps (ADR 0036 §6.9).
// ACS handles the SAML Response POSTed back by the IdP. It consumes the
// server-side record of the login, validates the assertion against the request ID
// that record names, upserts the user, issues JWT tokens, and redirects the
// browser to the return path the record carries.
func (h *AuthSAMLHandler) ACS(c *gin.Context) {
	if !h.saml.Enabled() {
		c.JSON(http.StatusNotFound, gin.H{"error": "Sso not enabled"})
		return
	}

	// RelayState is the token this panel minted and handed the IdP. It arrives
	// attacker-controlled, so its shape is validated before it is used for
	// anything at all — including as a cookie name.
	relayState := strings.TrimSpace(c.Request.FormValue("RelayState"))
	var (
		cookieName string
		binding    string
	)
	if auth.ValidSAMLRelayToken(relayState) {
		cookieName = auth.SAMLLoginCookieName(relayState)
		if v, err := c.Cookie(cookieName); err == nil {
			binding = v
		}
		// Cleared on EVERY path, before the verdict is known: it is single-use
		// material, and leaving it behind would keep a spent token's cookie in
		// the browser for its whole MaxAge.
		h.clearSAMLBindingCookie(c, cookieName)
	}

	res, err := h.saml.CompleteLogin(c.Request.Context(), c.Request, auth.CompleteLoginOptions{
		RelayState:    relayState,
		BindingCookie: binding,
		Now:           time.Now(),
	})
	if err != nil {
		// Classify once, then use the same code for the metric and the audit row
		// so the two can never disagree about why a login was refused.
		reason := samlFailureReason(err)
		metrics.SAMLACSFailureTotal.With(reason).Inc()
		recordAuthEvent(c, h.authEvents, domain.AuthMethodSAML, domain.AuthOutcomeFailure, 0, "", reason)
		log.Warn("saml: assertion refused", "err", err)
		c.Redirect(http.StatusFound, panelRedirect(c, samlFailureRedirect(err)))
		return
	}

	assertion := res.Assertion
	// The mapping rules come from the snapshot whose digest was checked, so a
	// concurrent admin edit cannot apply the new rules to a login that began
	// under the old ones (ADR 0036 D2).
	cfg := res.Config
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
		c.Redirect(http.StatusFound, panelRedirect(c, "/sso-error?error=sso_conflict"))
		return
	}
	if err != nil {
		recordAuthEvent(c, h.authEvents, domain.AuthMethodSAML, domain.AuthOutcomeFailure, 0, assertion.UPN, "sso_error")
		c.Redirect(http.StatusFound, panelRedirect(c, "/sso-error?error=sso_error"))
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

	// The redirect target comes from the server-side record, never from the ACS
	// payload. Re-sanitize anyway: the record was written from an earlier
	// request's query string, and server-side hardening must not depend on the
	// SPA's navigate() neutralizing it.
	returnTo := sanitizeReturnTo(res.ReturnTo, "/user/me")
	c.Redirect(http.StatusFound, panelRedirect(c, "/sso-callback?next="+url.QueryEscape(returnTo)))
}

// clearSAMLBindingCookie expires the per-request binding cookie. The attributes
// must match the ones used to set it — (name, path, domain, secure) is the key
// the browser stores under, so a mismatch leaves the original in place.
func (h *AuthSAMLHandler) clearSAMLBindingCookie(c *gin.Context, name string) {
	c.SetSameSite(http.SameSiteNoneMode)
	c.SetCookie(name, "", -1, samlACSCookiePath(c), "", true, true)
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
