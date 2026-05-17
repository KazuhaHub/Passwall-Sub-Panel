package auth

import (
	"github.com/KazuhaHub/passwall-sub-panel/internal/config"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// ResolveRoleFromAssertion picks the panel role to assign to an SSO
// principal based on the admin-configured role rules and the
// attribute set the IdP sent.
//
// Evaluation:
//   - Rules are evaluated in order; the first rule whose Value appears
//     in the configured attribute decides the role.
//   - rule.Attribute is the IdP attribute Name (SAML URN / OIDC claim).
//     An empty string is shorthand for "the groups attribute" — looked
//     up via groupsAttrName (the AttributeMapping.Groups URN) so the
//     common "groupID → admin" rule can omit the long URN.
//   - When the rules list is empty, fall back to synthesising rules
//     from legacyAdminGroupIDs: every group ID becomes
//     `{Attribute: "", Value: gid, Role: admin}`. This keeps existing
//     "admin_group_ids = [g1, g2]" configs working without any
//     migration step and lets admins move to RoleRules at their own
//     pace.
//
// Returns:
//   - role:    the panel role to apply ("admin" / "operator" / "user")
//   - matched: true iff a rule fired. The caller can use this to
//     distinguish "IdP said user" (matched=true, role="user") from
//     "no rule fired" (matched=false). Today both produce RoleUser,
//     but keeping the signal separate leaves room for "deny login
//     when no rule matches" policies later.
//
// The fallback role for an unmatched assertion is domain.RoleUser.
func ResolveRoleFromAssertion(
	rules []config.SSORoleRule,
	legacyAdminGroupIDs []string,
	groupsAttrName string,
	attrs map[string][]string,
	groups []string,
) (role domain.Role, matched bool) {
	effective := rules
	if len(effective) == 0 && len(legacyAdminGroupIDs) > 0 {
		effective = make([]config.SSORoleRule, 0, len(legacyAdminGroupIDs))
		for _, g := range legacyAdminGroupIDs {
			effective = append(effective, config.SSORoleRule{
				Attribute: "", // groups shortcut
				Value:     g,
				Role:      string(domain.RoleAdmin),
			})
		}
	}
	if len(effective) == 0 {
		return domain.RoleUser, false
	}

	for _, r := range effective {
		values := lookupRuleValues(r.Attribute, groupsAttrName, attrs, groups)
		for _, v := range values {
			if v == r.Value {
				resolved := normaliseRole(r.Role)
				if resolved == "" {
					return domain.RoleUser, false
				}
				return resolved, true
			}
		}
	}
	return domain.RoleUser, false
}

// lookupRuleValues resolves the attribute slice a rule should look at.
// Empty rule.Attribute is the "groups" shortcut — we prefer the parsed
// Groups slice (which already deduped under the configured Groups URN)
// and fall back to attrs[groupsAttrName] in case a deployment configured
// Groups under a non-default name and we somehow missed parsing.
func lookupRuleValues(ruleAttr, groupsAttrName string, attrs map[string][]string, groups []string) []string {
	if ruleAttr == "" || ruleAttr == "groups" {
		if len(groups) > 0 {
			return groups
		}
		if groupsAttrName != "" {
			return attrs[groupsAttrName]
		}
		return attrs["groups"]
	}
	return attrs[ruleAttr]
}

// normaliseRole maps the free-form rule.Role string to a domain.Role.
// We treat unknown role strings as a config error and refuse to apply
// them — returning empty so the caller can decide whether to drop to
// RoleUser or surface the error. The known set tracks the panel's
// role enum; future additions become recognised here.
func normaliseRole(s string) domain.Role {
	switch domain.Role(s) {
	case domain.RoleAdmin, domain.RoleOperator, domain.RoleUser:
		return domain.Role(s)
	}
	return ""
}
