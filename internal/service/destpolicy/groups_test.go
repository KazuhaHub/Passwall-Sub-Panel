package destpolicy

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/KazuhaHub/passwall-protocol/protocol"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// ALLOWLIST GROUPS. A group whose state is report or on compiles, for every
// panel that has members of it on its roster, to a block of rules in phase 4:
// g<gid>x1 (an allow of the lists), g<gid>x2 (an allow of the base and extra
// lists) and g<gid>, the catch-all that denies (on) or records (report) all the
// rest. Only for an agent that declares the groups capability.

// allowGroup writes a group's allowlist: the lists it names, and a base and an
// extra list built from the given text ("" for none).
func (h *harness) allowGroup(gid int64, state domain.DestState, lists []*domain.DestList, base, extra string) *domain.DestGroupMode {
	h.t.Helper()
	m := &domain.DestGroupMode{GroupID: gid, State: state}
	for _, l := range lists {
		m.ListIDs = append(m.ListIDs, l.ID)
	}
	if base != "" {
		m.BaseListID = h.list("g"+itoa(gid)+" base", base).ID
	}
	if extra != "" {
		m.ExtraListID = h.list("g"+itoa(gid)+" extra", extra).ID
	}
	h.setMode(m)
	return m
}

// setMode stores a group's allowlist state in the definitions the compiler reads
// and advances the generation, as a write of the real store does.
func (h *harness) setMode(m *domain.DestGroupMode) {
	h.t.Helper()
	c := *m
	h.defs.modes = slices.DeleteFunc(h.defs.modes, func(x *domain.DestGroupMode) bool { return x.GroupID == m.GroupID })
	h.defs.modes = append(h.defs.modes, &c)
	slices.SortFunc(h.defs.modes, func(a, b *domain.DestGroupMode) int { return int(a.GroupID - b.GroupID) })
	if err := h.repos.SourceRules.Upsert(h.ctx, &domain.DestSourceRule{NodeID: 999, State: domain.DestStateOff}, nil); err != nil {
		h.t.Fatal(err)
	}
}

// modeOf is a copy of a group's current state to edit and hand to setMode.
func (h *harness) modeOf(gid int64) *domain.DestGroupMode {
	for _, m := range h.defs.modes {
		if m.GroupID == gid {
			c := *m
			return &c
		}
	}
	h.t.Fatalf("no allowlist for group %d", gid)
	return nil
}

func ruleByID(p *protocol.DestinationPolicy, id string) *protocol.DestinationRule {
	if p == nil {
		return nil
	}
	for i := range p.Rules {
		if p.Rules[i].ID == id {
			return &p.Rules[i]
		}
	}
	return nil
}

func keys(users ...int64) []protocol.SubjectKey {
	var out []protocol.SubjectKey
	for _, u := range users {
		out = append(out, protocol.NewSubjectKey(u))
	}
	return out
}

func groupFleet() fleet {
	return fleet{panelID: 9, nodes: []*domain.Node{node(1, goodSniffing), node(2, goodSniffing)}, users: []int64{10, 11, 12, 13}}
}

