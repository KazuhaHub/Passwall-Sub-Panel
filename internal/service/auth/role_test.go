package auth

import (
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/config"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// Helpers shared across the resolver tests. Build the assertion-shaped
// inputs once so each test reads as "given these rules, what happens".
func attrs(kv map[string][]string) map[string][]string { return kv }
func groupsOf(g ...string) []string                    { return g }

func TestResolveRoleForSSO_EmptyRulesLeaveCurrentAlone(t *testing.T) {
	// No rules → SSO is silent. Whatever the user currently is, stays.
	role, sso := ResolveRoleForSSO(nil, domain.RoleAdmin, "groups", nil, nil)
	if role != domain.RoleAdmin || sso {
		t.Fatalf("empty rules: got (%q, sso=%v), want (admin, false)", role, sso)
	}
}

func TestResolveRoleForSSO_FirstMatchWins(t *testing.T) {
	// Two rules, both could fire; first-listed one decides.
	rules := []config.SSORoleRule{
		{Attribute: "", Value: "g-ops", Role: "operator"},
		{Attribute: "", Value: "g-adm", Role: "admin"},
	}
	role, sso := ResolveRoleForSSO(rules, domain.RoleUser, "groups", nil, groupsOf("g-adm", "g-ops"))
	if role != domain.RoleOperator || !sso {
		t.Fatalf("first match: got (%q, sso=%v), want (operator, true)", role, sso)
	}
}

// An empty rule Value must never match — even against a groups attribute that
// contains an empty-string element. Otherwise a blank Value on an admin-granting
// rule (an easy admin typo) would elevate any such login (privilege-escalation
// footgun).
func TestResolveRoleForSSO_EmptyRuleValueNeverMatches(t *testing.T) {
	rules := []config.SSORoleRule{
		{Attribute: "", Value: "", Role: "admin"}, // blank Value
	}
	role, _ := ResolveRoleForSSO(rules, domain.RoleUser, "groups", nil, groupsOf("", "some-group"))
	if role == domain.RoleAdmin {
		t.Fatalf("blank-Value rule must not elevate to admin; got %q", role)
	}
}

func TestResolveRoleForSSO_CustomAttributeMatch(t *testing.T) {
	// Non-empty attribute name → exact attribute lookup, not groups.
	rules := []config.SSORoleRule{
		{Attribute: "panel_role", Value: "auditor", Role: "auditor"},
	}
	a := attrs(map[string][]string{"panel_role": {"auditor"}})
	role, sso := ResolveRoleForSSO(rules, domain.RoleUser, "groups", a, nil)
	if string(role) != "auditor" || !sso {
		t.Fatalf("custom attribute + custom role: got (%q, sso=%v), want (auditor, true)", role, sso)
	}
}

func TestResolveRoleForSSO_DemoteWhenRuleDoesNotMatchAndNoKeep(t *testing.T) {
	rules := []config.SSORoleRule{
		{Attribute: "", Value: "g-adm", Role: "admin"}, // Keep defaults to false
	}
	role, sso := ResolveRoleForSSO(rules, domain.RoleAdmin, "groups", nil, groupsOf("g-other"))
	if role != domain.RoleUser || !sso {
		t.Fatalf("no match + no keep: got (%q, sso=%v), want (user, true)", role, sso)
	}
}

func TestResolveRoleForSSO_KeepPreservesOnMiss(t *testing.T) {
	// Rule with Keep=true protects admins whose group attribute didn't
	// land on the right value this login.
	rules := []config.SSORoleRule{
		{Attribute: "", Value: "g-adm", Role: "admin", Keep: true},
	}
	role, sso := ResolveRoleForSSO(rules, domain.RoleAdmin, "groups", nil, groupsOf("g-other"))
	if role != domain.RoleAdmin || !sso {
		t.Fatalf("no match + keep: got (%q, sso=%v), want (admin, true)", role, sso)
	}
}

func TestResolveRoleForSSO_KeepBlocksDifferentRuleFromOverriding(t *testing.T) {
	// User is currently admin (claimed by a Keep=true admin rule).
	// A different rule fires that would assign operator. Keep wins.
	rules := []config.SSORoleRule{
		{Attribute: "", Value: "g-ops", Role: "operator"},
		{Attribute: "", Value: "g-adm", Role: "admin", Keep: true},
	}
	role, sso := ResolveRoleForSSO(rules, domain.RoleAdmin, "groups", nil, groupsOf("g-ops"))
	if role != domain.RoleAdmin || !sso {
		t.Fatalf("keep blocks override: got (%q, sso=%v), want (admin, true)", role, sso)
	}
}

func TestResolveRoleForSSO_UnclaimedRoleStaysPanelManaged(t *testing.T) {
	// User is operator, but no rule outputs operator at all — operator
	// is panel-managed in this deployment. Even though a rule fires
	// (admin), the absence of any operator-producing rule means we
	// don't demote operator down to user when the IdP didn't match
	// this user's row... wait, in this scenario a rule DID match and
	// outputs admin. Operator -> admin is a legitimate promote.
	rules := []config.SSORoleRule{
		{Attribute: "", Value: "g-adm", Role: "admin"},
	}
	role, sso := ResolveRoleForSSO(rules, domain.RoleOperator, "groups", nil, groupsOf("g-adm"))
	if role != domain.RoleAdmin || !sso {
		t.Fatalf("operator -> admin via rule: got (%q, sso=%v), want (admin, true)", role, sso)
	}
}

func TestResolveRoleForSSO_NoMatchAndCurrentNotClaimed(t *testing.T) {
	// Rules talk about admin only. Current user is operator (custom
	// role the rules don't manage). No rule fired → leave operator
	// alone, sso=false.
	rules := []config.SSORoleRule{
		{Attribute: "", Value: "g-adm", Role: "admin"},
	}
	role, sso := ResolveRoleForSSO(rules, domain.RoleOperator, "groups", nil, groupsOf("g-other"))
	if role != domain.RoleOperator || sso {
		t.Fatalf("unclaimed role on miss: got (%q, sso=%v), want (operator, false)", role, sso)
	}
}

func TestResolveRoleForSSO_MatchedRoleEqualsCurrent(t *testing.T) {
	// Matched rule says admin, user is already admin. ssoAuthoritative
	// is still true (a rule fired) but caller can detect no-op via
	// final == current.
	rules := []config.SSORoleRule{
		{Attribute: "", Value: "g-adm", Role: "admin"},
	}
	role, sso := ResolveRoleForSSO(rules, domain.RoleAdmin, "groups", nil, groupsOf("g-adm"))
	if role != domain.RoleAdmin || !sso {
		t.Fatalf("admin matched admin: got (%q, sso=%v), want (admin, true)", role, sso)
	}
}

func TestMatchFirstRule_NoMatchReturnsEmptyAndFalse(t *testing.T) {
	rules := []config.SSORoleRule{{Attribute: "", Value: "x", Role: "admin"}}
	r, ok := MatchFirstRule(rules, "groups", nil, groupsOf("y"))
	if r != "" || ok {
		t.Fatalf("no match: got (%q, %v), want (\"\", false)", r, ok)
	}
}
