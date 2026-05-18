package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/KazuhaHub/passwall-sub-panel/internal/config"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/auth"
)

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
}

func NewAdminOIDCHandler(repo ports.OIDCConfigRepo, oidcSvc *auth.OIDCService) *AdminOIDCHandler {
	return &AdminOIDCHandler{repo: repo, oidc: oidcSvc}
}

type oidcAttrDTO struct {
	Username    string `json:"username"`
	Email       string `json:"email"`
	DisplayName string `json:"display_name"`
	Groups      string `json:"groups"`
}

type oidcConfigDTO struct {
	Enabled                   bool           `json:"enabled"`
	IssuerURL                 string         `json:"issuer_url"`
	ClientID                  string         `json:"client_id"`
	HasClientSecret           bool           `json:"has_client_secret"`
	RedirectURL               string         `json:"redirect_url"`
	Scopes                    []string       `json:"scopes"`
	AttributeMapping          oidcAttrDTO    `json:"attribute_mapping"`
	RoleRules        []config.SSORoleRule `json:"role_rules"`
	DefaultGroupSlug string               `json:"default_group_slug"`
	AllowAutoCreate  bool                 `json:"allow_auto_create"`
	NewUserDefaults  samlNewUserDTO       `json:"new_user_defaults"`
}

type oidcUpdateRequest struct {
	Enabled                   bool           `json:"enabled"`
	IssuerURL                 string         `json:"issuer_url"`
	ClientID                  string         `json:"client_id"`
	ClientSecret              string         `json:"client_secret"`
	RedirectURL               string         `json:"redirect_url"`
	Scopes                    []string       `json:"scopes"`
	AttributeMapping          oidcAttrDTO    `json:"attribute_mapping"`
	RoleRules        []config.SSORoleRule `json:"role_rules"`
	DefaultGroupSlug string               `json:"default_group_slug"`
	AllowAutoCreate  bool                 `json:"allow_auto_create"`
	NewUserDefaults  samlNewUserDTO       `json:"new_user_defaults"`
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
		DefaultGroupSlug: req.DefaultGroupSlug,
		AllowAutoCreate:  req.AllowAutoCreate,
		NewUserDefaults: config.SAMLNewUserDefaults{
			ExpireDays:         req.NewUserDefaults.ExpireDays,
			TrafficLimitBytes:  req.NewUserDefaults.TrafficLimitBytes,
			TrafficResetPeriod: req.NewUserDefaults.TrafficResetPeriod,
		},
	}
	config.ApplyOIDCDefaults(cfg)

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
		DefaultGroupSlug: c.DefaultGroupSlug,
		AllowAutoCreate:  c.AllowAutoCreate,
		NewUserDefaults: samlNewUserDTO{
			ExpireDays:         c.NewUserDefaults.ExpireDays,
			TrafficLimitBytes:  c.NewUserDefaults.TrafficLimitBytes,
			TrafficResetPeriod: c.NewUserDefaults.TrafficResetPeriod,
		},
	}
}