func TestAssemble_GroupRulesAtPhase4(t *testing.T) {
	h := newHarness(t)
	h.setGroups(map[int64]int64{10: 5, 11: 5, 12: 3, 13: 4, 20: 9})
	a := h.list("a", "DOMAIN-SUFFIX,a.example.com")
	b := h.list("b", "DOMAIN-SUFFIX,b.example.com")
	empty := h.list("empty", "")
	allow := h.policy("allow", domain.DestActionAllow, domain.DestStateOn, func(p *domain.DestPolicy) { p.ListIDs = []int64{a.ID} })
	block := smtpPolicy(h, "block")
	watch := h.policy("watch", domain.DestActionBlock, domain.DestStateReport, func(p *domain.DestPolicy) { p.ListIDs = []int64{b.ID} })
	if err := h.repos.SourceRules.Upsert(h.ctx, &domain.DestSourceRule{NodeID: 1, CIDRs: []string{"203.0.113.0/24"}, State: domain.DestStateOn}, nil); err != nil {
		t.Fatal(err)
	}
	// Written in an order that is not the emission order.
	h.allowGroup(5, domain.DestStateOn, []*domain.DestList{a}, "DOMAIN-SUFFIX,doh.example", "DOMAIN-SUFFIX,more.example")
	h.allowGroup(3, domain.DestStateReport, []*domain.DestList{b}, "", "")
	h.allowGroup(4, domain.DestStateOn, []*domain.DestList{empty}, "DOMAIN-SUFFIX,doh.example", "")
	h.allowGroup(8, domain.DestStateOff, []*domain.DestList{a}, "DOMAIN-SUFFIX,doh.example", "")
	// Group 9 is on, but none of its members is on this panel's roster.
	h.allowGroup(9, domain.DestStateOn, []*domain.DestList{a}, "", "")
	h.publishNow()

	res := h.compile(groupFleet(), policyCap, groupsCap)
	want := []string{"s1", idOf(allow), idOf(block), "g3x1", "g3", "g4x2", "g4", "g5x1", "g5x2", "g5", idOf(watch)}
	if got := ruleIDs(res.Policy); !slices.Equal(got, want) {
		t.Fatalf("rule ids = %v, want %v: groups after the blocks, before the report-only rules, by group id", got, want)
	}
	if err := protocol.ValidateDestinationPolicy(res.Policy); err != nil {
		t.Fatalf("the output does not validate: %v", err)
	}
	p := res.Policy
	if r := ruleByID(p, "g5x1"); r.Action != protocol.RuleAllow || !slices.Equal(r.Domains, []string{"domain:a.example.com"}) || !slices.Equal(r.Subjects, keys(10, 11)) || r.CatchAll {
		t.Errorf("g5x1 = %+v", r)
	}
	if r := ruleByID(p, "g5x2"); r.Action != protocol.RuleAllow || !slices.Equal(r.Domains, []string{"domain:doh.example", "domain:more.example"}) || !slices.Equal(r.Subjects, keys(10, 11)) {
		t.Errorf("g5x2 = %+v: the base and the extra list together", r)
	}
	if r := ruleByID(p, "g5"); r.Action != protocol.RuleBlock || !r.CatchAll || !slices.Equal(r.Subjects, keys(10, 11)) || len(r.Domains) != 0 {
		t.Errorf("g5 = %+v: an on group denies the rest", r)
	}
	if r := ruleByID(p, "g3"); r.Action != protocol.RuleObserve || !r.CatchAll || !slices.Equal(r.Subjects, keys(12)) {
		t.Errorf("g3 = %+v: a report group only records the rest", r)
	}
	if ruleByID(p, "g3x2") != nil || ruleByID(p, "g4x1") != nil {
		t.Errorf("an empty x1 or x2 must not be emitted: %v", ruleIDs(p))
	}
	if r := ruleByID(p, "g4"); !slices.Equal(r.Subjects, keys(13)) {
		t.Errorf("g4 subjects = %v", r.Subjects)
	}
	for _, off := range []string{"g8", "g8x1", "g9", "g9x1"} {
		if ruleByID(p, off) != nil {
			t.Errorf("%s: an off group, or one with no member on this panel, has no rules", off)
		}
	}
}

// A group is a set of subjects: another panel's roster gives another set.
func TestAssemble_GroupRulesCarryOnlyThisPanelsMembers(t *testing.T) {
	h := newHarness(t)
	h.setGroups(map[int64]int64{10: 5, 11: 5, 12: 5})
	a := h.list("a", "DOMAIN-SUFFIX,a.example.com")
	h.allowGroup(5, domain.DestStateOn, []*domain.DestList{a}, "", "")
	h.publishNow()
	f := fleet{panelID: 9, nodes: []*domain.Node{node(1, goodSniffing)}, users: []int64{11}}
	res := h.compile(f, policyCap, groupsCap)
	if r := ruleByID(res.Policy, "g5"); r == nil || !slices.Equal(r.Subjects, keys(11)) {
		t.Fatalf("g5 = %+v, want only the roster's member", r)
	}
}

