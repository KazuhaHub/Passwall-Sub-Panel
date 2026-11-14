package destpolicy

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// The pieces of the allowlist-group admin API that live in the compiler: the
// per-node limit judged the same way on every path, the status field that says
// a group was dropped for weak sniffing, and the policy budget counting the
// subjects the allowlist groups carry.

// A node that is over the limit only because of the members its tags select
// must not be told it is the sniffing restriction that did it: the unrestricted
// policy was over as well, with the same slack.
func TestOverLimit_UnrestrictedCountsGroupSlack(t *testing.T) {
	h := newHarness(t)
	h.setGroups(map[int64]int64{10: 5})
	addrs := h.list("addrs", "IP-CIDR,198.51.100.0/24")
	doms := h.list("doms", "DOMAIN-SUFFIX,a.example.com")
	// A domain block needs sniffing: on the weak inbound it is restricted, which
	// is what makes the policy "changed" by restriction.
	h.policy("doms", domain.DestActionBlock, domain.DestStateOn, func(p *domain.DestPolicy) { p.ListIDs = []int64{doms.ID} })
	h.allowGroup(5, domain.DestStateOn, []*domain.DestList{addrs}, "IP-CIDR,203.0.113.0/24", "")
	h.publishNow()
	h.svc.members = fixedMembers{5: 20000} // 3 rules x 20000 members: over the 50000
	f := fleet{panelID: 9, nodes: []*domain.Node{node(1, goodSniffing), node(2, weakSniffing)}, users: []int64{10}}

	res := h.compile(f, policyCap, groupsCap)
	if res.Policy != nil {
		t.Fatalf("a policy was sent: %v", ruleIDs(res.Policy))
	}
	var detail string
	for _, is := range res.Issues {
		if is.Code == IssueOverLimit {
			detail = is.Detail
		}
	}
	if detail != "subjects over limit" {
		t.Fatalf("over-limit detail = %q: the unrestricted policy was over the limit too, so restricting is not the cause", detail)
	}
}

// The fallback is judged against the limit with the same slack as the policy it
// stands in for: a group whose tag-matched members overrun the node's budget
// does not get its last policy back just because the roster is small.
func TestFallback_RestrictedFallbackCountsGroupSlack(t *testing.T) {
	h := newHarness(t)
	h.setGroups(map[int64]int64{10: 5, 11: 5})
	a := h.list("a", "DOMAIN-SUFFIX,a.example.com")
	h.allowGroup(5, domain.DestStateOn, []*domain.DestList{a}, "DOMAIN-SUFFIX,doh.example", "")
	h.publishNow()
	members := fixedMembers{5: 2}
	h.svc.members = members
	f := fleet{panelID: 9, nodes: []*domain.Node{node(1, goodSniffing)}, users: []int64{10, 11}}
	full := h.compile(f, policyCap, groupsCap)
	if got := ruleIDs(full.Policy); !slices.Equal(got, []string{"g5x1", "g5x2", "g5"}) {
		t.Fatalf("setup rules = %v", got)
	}
	h.observe(applied(full.Digest), policyCap, groupsCap)

	members[5] = 20000
	res := h.compile(f, policyCap, groupsCap)
	if res.Policy != nil {
		t.Fatalf("fallback rules = %v: the tag-matched members are over the limit, with or without the restriction", ruleIDs(res.Policy))
	}
	if m := h.meta(); m.FallbackReason != domain.DestFallbackOverLimit {
		t.Fatalf("fallback = %q, want over_limit", m.FallbackReason)
	}
}

// ---- sniffing_groups_dropped ----

