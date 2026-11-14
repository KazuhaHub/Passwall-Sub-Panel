package destpolicy

import (
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/KazuhaHub/passwall-protocol/protocol"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// REPORT-ONLY. A policy in the report state compiles to the wire action
// "observe" in phase 5, behind every enforced rule, and the one property that
// matters is that adding or removing it never changes a verdict: the first
// matching rule terminates, so a report rule placed in the enforcing phases
// would shadow an enforced block behind it and quietly loosen the policy.

func idOf(p *domain.DestPolicy) string { return "p" + strconv.FormatInt(p.ID, 10) }

func TestReport_BlockPolicyCompilesToObserveInPhaseFive(t *testing.T) {
	h := newHarness(t)
	l := h.list("watch", "DOMAIN-SUFFIX,watched.example")
	enf := h.list("enforced", "DOMAIN-SUFFIX,enforced.example")
	allowL := h.list("ok", "DOMAIN-SUFFIX,fine.example")

	watch := h.policy("watch", domain.DestActionBlock, domain.DestStateReport, func(p *domain.DestPolicy) { p.ListIDs = []int64{l.ID} })
	block := h.policy("block", domain.DestActionBlock, domain.DestStateOn, func(p *domain.DestPolicy) { p.ListIDs = []int64{enf.ID} })
	allow := h.policy("allow", domain.DestActionAllow, domain.DestStateOn, func(p *domain.DestPolicy) { p.ListIDs = []int64{allowL.ID} })
	h.publishNow()

	res := h.compile(base1())
	if res.Policy == nil {
		t.Fatal("no policy")
	}
	if got, want := joinIDs(ruleIDs(res.Policy)), joinIDs([]string{idOf(allow), idOf(block), idOf(watch)}); got != want {
		t.Fatalf("rule ids = %s, want %s: the report rule goes last, whatever its priority", got, want)
	}
	last := res.Policy.Rules[len(res.Policy.Rules)-1]
	if last.Action != protocol.RuleObserve || !slices.Equal(last.Domains, []string{"domain:watched.example"}) {
		t.Fatalf("report rule = %+v", last)
	}
	if err := protocol.ValidateDestinationPolicy(res.Policy); err != nil {
		t.Fatalf("a policy with an observe rule must validate: %v", err)
	}
}

// A report-state source rule is an s* observe rule, after the enforcing
// phases, and an enforced source rule on another inbound stays in phase 0.
func TestReport_SourceRuleCompilesToObserveAndNeverSharesARuleWithAnEnforcedOne(t *testing.T) {
	h := newHarness(t)
	h.mmdb(map[string]string{"1.0.0.0/24": "CN"})
	// Same countries on both inbounds, but one is enforced and one report-only:
	// they must not merge into one rule, or one of the two states would be a lie.
	if err := h.repos.SourceRules.Upsert(h.ctx, &domain.DestSourceRule{NodeID: 1, Countries: []string{"CN"}, State: domain.DestStateOn}, nil); err != nil {
		t.Fatal(err)
	}
	if err := h.repos.SourceRules.Upsert(h.ctx, &domain.DestSourceRule{NodeID: 2, Countries: []string{"CN"}, State: domain.DestStateReport}, nil); err != nil {
		t.Fatal(err)
	}
	smtp := h.policy("smtp", domain.DestActionBlock, domain.DestStateOn, func(p *domain.DestPolicy) { p.Inline = domain.DestPolicyInline{Ports: "25"} })
	h.publishNow()

	res := h.compile(base1())
	if got, want := joinIDs(ruleIDs(res.Policy)), joinIDs([]string{"s1", idOf(smtp), "s2"}); got != want {
		t.Fatalf("rule ids = %s, want %s", got, want)
	}
	enforced, watch := res.Policy.Rules[0], res.Policy.Rules[2]
	if enforced.Action != protocol.RuleBlock || !slices.Equal(enforced.Inbounds, []protocol.ListenerKey{"lst_1"}) {
		t.Fatalf("enforced source rule = %+v", enforced)
	}
	if watch.Action != protocol.RuleObserve || !slices.Equal(watch.Inbounds, []protocol.ListenerKey{"lst_2"}) || !slices.Equal(watch.SourceCIDRs, []string{"1.0.0.0/24"}) {
		t.Fatalf("report source rule = %+v", watch)
	}
	if err := protocol.ValidateDestinationPolicy(res.Policy); err != nil {
		t.Fatal(err)
	}
}

// THE PROPERTY. For every connection, the verdict with a report-only policy
// in place is the verdict without it, or "observe" where there was none.
func TestReport_NeverChangesAVerdict(t *testing.T) {
	build := func(withWatch bool) *protocol.DestinationPolicy {
		h := newHarness(t)
		enf := h.list("enforced", "DOMAIN-SUFFIX,both.example\nDOMAIN-SUFFIX,enforced.example")
		allowL := h.list("ok", "DOMAIN-SUFFIX,fine.example")
		h.policy("block", domain.DestActionBlock, domain.DestStateOn, func(p *domain.DestPolicy) { p.ListIDs = []int64{enf.ID} })
		h.policy("allow", domain.DestActionAllow, domain.DestStateOn, func(p *domain.DestPolicy) { p.ListIDs = []int64{allowL.ID} })
		if withWatch {
			w := h.list("watch", "DOMAIN-SUFFIX,both.example\nDOMAIN-SUFFIX,watched.example\nDOMAIN-SUFFIX,fine.example")
			// It is created FIRST in priority terms on purpose: it must still
			// compile behind the enforced block.
			h.policy("watch", domain.DestActionBlock, domain.DestStateReport, func(p *domain.DestPolicy) { p.ListIDs = []int64{w.ID} })
		}
		h.publishNow()
		return h.compile(base1()).Policy
	}
	without, with := build(false), build(true)
	if without == nil || with == nil {
		t.Fatal("setup")
	}
	changed := false
	for _, target := range []string{"enforced.example", "both.example", "fine.example", "watched.example", "other.example", "a.watched.example", "sub.fine.example"} {
		in := protocol.MatchInput{Subject: protocol.NewSubjectKey(10), Target: target, Port: 443, Network: "tcp"}
		before, after := protocol.MatchDestination(without, in), protocol.MatchDestination(with, in)
		switch {
		case before.Verdict == after.Verdict:
		case before.Verdict == protocol.MatchDefault && after.Verdict == protocol.MatchObserve:
			changed = true
		default:
			t.Errorf("%s: verdict %s became %s by adding a report-only policy", target, before.Verdict, after.Verdict)
		}
	}
	if !changed {
		t.Fatal("the report-only policy recorded nothing: the test would pass for a policy that does nothing")
	}
}

func TestReport_SplitsLikeAnEnforcedPolicy(t *testing.T) {
	h := newHarness(t)
	l := h.list("l", "DOMAIN-SUFFIX,x.example")
	p := h.policy("bt", domain.DestActionBlock, domain.DestStateReport, func(p *domain.DestPolicy) {
		p.ListIDs = []int64{l.ID}
		p.Inline = domain.DestPolicyInline{Protocols: []string{"bittorrent"}}
	})
	h.publishNow()
	res := h.compile(base1())
	if got, want := joinIDs(ruleIDs(res.Policy)), joinIDs([]string{idOf(p) + "x1", idOf(p) + "x2"}); got != want {
		t.Fatalf("ids = %s, want %s", got, want)
	}
	for _, r := range res.Policy.Rules {
		if r.Action != protocol.RuleObserve {
			t.Fatalf("rule %s action = %s", r.ID, r.Action)
		}
	}
	if err := protocol.ValidateDestinationPolicy(res.Policy); err != nil {
		t.Fatal(err)
	}
}

// A report-only policy with domains is prechecked for sniffing like an enforced
// one, but what it finds only sheds the observe rule: report-only never pushes a
// node into the fallback (see the shedding tests below).
func TestReport_DomainRulesNeedSniffingToo(t *testing.T) {
	h := newHarness(t)
	l := h.list("l", "DOMAIN-SUFFIX,x.example")
	h.policy("w", domain.DestActionBlock, domain.DestStateReport, func(p *domain.DestPolicy) { p.ListIDs = []int64{l.ID} })
	h.publishNow()
	f := fleet{panelID: 9, nodes: []*domain.Node{node(1, `{"enabled":true,"metadataOnly":true}`)}, users: []int64{10}}
	res := h.compile(f)
	if res.Policy != nil || !hasIssue(res, IssueReportOnlyNotDeployed, "sniffing") {
		t.Fatalf("a node whose sniffing cannot supply domains was sent the report rule: %+v / %+v", res.Policy, res.Issues)
	}
}

// A policy that only reports still counts as "in force": a node is sent it, and
// a snapshot of nothing but report-only rules is not empty.
func TestReport_AloneIsAPolicy(t *testing.T) {
	h := newHarness(t)
	h.policy("w", domain.DestActionBlock, domain.DestStateReport, func(p *domain.DestPolicy) { p.Inline = domain.DestPolicyInline{Ports: "25"} })
	h.publishNow()
	if res := h.compile(base1()); res.Policy == nil || len(res.Policy.Rules) != 1 {
		t.Fatalf("policy = %+v", res.Policy)
	}
}

// Switching a policy between on and report is a different rule: the fallback
// must not carry the old action across.
func TestReport_TheFallbackDropsARuleWhoseStateChanged(t *testing.T) {
	h := newHarness(t)
	l := h.list("l", "DOMAIN-SUFFIX,a.example.com")
	pol := h.policy("A", domain.DestActionBlock, domain.DestStateOn, func(p *domain.DestPolicy) { p.ListIDs = []int64{l.ID} })
	other := h.policy("B", domain.DestActionBlock, domain.DestStateOn, func(p *domain.DestPolicy) { p.Inline = domain.DestPolicyInline{Ports: "25"} })
	h.publishNow()
	res := h.compile(base1())
	h.observe(applied(res.Digest))

	// A goes to report-only, and the node then refuses the new policy.
	pol.State = domain.DestStateReport
	if err := h.repos.Policies.Update(h.ctx, pol, nil); err != nil {
		t.Fatal(err)
	}
	h.publishNow()
	res2 := h.compile(base1())
	h.observe(rejected(res2.Digest))
	got := h.compile(base1())
	if got.Policy == nil || joinIDs(ruleIDs(got.Policy)) != idOf(other) {
		t.Fatalf("fallback rules = %v, want only %s: A's enforcing rule is no longer what A says", ruleIDs(got.Policy), idOf(other))
	}
}

// REPORT-ONLY MUST NOT DISTURB ENFORCEMENT. A report-only (observe) rule never
// changes a verdict, so it must not change one indirectly either: an inbound
// whose sniffing cannot supply what only a report-only rule needs, or a
// per-node budget only a report-only rule overruns, drops those observe rules
// from the minted policy and keeps the enforced ones. It does not push the whole
// node into the fallback.

func hasIssue(res Result, code, detailPart string) bool {
	for _, i := range res.Issues {
		if i.Code == code && strings.Contains(i.Detail, detailPart) {
			return true
		}
	}
	return false
}

func TestReport_InsufficientSniffingForAnObserveRuleOnlyDropsThatRule(t *testing.T) {
	h := newHarness(t)
	l := h.list("watch", "DOMAIN-SUFFIX,watched.example")
	enf := h.policy("smtp", domain.DestActionBlock, domain.DestStateOn, func(p *domain.DestPolicy) { p.Inline = domain.DestPolicyInline{Ports: "25"} })
	h.policy("watch", domain.DestActionBlock, domain.DestStateReport, func(p *domain.DestPolicy) { p.ListIDs = []int64{l.ID} })
	h.publishNow()

	f := fleet{panelID: 9, nodes: []*domain.Node{node(1, `{"enabled":true,"metadataOnly":true}`)}, users: []int64{10}}
	res := h.compile(f)
	if res.Policy == nil || joinIDs(ruleIDs(res.Policy)) != idOf(enf) {
		t.Fatalf("rules = %v, want only the enforced %s", ruleIDs(res.Policy), idOf(enf))
	}
	if err := protocol.ValidateDestinationPolicy(res.Policy); err != nil {
		t.Fatal(err)
	}
	if !hasIssue(res, IssueReportOnlyNotDeployed, "sniffing") {
		t.Fatalf("issues = %+v, want a report-only note naming sniffing", res.Issues)
	}
	if m := h.meta(); m.FallbackReason != domain.DestFallbackNone || m.PrecheckListeners != "" {
		t.Fatalf("report-only pushed the node into a fallback: %+v", m)
	}

	// Sniffing fixed: the observe rule comes back, the note goes.
	good := h.compile(base1())
	if len(ruleIDs(good.Policy)) != 2 || hasIssue(good, IssueReportOnlyNotDeployed, "") {
		t.Fatalf("rules = %v issues = %+v", ruleIDs(good.Policy), good.Issues)
	}
}

// An enforced domain rule is not held back by inbounds that cannot sniff
// either: with no inbound able to run it the rule is dropped (and the inbound
// said so), and the node is not in a fallback.
func TestReport_AnEnforcedDomainRuleIsDroppedWhereNoInboundCanSniff(t *testing.T) {
	h := newHarness(t)
	l := h.list("l", "DOMAIN-SUFFIX,x.example")
	h.policy("block", domain.DestActionBlock, domain.DestStateOn, func(p *domain.DestPolicy) { p.ListIDs = []int64{l.ID} })
	h.policy("watch", domain.DestActionBlock, domain.DestStateReport, func(p *domain.DestPolicy) { p.ListIDs = []int64{l.ID} })
	h.publishNow()
	f := fleet{panelID: 9, nodes: []*domain.Node{node(1, `{"enabled":true,"metadataOnly":true}`)}, users: []int64{10}}
	res := h.compile(f)
	if res.Policy != nil {
		t.Fatalf("sent %v", ruleIDs(res.Policy))
	}
	if m := h.meta(); m.FallbackReason != domain.DestFallbackNone || !strings.Contains(m.PrecheckListeners, `"lst_1"`) {
		t.Fatalf("state = %+v", m)
	}
	if !hasSniffIssue(res, "lst_1") || !hasIssue(res, IssueReportOnlyNotDeployed, "sniffing") {
		t.Fatalf("issues = %+v", res.Issues)
	}
}

// Nothing but an observe rule that cannot be deployed: no policy, and no
// fallback state either (nothing enforced is at stake).
func TestReport_OnlyObserveRulesThatCannotBeDeployedIsNoPolicy(t *testing.T) {
	h := newHarness(t)
	l := h.list("watch", "DOMAIN-SUFFIX,watched.example")
	h.policy("watch", domain.DestActionBlock, domain.DestStateReport, func(p *domain.DestPolicy) { p.ListIDs = []int64{l.ID} })
	h.publishNow()
	f := fleet{panelID: 9, nodes: []*domain.Node{node(1, `{"enabled":true,"metadataOnly":true}`)}, users: []int64{10}}
	res := h.compile(f)
	if res.Policy != nil || !hasIssue(res, IssueReportOnlyNotDeployed, "sniffing") {
		t.Fatalf("policy = %+v issues = %+v", res.Policy, res.Issues)
	}
	if m, err := h.repos.Agents.Get(h.ctx, "agt_9"); err == nil && m.FallbackReason != domain.DestFallbackNone {
		t.Fatalf("fallback = %q", m.FallbackReason)
	}
}

func TestReport_AnObserveRuleOverTheBudgetOnlyDropsThatRule(t *testing.T) {
	h := newHarness(t)
	l := h.list("l", "DOMAIN-SUFFIX,x.example.com")
	enf := h.policy("smtp", domain.DestActionBlock, domain.DestStateOn, func(p *domain.DestPolicy) { p.Inline = domain.DestPolicyInline{Ports: "25"} })
	h.policy("grp", domain.DestActionBlock, domain.DestStateReport, func(p *domain.DestPolicy) {
		p.ListIDs, p.Scope, p.GroupIDs = []int64{l.ID}, domain.DestScopeGroups, []int64{5}
	})
	h.publishNow()
	h.defs.Definitions = bigGroup{Definitions: h.repos, group: 5}

	users := make([]int64, 0, protocol.MaxPolicySubjects+1)
	for i := int64(1); i <= protocol.MaxPolicySubjects+1; i++ {
		users = append(users, i)
	}
	f := fleet{panelID: 9, nodes: []*domain.Node{node(1, goodSniffing)}, users: users}
	res := h.compile(f)
	if res.Policy == nil || joinIDs(ruleIDs(res.Policy)) != idOf(enf) {
		t.Fatalf("rules = %v, want only the enforced %s", ruleIDs(res.Policy), idOf(enf))
	}
	if !hasIssue(res, IssueReportOnlyNotDeployed, "limit") {
		t.Fatalf("issues = %+v", res.Issues)
	}
	if m := h.meta(); m.FallbackReason != domain.DestFallbackNone || m.OverLimit != "" {
		t.Fatalf("report-only pushed the node into a fallback: %+v", m)
	}
}

// A fallback that is trimmed from the LKG sheds its observe rules too when only
// they cannot be deployed, instead of sending nothing.
func TestReport_TheFallbackShedsObserveRulesThatNeedSniffing(t *testing.T) {
	h := newHarness(t)
	l := h.list("watch", "DOMAIN-SUFFIX,watched.example")
	enf := h.policy("smtp", domain.DestActionBlock, domain.DestStateOn, func(p *domain.DestPolicy) { p.Inline = domain.DestPolicyInline{Ports: "25"} })
	h.policy("watch", domain.DestActionBlock, domain.DestStateReport, func(p *domain.DestPolicy) { p.ListIDs = []int64{l.ID} })
	h.publishNow()
	res := h.compile(base1())
	h.observe(applied(res.Digest))
	if len(ruleIDs(res.Policy)) != 2 {
		t.Fatalf("setup: %v", ruleIDs(res.Policy))
	}

	// A new ENFORCED domain rule needs sniffing the inbound cannot give: the
	// node falls back on its LKG, whose observe rule needs sniffing too.
	l2 := h.list("block", "DOMAIN-SUFFIX,blocked.example")
	h.policy("block", domain.DestActionBlock, domain.DestStateOn, func(p *domain.DestPolicy) { p.ListIDs = []int64{l2.ID} })
	h.publishNow()
	f := fleet{panelID: 9, nodes: []*domain.Node{node(1, `{"enabled":true,"metadataOnly":true}`)}, users: []int64{10}}
	got := h.compile(f)
	if got.Policy == nil || joinIDs(ruleIDs(got.Policy)) != idOf(enf) {
		t.Fatalf("fallback rules = %v, want only the enforced %s", ruleIDs(got.Policy), idOf(enf))
	}
	if !hasIssue(got, IssueReportOnlyNotDeployed, "sniffing") {
		t.Fatalf("issues = %+v", got.Issues)
	}
}