// REPORT NEVER CHANGES A VERDICT. Under the group's report state nothing a
// member could reach before is refused, and a connection refused before is
// refused still: the member allows only admit what would have been admitted,
// and the catch-all only records.
func TestReport_GroupReportNeverChangesAVerdict(t *testing.T) {
	build := func(state domain.DestState) *protocol.DestinationPolicy {
		h := newHarness(t)
		h.setGroups(map[int64]int64{10: 5})
		bad := h.list("bad", "DOMAIN-SUFFIX,bad.example")
		ok := h.list("ok", "DOMAIN-SUFFIX,ok.example")
		listed := h.list("listed", "DOMAIN-SUFFIX,listed.example\nDOMAIN-SUFFIX,bad.example")
		h.policy("block", domain.DestActionBlock, domain.DestStateOn, func(p *domain.DestPolicy) { p.ListIDs = []int64{bad.ID} })
		h.policy("allow", domain.DestActionAllow, domain.DestStateOn, func(p *domain.DestPolicy) { p.ListIDs = []int64{ok.ID} })
		if state != "" {
			// The group's own list names bad.example too: a report group must
			// not turn the block above into an allow.
			h.allowGroup(5, state, []*domain.DestList{listed}, "DOMAIN-SUFFIX,doh.example", "")
		}
		h.publishNow()
		return h.compile(groupFleet(), policyCap, groupsCap).Policy
	}
	without, report, on := build(""), build(domain.DestStateReport), build(domain.DestStateOn)
	if without == nil || report == nil || on == nil {
		t.Fatal("setup")
	}
	member, other := protocol.NewSubjectKey(10), protocol.NewSubjectKey(11)
	targets := []string{"bad.example", "x.bad.example", "ok.example", "listed.example", "doh.example", "elsewhere.example", "a.listed.example"}
	recorded := false
	for _, target := range targets {
		for _, who := range []protocol.SubjectKey{member, other} {
			in := protocol.MatchInput{Subject: who, Target: target, Port: 443, Network: "tcp"}
			before, after := protocol.MatchDestination(without, in), protocol.MatchDestination(report, in)
			if (before.Verdict == protocol.MatchBlock) != (after.Verdict == protocol.MatchBlock) {
				t.Errorf("%s as %s: verdict %s became %s by putting a group in report", target, who, before.Verdict, after.Verdict)
			}
			if who == other && before.Verdict != after.Verdict {
				t.Errorf("%s: a non-member's verdict moved %s -> %s", target, before.Verdict, after.Verdict)
			}
			recorded = recorded || after.Verdict == protocol.MatchObserve
		}
	}
	if !recorded {
		t.Fatal("the report group recorded nothing: the test would pass for a group that does nothing")
	}
	// Sanity: turned on, the same group does refuse what the lists do not name.
	if v := protocol.MatchDestination(on, protocol.MatchInput{Subject: member, Target: "elsewhere.example", Port: 443, Network: "tcp"}).Verdict; v != protocol.MatchBlock {
		t.Fatalf("an on group let elsewhere.example through: %s", v)
	}
	if v := protocol.MatchDestination(on, protocol.MatchInput{Subject: member, Target: "doh.example", Port: 443, Network: "tcp"}).Verdict; v != protocol.MatchAllow {
		t.Fatalf("an on group refused its base allowance: %s", v)
	}
}

// The upper bound that is validated before publishing counts the groups' rules
// and lists: nothing is left for the node to discover.
func TestPublish_BoundCountsGroupLists(t *testing.T) {
	h := newHarness(t)
	for i := 0; i < 254; i++ { // 254 rules, and the group's three make 257
		h.policy("p"+itoa(int64(i)), domain.DestActionBlock, domain.DestStateOn, func(p *domain.DestPolicy) {
			p.Inline = domain.DestPolicyInline{Ports: "26"}
		})
	}
	h.publishNow()
	a := h.list("a", "DOMAIN-SUFFIX,a.example.com")
	h.allowGroup(5, domain.DestStateOn, []*domain.DestList{a}, "DOMAIN-SUFFIX,doh.example", "")
	_, err := h.svc.Publish(h.ctx)
	var refused *PublishRefusedError
	if err == nil || !errorsAs(err, &refused) {
		t.Fatalf("publish = %v, want a refusal: the group's rules are in the bound", err)
	}
	if refused.Detail.Kind != "rules" || refused.Detail.Used != 257 {
		t.Fatalf("detail = %+v", refused.Detail)
	}
	// An off group is not in the snapshot and is not counted.
	m := h.modeOf(5)
	m.State = domain.DestStateOff
	h.setMode(m)
	h.publishNow()
}

