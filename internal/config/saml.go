package config

import "time"

// SAMLConfig is the schema of saml.yaml. It lives in this package — not in
// adapters/yaml — so both the loader (adapter) and the consumer (service)
// can reference the same type without one importing the other.
type SAMLConfig struct {
	Enabled bool    `yaml:"enabled"`
	SP      SPConf  `yaml:"sp"`
	IDP     IDPConf `yaml:"idp"`

	AttributeMapping SAMLAttributeMap `yaml:"attribute_mapping"`
	AdminGroupIDs    []string         `yaml:"admin_group_ids"`
	DefaultGroupSlug string           `yaml:"default_group_slug"`

	NewUserDefaults SAMLNewUserDefaults `yaml:"new_user_defaults"`
}

type SPConf struct {
	EntityID    string `yaml:"entity_id"`
	ACSURL      string `yaml:"acs_url"`
	CertPEMPath string `yaml:"cert_pem_path"`
	KeyPEMPath  string `yaml:"key_pem_path"`
}

type IDPConf struct {
	MetadataURL             string        `yaml:"metadata_url"`
	MetadataRefreshInterval time.Duration `yaml:"metadata_refresh_interval"`
}

type SAMLAttributeMap struct {
	UPN         string `yaml:"upn"`
	Email       string `yaml:"email"`
	DisplayName string `yaml:"display_name"`
	Groups      string `yaml:"groups"`
}

type SAMLNewUserDefaults struct {
	TrafficLimitBytes  int64  `yaml:"traffic_limit_bytes"`
	ExpireDays         int    `yaml:"expire_days"`
	TrafficResetPeriod string `yaml:"traffic_reset_period"`
}

// ApplySAMLDefaults fills in any zero fields with sensible defaults.
// Kept here so both the YAML loader and a future bootstrap CLI share one rule set.
func ApplySAMLDefaults(c *SAMLConfig) {
	if c.IDP.MetadataRefreshInterval == 0 {
		c.IDP.MetadataRefreshInterval = 24 * time.Hour
	}
	if c.AttributeMapping.UPN == "" {
		c.AttributeMapping.UPN = "http://schemas.xmlsoap.org/ws/2005/05/identity/claims/upn"
	}
	if c.AttributeMapping.Email == "" {
		c.AttributeMapping.Email = "http://schemas.xmlsoap.org/ws/2005/05/identity/claims/emailaddress"
	}
	if c.AttributeMapping.DisplayName == "" {
		c.AttributeMapping.DisplayName = "http://schemas.xmlsoap.org/ws/2005/05/identity/claims/displayname"
	}
	if c.AttributeMapping.Groups == "" {
		c.AttributeMapping.Groups = "http://schemas.microsoft.com/ws/2008/06/identity/claims/groups"
	}
}
