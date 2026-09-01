package auth

import (
	"strings"

	"github.com/KazuhaHub/passwall-sub-panel/internal/config"
)

// MatchFirstGroupRule scans the group rules in order and returns the
// slug of the first matching rule's group, plus whether any rule
// matched. A miss is not an error — the caller falls back to the
// deployment's default group.
//
// Matching goes through the same attributeMatches / lookupRuleValues
// pair the role rules use, so "attribute" and "value" mean exactly what
// they mean over there: empty attribute is the groups shortcut, the
// comparison is exact, and a multi-valued attribute matches when any
// one of its values is equal.
//
// The returned slug is trimmed. A rule whose Group is blank after
// trimming is SKIPPED rather than treated as a match for "no group":
// an empty Group is an incomplete rule an admin is still filling in,
// and letting it match would shadow every rule below it — a
// half-written row at the top of the list would silently disable the
// rest of the segmentation.
func MatchFirstGroupRule(
	rules []config.SSOGroupRule,
	groupsAttrName string,
	attrs map[string][]string,
	groups []string,
) (string, bool) {
	for _, r := range rules {
		slug := strings.TrimSpace(r.Group)
		if slug == "" {
			continue
		}
		if attributeMatches(r.Attribute, r.Value, groupsAttrName, attrs, groups) {
			return slug, true
		}
	}
	return "", false
}

// idpSpokeAboutGroups reports whether the assertion actually carried any of
// the attributes this rule set reads.
//
// It exists because attributeMatches cannot tell "the IdP says you are not in
// that group" from "the IdP said nothing at all", and only the first is a
// revocation. The second happens for reasons that have nothing to do with the
// principal: Entra stops emitting `groups` and sends `hasgroups` once a user
// is in roughly 150+ groups, a claim mapping can be edited or broken, a new
// app registration can ship without the claim configured.
//
// Demoting on silence would turn any of those into a fleet-wide entitlement
// change — every SSO user leaving their OU on their next login, losing its
// quota and its node set — triggered by a directory-side edit nobody connected to
// PSP. So silence means SSO has no opinion, and the stored group stands.
func idpSpokeAboutGroups(
	rules []config.SSOGroupRule,
	groupsAttrName string,
	attrs map[string][]string,
	groups []string,
) bool {
	for _, r := range rules {
		if len(lookupRuleValues(r.Attribute, groupsAttrName, attrs, groups)) > 0 {
			return true
		}
	}
	return false
}

// ResolveGroupForSSO applies the full SSO group policy on an EXISTING
// user: rule matching PLUS per-rule Keep semantics PLUS the
// "panel-managed group" carve-out. It is the group-shaped twin of
// ResolveRoleForSSO, and the matrix is deliberately identical so an
// admin only has to learn one model.
//
// current is the user's present group slug ("" when they are in no
// group). defaultSlug is the deployment's DefaultGroupSlug, which plays
// the part RoleUser plays for roles: the place an IdP-managed principal
// falls back to when the IdP stops claiming them.
//
// Decision matrix:
//  1. No group rules configured → SSO is silent about groups. Leave
//     current alone, ssoAuthoritative=false. This is every deployment
//     immediately after upgrading, which is why enabling group sync
//     cannot re-home anyone until an admin writes a rule.
//  2. A rule matched:
//     a. matched == current → no change, ssoAuthoritative=true.
//     b. matched != current AND some rule claiming `current` has
//     Keep=true → preserve current (a hand-granted OU wins).
//     c. otherwise → apply matched.
//  3. No rule matched:
//     a. No rule claims `current` → this OU is not part of the rule
//     set's vocabulary, so an admin placed them there by hand and
//     the IdP has no opinion. Leave it alone.
//     b. Some rule claims `current` with Keep=true → preserve.
//     c. Some rule claims `current`, none with Keep → the OU IS
//     IdP-managed and the IdP is saying "not this principal, not
//     this time". Fall back to defaultSlug. This is the branch that
//     closes the entitlement hole: losing the IdP group that backs a
//     premium OU has to actually cost the premium OU.
//
// Returns (final, ssoAuthoritative); ssoAuthoritative=false means the
// caller should leave the stored group untouched rather than write
// final.
func ResolveGroupForSSO(
	rules []config.SSOGroupRule,
	current string,
	defaultSlug string,
	groupsAttrName string,
	attrs map[string][]string,
	groups []string,
) (final string, ssoAuthoritative bool) {
	// Fast path and statement of intent; it is not the only thing
	// keeping an upgrade from re-homing users. With no rules, nothing
	// can be claimed, so the currentClaimed carve-out below returns the
	// same answer. Kept for symmetry with ResolveRoleForSSO and because
	// "no rules means SSO is silent" deserves to be legible at the top
	// rather than inferred from a later branch.
	if len(rules) == 0 {
		return current, false
	}

	matched, hasMatch := MatchFirstGroupRule(rules, groupsAttrName, attrs, groups)

	// "Is the user's current group claimed by some rule, and does any
	// claiming rule want to preserve it on miss?"
	currentClaimed, currentKeep := false, false
	if current != "" {
		for _, r := range rules {
			if strings.TrimSpace(r.Group) != current {
				continue
			}
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

	if !currentClaimed {
		return current, false
	}
	if currentKeep {
		return current, true
	}
	// Below here we would demote. Two conditions have to hold first, and
	// neither is about this principal.
	//
	// The IdP must have actually said something. See idpSpokeAboutGroups:
	// an absent claim is not a revocation, and treating it as one turns a
	// directory-side accident into a fleet-wide entitlement change.
	if !idpSpokeAboutGroups(rules, groupsAttrName, attrs, groups) {
		return current, false
	}
	// And there has to be somewhere to demote TO. An empty defaultSlug
	// means no group, and a user in no group inherits nothing — which
	// resolves to 0/0/0, i.e. UNLIMITED traffic and no connection caps.
	// Demoting into that would hand a revoked principal MORE than they
	// had, the exact inversion of what this branch exists to do. The
	// settings validator refuses to store rules without a default group,
	// so this is the belt to that braces.
	if defaultSlug == "" {
		return current, false
	}
	return defaultSlug, true
}