// A report group has an observe catch-all, which is what shedding report-only
// rules takes out. It must never take out the catch-all of a group and leave
// the group's allows behind (a policy that does not validate), so a group's
// rules are enforcement and stay.
func TestShed_NeverLeavesGroupMembersWithoutCatchAll(t *testing.T) {
	weak := `{"enabled":true,"metadataOnly":true}`
	t.Run("the report-only policy is shed and the report group stays whole", func(t *testing.T) {
		h := newHarness(t)
		h.setGroups(map[int64]int64{10: 5})
		w := h.list("watch", "DOMAIN-SUFFIX,watched.example")
		h.policy("watch", domain.DestActionBlock, domain.DestStateReport, func(p *domain.DestPolicy) { p.ListIDs = []int64{w.ID} })
		// The group's allow names an address range only: it needs no sniffing.
		cidrs := h.list("net", "IP-CIDR,198.51.100.0/24")
		h.allowGroup(5, domain.DestStateReport, []*domain.DestList{cidrs}, "", "")
		h.publishNow()
		f := fleet{panelID: 9, nodes: []*domain.Node{node(1, weak)}, users: []int64{10}}
		res := h.compile(f, policyCap, groupsCap)
		if got := ruleIDs(res.Policy); !slices.Equal(got, []string{"g5x1", "g5"}) {
			t.Fatalf("rules = %v, want the group's two and not the report-only policy", got)
		}
		if err := protocol.ValidateDestinationPolicy(res.Policy); err != nil {
			t.Fatal(err)
		}
		if !slices.ContainsFunc(res.Issues, func(i domain.NodeAgentIssue) bool { return i.Code == IssueReportOnlyNotDeployed }) {
			t.Fatalf("issues = %+v", res.Issues)
		}
	})
	t.Run("a report group that needs the sniffing the inbound cannot give is not deployed at all", func(t *testing.T) {
		h := newHarness(t)
		h.setGroups(map[int64]int64{10: 5})
		a := h.list("a", "DOMAIN-SUFFIX,a.example.com")
		h.allowGroup(5, domain.DestStateReport, []*domain.DestList{a}, "", "")
		h.publishNow()
		f := fleet{panelID: 9, nodes: []*domain.Node{node(1, weak)}, users: []int64{10}}
		res := h.compile(f, policyCap, groupsCap)
		if res.Policy != nil {
			t.Fatalf("a policy was sent: %v", ruleIDs(res.Policy))
		}
		if !slices.ContainsFunc(res.Issues, func(i domain.NodeAgentIssue) bool { return i.Code == IssueSniffInsufficient }) {
			t.Fatalf("issues = %+v, want the sniffing fallback (the node is not eligible for the group)", res.Issues)
		}
		if m := h.meta(); m.FallbackReason != domain.DestFallbackSniffing {
			t.Fatalf("fallback = %q", m.FallbackReason)
		}
	})
}

// D1. Group rules are sent only to a node that declares the capability: an
// older node would run them with the protocol blocks behind the allows.
func TestCompile_NoGroupRulesWithoutGroupsCapability(t *testing.T) {
	h := newHarness(t)
	h.setGroups(map[int64]int64{10: 5})
	a := h.list("a", "DOMAIN-SUFFIX,a.example.com")
	smtp := smtpPolicy(h, "smtp")
	h.allowGroup(5, domain.DestStateOn, []*domain.DestList{a}, "", "")
	h.publishNow()

	without := h.compile(base1(), policyCap)
	if got := ruleIDs(without.Policy); !slices.Equal(got, []string{idOf(smtp)}) {
		t.Fatalf("without the capability: rules = %v, want only the policy's", got)
	}
	with := h.compile(base1(), policyCap, groupsCap)
	if got := ruleIDs(with.Policy); !slices.Equal(got, []string{idOf(smtp), "g5x1", "g5"}) {
		t.Fatalf("with the capability: rules = %v", got)
	}
	// The capability moving is a new policy at once (the compile is not served
	// from what it answered a moment ago).
	if back := h.compile(base1(), policyCap); back.Digest != without.Digest {
		t.Fatal("losing the capability did not take the group rules back")
	}
}

