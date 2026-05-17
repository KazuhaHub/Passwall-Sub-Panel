package config

import "time"

// SAMLConfig is the persisted SAML/SSO configuration schema. It lives in
// this package so storage adapters and auth services can share one type
// without importing each other.
//
// Mode controls how much of the form the admin fills in:
//   - "auto": one-click via App Federation Metadata URL. The panel derives
//     SP entity_id / ACS URL from the panel's public base URL, auto-generates
//     a self-signed SP keypair on first save, and uses the Microsoft-default
//     claim URIs for attribute mapping. Periodic refresh of the IdP metadata
//     is always on.
//   - "manual": every field is admin-controlled.
type SAMLConfig struct {
	Enabled bool    `yaml:"enabled"`
	Mode    string  `yaml:"mode"`
	SP      SPConf  `yaml:"sp"`
	IDP     IDPConf `yaml:"idp"`

	AttributeMapping SAMLAttributeMap `yaml:"attribute_mapping"`

	// RoleRules is the attribute-driven role mapping evaluated in order.
	// The first rule whose value appears in the configured attribute
	// decides the panel role. Each rule carries its own Keep flag that
	// controls whether the role is preserved when no rule fires. See
	// internal/service/auth.ResolveRoleForSSO for the full matcher.
	RoleRules []SSORoleRule `yaml:"role_rules"`

	DefaultGroupSlug string `yaml:"default_group_slug"`

	// AllowAutoCreate controls whether an unprivileged SSO login may
	// provision a fresh account. When false (the closed-deployment
	// default) only principals a rule promotes to admin or operator
	// bootstrap an account; every other unknown UPN is bounced to the
	// "contact your administrator" page.
	AllowAutoCreate bool `yaml:"allow_auto_create"`

	NewUserDefaults SAMLNewUserDefaults `yaml:"new_user_defaults"`
}

type SPConf struct {
	EntityID string `yaml:"entity_id"`
	ACSURL   string `yaml:"acs_url"`
	CertPEM  string `yaml:"cert_pem,omitempty"`
	KeyPEM   string `yaml:"key_pem,omitempty"`
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
// Kept here so runtime storage and a future bootstrap CLI share one rule set.
func ApplySAMLDefaults(c *SAMLConfig) {
	switch c.Mode {
	case "auto", "manual":
		// keep
	default:
		c.Mode = "auto"
	}
	if c.IDP.MetadataRefreshInterval == 0 {
		c.IDP.MetadataRefreshInterval = 24 * time.Hour
	}
	// UPN source. Default to "nameid" because SAML 2.0's <Subject><NameID>
	// is the spec-canonical location for the authenticated subject and
	// every major IdP except Microsoft Entra populates it by default
	// (Okta, Google Workspace, Keycloak, ADFS, OneLogin, Auth0...).
	// Entra admins switch this field to a UPN attribute Name URN (e.g.
	// http://schemas.xmlsoap.org/ws/2005/05/identity/claims/upn) and
	// add the matching claim on the IdP side — see saml.go ParseACSResponse
	// for the two supported source forms.
	if c.AttributeMapping.UPN == "" {
		c.AttributeMapping.UPN = "nameid"
	}
	if c.AttributeMapping.Email == "" {
		c.AttributeMapping.Email = "http://schemas.xmlsoap.org/ws/2005/05/identity/claims/emailaddress"
	}
	if c.AttributeMapping.DisplayName == "" {
		// Microsoft Entra's "displayname" claim — the user-facing real
		// display name from user.displayname. Entra emits it when the
		// claim has been explicitly added to the SAML app's "Attributes &
		// Claims" list.
		c.AttributeMapping.DisplayName = "http://schemas.microsoft.com/identity/claims/displayname"
	}
	if c.AttributeMapping.Groups == "" {
		c.AttributeMapping.Groups = "http://schemas.microsoft.com/ws/2008/06/identity/claims/groups"
	}
	if c.NewUserDefaults.TrafficResetPeriod == "" {
		c.NewUserDefaults.TrafficResetPeriod = "monthly"
	}
}