func TestStatus_SniffingGroupsDroppedNamesTheGroupsDroppedOnThisNode(t *testing.T) {
	ah := newAdminHarness(t)
	ah.admin.Groups = fakeGroupNames{5: "Guests", 6: "Staff"}
	ah.setGroups(map[int64]int64{10: 5, 11: 6})
	doms := ah.list("doms", "DOMAIN-SUFFIX,a.example.com")
	addrs := ah.list("addrs", "IP-CIDR,198.51.100.0/24")
	ah.allowGroup(5, domain.DestStateOn, []*domain.DestList{doms}, "", "")
	ah.allowGroup(6, domain.DestStateOn, []*domain.DestList{addrs}, "", "")
	ah.publishNow()

	weak := fleet{panelID: 9, nodes: []*domain.Node{node(1, goodSniffing), node(2, weakSniffing)}, users: []int64{10, 11}}
	res := ah.compile(weak, policyCap, groupsCap)
	ah.observe(applied(res.Digest), policyCap, groupsCap)
	v, err := ah.admin.Status(ah.ctx)
	if err != nil {
		t.Fatal(err)
	}
	got := nodeOf(t, v, 9).SniffingGroupsDropped
	if want := []AllowlistGroupRef{{ID: 5, Name: "Guests"}}; !slices.Equal(got, want) {
		t.Fatalf("sniffing_groups_dropped = %+v, want %+v: only the group that needs sniffing is dropped", got, want)
	}

	// Fixing the inbound clears it.
	good := fleet{panelID: 9, nodes: []*domain.Node{node(1, goodSniffing), node(2, goodSniffing)}, users: []int64{10, 11}}
	res = ah.compile(good, policyCap, groupsCap)
	ah.observe(applied(res.Digest), policyCap, groupsCap)
	if v, err = ah.admin.Status(ah.ctx); err != nil {
		t.Fatal(err)
	}
	if got := nodeOf(t, v, 9).SniffingGroupsDropped; len(got) != 0 {
		t.Fatalf("sniffing_groups_dropped = %+v after the sniffing was fixed", got)
	}
	// Never null.
	if b, _ := jsonMarshal(NodeState{SniffingGroupsDropped: []AllowlistGroupRef{}}); !strings.Contains(string(b), `"sniffing_groups_dropped":[]`) {
		t.Fatalf("json = %s", b)
	}
}