func TestCompile_AGroupAloneWithoutTheCapabilityIsNoPolicy(t *testing.T) {
	h := newHarness(t)
	h.setGroups(map[int64]int64{10: 5})
	a := h.list("a", "DOMAIN-SUFFIX,a.example.com")
	h.allowGroup(5, domain.DestStateOn, []*domain.DestList{a}, "", "")
	h.publishNow()
	if res := h.compile(base1(), policyCap); res.Policy != nil {
		t.Fatalf("a node without the capability was sent %v", ruleIDs(res.Policy))
	}
	if res := h.compile(base1(), policyCap, groupsCap); res.Policy == nil {
		t.Fatal("a node with it was sent nothing")
	}
}

// The LKG records which groups the node runs: the groups whose catch-all is in
// the policy it applied.
func TestLKG_RecordsAppliedGroups(t *testing.T) {
	h := newHarness(t)
	h.setGroups(map[int64]int64{10: 5, 11: 3})
	a := h.list("a", "DOMAIN-SUFFIX,a.example.com")
	smtpPolicy(h, "smtp")
	h.allowGroup(5, domain.DestStateOn, []*domain.DestList{a}, "", "")
	h.allowGroup(3, domain.DestStateReport, []*domain.DestList{a}, "", "")
	h.publishNow()
	res := h.compile(base1(), policyCap, groupsCap)
	h.observe(applied(res.Digest), policyCap, groupsCap)
	if got := h.meta().AppliedGroups; !slices.Equal(got, []int64{3, 5}) {
		t.Fatalf("applied groups = %v, want [3 5]", got)
	}

	// A policy without a group records none.
	for _, gid := range []int64{5, 3} {
		m := h.modeOf(gid)
		m.State = domain.DestStateOff
		h.setMode(m)
	}
	h.publishNow()
	res = h.compile(base1(), policyCap, groupsCap)
	h.observe(applied(res.Digest), policyCap, groupsCap)
	if got := h.meta().AppliedGroups; len(got) != 0 {
		t.Fatalf("applied groups = %v, want none", got)
	}
}

// ---- the fallback ----

// groupLKG applies a policy with group 5 (on) and group 3 (report) and returns
// the harness, ready for the definitions to move and a new policy to be refused.
func groupLKG(t *testing.T) (*harness, fleet) {
	t.Helper()
	h := newHarness(t)
	h.setGroups(map[int64]int64{10: 5, 11: 5, 12: 3})
	a := h.list("a", "DOMAIN-SUFFIX,a.example.com")
	h.allowGroup(5, domain.DestStateOn, []*domain.DestList{a}, "DOMAIN-SUFFIX,doh.example", "")
	h.allowGroup(3, domain.DestStateReport, []*domain.DestList{a}, "", "")
	h.publishNow()
	f := base1()
	full := h.compile(f, policyCap, groupsCap)
	if got := ruleIDs(full.Policy); !slices.Equal(got, []string{"g3x1", "g3", "g5x1", "g5x2", "g5"}) {
		t.Fatalf("setup rules = %v", got)
	}
	h.observe(applied(full.Digest), policyCap, groupsCap)
	return h, f
}

// refuseNext publishes something new and has the node refuse it, so the next
// compile is the fallback.
func (h *harness) refuseNext(f fleet) {
	h.t.Helper()
	smtpPolicy(h, "new"+itoa(int64(h.state().Generation)))
	h.publishNow()
	next := h.compile(f, policyCap, groupsCap)
	h.observe(rejected(next.Digest), policyCap, groupsCap)
}

