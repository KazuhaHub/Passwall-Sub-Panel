package handler

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/KazuhaHub/passwall-sub-panel/internal/config"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/auth"
)

// oidcIssuerHTTPS reports whether the issuer URL is acceptable to save. When
// OIDC is enabled the issuer drives discovery + token exchange, so require an
// https:// URL — http:// invites credential-downgrade and plain-HTTP internal
// SSRF (the safe-HTTP client already blocks loopback). A disabled config may
// carry any/empty issuer.
func oidcIssuerHTTPS(enabled bool, issuer string) bool {
	if !enabled {
		return true
	}
	u, err := url.Parse(strings.TrimSpace(issuer))
	return err == nil && u.Scheme == "https" && u.Host != ""
}

// AdminOIDCHandler exposes /api/admin/settings/oidc — GET returns the
// stored OIDC configuration, PUT replaces it and reloads the live
// provider so an admin edit takes effect immediately. ClientSecret is
// never returned in plaintext; an empty string in the PUT body means
// "keep existing".
//
// SAML and OIDC can both be enabled from v2.3.2 onwards — the SSO
// identity model keys accounts on (provider, subject) tuples so the
// protocol namespaces don't collide. The login page surfaces a button
// per enabled provider.
type AdminOIDCHandler struct {
	repo ports.OIDCConfigRepo
	oidc *auth.OIDCService
	// groups: see AdminSAMLHandler.groups — group-rule targets are
	// validated where they are typed rather than at somebody's login.
	groups ports.GroupRepo
}

func NewAdminOIDCHandler(repo ports.OIDCConfigRepo, oidcSvc *auth.OIDCService, groups ports.GroupRepo) *AdminOIDCHandler {
	return &AdminOIDCHandler{repo: repo, oidc: oidcSvc, groups: groups}
}

type oidcAttrDTO struct {
	Username    string `json:"username"`
	Email       string `json:"email"`
	DisplayName string `json:"display_name"`
	Groups      string `json:"groups"`
}

type oidcConfigDTO struct {
	Enabled          bool                  `json:"enabled"`
	IssuerURL        string                `json:"issuer_url"`
	ClientID         string                `json:"client_id"`
	HasClientSecret  bool                  `json:"has_client_secret"`
	RedirectURL      string                `json:"redirect_url"`
	Scopes           []string              `json:"scopes"`
	AttributeMapping oidcAttrDTO           `json:"attribute_mapping"`
	RoleRules        []config.SSORoleRule  `json:"role_rules"`
	GroupRules       []config.SSOGroupRule `json:"group_rules"`
	DefaultGroupSlug string                `json:"default_group_slug"`
	AllowAutoCreate  bool                  `json:"allow_auto_create"`
	NewUserDefaults  samlNewUserDTO        `json:"new_user_defaults"`
}

type oidcUpdateRequest struct {
	Enabled          bool                  `json:"enabled"`
	IssuerURL        string                `json:"issuer_url"`
	ClientID         string                `json:"client_id"`
	ClientSecret     string                `json:"client_secret"`
	RedirectURL      string                `json:"redirect_url"`
	Scopes           []string              `json:"scopes"`
	AttributeMapping oidcAttrDTO           `json:"attribute_mapping"`
	RoleRules        []config.SSORoleRule  `json:"role_rules"`
	GroupRules       []config.SSOGroupRule `json:"group_rules"`
	DefaultGroupSlug string                `json:"default_group_slug"`
	AllowAutoCreate  bool                  `json:"allow_auto_create"`
	NewUserDefaults  samlNewUserDTO        `json:"new_user_defaults"`
}

func (h *AdminOIDCHandler) Get(c *gin.Context) {
	cfg, err := h.repo.Load(c.Request.Context())
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, toOIDCDTO(cfg))
}

func (h *AdminOIDCHandler) Put(c *gin.Context) {
	var req oidcUpdateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	// Before anything is stored: a rule pointing at a group that does
	// not exist would otherwise only be discovered at somebody's login.
	if err := validateSSOGroupRules(c.Request.Context(), h.groups, req.GroupRules, req.DefaultGroupSlug); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	existing, err := h.repo.Load(c.Request.Context())
	if err != nil {
		respondError(c, err)
		return
	}
	secret := req.ClientSecret
	if secret == "" {
		secret = existing.ClientSecret
	}
	cfg := &config.OIDCConfig{
		Enabled:      req.Enabled,
		IssuerURL:    req.IssuerURL,
		ClientID:     req.ClientID,
		ClientSecret: secret,
		RedirectURL:  req.RedirectURL,
		Scopes:       req.Scopes,
		AttributeMapping: config.OIDCAttributeMap{
			Username:    req.AttributeMapping.Username,
			Email:       req.AttributeMapping.Email,
			DisplayName: req.AttributeMapping.DisplayName,
			Groups:      req.AttributeMapping.Groups,
		},
		RoleRules:        req.RoleRules,
		GroupRules:       req.GroupRules,
		DefaultGroupSlug: req.DefaultGroupSlug,
		AllowAutoCreate:  req.AllowAutoCreate,
		NewUserDefaults: config.SAMLNewUserDefaults{
			ExpireDays:         req.NewUserDefaults.ExpireDays,
			TrafficLimitBytes:  req.NewUserDefaults.TrafficLimitBytes,
			TrafficResetPeriod: req.NewUserDefaults.TrafficResetPeriod,
		},
	}
	config.ApplyOIDCDefaults(cfg)

	if !oidcIssuerHTTPS(cfg.Enabled, cfg.IssuerURL) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "issuer_url must be an https:// URL"})
		return
	}

	if err := h.repo.Save(c.Request.Context(), cfg); err != nil {
		respondError(c, err)
		return
	}

	if err := h.oidc.Reload(c.Request.Context(), cfg); err != nil {
		c.JSON(http.StatusOK, gin.H{
			"saved":        true,
			"reload_error": err.Error(),
			"config":       toOIDCDTO(cfg),
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{"saved": true, "config": toOIDCDTO(cfg)})
}

func toOIDCDTO(c *config.OIDCConfig) oidcConfigDTO {
	return oidcConfigDTO{
		Enabled:         c.Enabled,
		IssuerURL:       c.IssuerURL,
		ClientID:        c.ClientID,
		HasClientSecret: c.ClientSecret != "",
		RedirectURL:     c.RedirectURL,
		Scopes:          c.Scopes,
		AttributeMapping: oidcAttrDTO{
			Username:    c.AttributeMapping.Username,
			Email:       c.AttributeMapping.Email,
			DisplayName: c.AttributeMapping.DisplayName,
			Groups:      c.AttributeMapping.Groups,
		},
		RoleRules:        c.RoleRules,
		GroupRules:       c.GroupRules,
		DefaultGroupSlug: c.DefaultGroupSlug,
		AllowAutoCreate:  c.AllowAutoCreate,
		NewUserDefaults: samlNewUserDTO{
			ExpireDays:         c.NewUserDefaults.ExpireDays,
			TrafficLimitBytes:  c.NewUserDefaults.TrafficLimitBytes,
			TrafficResetPeriod: c.NewUserDefaults.TrafficResetPeriod,
		},
	}
}
