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
	AdminGroupIDs    []string         `yaml:"admin_group_ids"`
	DefaultGroupSlug string           `yaml:"default_group_slug"`

	// AllowAutoCreate controls whether a non-admin SSO login may provision
	// a fresh account. When false (the original behaviour) only IdP-admin
	// users are auto-provisioned and everyone else is bounced to the
	// "contact your administrator" page — handy for closed deployments
	// where the admin invites accounts manually first.
	AllowAutoCreate bool `yaml:"allow_auto_create"`

	// KeepAdminWhenNotInGroup, when true, leaves a panel admin's role
	// untouched on SSO login even if the IdP no longer reports them in
	// any of the AdminGroupIDs. Default is false — IdP groups are
	// authoritative for admin rights, both granting and revoking. Flip
	// to true to protect panel-side manual admin grants from automatic
	// demotion on every SSO sync. Operator role is panel-side and
	// unaffected by this flag.
	KeepAdminWhenNotInGroup bool `yaml:"keep_admin_when_not_in_group"`

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
