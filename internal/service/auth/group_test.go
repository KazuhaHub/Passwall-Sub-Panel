package auth

import (
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/config"
)

// The group resolver decides which OU a principal lands in, and since
// v3.9.2-beta.10 an OU carries the user's quota and connection caps.
// Every branch here can be wrong silently and in the direction of
// entitlement, so each one gets a test that names the consequence.

func groupRule(value, group string) config.SSOGroupRule {
	return config.SSOGroupRule{Attribute: "", Value: value, Group: group}
}

// The upgrade-safety property, and the reason group sync needs no
// migration flag: a deployment that has not written any group rule has
// a rule set that claims nothing, so no existing user can be re-homed
// by merely upgrading.
//
// This pins the PROPERTY, not any one line that delivers it. Two
// independent paths do: the len(rules)==0 early return, and the
// "current group is unclaimed" carve-out below it, which reaches the
// same answer because an empty rule set can claim nothing. Deleting
// either one on its own leaves this test green — verified by mutation,
// and recorded here so a later reader does not mistake this for a
// guard-specific test and delete the surviving half thinking it is
// covered.
func TestResolveGroupForSSO_NoRulesNeverMoveAnyone(t *testing.T) {
	got, sso := ResolveGroupForSSO(nil, "vip", "default", "groups", nil, groupsOf("anything"))
	if got != "vip" || sso {
		t.Fatalf("no rules: got (%q, sso=%v), want (vip, false)", got, sso)
	}
}

func TestResolveGroupForSSO_FirstMatchWins(t *testing.T) {
	rules := []config.SSOGroupRule{
		groupRule("g-staff", "staff"),
		groupRule("g-vip", "vip"),
	}
	// The principal is in BOTH IdP groups; list order decides, exactly
	// as it does for role rules.
	got, sso := ResolveGroupForSSO(rules, "", "default", "groups", nil, groupsOf("g-vip", "g-staff"))
	if got != "staff" || !sso {
		t.Fatalf("first match: got (%q, sso=%v), want (staff, true)", got, sso)
	}
}

// The branch that closes the entitlement hole. The IdP dropped this
// principal from the group backing a premium OU; if PSP left them in
// it, they would keep that OU's quota and nodes indefinitely.
func TestResolveGroupForSSO_DemotesWhenIdPStopsClaiming(t *testing.T) {
	rules := []config.SSOGroupRule{groupRule("g-vip", "vip")}
	got, sso := ResolveGroupForSSO(rules, "vip", "default", "groups", nil, groupsOf("g-staff"))
	if got != "default" || !sso {
		t.Fatalf("demote: got (%q, sso=%v), want (default, true)", got, sso)
	}
}

// The carve-out that keeps hand-managed OUs out of the IdP's reach. An
// admin put this user in an OU no rule mentions, so the IdP has no
// opinion about it and must not fall them back to the default.
func TestResolveGroupForSSO_UnclaimedGroupIsPanelManaged(t *testing.T) {
	rules := []config.SSOGroupRule{groupRule("g-vip", "vip")}
	got, sso := ResolveGroupForSSO(rules, "hand-placed", "default", "groups", nil, groupsOf("g-staff"))
	if got != "hand-placed" || sso {
		t.Fatalf("unclaimed: got (%q, sso=%v), want (hand-placed, false)", got, sso)
	}
}

// Keep=true makes a claimed OU survive a miss — the escape hatch for
// "this OU is granted by hand even though a rule can also grant it".
func TestResolveGroupForSSO_KeepPreservesOnMiss(t *testing.T) {
	rules := []config.SSOGroupRule{{Attribute: "", Value: "g-vip", Group: "vip", Keep: true}}
	got, sso := ResolveGroupForSSO(rules, "vip", "default", "groups", nil, groupsOf("g-staff"))
	if got != "vip" || !sso {
		t.Fatalf("keep on miss: got (%q, sso=%v), want (vip, true)", got, sso)
	}
}

