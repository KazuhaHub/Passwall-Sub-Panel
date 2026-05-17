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
//
// Keep controls what happens when this rule does NOT match on a given
// SSO login:
//   - Keep=true:  if the user's current panel role equals this rule's
//                 Role, leave it alone (the rule "owns" that role and
//                 wants to preserve panel-side grants).
//   - Keep=false: rule-driven sync demotes the user away from this
//                 role when no matching rule fires; the user falls to
//                 RoleUser unless a different rule grants something
//                 else.
// Per-rule rather than a single global switch so an admin can run
// e.g. "auditor is panel-managed, keep on miss" alongside "admin is
// IdP-authoritative, demote on miss" in the same config.
type SSORoleRule struct {
	Attribute string `yaml:"attribute" json:"attribute"`
	Value     string `yaml:"value" json:"value"`
	Role      string `yaml:"role" json:"role"`
	Keep      bool   `yaml:"keep" json:"keep"`
}
