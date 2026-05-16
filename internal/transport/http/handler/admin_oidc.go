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
// Mutual exclusion: enabling OIDC disables SAML (and vice versa) — SSO
// providers can only run one at a time.
type AdminOIDCHandler struct {
	repo     ports.OIDCConfigRepo
	oidc     *auth.OIDCService
	samlRepo ports.SAMLConfigRepo
	saml     *auth.SAMLService
}

func NewAdminOIDCHandler(repo ports.OIDCConfigRepo, oidcSvc *auth.OIDCService,
	samlRepo ports.SAMLConfigRepo, samlSvc *auth.SAMLService) *AdminOIDCHandler {
	return &AdminOIDCHandler{repo: repo, oidc: oidcSvc, samlRepo: samlRepo, saml: samlSvc}
}

type oidcAttrDTO struct {
	Username    string `json:"username"`
	Email       string `json:"email"`
	DisplayName string `json:"display_name"`
	Groups      string `json:"groups"`
}

type oidcConfigDTO struct {
	Enabled          bool           `json:"enabled"`
	IssuerURL        string         `json:"issuer_url"`
	ClientID         string         `json:"client_id"`
	HasClientSecret  bool           `json:"has_client_secret"`
	RedirectURL      string         `json:"redirect_url"`
	Scopes           []string       `json:"scopes"`
	AttributeMapping oidcAttrDTO    `json:"attribute_mapping"`
	AdminGroupIDs    []string       `json:"admin_group_ids"`
	DefaultGroupSlug string         `json:"default_group_slug"`
	AllowAutoCreate  bool           `json:"allow_auto_create"`
	NewUserDefaults  samlNewUserDTO `json:"new_user_defaults"`
}

type oidcUpdateRequest struct {
	Enabled          bool           `json:"enabled"`
	IssuerURL        string         `json:"issuer_url"`
	ClientID         string         `json:"client_id"`
	ClientSecret     string         `json:"client_secret"`
	RedirectURL      string         `json:"redirect_url"`
	Scopes           []string       `json:"scopes"`
	AttributeMapping oidcAttrDTO    `json:"attribute_mapping"`
	AdminGroupIDs    []string       `json:"admin_group_ids"`
	DefaultGroupSlug string         `json:"default_group_slug"`
	AllowAutoCreate  bool           `json:"allow_auto_create"`
	NewUserDefaults  samlNewUserDTO `json:"new_user_defaults"`
}

func (h *AdminOIDCHandler) Get(c *gin.Context) {
	cfg, err := h.repo.Load(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
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
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
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
		AdminGroupIDs:    req.AdminGroupIDs,
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
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// Mutual exclusion — if we just enabled OIDC, disable SAML.
	if cfg.Enabled && h.samlRepo != nil {
		if samlCfg, err := h.samlRepo.Load(c.Request.Context()); err == nil && samlCfg.Enabled {
			samlCfg.Enabled = false
			_ = h.samlRepo.Save(c.Request.Context(), samlCfg)
			if h.saml != nil {
				_ = h.saml.Reload(c.Request.Context(), samlCfg)
			}
		}
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
		AdminGroupIDs:    c.AdminGroupIDs,
		DefaultGroupSlug: c.DefaultGroupSlug,
		AllowAutoCreate:  c.AllowAutoCreate,
		NewUserDefaults: samlNewUserDTO{
			ExpireDays:         c.NewUserDefaults.ExpireDays,
			TrafficLimitBytes:  c.NewUserDefaults.TrafficLimitBytes,
			TrafficResetPeriod: c.NewUserDefaults.TrafficResetPeriod,
		},
	}
}