// With every rule dropped the node is sent nothing, and the status still says
// which group is not in force and why.
func TestStatus_SniffingGroupsDroppedSurvivesAnEmptyPolicy(t *testing.T) {
	ah := newAdminHarness(t)
	ah.admin.Groups = fakeGroupNames{5: "Guests"}
	ah.setGroups(map[int64]int64{10: 5})
	doms := ah.list("doms", "DOMAIN-SUFFIX,a.example.com")
	ah.allowGroup(5, domain.DestStateOn, []*domain.DestList{doms}, "", "")
	ah.publishNow()

	weak := fleet{panelID: 9, nodes: []*domain.Node{node(1, weakSniffing)}, users: []int64{10}}
	if res := ah.compile(weak, policyCap, groupsCap); res.Policy != nil {
		t.Fatalf("a policy was sent: %v", ruleIDs(res.Policy))
	}
	v, err := ah.admin.Status(ah.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got := nodeOf(t, v, 9).SniffingGroupsDropped; !slices.Equal(got, []AllowlistGroupRef{{ID: 5, Name: "Guests"}}) {
		t.Fatalf("sniffing_groups_dropped = %+v", got)
	}
}

// ---- the budget ----

// The policy budget counts what the allowlist groups put on a node: each
// member once per rule the group has, and the group's lists.
func TestBudget_CountsAllowlistGroupSubjectsAndLists(t *testing.T) {
	ah := newAdminHarness(t)
	ah.admin.Counter = fakeCounter{5: 3, 6: 1}
	doms := ah.list("doms", "DOMAIN-SUFFIX,a.example.com\nDOMAIN-SUFFIX,b.example.com")
	ah.allowGroup(5, domain.DestStateOn, []*domain.DestList{doms}, "DOMAIN-SUFFIX,doh.example", "") // x1, x2 and the catch-all
	ah.allowGroup(6, domain.DestStateOff, []*domain.DestList{doms}, "", "")                         // off: nothing
	ah.publishNow()

	b, err := ah.admin.Budget(ah.ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if b.Subjects.Used != 3*3 {
		t.Fatalf("subjects = %d, want 9: three members x three rules", b.Subjects.Used)
	}
	if b.Domains.Used != 3 || b.Rules.Used != 3 {
		t.Fatalf("domains = %d rules = %d, want the group's lists counted (3 domains, 3 rules)", b.Domains.Used, b.Rules.Used)
	}
}

func TestBudget_CheckGroupRefusesAGroupThatWouldOverrunTheSubjects(t *testing.T) {
	ah := newAdminHarness(t)
	counter := fakeCounter{5: 17000}
	ah.admin.Counter = counter
	doms := ah.list("doms", "DOMAIN-SUFFIX,a.example.com")
	base := ah.list("base", "DOMAIN-SUFFIX,doh.example")
	m := &domain.DestGroupMode{GroupID: 5, State: domain.DestStateOn, ListIDs: []int64{doms.ID}, BaseListID: base.ID}
	err := ah.admin.CheckGroupBudget(ah.ctx, GroupHypo{Mode: m})
	v := validationCode(t, err)
	if v.Code != CodePolicyOverLimit || v.Kind != "subjects" {
		t.Fatalf("err = %+v, want a subjects over-limit: 17000 members x 3 rules is over 50000", v)
	}
	// The same group at 16000 members fits.
	counter[5] = 16000
	if err := ah.admin.CheckGroupBudget(ah.ctx, GroupHypo{Mode: m}); err != nil {
		t.Fatal(err)
	}
	// A group that is turned off carries nothing.
	counter[5] = 17000
	off := *m
	off.State = domain.DestStateOff
	if err := ah.admin.CheckGroupBudget(ah.ctx, GroupHypo{Mode: &off}); err != nil {
		t.Fatalf("an off group was refused: %v", err)
	}
}

// A new group's lists do not exist yet when the save is judged: they come with
// the hypothesis, under ids the store has not given them.
func TestBudget_GroupHypothesisCountsTheListsAGroupBringsAlong(t *testing.T) {
	ah := newAdminHarness(t)
	ah.admin.Counter = fakeCounter{5: 2}
	base := &domain.DestList{
		ID: -1, Name: "base", Kind: domain.DestListCustom, Side: domain.DestSideDest,
		Entries: []byte("domain:doh.example\ndomain:dns.example"), EntryCount: 2,
	}
	m := &domain.DestGroupMode{GroupID: 5, State: domain.DestStateOn, BaseListID: -1}
	b, err := ah.admin.BudgetWithGroup(ah.ctx, GroupHypo{Mode: m, Lists: []*domain.DestList{base}})
	if err != nil {
		t.Fatal(err)
	}
	if b.Domains.Used != 2 || b.Rules.Used != 2 || b.Subjects.Used != 4 {
		t.Fatalf("budget = %+v, want 2 domains, 2 rules (x2 and the catch-all) and 4 subjects", b)
	}
	// Nothing was stored.
	stored, err := ah.admin.Budget(ah.ctx, nil)
	if err != nil || stored.Rules.Used != 0 {
		t.Fatalf("stored budget = %+v, %v: the hypothesis must not be written", stored, err)
	}
}

type fakeCounter map[int64]int

func (f fakeCounter) CountUsersByGroup(context.Context) (map[int64]int, error) {
	out := make(map[int64]int, len(f))
	for k, v := range f {
		out[k] = v
	}
	return out, nil
}

// ---- the test tool ----

// A step that a group's rule decided is named after the group, so the trace
// reads "denied by Guests" and not "g5".
func TestTest_AGroupStepIsNamedAfterTheGroup(t *testing.T) {
	ah := newAdminHarness(t)
	ah.admin.Groups = fakeGroupNames{5: "Guests"}
	ah.setGroups(map[int64]int64{10: 5})
	doms := ah.list("doms", "DOMAIN-SUFFIX,a.example.com")
	ah.allowGroup(5, domain.DestStateOn, []*domain.DestList{doms}, "DOMAIN-SUFFIX,doh.example", "")
	ah.publishNow()

	res, err := ah.admin.Test(ah.ctx, TestInput{Target: "elsewhere.example", UserID: 10, PanelID: 9})
	if err != nil {
		t.Fatal(err)
	}
	if res.Verdict != "block" || res.TerminatingStep != "g5" {
		t.Fatalf("verdict %q by %q, want block by g5: %+v", res.Verdict, res.TerminatingStep, res.Steps)
	}
	var got []TestStep
	for _, s := range res.Steps {
		if s.Kind == "group" {
			got = append(got, s)
		}
	}
	if len(got) != 3 {
		t.Fatalf("steps = %+v, want the three group steps", res.Steps)
	}
	for _, s := range got {
		if s.GroupID != 5 || s.Name != "Guests" || s.ReportOnly {
			t.Fatalf("step = %+v, want group 5 named Guests, not report-only", s)
		}
	}
	// A report group's steps say so.
	m := ah.modeOf(5)
	m.State = domain.DestStateReport
	ah.setMode(m)
	ah.publishNow()
	res, _ = ah.admin.Test(ah.ctx, TestInput{Target: "elsewhere.example", UserID: 10, PanelID: 9})
	if res.Verdict != "observe" || res.TerminatingStep != "g5" {
		t.Fatalf("report: verdict %q by %q", res.Verdict, res.TerminatingStep)
	}
	for _, s := range res.Steps {
		if s.Kind == "group" && !s.ReportOnly {
			t.Fatalf("a report group's step is not marked report-only: %+v", s)
		}
	}
}
