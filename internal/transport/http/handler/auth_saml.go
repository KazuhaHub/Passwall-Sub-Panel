package handler

import (
	"net/http"

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
)

// AuthSAMLHandler exposes /api/auth/saml/{login,acs,metadata}.
type AuthSAMLHandler struct {
	saml *auth.SAMLService
	auth *auth.Service
	user *user.Service
	cfg  *config.Config
}

func NewAuthSAMLHandler(samlSvc *auth.SAMLService, authSvc *auth.Service,
	userSvc *user.Service, cfg *config.Config) *AuthSAMLHandler {
	return &AuthSAMLHandler{saml: samlSvc, auth: authSvc, user: userSvc, cfg: cfg}
}

// Login initiates SP-initiated SSO by redirecting the browser to the IdP.
func (h *AuthSAMLHandler) Login(c *gin.Context) {
	if !h.saml.Enabled() {
		c.JSON(http.StatusNotFound, gin.H{"error": "sso not enabled"})
		return
	}
	relayState := c.Query("return_to")
	if relayState == "" {
		relayState = "/admin"
	}
	redirectURL, err := h.saml.BuildAuthnURL(relayState)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.Redirect(http.StatusFound, redirectURL)
}

// ACS handles the SAML Response POSTed back by the IdP. Validates the
// assertion, upserts the user, issues JWT tokens, and redirects the
// browser to RelayState (or /admin by default) with HttpOnly cookies set.
func (h *AuthSAMLHandler) ACS(c *gin.Context) {
	if !h.saml.Enabled() {
		c.JSON(http.StatusNotFound, gin.H{"error": "sso not enabled"})
		return
	}
	assertion, err := h.saml.ParseACSResponse(c.Request)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": err.Error()})
		return
	}

	samlCfg := h.saml.Config()
	isAdmin := h.saml.IsAdmin(assertion.Groups)
	u, err := h.user.EnsureSSO(c.Request.Context(), user.EnsureSSOInput{
		UPN:                assertion.UPN,
		Email:              assertion.Email,
		DisplayName:        assertion.DisplayName,
		Groups:             assertion.Groups,
		IsAdmin:            isAdmin,
		DefaultGroupSlug:   samlCfg.DefaultGroupSlug,
		DefaultExpireDays:  samlCfg.NewUserDefaults.ExpireDays,
		DefaultLimitBytes:  samlCfg.NewUserDefaults.TrafficLimitBytes,
		DefaultResetPeriod: domain.ResetPeriod(samlCfg.NewUserDefaults.TrafficResetPeriod),
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if !u.Enabled {
		c.JSON(http.StatusForbidden, gin.H{"error": "account disabled"})
		return
	}
	access, refresh, err := h.auth.IssueTokens(u)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	secure := false // upgrade to true once HTTPS is terminated by the reverse proxy and forwarded headers indicate it
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie(CookieAccessToken, access, int(h.cfg.AccessTTL().Seconds()), "/", "", secure, true)
	c.SetCookie(CookieRefreshToken, refresh, int(h.cfg.RefreshTTL().Seconds()), "/", "", secure, true)

	relayState := c.Request.FormValue("RelayState")
	if relayState == "" {
		relayState = "/admin"
	}
	c.Redirect(http.StatusFound, relayState)
}

// Metadata serves the SP metadata XML for IdP-side onboarding.
func (h *AuthSAMLHandler) Metadata(c *gin.Context) {
	if !h.saml.Enabled() {
		c.JSON(http.StatusNotFound, gin.H{"error": "sso not enabled"})
		return
	}
	xml, err := h.saml.SPMetadataXML()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.Data(http.StatusOK, "application/samlmetadata+xml", xml)
}
