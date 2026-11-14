package destpolicy

import (
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

func boolp(v bool) *bool { return &v }

// counts_as_risk is the admin's to set, on a block policy only. A report-only
// block policy may store it (it counts once it is enforced), an allow policy
// may not.
func TestPreparePolicy_CountsAsRisk(t *testing.T) {
	ah := newAdminHarness(t)
	ctx := ah.ctx
	in := PolicyInput{Name: "p", Action: domain.DestActionBlock, State: domain.DestStateOn, Scope: domain.DestScopeAll,
		Inline: domain.DestPolicyInline{Ports: "25"}, CountsAsRisk: boolp(true)}

	p, err := ah.admin.PreparePolicy(ctx, in, nil)
	if err != nil || !p.CountsAsRisk {
		t.Fatalf("block + counts_as_risk = (%+v, %v)", p, err)
	}
	rep := in
	rep.State = domain.DestStateReport
	if p, err = ah.admin.PreparePolicy(ctx, rep, nil); err != nil || !p.CountsAsRisk {
		t.Fatalf("report-only block must store counts_as_risk: (%+v, %v)", p, err)
	}
	if p, err = ah.admin.PreparePolicy(ctx, func() PolicyInput { i := in; i.CountsAsRisk = nil; return i }(), nil); err != nil || p.CountsAsRisk {
		t.Fatalf("omitted on create must be false: (%+v, %v)", p, err)
	}

	allow := in
	allow.Action = domain.DestActionAllow
	if _, err := ah.admin.PreparePolicy(ctx, allow, nil); err == nil || validationCode(t, err).Code != CodePolicyInvalid || validationCode(t, err).Field != "counts_as_risk" {
		t.Fatalf("allow + counts_as_risk = %v, want dest_policy_invalid on counts_as_risk", err)
	}
	allow.CountsAsRisk = boolp(false)
	if _, err := ah.admin.PreparePolicy(ctx, allow, nil); err != nil {
		t.Fatalf("allow + counts_as_risk=false is fine: %v", err)
	}

	// An edit: omitted keeps the stored value, explicit changes it, and turning
	// the policy into an allow clears an omitted flag instead of refusing it.
	existing := ah.policy("e", domain.DestActionBlock, domain.DestStateOn, func(p *domain.DestPolicy) {
		p.Inline = domain.DestPolicyInline{Ports: "25"}
		p.CountsAsRisk = true
	})
	edit := PolicyInput{Name: "e", Action: domain.DestActionBlock, State: domain.DestStateOn, Scope: domain.DestScopeAll, Inline: domain.DestPolicyInline{Ports: "25"}}
	if p, err := ah.admin.PreparePolicy(ctx, edit, existing); err != nil || !p.CountsAsRisk {
		t.Fatalf("omitted on edit must keep true: (%+v, %v)", p, err)
	}
	edit.CountsAsRisk = boolp(false)
	if p, err := ah.admin.PreparePolicy(ctx, edit, existing); err != nil || p.CountsAsRisk {
		t.Fatalf("explicit false on edit: (%+v, %v)", p, err)
	}
	edit.CountsAsRisk, edit.Action = nil, domain.DestActionAllow
	if p, err := ah.admin.PreparePolicy(ctx, edit, existing); err != nil || p.CountsAsRisk {
		t.Fatalf("a block turned allow drops the flag: (%+v, %v)", p, err)
	}
}

// The dest_block signal flags an account once the BLOCK hits of policies that
// count as risk reach the threshold, and only those: the same volume on a
// policy that does not count, or reported (observe) rather than enforced, never
// flags.
func TestRiskView_ACountingBlockPolicyFlagsAtTheThresholdAndANonCountingOneDoesNot(t *testing.T) {
	h := newHarness(t)
	counting := h.policy("counting", domain.DestActionBlock, domain.DestStateOn, func(p *domain.DestPolicy) {
		p.Inline = domain.DestPolicyInline{Ports: "25"}
		p.CountsAsRisk = true
	})
	plain := h.policy("plain", domain.DestActionBlock, domain.DestStateOn, func(p *domain.DestPolicy) { p.Inline = domain.DestPolicyInline{Ports: "26"} })
	hour := int64(3600000) * 400
	row := func(user int64, src string, action string, n int64) domain.DestHit {
		return domain.DestHit{HourMS: hour, PanelID: 1, UserID: user, Source: src, Action: action, Dest: "a", Port: 1, Count: n, FirstMS: hour, LastMS: hour}
	}
	threshold := int64(domain.RiskDefaultDestBlockThreshold)
	if _, err := h.repos.Hits.StoreBatch(h.ctx, "agt_1", "b", h.clock.Now(), []domain.DestHit{
		row(10, idOf(counting), "block", threshold),     // at the threshold
		row(11, idOf(counting), "block", threshold-1),   // one short
		row(12, idOf(plain), "block", threshold*5),      // plenty, but the policy does not count
		row(13, idOf(counting), "observe", threshold*5), // reported only
	}); err != nil {
		t.Fatal(err)
	}
	caps := []string{policyCap, hitsCap}
	agents := rvAgents{agents: []*domain.NodeAgent{{AgentID: "agt_1", PanelID: 1, ObservedCapabilities: caps, ObservedCoreEngine: domain.NodeCoreXray}}}
	if err := h.repos.Agents.Patch(h.ctx, "agt_1", domain.DestAgentPolicyPatch{
		MintedSHA256: ptr("d1"), ReportedSHA256: ptr("d1"), ReportedState: ptr("applied"), CollectEffective: ptr("hits"),
	}); err != nil {
		t.Fatal(err)
	}
	var clients []*domain.PSPClient
	for _, u := range []int64{10, 11, 12, 13} {
		clients = append(clients, &domain.PSPClient{UserID: u, PanelID: 1})
	}
	view := NewRiskView(RiskViewDeps{Hits: h.repos.Hits, Policies: h.repos.Policies, Agents: agents, Meta: h.repos.Agents, Clients: rvClients{clients: clients}})
	got, err := view.DestBlockInputs(h.ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	state := func(uid int64) domain.GeoState {
		v, _ := domain.EvaluateDestBlock(domain.DestBlockPolicy{}, got[uid])
		return v.State
	}
	if state(10) != domain.GeoStateFlagged {
		t.Errorf("a counting policy at the threshold: %s, want flagged", state(10))
	}
	if state(11) == domain.GeoStateFlagged {
		t.Errorf("one below the threshold flagged")
	}
	if state(12) != domain.GeoStateIdle {
		t.Errorf("a non-counting policy's blocks: %s, want idle (they are not counted at all)", state(12))
	}
	if state(13) != domain.GeoStateIdle {
		t.Errorf("report-only hits: %s, want idle", state(13))
	}
}

// A report-only rule names itself in a test trace like an enforced one does
// (kind block, the policy and its name) and is marked report_only.
func TestTest_AReportOnlyRuleIsNamedInTheTrace(t *testing.T) {
	ah := newAdminHarness(t)
	l := ah.list("watch", "DOMAIN-SUFFIX,watched.example.com")
	pol := ah.policy("Watched", domain.DestActionBlock, domain.DestStateReport, func(p *domain.DestPolicy) { p.ListIDs = []int64{l.ID} })
	ah.publishNow()
	res, err := ah.admin.Test(ah.ctx, TestInput{Target: "www.watched.example.com", PanelID: 9})
	if err != nil {
		t.Fatal(err)
	}
	var step *TestStep
	for i := range res.Steps {
		if res.Steps[i].RuleID == "p"+itoa(pol.ID) {
			step = &res.Steps[i]
		}
	}
	if step == nil {
		t.Fatalf("no step for the report-only rule: %+v", res.Steps)
	}
	if step.Kind != "block" || !step.ReportOnly || step.PolicyID != pol.ID || step.Name != "Watched" {
		t.Fatalf("step = %+v, want kind block, report_only, with the policy id and name", step)
	}
	if res.Verdict == "block" {
		t.Fatalf("a report-only rule blocked: %+v", res)
	}
}

// A node that left report-only rules out says so in the status: why, per node.
func TestStatus_NamesWhyReportOnlyRulesAreNotDeployed(t *testing.T) {
	ah := newAdminHarness(t)
	ctx := ah.ctx
	l := ah.list("watch", "DOMAIN-SUFFIX,watched.example")
	ah.policy("smtp", domain.DestActionBlock, domain.DestStateOn, func(p *domain.DestPolicy) { p.Inline = domain.DestPolicyInline{Ports: "25"} })
	ah.policy("watch", domain.DestActionBlock, domain.DestStateReport, func(p *domain.DestPolicy) { p.ListIDs = []int64{l.ID} })
	ah.publishNow()
	shed := func() *string {
		t.Helper()
		v, err := ah.admin.Status(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return nodeOf(t, v, 9).ReportOnlyNotDeployed
	}

	ah.compile(base1())
	if got := shed(); got != nil {
		t.Fatalf("report_only_not_deployed = %q with everything deployed, want null", *got)
	}
	// Sniffing that cannot supply the domain rule: only the report rule is left out.
	weak := fleet{panelID: 9, nodes: []*domain.Node{node(1, `{"enabled":true,"metadataOnly":true}`)}, users: []int64{10}}
	ah.compile(weak)
	if got := shed(); got == nil || *got != "sniffing" {
		t.Fatalf("report_only_not_deployed = %v, want sniffing", got)
	}
	// Fixed: it clears again.
	ah.compile(base1())
	if got := shed(); got != nil {
		t.Fatalf("report_only_not_deployed = %q after the sniffing was fixed, want null", *got)
	}
}

// A report-only SOURCE rule is marked report_only too, and keeps kind "source"
// with its inbound; an enforced source rule is not marked.
func TestTest_AReportOnlySourceRuleIsMarkedReportOnly(t *testing.T) {
	ah := newAdminHarness(t)
	ctx := ah.ctx
	ah.mmdb(map[string]string{"1.0.0.0/24": "CN"})
	if err := ah.repos.SourceRules.Upsert(ctx, &domain.DestSourceRule{NodeID: 1, Countries: []string{"CN"}, State: domain.DestStateReport}, nil); err != nil {
		t.Fatal(err)
	}
	if err := ah.repos.SourceRules.Upsert(ctx, &domain.DestSourceRule{NodeID: 2, Countries: []string{"US"}, State: domain.DestStateOn}, nil); err != nil {
		t.Fatal(err)
	}
	pol := ah.policy("Enforced", domain.DestActionBlock, domain.DestStateOn, func(p *domain.DestPolicy) { p.Inline = domain.DestPolicyInline{Ports: "25"} })
	ah.publishNow()
	res, err := ah.admin.Test(ctx, TestInput{Target: "example.net", Port: 443, PanelID: 9, InboundNodeID: 1, SourceIP: "1.0.0.9"})
	if err != nil {
		t.Fatal(err)
	}
	var src *TestStep
	for i := range res.Steps {
		if res.Steps[i].Kind == "source" && res.Steps[i].NodeID == 1 {
			src = &res.Steps[i]
		}
		if res.Steps[i].PolicyID == pol.ID && res.Steps[i].ReportOnly {
			t.Errorf("an enforced policy step is marked report_only: %+v", res.Steps[i])
		}
	}
	if src == nil || !src.ReportOnly || src.Result != "hit" {
		t.Fatalf("source step = %+v (steps %+v), want kind source, report_only, hit", src, res.Steps)
	}
	if res.Verdict != "observe" || res.TerminatingStep != "s1" {
		t.Fatalf("verdict %q terminating %q: a report-only hit ends the evaluation with verdict observe, never block", res.Verdict, res.TerminatingStep)
	}
	// An enforced source rule on another inbound is not report_only.
	res, _ = ah.admin.Test(ctx, TestInput{Target: "example.net", PanelID: 9, InboundNodeID: 2, SourceIP: "9.9.9.9"})
	for _, st := range res.Steps {
		if st.Kind == "source" && st.NodeID == 2 && st.ReportOnly {
			t.Fatalf("an enforced source step is marked report_only: %+v", st)
		}
	}
}

// What the status says was left out is what the node was actually sent.
func TestStatus_ReportOnlyShedDescribesWhatWasSent(t *testing.T) {
	ah := newAdminHarness(t)
	ctx := ah.ctx
	l := ah.list("watch", "DOMAIN-SUFFIX,watched.example")
	ah.policy("smtp", domain.DestActionBlock, domain.DestStateOn, func(p *domain.DestPolicy) { p.Inline = domain.DestPolicyInline{Ports: "25"} })
	ah.publishNow()
	first := ah.compile(base1())
	ah.observe(applied(first.Digest)) // the last known good: the port rule alone

	ah.policy("more", domain.DestActionBlock, domain.DestStateOn, func(p *domain.DestPolicy) { p.Inline = domain.DestPolicyInline{Ports: "26"} })
	ah.policy("watch", domain.DestActionBlock, domain.DestStateReport, func(p *domain.DestPolicy) { p.ListIDs = []int64{l.ID} })
	ah.publishNow()
	weak := fleet{panelID: 9, nodes: []*domain.Node{node(1, `{"enabled":true,"metadataOnly":true}`)}, users: []int64{10}}
	shed := func() *string {
		t.Helper()
		v, err := ah.admin.Status(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return nodeOf(t, v, 9).ReportOnlyNotDeployed
	}
	res := ah.compile(weak)
	if got := shed(); got == nil || *got != "sniffing" {
		t.Fatalf("sent the desired policy without its report rule: report_only_not_deployed = %v, want sniffing", got)
	}
	// The node refuses what it was sent: it falls back to the last known good,
	// which has no report-only rule, so nothing was left out of what it gets
	// now. The desired policy's reason must not outlive that.
	ah.observe(rejected(res.Digest))
	fb := ah.compile(weak)
	if fb.Policy == nil {
		t.Fatalf("no fallback was sent")
	}
	if got := shed(); got != nil {
		t.Fatalf("report_only_not_deployed = %q after a fallback that dropped nothing, want null", *got)
	}
	for _, i := range fb.Issues {
		if i.Code == IssueReportOnlyNotDeployed {
			t.Fatalf("the fallback carries the desired policy's shedding issue: %+v", i)
		}
	}
}

// Nothing sent, nothing left out: a paused node reads null.
func TestStatus_ReportOnlyShedIsEmptyWhenNothingIsSent(t *testing.T) {
	ah := newAdminHarness(t)
	ctx := ah.ctx
	l := ah.list("watch", "DOMAIN-SUFFIX,watched.example")
	ah.policy("smtp", domain.DestActionBlock, domain.DestStateOn, func(p *domain.DestPolicy) { p.Inline = domain.DestPolicyInline{Ports: "25"} })
	ah.policy("watch", domain.DestActionBlock, domain.DestStateReport, func(p *domain.DestPolicy) { p.ListIDs = []int64{l.ID} })
	ah.publishNow()
	weak := fleet{panelID: 9, nodes: []*domain.Node{node(1, `{"enabled":true,"metadataOnly":true}`)}, users: []int64{10}}
	ah.compile(weak)
	if err := ah.svc.SetPaused(ctx, true); err != nil {
		t.Fatal(err)
	}
	ah.compile(weak)
	v, _ := ah.admin.Status(ctx)
	if got := nodeOf(t, v, 9).ReportOnlyNotDeployed; got != nil {
		t.Fatalf("report_only_not_deployed = %q while paused, want null", *got)
	}
}

// A node whose only policy is report-only, all of it left out, is sent
// nothing; the status still says why, matching the issue it raises.
func TestStatus_ReportOnlyShedSurvivesWhenSheddingEmptiesThePolicy(t *testing.T) {
	ah := newAdminHarness(t)
	ctx := ah.ctx
	l := ah.list("watch", "DOMAIN-SUFFIX,watched.example")
	ah.policy("watch", domain.DestActionBlock, domain.DestStateReport, func(p *domain.DestPolicy) { p.ListIDs = []int64{l.ID} })
	ah.publishNow()
	weak := fleet{panelID: 9, nodes: []*domain.Node{node(1, `{"enabled":true,"metadataOnly":true}`)}, users: []int64{10}}
	res := ah.compile(weak)
	if res.Policy != nil {
		t.Fatalf("a policy was sent although every rule was left out: %+v", res.Policy)
	}
	raised := false
	for _, i := range res.Issues {
		raised = raised || i.Code == IssueReportOnlyNotDeployed
	}
	if !raised {
		t.Fatalf("issues %+v, want %s", res.Issues, IssueReportOnlyNotDeployed)
	}
	v, err := ah.admin.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got := nodeOf(t, v, 9).ReportOnlyNotDeployed; got == nil || *got != "sniffing" {
		t.Fatalf("report_only_not_deployed = %v, want sniffing", got)
	}
	// Fixed: the rule deploys and the status clears.
	ah.compile(base1())
	v, _ = ah.admin.Status(ctx)
	if got := nodeOf(t, v, 9).ReportOnlyNotDeployed; got != nil {
		t.Fatalf("report_only_not_deployed = %q after the sniffing was fixed, want null", *got)
	}
}

// The last known good holds only report-only domain rules; the inbound then
// loses the sniffing they need, and the desired policy adds an enforced domain
// rule, so the node falls back. Leaving the report rules out empties the
// fallback: nothing is sent, nothing panics, and the status says why.
func TestStatus_ReportOnlyShedWhenSheddingEmptiesTheFallback(t *testing.T) {
	ah := newAdminHarness(t)
	ctx := ah.ctx
	l := ah.list("watch", "DOMAIN-SUFFIX,watched.example")
	ah.policy("watch", domain.DestActionBlock, domain.DestStateReport, func(p *domain.DestPolicy) { p.ListIDs = []int64{l.ID} })
	ah.publishNow()
	first := ah.compile(base1())
	if first.Policy == nil {
		t.Fatal("the report-only policy was not sent")
	}
	ah.observe(applied(first.Digest))

	bl := ah.list("bad", "DOMAIN-SUFFIX,bad.example")
	ah.policy("bad", domain.DestActionBlock, domain.DestStateOn, func(p *domain.DestPolicy) { p.ListIDs = []int64{bl.ID} })
	ah.publishNow()
	weak := fleet{panelID: 9, nodes: []*domain.Node{node(1, `{"enabled":true,"metadataOnly":true}`)}, users: []int64{10}}
	res := ah.compile(weak)
	if res.Policy != nil {
		t.Fatalf("sent %+v, want nothing", res.Policy)
	}
	raised := false
	for _, i := range res.Issues {
		raised = raised || i.Code == IssueReportOnlyNotDeployed
	}
	v, err := ah.admin.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	got := nodeOf(t, v, 9).ReportOnlyNotDeployed
	if !raised || got == nil || *got != "sniffing" {
		t.Fatalf("issue raised %v, report_only_not_deployed = %v: want both, sniffing (issues %+v)", raised, got, res.Issues)
	}
}

// A block that becomes an allow in the same request drops counts_as_risk
// silently, a client echoing the whole DTO back; an allow that stays an allow
// with the flag, or a new allow, is refused.
func TestPreparePolicy_CountsAsRiskOnABlockBecomingAnAllow(t *testing.T) {
	ah := newAdminHarness(t)
	ctx := ah.ctx
	blk := ah.policy("b", domain.DestActionBlock, domain.DestStateOn, func(p *domain.DestPolicy) {
		p.Inline = domain.DestPolicyInline{Ports: "25"}
		p.CountsAsRisk = true
	})
	in := PolicyInput{Name: "b", Action: domain.DestActionAllow, State: domain.DestStateOn, Scope: domain.DestScopeAll,
		Inline: domain.DestPolicyInline{Ports: "25"}, CountsAsRisk: boolp(true)}
	p, err := ah.admin.PreparePolicy(ctx, in, blk)
	if err != nil || p.CountsAsRisk || p.Action != domain.DestActionAllow {
		t.Fatalf("block -> allow echoing true = (%+v, %v), want it cleared without an error", p, err)
	}
	alw := ah.policy("a", domain.DestActionAllow, domain.DestStateOn, func(p *domain.DestPolicy) { p.Inline = domain.DestPolicyInline{Ports: "27"} })
	in = PolicyInput{Name: "a", Action: domain.DestActionAllow, State: domain.DestStateOn, Scope: domain.DestScopeAll,
		Inline: domain.DestPolicyInline{Ports: "27"}, CountsAsRisk: boolp(true)}
	if _, err := ah.admin.PreparePolicy(ctx, in, alw); err == nil || validationCode(t, err).Field != "counts_as_risk" {
		t.Fatalf("an allow kept as an allow with true = %v, want 400 on counts_as_risk", err)
	}
	if _, err := ah.admin.PreparePolicy(ctx, in, nil); err == nil || validationCode(t, err).Field != "counts_as_risk" {
		t.Fatalf("a new allow with true = %v, want 400 on counts_as_risk", err)
	}
	// An allow turned block may set it.
	in.Action = domain.DestActionBlock
	if p, err := ah.admin.PreparePolicy(ctx, in, alw); err != nil || !p.CountsAsRisk {
		t.Fatalf("allow -> block with true = (%+v, %v)", p, err)
	}
}
