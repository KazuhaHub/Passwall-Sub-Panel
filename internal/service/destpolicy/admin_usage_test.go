package destpolicy

import (
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// markUsageApplied makes agent agt_9 look like a node that applied the tier-B
// policy: minted and reported equal, level hits_and_usage.
func (ah *adminHarness) markUsageApplied(level string) {
	ah.t.Helper()
	if err := ah.repos.Agents.Patch(ah.ctx, "agt_9", domain.DestAgentPolicyPatch{
		MintedSHA256: ptr("d1"), ReportedSHA256: ptr("d1"), ReportedState: ptr("applied"), CollectEffective: ptr(level),
	}); err != nil {
		ah.t.Fatal(err)
	}
}

func TestStatus_CollectingUsageIsDerivedFromTheTierBLevel(t *testing.T) {
	ah := newAdminHarness(t)
	agent := ah.admin.Agents.(fakeAgents).agents[0]
	agent.ObservedCapabilities = []string{policyCap, hitsCap, usageCap}

	ah.markUsageApplied("hits")
	v, err := ah.admin.Status(ah.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n := nodeOf(t, v, 9); !n.Collecting || n.CollectingUsage {
		t.Fatalf("a hits node = collecting %v usage %v", n.Collecting, n.CollectingUsage)
	}
	ah.markUsageApplied("hits_and_usage")
	v, _ = ah.admin.Status(ah.ctx)
	if n := nodeOf(t, v, 9); !n.Collecting || !n.CollectingUsage {
		t.Fatalf("a tier-B node = collecting %v usage %v", n.Collecting, n.CollectingUsage)
	}
	if x := nodeOf(t, v, 10); x.CollectingUsage {
		t.Fatal("a 3X-UI node cannot collect usage")
	}
}

// The account view and the privacy page ask which nodes are collecting usage,
// through the same derivation.
func TestUsageNodes_AreTheTierBNodesTheAccountHasAClientOn(t *testing.T) {
	ah := newAdminHarness(t)
	agent := ah.admin.Agents.(fakeAgents).agents[0]
	agent.ObservedCapabilities = []string{policyCap, hitsCap, usageCap}

	ah.markUsageApplied("hits")
	if nodes, err := ah.admin.UserUsageNodes(ah.ctx, 10); err != nil || len(nodes) != 0 {
		t.Fatalf("a hits node: %+v, %v; want none", nodes, err)
	}
	ah.markUsageApplied("hits_and_usage")
	nodes, err := ah.admin.UserUsageNodes(ah.ctx, 10)
	if err != nil || len(nodes) != 1 || nodes[0].PanelID != 9 || nodes[0].Name != "native-9" {
		t.Fatalf("nodes = %+v, %v", nodes, err)
	}
	// An account with no client there sees none, and an unknown account too.
	if nodes, _ := ah.admin.UserUsageNodes(ah.ctx, 11); len(nodes) != 0 {
		t.Fatalf("an account with no client on the node: %+v", nodes)
	}
	if n, err := ah.admin.CollectingUsageNodes(ah.ctx); err != nil || n != 1 {
		t.Fatalf("CollectingUsageNodes = %d, %v; want 1", n, err)
	}
	agent.ObservedCoreEngine = domain.NodeCoreSingBox
	if n, _ := ah.admin.CollectingUsageNodes(ah.ctx); n != 0 {
		t.Fatalf("a sing-box node: %d", n)
	}
}

func TestUsageRetentionDays_IsTheSettingClamped(t *testing.T) {
	ah := newAdminHarness(t)
	if got := ah.admin.UsageRetentionDays(ah.ctx); got != 7 {
		t.Fatalf("default = %d, want 7", got)
	}
}

// A node running the collect-only policy enforces nothing, whatever the record
// of what it last applied says: the status must not show that record's rules and
// groups as running (a pause and a node with nothing in force alike).
func TestStatus_ACollectOnlyNodeShowsNoAppliedRulesOrGroups(t *testing.T) {
	ah := newAdminHarness(t)
	agent := ah.admin.Agents.(fakeAgents).agents[0]
	agent.ObservedCapabilities = []string{policyCap, hitsCap, usageCap}
	if err := ah.repos.Agents.Patch(ah.ctx, "agt_9", domain.DestAgentPolicyPatch{
		AppliedSHA256: ptr("old"), AppliedRuleCount: ptr(5), AppliedGroups: &[]int64{3},
		MintedSHA256: ptr(collectOnlyDigest), ReportedSHA256: ptr(collectOnlyDigest), ReportedState: ptr("applied"), CollectEffective: ptr("hits_and_usage"),
	}); err != nil {
		t.Fatal(err)
	}
	v, err := ah.admin.Status(ah.ctx)
	if err != nil {
		t.Fatal(err)
	}
	n := nodeOf(t, v, 9)
	if n.AppliedRules != 0 || len(n.AllowlistGroups) != 0 {
		t.Fatalf("applied_rules %d, groups %v: the node enforces nothing", n.AppliedRules, n.AllowlistGroups)
	}
}

// A node that refused the collect-only policy runs no usage collection, and
// nothing else says so: the status carries it, so the administrator can see
// that usage stopped and retry. A retry clears it.
func TestStatus_ARefusedCollectOnlyPolicyIsSaidAndRetryClearsIt(t *testing.T) {
	ah := newAdminHarness(t)
	agent := ah.admin.Agents.(fakeAgents).agents[0]
	agent.ObservedCapabilities = []string{policyCap, hitsCap, usageCap}
	ah.panels.panels[9].AuditCollect = domain.AuditCollectHitsAndUsage
	if n := nodeOf(t, mustStatus(t, ah), 9); n.UsageRefused {
		t.Fatal("a node that refused nothing is flagged")
	}
	if err := ah.repos.Agents.Patch(ah.ctx, "agt_9", domain.DestAgentPolicyPatch{MintedSHA256: ptr(collectOnlyDigest)}); err != nil {
		t.Fatal(err)
	}
	if _, err := ah.admin.Service.ObserveStatus(ah.ctx, "agt_9", agent.ObservedCapabilities, rejected(collectOnlyDigest)); err != nil {
		t.Fatal(err)
	}
	n := nodeOf(t, mustStatus(t, ah), 9)
	if !n.UsageRefused || n.CollectingUsage {
		t.Fatalf("usage_refused %v collecting_usage %v", n.UsageRefused, n.CollectingUsage)
	}
	// Set to hits, there is nothing to refuse, so nothing is said.
	ah.panels.panels[9].AuditCollect = domain.AuditCollectHits
	if nodeOf(t, mustStatus(t, ah), 9).UsageRefused {
		t.Fatal("a hits node is flagged")
	}
	ah.panels.panels[9].AuditCollect = domain.AuditCollectHitsAndUsage
	if ok, err := ah.admin.Service.Retry(ah.ctx, "agt_9"); err != nil || !ok {
		t.Fatalf("Retry = %v, %v", ok, err)
	}
	if nodeOf(t, mustStatus(t, ah), 9).UsageRefused {
		t.Fatal("retry did not clear it")
	}
}

func mustStatus(t *testing.T, ah *adminHarness) *StatusView {
	t.Helper()
	v, err := ah.admin.Status(ah.ctx)
	if err != nil {
		t.Fatal(err)
	}
	return v
}
