package config

// SSORoleRule maps an IdP-side attribute value to a panel role. SAML and
// OIDC configs each carry a slice of these; the SSO login pipeline
// evaluates them in order and the first matching rule decides the
// panel role for that login. No match → RoleUser default.
//
// Attribute names follow the IdP's own conventions:
//   * SAML: the Attribute Name URN, e.g.
//     "http://schemas.microsoft.com/ws/2008/06/identity/claims/groups".
//     Empty string means "use whatever URN is configured under
//     AttributeMapping.Groups" — the common case of "this group ID
//     maps to admin" without having to repeat the long URN per rule.
//   * OIDC: the claim name, e.g. "groups", "roles", "panel_role".
//     Empty string is treated the same way as SAML — falls back to the
//     groups claim.
//
// Role is a free-form string (not a typed enum) so future panel role
// additions don't require config-schema churn — a new role becomes
// usable the moment domain.Role recognises it.
type SSORoleRule struct {
	Attribute string `yaml:"attribute" json:"attribute"`
	Value     string `yaml:"value" json:"value"`
	Role      string `yaml:"role" json:"role"`
}