func TestFallback_KeepsSameStateGroup(t *testing.T) {
	h, f := groupLKG(t)
	h.refuseNext(f)
	res := h.compile(f, policyCap, groupsCap)
	if got := ruleIDs(res.Policy); !slices.Equal(got, []string{"g3x1", "g3", "g5x1", "g5x2", "g5"}) {
		t.Fatalf("fallback rules = %v, want both groups as they were", got)
	}
	if err := protocol.ValidateDestinationPolicy(res.Policy); err != nil {
		t.Fatal(err)
	}
}

func TestFallback_DropsGroupTurnedOff(t *testing.T) {
	h, f := groupLKG(t)
	m := h.modeOf(5)
	m.State = domain.DestStateOff
	h.setMode(m)
	h.refuseNext(f)
	res := h.compile(f, policyCap, groupsCap)
	if got := ruleIDs(res.Policy); !slices.Equal(got, []string{"g3x1", "g3"}) {
		t.Fatalf("fallback rules = %v: an off group goes, entirely (its allows without its catch-all would be invalid)", got)
	}
	if err := protocol.ValidateDestinationPolicy(res.Policy); err != nil {
		t.Fatal(err)
	}
}

// The wire action of a group's catch-all is its state: block for on, observe
// for report. A group that moved between them is a different rule and is
// dropped, like a policy that changed action (only ever loosening).
func TestFallback_DropsGroupWhoseStateChanged(t *testing.T) {
	h, f := groupLKG(t)
	for gid, to := range map[int64]domain.DestState{5: domain.DestStateReport, 3: domain.DestStateOn} {
		m := h.modeOf(gid)
		m.State = to
		h.setMode(m)
	}
	h.refuseNext(f)
	if res := h.compile(f, policyCap, groupsCap); res.Policy != nil {
		t.Fatalf("fallback rules = %v, want none: both groups changed state", ruleIDs(res.Policy))
	}
}

func TestFallback_TrimsToCurrentMembers(t *testing.T) {
	h, f := groupLKG(t)
	// User 11 leaves group 5 (still on the roster), and user 12, group 3's only
	// member, leaves the roster.
	h.setGroups(map[int64]int64{10: 5, 11: 6, 12: 3})
	f.users = []int64{10, 11}
	h.refuseNext(f)
	res := h.compile(f, policyCap, groupsCap)
	if got := ruleIDs(res.Policy); !slices.Equal(got, []string{"g5x1", "g5x2", "g5"}) {
		t.Fatalf("fallback rules = %v: group 3 has nobody left and goes whole", got)
	}
	for _, r := range res.Policy.Rules {
		if !slices.Equal(r.Subjects, keys(10)) {
			t.Fatalf("%s subjects = %v, want only the current member", r.ID, r.Subjects)
		}
	}
	if err := protocol.ValidateDestinationPolicy(res.Policy); err != nil {
		t.Fatal(err)
	}
}

func TestFallback_DropsGroupRulesForANodeWithoutTheCapability(t *testing.T) {
	h, f := groupLKG(t)
	h.refuseNext(f)
	// Losing the capability is a reason to try the refused policy again; either
	// way no group rule is sent to the node.
	res := h.compile(f, policyCap)
	for _, id := range ruleIDs(res.Policy) {
		if strings.HasPrefix(id, "g") {
			t.Fatalf("rules = %v: a node without the capability got a group rule", ruleIDs(res.Policy))
		}
	}
}

// ---- the per-node limit ----

type fixedMembers map[int64]int

func (f fixedMembers) TagMatchedMembers(_ context.Context, _, gid int64) (int, error) {
	return f[gid], nil
}

