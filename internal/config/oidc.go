package config

// OIDCConfig holds the runtime-editable OIDC/OAuth2 SSO settings, stored
// in MySQL via ports.OIDCConfigRepo. Parallels SAMLConfig but uses an
// OpenID Connect Discovery URL + client credentials instead of SAML
// metadata.
//
// Attribute mapping uses ID-token claim names (e.g. "preferred_username",
// "email", "groups") — whatever the IdP includes in its ID token.
type OIDCConfig struct {
	Enabled bool `yaml:"enabled" json:"enabled"`

	// IssuerURL is the OIDC discovery base, e.g. "https://login.example.com".
	// /.well-known/openid-configuration is fetched from here.
	IssuerURL string `yaml:"issuer_url" json:"issuer_url"`
	// ClientID and ClientSecret are the OAuth2 client credentials issued
	// by the IdP for this panel.
	ClientID     string `yaml:"client_id" json:"client_id"`
	ClientSecret string `yaml:"client_secret" json:"client_secret"`
	// RedirectURL is the OAuth2 callback URL — must match what's
	// registered with the IdP, typically "<panel-base>/api/auth/oidc/callback".
	RedirectURL string `yaml:"redirect_url" json:"redirect_url"`
	// Scopes is the OAuth2 scopes list. "openid" is always added; common
	// extras are "profile" and "email" (and "groups" if your IdP supports it).
	Scopes []string `yaml:"scopes" json:"scopes"`

	AttributeMapping OIDCAttributeMap `yaml:"attribute_mapping" json:"attribute_mapping"`

	AdminGroupIDs    []string `yaml:"admin_group_ids" json:"admin_group_ids"`
	DefaultGroupSlug string   `yaml:"default_group_slug" json:"default_group_slug"`

	// AllowAutoCreate: see SAMLConfig.AllowAutoCreate. Off by default; only
	// IdP-admin users are auto-provisioned until the admin opts in.
	AllowAutoCreate bool `yaml:"allow_auto_create" json:"allow_auto_create"`

	// KeepAdminWhenNotInGroup: see SAMLConfig docs — when true, panel
	// admin role is preserved even after the IdP stops reporting them
	// in any AdminGroupID. Default false = IdP groups authoritative.
	KeepAdminWhenNotInGroup bool `yaml:"keep_admin_when_not_in_group" json:"keep_admin_when_not_in_group"`

	NewUserDefaults SAMLNewUserDefaults `yaml:"new_user_defaults" json:"new_user_defaults"`
}

// OIDCAttributeMap names the ID-token claims to read for each user field.
type OIDCAttributeMap struct {
	Username    string `yaml:"username" json:"username"`
	Email       string `yaml:"email" json:"email"`
	DisplayName string `yaml:"display_name" json:"display_name"`
	Groups      string `yaml:"groups" json:"groups"`
}

// ApplyOIDCDefaults fills zero fields with documented defaults.
func ApplyOIDCDefaults(c *OIDCConfig) {
	if len(c.Scopes) == 0 {
		c.Scopes = []string{"openid", "profile", "email"}
	}
	if c.AttributeMapping.Username == "" {
		c.AttributeMapping.Username = "preferred_username"
	}
	if c.AttributeMapping.Email == "" {
		c.AttributeMapping.Email = "email"
	}
	if c.AttributeMapping.DisplayName == "" {
		c.AttributeMapping.DisplayName = "name"
	}
	if c.AttributeMapping.Groups == "" {
		c.AttributeMapping.Groups = "groups"
	}
	if c.NewUserDefaults.TrafficResetPeriod == "" {
		c.NewUserDefaults.TrafficResetPeriod = "monthly"
	}
}
