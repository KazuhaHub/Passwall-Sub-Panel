package handler

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/KazuhaHub/passwall-sub-panel/internal/config"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/samlkey"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/auth"
)

// AdminSAMLHandler exposes /api/admin/settings/saml — GET returns the
// stored SAML/SSO configuration, PUT replaces it and reloads the live
// ServiceProvider so the edit takes effect immediately.
//
// Sensitive fields (SP private key PEM) are NEVER returned in plaintext.
// The GET response carries a "has_sp_key" boolean instead; the admin
// re-pastes the key only when actually changing it.
//
// SAML and OIDC can run side-by-side from v2.3.2 onwards — the SSO
// identity model keys accounts on (provider, subject) tuples, so the
// two protocols have disjoint namespaces and no longer need to be
// mutually exclusive at the config level.
type AdminSAMLHandler struct {
	repo     ports.SAMLConfigRepo
	saml     *auth.SAMLService
	settings ports.SettingsRepo
}

func NewAdminSAMLHandler(repo ports.SAMLConfigRepo, samlSvc *auth.SAMLService,
	settings ports.SettingsRepo) *AdminSAMLHandler {
	return &AdminSAMLHandler{
		repo:     repo,
		saml:     samlSvc,
		settings: settings,
	}
}

type samlSPDTO struct {
	EntityID  string `json:"entity_id"`
	ACSURL    string `json:"acs_url"`
	CertPEM   string `json:"cert_pem"`
	HasKeyPEM bool   `json:"has_key_pem"`
}

type samlIDPDTO struct {
	MetadataURL          string `json:"metadata_url"`
	MetadataRefreshHours int    `json:"metadata_refresh_hours"`
}

type samlAttrDTO struct {
	UPN         string `json:"upn"`
	Email       string `json:"email"`
	DisplayName string `json:"display_name"`
	Groups      string `json:"groups"`
}

type samlNewUserDTO struct {
	ExpireDays         int    `json:"expire_days"`
	TrafficLimitBytes  int64  `json:"traffic_limit_bytes"`
	TrafficResetPeriod string `json:"traffic_reset_period"`
}

type samlConfigDTO struct {
	Enabled                 bool                 `json:"enabled"`
	Mode                    string               `json:"mode"`
	SP                      samlSPDTO            `json:"sp"`
	IDP                     samlIDPDTO           `json:"idp"`
	AttributeMapping samlAttrDTO          `json:"attribute_mapping"`
	RoleRules        []config.SSORoleRule `json:"role_rules"`
	DefaultGroupSlug string               `json:"default_group_slug"`
	AllowAutoCreate  bool                 `json:"allow_auto_create"`
	NewUserDefaults  samlNewUserDTO       `json:"new_user_defaults"`
}

// samlUpdateRequest is the same shape as samlConfigDTO but SP.KeyPEM is an
// explicit field that, when empty, preserves the existing stored key.
type samlUpdateRequest struct {
	Enabled bool   `json:"enabled"`
	Mode    string `json:"mode"`
	SP      struct {
		EntityID string `json:"entity_id"`
		ACSURL   string `json:"acs_url"`
		CertPEM  string `json:"cert_pem"`
		KeyPEM   string `json:"key_pem"`
	} `json:"sp"`
	IDP struct {
		MetadataURL          string `json:"metadata_url"`
		MetadataRefreshHours int    `json:"metadata_refresh_hours"`
	} `json:"idp"`
	AttributeMapping samlAttrDTO          `json:"attribute_mapping"`
	RoleRules        []config.SSORoleRule `json:"role_rules"`
	DefaultGroupSlug string               `json:"default_group_slug"`
	AllowAutoCreate  bool                 `json:"allow_auto_create"`
	NewUserDefaults  samlNewUserDTO       `json:"new_user_defaults"`
}

type samlFetchRequest struct {
	URL string `json:"url" binding:"required"`
}

// FetchMetadata pulls the IdP metadata XML from the given URL and returns
// a small summary the admin UI shows next to its input field. Pure
// read-only — nothing is persisted. Used by the auto-mode "Fetch & verify"
// button so admins can confirm the URL reaches the intended directory
// before saving.
func (h *AdminSAMLHandler) FetchMetadata(c *gin.Context) {
	var req samlFetchRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	summary, err := auth.FetchIDPMetadataSummary(c.Request.Context(), req.URL)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, summary)
}

