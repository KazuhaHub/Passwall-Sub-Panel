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
	// Note is admin-facing free-form text for documenting the rule
	// ("Entra global admins", "ops on-call group", etc.). Never read
	// by the resolver — exists purely to make the rules table
	// readable when the rule count grows.
	Note string `yaml:"note" json:"note"`
}

// SSOGroupRule maps an IdP-side attribute value to a panel group (OU).
// SAML and OIDC configs each carry a slice of these; on FIRST-TIME
// provisioning the pipeline evaluates them in order and the first
// matching rule decides which group the new user lands in. No match →
// DefaultGroupSlug.
//
// Attribute and Value follow exactly the same resolution and matching
// rules as SSORoleRule — same code, not a parallel implementation — so
// an admin who has written role rules already knows how to write these.
//
// Deliberately a separate list rather than a Group field on
// SSORoleRule. "Which principals are administrators" and "which OU does
// this principal belong to" are rarely the same condition, and folding
// them into one first-match list would force the two decisions to share
// a single priority order. SSORoleRule.Keep is also role-specific and
// has no meaning for group placement.
//
// Group rules are evaluated on EVERY login, not only at creation, for
// the same reason role rules are: a group decides node assignment,
// subscription content and — since v3.9.2-beta.10 — the user's
// entitlements. If PSP never re-read the IdP, a principal removed from
// the group backing a premium OU would keep that OU's quota and nodes
// forever. Not syncing errs toward OVER-entitlement, which is the worse
// direction to miss in.
//
// What makes that safe on upgrade is the same carve-out the role engine
// uses, not a migration flag: a deployment with no group rules
// configured has a rule set that claims no OU, so nothing is
// IdP-managed and no existing user moves. See ResolveGroupForSSO for
// the full matrix.
type SSOGroupRule struct {
	Attribute string `yaml:"attribute" json:"attribute"`
	Value     string `yaml:"value" json:"value"`
	// Group is the panel group's SLUG, not its display name or id.
	// A slug that does not resolve is a configuration error and fails
	// the login loudly rather than falling back to the default group:
	// silently defaulting would leave an operator believing their
	// segmentation works when every user is landing in one bucket.
	Group string `yaml:"group" json:"group"`
	// Keep mirrors SSORoleRule.Keep for group placement: when this rule
	// does not match a given login and the user is currently IN this
	// rule's group, Keep=true leaves them there (the OU is treated as
	// panel-managed) while Keep=false lets them fall back to the
	// default group. Per-rule rather than global so "contractors is
	// IdP-authoritative" can sit beside "vip is granted by hand" in one
	// config.
	Keep bool `yaml:"keep" json:"keep"`
	// Note is admin-facing free-form text, never read by the resolver.
	Note string `yaml:"note" json:"note"`
}
