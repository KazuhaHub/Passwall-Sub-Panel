package auth

import (
	"strings"

	"github.com/KazuhaHub/passwall-sub-panel/internal/config"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// MatchFirstRule scans the rules in order and returns the first rule's
// role (as a normalised domain.Role) plus a boolean reporting whether
// any rule matched. Used by the AllowAutoCreate gate where the caller
// only cares about the IdP's "what role did you assign" signal,
// independent of the user's current panel role.
//
// rule.Attribute resolution:
//   - empty or "groups": match against the parsed Groups slice (which
//     was already parsed under the configured Groups attribute URN).
//     This is the shortcut for the common "this group ID → admin" rule.
//   - any other string: literal attribute Name lookup in attrs.
//
// Value match is exact (case-sensitive, no wildcards). Multi-valued
// attributes match if any one value equals rule.Value.
func MatchFirstRule(
	rules []config.SSORoleRule,
	groupsAttrName string,
	attrs map[string][]string,
	groups []string,
) (domain.Role, bool) {
	for _, r := range rules {
		if ruleMatches(r, groupsAttrName, attrs, groups) {
			return parseRoleString(r.Role), true
		}
	}
	return "", false
}

// ResolveRoleForSSO applies the full SSO role policy: rule matching
// PLUS per-rule Keep semantics PLUS the "panel-managed role" carve-out.
//
// Decision matrix:
//   1. No rules configured → SSO is silent about roles. Leave current
//      alone and signal ssoAuthoritative=false so the caller can skip
//      the DB update entirely.
//   2. A rule matched:
//        a. matched.Role == current → no change, ssoAuthoritative=true.
//        b. matched.Role != current AND some rule outputting `current`
//           has Keep=true → preserve current (panel-side grant wins).
//        c. otherwise → apply matched.Role.
//   3. No rule matched:
//        a. No rule outputs `current` → the role isn't IdP-managed at
//           all (think: admin manually granted a custom "auditor" role
//           the IdP doesn't know about). Leave current alone.
//        b. Some rule outputs `current` with Keep=true → preserve.
//        c. Some rule outputs `current` but none have Keep=true →
//           demote to RoleUser (the role is IdP-managed and the IdP
//           is saying "not this user, not this time").
//
// Returns (final, ssoAuthoritative). final is the role to write;
// ssoAuthoritative=false means SSO had nothing to say and the caller
// should treat final as "leave the row's current role untouched".
func ResolveRoleForSSO(
	rules []config.SSORoleRule,
	current domain.Role,
	groupsAttrName string,
	attrs map[string][]string,
	groups []string,
) (final domain.Role, ssoAuthoritative bool) {
	if len(rules) == 0 {
		return current, false
	}

	matched, hasMatch := MatchFirstRule(rules, groupsAttrName, attrs, groups)

	// "Is the user's current role claimed by some rule, and does any
	// claiming rule want to preserve it on miss?"
	currentClaimed, currentKeep := false, false
	for _, r := range rules {
		if parseRoleString(r.Role) == current {
			currentClaimed = true
			if r.Keep {
				currentKeep = true
				break
			}
		}
	}

	if hasMatch {
		if matched == current {
			return current, true
		}
		if currentClaimed && currentKeep {
			return current, true
		}
		return matched, true
	}

	// No rule fired.
	if !currentClaimed {
		// The role isn't part of this rule set's vocabulary —
		// panel-managed. SSO doesn't touch it.
		return current, false
	}
	if currentKeep {
		return current, true
	}
	return domain.RoleUser, true
}

func ruleMatches(r config.SSORoleRule, groupsAttrName string, attrs map[string][]string, groups []string) bool {
	values := lookupRuleValues(r.Attribute, groupsAttrName, attrs, groups)
	for _, v := range values {
		if v == r.Value {
			return true
		}
	}
	return false
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

// parseRoleString trims whitespace and returns the role as a
// domain.Role. Unlike pre-v2.5 we do NOT restrict to a known allowlist
// — admins can define custom role strings (e.g. "auditor"). Middleware
// only grants elevated permissions to admin/operator; unknown roles
// flow through as user-level access until the panel learns about them.
func parseRoleString(s string) domain.Role {
	return domain.Role(strings.TrimSpace(s))
}