func (h *AdminSAMLHandler) Get(c *gin.Context) {
	cfg, err := h.repo.Load(c.Request.Context())
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, toSAMLDTO(cfg))
}

func (h *AdminSAMLHandler) Put(c *gin.Context) {
	var req samlUpdateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	existing, err := h.repo.Load(c.Request.Context())
	if err != nil {
		respondError(c, err)
		return
	}

	mode := req.Mode
	if mode != "auto" && mode != "manual" {
		mode = "auto"
	}

	refreshHours := req.IDP.MetadataRefreshHours
	if refreshHours <= 0 {
		refreshHours = 24
	}

	cfg := &config.SAMLConfig{
		Enabled: req.Enabled,
		Mode:    mode,
		SP: config.SPConf{
			EntityID: req.SP.EntityID,
			ACSURL:   req.SP.ACSURL,
			CertPEM:  req.SP.CertPEM,
			KeyPEM:   firstNonEmpty(req.SP.KeyPEM, existing.SP.KeyPEM),
		},
		IDP: config.IDPConf{
			MetadataURL:             req.IDP.MetadataURL,
			MetadataRefreshInterval: time.Duration(refreshHours) * time.Hour,
		},
		AttributeMapping: config.SAMLAttributeMap{
			UPN:         req.AttributeMapping.UPN,
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

	// In auto mode, derive SP entity_id / ACS URL from the panel's public
	// base URL (sub_base_url) and auto-generate a self-signed SP keypair
	// the first time SAML is enabled — admin should not have to fill those
	// fields by hand.
	//
	// Attribute mapping is NOT reset here: auto mode only takes care of
	// "things derivable from the IdP metadata + panel base URL". Claim
	// URNs are policy decisions per deployment (an admin's Entra tenant
	// may emit a custom-namespace UPN claim, etc.) and must remain
	// admin-editable. ApplySAMLDefaults still seeds the four well-known
	// claim URLs when the admin leaves a field blank.
	if cfg.Mode == "auto" {
		base := resolveSubBaseForRequest(c.Request.Context(), h.settings, c.Request)
		cfg.SP.EntityID = base + "/api/auth/saml/metadata"
		cfg.SP.ACSURL = base + "/api/auth/saml/acs"
		if cfg.SP.CertPEM == "" || cfg.SP.KeyPEM == "" {
			cert, key, err := samlkey.GenerateSelfSigned(cfg.SP.EntityID)
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "Generate SP keypair: " + err.Error()})
				return
			}
			cfg.SP.CertPEM = cert
			cfg.SP.KeyPEM = key
		}
	}

	config.ApplySAMLDefaults(cfg)

	if err := h.repo.Save(c.Request.Context(), cfg); err != nil {
		respondError(c, err)
		return
	}

	// Best-effort live reload: persistence already succeeded, so a bad SP
	// build (eg. malformed cert) is reported but does not fail the request.
	if err := h.saml.Reload(c.Request.Context(), cfg); err != nil {
		c.JSON(http.StatusOK, gin.H{
			"saved":        true,
			"reload_error": err.Error(),
			"config":       toSAMLDTO(cfg),
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{"saved": true, "config": toSAMLDTO(cfg)})
}

func toSAMLDTO(c *config.SAMLConfig) samlConfigDTO {
	hours := int(c.IDP.MetadataRefreshInterval / time.Hour)
	if hours <= 0 {
		hours = 24
	}
	mode := c.Mode
	if mode != "auto" && mode != "manual" {
		mode = "auto"
	}
	return samlConfigDTO{
		Enabled: c.Enabled,
		Mode:    mode,
		SP: samlSPDTO{
			EntityID:  c.SP.EntityID,
			ACSURL:    c.SP.ACSURL,
			CertPEM:   c.SP.CertPEM,
			HasKeyPEM: c.SP.KeyPEM != "",
		},
		IDP: samlIDPDTO{
			MetadataURL:          c.IDP.MetadataURL,
			MetadataRefreshHours: hours,
		},
		AttributeMapping: samlAttrDTO{
			UPN:         c.AttributeMapping.UPN,
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

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