// Keep also wins when a DIFFERENT rule matched: the hand-granted OU
// outranks the one the IdP would assign, mirroring ResolveRoleForSSO.
func TestResolveGroupForSSO_KeepBeatsAMatchedRule(t *testing.T) {
	rules := []config.SSOGroupRule{
		groupRule("g-staff", "staff"),
		{Attribute: "", Value: "g-vip", Group: "vip", Keep: true},
	}
	got, sso := ResolveGroupForSSO(rules, "vip", "default", "groups", nil, groupsOf("g-staff"))
	if got != "vip" || !sso {
		t.Fatalf("keep beats match: got (%q, sso=%v), want (vip, true)", got, sso)
	}
}

// Already in the right place: authoritative, but nothing changes.
func TestResolveGroupForSSO_MatchEqualsCurrent(t *testing.T) {
	rules := []config.SSOGroupRule{groupRule("g-vip", "vip")}
	got, sso := ResolveGroupForSSO(rules, "vip", "default", "groups", nil, groupsOf("g-vip"))
	if got != "vip" || !sso {
		t.Fatalf("no-op match: got (%q, sso=%v), want (vip, true)", got, sso)
	}
}

// A blank Value must never match, for the same reason it must not on a
// role rule: an empty string legitimately appears in a groups claim, so
// a half-typed rule would capture every such login into its OU.
func TestResolveGroupForSSO_EmptyValueNeverMatches(t *testing.T) {
	rules := []config.SSOGroupRule{groupRule("", "vip")}
	got, sso := ResolveGroupForSSO(rules, "", "default", "groups", nil, groupsOf(""))
	if got != "" || sso {
		t.Fatalf("blank value: got (%q, sso=%v), want (\"\", false)", got, sso)
	}
}

// A rule with no Group yet is an admin mid-edit. It must be skipped
// rather than match, otherwise an incomplete row at the top of the list
// silently disables every rule below it.
func TestMatchFirstGroupRule_BlankGroupIsSkippedNotMatched(t *testing.T) {
	rules := []config.SSOGroupRule{
		groupRule("g-vip", "   "),
		groupRule("g-vip", "vip"),
	}
	got, ok := MatchFirstGroupRule(rules, "groups", nil, groupsOf("g-vip"))
	if got != "vip" || !ok {
		t.Fatalf("blank group: got (%q, ok=%v), want (vip, true)", got, ok)
	}
}

// Group rules must read attributes through exactly the same lookup the
// role rules use, including the non-groups attribute path. If the two
// matchers ever diverge, a rule that fires for roles but not for groups
// would be a silent misconfiguration.
func TestMatchFirstGroupRule_NamedAttributeSharesRoleLookup(t *testing.T) {
	a := attrs(map[string][]string{"department": {"finance"}})
	rules := []config.SSOGroupRule{{Attribute: "department", Value: "finance", Group: "fin"}}
	got, ok := MatchFirstGroupRule(rules, "groups", a, nil)
	if got != "fin" || !ok {
		t.Fatalf("named attribute: got (%q, ok=%v), want (fin, true)", got, ok)
	}

	// Same input, same expectation, through the role matcher — the
	// assertion that the two kinds agree rather than merely both work.
	roleRules := []config.SSORoleRule{{Attribute: "department", Value: "finance", Role: "operator"}}
	if _, matched := MatchFirstRule(roleRules, "groups", a, nil); !matched {
		t.Fatal("role matcher did not agree with the group matcher on the same input")
	}
}

// A user in no group at all must not be treated as "claimed" by a rule
// whose Group is blank — that would make every group-less principal
// look IdP-managed and fall them into the default group.
func TestResolveGroupForSSO_NoCurrentGroupIsNotClaimedByBlankRule(t *testing.T) {
	rules := []config.SSOGroupRule{{Attribute: "", Value: "g-x", Group: ""}}
	got, sso := ResolveGroupForSSO(rules, "", "default", "groups", nil, groupsOf("g-other"))
	if got != "" || sso {
		t.Fatalf("group-less: got (%q, sso=%v), want (\"\", false)", got, sso)
	}
}