// A group's subjects are counted by the members its tags match (whatever the
// roster holds now), times the rules the group has. The roster shrinks when
// members are made ineligible and its count would let the node flip in and
// out of the limit; the tag-matched count does not move with it.
func TestOverLimit_GroupSubjectsCountTagMatchedMembers(t *testing.T) {
	h := newHarness(t)
	h.setGroups(map[int64]int64{10: 5, 11: 5})
	a := h.list("a", "DOMAIN-SUFFIX,a.example.com")
	h.allowGroup(5, domain.DestStateOn, []*domain.DestList{a}, "DOMAIN-SUFFIX,doh.example", "")
	h.publishNow()
	counter := fixedMembers{5: 2}
	h.svc.members = counter
	f := fleet{panelID: 9, nodes: []*domain.Node{node(1, goodSniffing)}, users: []int64{10, 11}}

	if res := h.compile(f, policyCap, groupsCap); res.Policy == nil {
		t.Fatal("setup: two members are far inside the limit")
	}
	// 3 rules x 20000 members is over the 50000 subjects, though the roster has two.
	counter[5] = 20000
	res := h.compile(f, policyCap, groupsCap)
	if res.Policy != nil {
		t.Fatalf("a policy was sent: %v", ruleIDs(res.Policy))
	}
	if len(res.Issues) != 1 || res.Issues[0].Code != IssueOverLimit || !strings.HasPrefix(res.Issues[0].Detail, "subjects over limit") {
		t.Fatalf("issues = %+v", res.Issues)
	}
	// The count moving back is seen (the key knows it).
	counter[5] = 2
	if res := h.compile(f, policyCap, groupsCap); res.Policy == nil {
		t.Fatal("did not recover when the count fell")
	}
}

// ---- the status ----

type fakeGroupNames map[int64]string

func (f fakeGroupNames) GetByID(_ context.Context, id int64) (*domain.Group, error) {
	if n, ok := f[id]; ok {
		return &domain.Group{ID: id, Name: n}, nil
	}
	return nil, domain.ErrNotFound
}

func TestStatus_AllowlistGroupsComeFromTheAppliedPolicy(t *testing.T) {
	ah := newAdminHarness(t)
	ah.admin.Groups = fakeGroupNames{3: "Staff", 5: "Guests"}
	ah.setGroups(map[int64]int64{10: 5, 11: 3})
	a := ah.list("a", "DOMAIN-SUFFIX,a.example.com")
	ah.allowGroup(5, domain.DestStateOn, []*domain.DestList{a}, "", "")
	ah.allowGroup(3, domain.DestStateReport, []*domain.DestList{a}, "", "")
	ah.publishNow()
	res := ah.compile(base1(), policyCap, groupsCap)
	ah.observe(applied(res.Digest), policyCap, groupsCap)

	v, err := ah.admin.Status(ah.ctx)
	if err != nil {
		t.Fatal(err)
	}
	got := nodeOf(t, v, 9).AllowlistGroups
	want := []AllowlistGroupRef{{ID: 3, Name: "Staff"}, {ID: 5, Name: "Guests"}}
	if !slices.Equal(got, want) {
		t.Fatalf("allowlist_groups = %+v, want %+v", got, want)
	}
	// None applied: an empty list, never null.
	if b, _ := jsonMarshal(NodeState{AllowlistGroups: []AllowlistGroupRef{}}); !strings.Contains(string(b), `"allowlist_groups":[]`) {
		t.Fatalf("json = %s", b)
	}
}

func errorsAs(err error, target any) bool { return errors.As(err, target) }

// End to end over the real store: the allowlist is read from the group's row.
func TestCompile_GroupAllowlistComesFromTheGroupRow(t *testing.T) {
	h := newHarness(t)
	h.setGroups(map[int64]int64{10: 5})
	if err := h.db.Exec("INSERT INTO groups_ (id, slug, name, created_at) VALUES (5, 'g5', 'g5', CURRENT_TIMESTAMP)").Error; err != nil {
		t.Fatal(err)
	}
	a := h.list("a", "DOMAIN-SUFFIX,a.example.com")
	al := &domain.DestAllowlist{State: domain.DestStateOn, ListIDs: []int64{a.ID}}
	if err := h.repos.GroupAllowlists.Save(h.ctx, 5, al, nil); err != nil {
		t.Fatal(err)
	}
	h.publishNow()
	res := h.compile(base1(), policyCap, groupsCap)
	if got := ruleIDs(res.Policy); !slices.Equal(got, []string{"g5x1", "g5"}) {
		t.Fatalf("rules = %v", got)
	}
}
