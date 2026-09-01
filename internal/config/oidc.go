package config

// OIDCConfig holds the runtime-editable OIDC/OAuth2 SSO settings, stored
// in MySQL via ports.OIDCConfigRepo. Parallels SAMLConfig but uses an
// OpenID Connect Discovery URL + client credentials instead of SAML
// metadata.
//
// Attribute mapping uses ID-token claim names (e.g. "preferred_username",
// "email", "groups") — whatever the IdP includes in its ID token.
type OIDCConfig struct {
	Enabled bool `json:"enabled"`

	// IssuerURL is the OIDC discovery base, e.g. "https://login.example.com".
	// /.well-known/openid-configuration is fetched from here.
	IssuerURL string `json:"issuer_url"`
	// ClientID and ClientSecret are the OAuth2 client credentials issued
	// by the IdP for this panel.
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret"`
	// RedirectURL is the OAuth2 callback URL — must match what's
	// registered with the IdP, typically "<panel-base>/api/auth/oidc/callback".
	RedirectURL string `json:"redirect_url"`
	// Scopes is the OAuth2 scopes list. "openid" is always added; common
	// extras are "profile" and "email" (and "groups" if your IdP supports it).
	Scopes []string `json:"scopes"`

	AttributeMapping OIDCAttributeMap `json:"attribute_mapping"`

	// RoleRules: see SAMLConfig — attribute-driven panel role mapping
	// evaluated in order; the first matching rule wins, each rule has
	// its own Keep flag for the "no match" branch.
	RoleRules []SSORoleRule `json:"role_rules"`

	// GroupRules place a principal into an OU from their IdP
	// attributes: first match wins, DefaultGroupSlug is the fallback,
	// and — unlike a create-time-only mapping — they are re-evaluated on
	// every login so a revoked IdP group actually costs the OU it backed.
	GroupRules []SSOGroupRule `json:"group_rules"`

	DefaultGroupSlug string `json:"default_group_slug"`

	// AllowAutoCreate: see SAMLConfig.AllowAutoCreate. Off by default;
	// only principals a rule promotes to admin / operator bootstrap an
	// account when this is off.
	AllowAutoCreate bool `json:"allow_auto_create"`

	NewUserDefaults SAMLNewUserDefaults `json:"new_user_defaults"`
}

// OIDCAttributeMap names the ID-token claims to read for each user field.
type OIDCAttributeMap struct {
	Username    string `json:"username"`
	Email       string `json:"email"`
	DisplayName string `json:"display_name"`
	Groups      string `json:"groups"`
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
