package destpolicy

import (
	"encoding/json"
	"slices"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

func TestDestinationSimulationExecutionSnapshotUsesExactCandidateReceipt(t *testing.T) {
	now := time.Date(2026, 10, 8, 23, 0, 0, 123000000, time.UTC)
	for _, tc := range []struct {
		name        string
		change      func(*domain.DestTestPanel, *domain.DestPolicyState)
		wantRules   any
		wantPending bool
	}{
		{"desired confirmed", func(*domain.DestTestPanel, *domain.DestPolicyState) {}, float64(2), false},
		{"desired unconfirmed", func(p *domain.DestTestPanel, _ *domain.DestPolicyState) { p.Runtime.ReportedSHA256 = "old" }, nil, true},
		{"fallback confirmed", func(p *domain.DestTestPanel, _ *domain.DestPolicyState) {
			p.Runtime.MintedKind = domain.DestCandidateFallback
			p.Runtime.FallbackReason = "rejected"
		}, float64(2), false},
		{"fallback waiting", func(p *domain.DestTestPanel, _ *domain.DestPolicyState) {
			p.Runtime.MintedKind = domain.DestCandidateFallback
			p.Runtime.FallbackReason = "rejected"
			p.Runtime.ReportedSHA256 = "old"
		}, nil, true},
		{"exhausted without empty", func(p *domain.DestTestPanel, _ *domain.DestPolicyState) {
			p.Runtime.MintedKind = domain.DestCandidateFallback
			p.Runtime.FallbackExhausted = true
		}, nil, false},
		{"exhausted stop waiting", func(p *domain.DestTestPanel, _ *domain.DestPolicyState) {
			p.Runtime.MintedKind = domain.DestCandidateEmpty
			p.Runtime.FallbackExhausted = true
			p.Runtime.ReportedSHA256 = "old"
		}, nil, true},
		{"exhausted stop confirmed", func(p *domain.DestTestPanel, _ *domain.DestPolicyState) {
			p.Runtime.MintedKind = domain.DestCandidateEmpty
			p.Runtime.FallbackExhausted = true
		}, float64(0), false},
		{"pause confirmed", func(p *domain.DestTestPanel, s *domain.DestPolicyState) {
			s.Paused = true
			p.Runtime.MintedKind = domain.DestCandidatePaused
		}, float64(0), false},
		{"offline confirmation is historical", func(p *domain.DestTestPanel, _ *domain.DestPolicyState) { p.Agent.LastSeen = nil }, nil, false},
		{"unknown candidate kind", func(p *domain.DestTestPanel, _ *domain.DestPolicyState) {
			p.Runtime.MintedKind = domain.DestCandidateKind("unknown")
		}, nil, false},
		{"missing candidate digest", func(p *domain.DestTestPanel, _ *domain.DestPolicyState) {
			p.Runtime.MintedSHA256 = ""
			p.Runtime.ReportedSHA256 = ""
		}, nil, true},
		{"missing apply timestamp", func(p *domain.DestTestPanel, _ *domain.DestPolicyState) { p.Runtime.AppliedAt = nil }, nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			current := destinationTestContext(t, domain.DestDefinitions{})
			p := statusFixture(now).DestTestPanel
			p.Runtime.MintedGeneration = current.State.PublishedGeneration
			reportedAt := now.Add(time.Minute)
			p.Runtime.AppliedAt, p.Runtime.ReportedAt = &now, &reportedAt
			tc.change(&p, &current.State)
			current.Panels = []domain.DestTestPanel{p}
			got, err := EvaluateDestinationTest(DestinationTestInput{Target: "example.test", Port: 443, Network: "tcp"}, current, time.Minute, now)
			if err != nil {
				t.Fatal(err)
			}
			body, err := json.Marshal(got.Nodes[0])
			if err != nil {
				t.Fatal(err)
			}
			var node map[string]any
			if err := json.Unmarshal(body, &node); err != nil {
				t.Fatal(err)
			}
			execution, ok := node["execution"].(map[string]any)
			if !ok {
				t.Fatal("simulation omits the tested node's execution snapshot")
			}
			if execution["applied_rules"] != tc.wantRules || (execution["pending_since"] != nil) != tc.wantPending {
				t.Fatalf("execution borrowed historical rules or lost pending receipt: %v", execution)
			}
			if execution["minted_kind"] != string(p.Runtime.MintedKind) || execution["fallback_exhausted"] != p.Runtime.FallbackExhausted || execution["engine"] != "xray" {
				t.Fatalf("execution lost candidate identity: %v", execution)
			}
			wantApplied := statusMillis(p.Runtime.AppliedAt)
			if tc.wantRules == float64(0) {
				wantApplied = statusMillis(p.Runtime.ReportedAt)
			}
			if wantApplied == nil && execution["applied_at"] != nil || wantApplied != nil && execution["applied_at"] != float64(*wantApplied) {
				t.Fatalf("execution borrowed the wrong confirmation time: %v", execution)
			}
		})
	}
}

func TestDestinationSimulationUnsupportedNodeOmitsHistoricalExecution(t *testing.T) {
	now := time.Now()
	p := statusFixture(now).DestTestPanel
	p.Runtime.MintedKind = domain.DestCandidateFallback
	p.Runtime.FallbackReason = "rejected"
	p.Runtime.AppliedAt = &now
	p.Agent.ObservedCapabilities = nil
	if got := destinationTestExecution(p, "unsupported_version"); got != nil {
		t.Fatal("unsupported node borrowed an old fallback receipt")
	}
}

func destinationTestContext(t *testing.T, defs domain.DestDefinitions) domain.DestTestContext {
	t.Helper()
	defs.State.Generation, defs.State.PublishedGeneration = 1, 1
	body, err := BuildDefinitionSnapshot(defs)
	if err != nil {
		t.Fatal(err)
	}
	return domain.DestTestContext{State: defs.State, Published: true, Snapshot: domain.DestPolicySnapshot{Generation: 1, Body: body}, SelectedPanelID: 1, UserIDs: []int64{2}, UserGroups: map[int64]int64{2: 8}, PolicyNames: map[int64]string{1: "Allow", 2: "Block", 3: "Watch"}, GroupNames: map[int64]string{8: "Scope"}}
}

func TestDestinationSimulationTracePreservesShadowingAndReferencedEntry(t *testing.T) {
	defs := domain.DestDefinitions{Lists: []domain.DestList{{ID: 7, Kind: domain.DestListCustom, Entries: []byte("domain:example.test\n"), EntryCount: 1}}, Policies: []domain.DestPolicy{
		{ID: 1, Action: domain.DestAllow, Scope: domain.DestScopeAll, Enabled: true, ListIDs: []int64{7}},
		{ID: 2, Action: domain.DestBlock, Scope: domain.DestScopeAll, Enabled: true, Inline: domain.DestInline{Ports: "443"}},
		{ID: 3, Action: domain.DestObserve, Scope: domain.DestScopeAll, Enabled: true, ListIDs: []int64{7}},
	}}
	got, err := EvaluateDestinationTest(DestinationTestInput{Target: "https://SUB.example.test/private?query=discarded", Port: 443, Network: "tcp", UserID: 2, PanelID: 1}, destinationTestContext(t, defs), time.Minute, time.Now())
	if err != nil || got.Verdict != "allow" || got.TerminatingStep == nil || *got.TerminatingStep != "allow" || len(got.Steps) != 5 || got.Steps[0].Result != "hit" || got.Steps[0].ListID != 7 || got.Steps[0].Entry != "domain:example.test" || got.Steps[2].Result != "shadowed" || got.Steps[3].Result != "shadowed" || got.Steps[4].Result != "skipped" || !slices.Contains(got.Notes, "url_host_only") {
		t.Fatal("simulation changed Protocol trace or lost list provenance")
	}
}

func TestDestinationSimulationExemptionAndGroupTrialUsePublishedMembership(t *testing.T) {
	for _, exempt := range []bool{false, true} {
		defs := domain.DestDefinitions{Groups: []domain.DestGroupMode{{GroupID: 8, Mode: "allowlist", Stage: "trial"}}}
		if exempt {
			defs.Exemptions = []domain.DestExemption{{UserID: 2}}
		}
		got, err := EvaluateDestinationTest(DestinationTestInput{Target: "example.test", Port: 443, Network: "tcp", UserID: 2, PanelID: 1}, destinationTestContext(t, defs), time.Minute, time.Now())
		want := "observe"
		if exempt {
			want = "exempt"
		}
		if err != nil || got.Verdict != want || got.Steps[1].GroupID != 8 || got.Steps[1].Name != "Scope" {
			t.Fatal("simulation lost exemption/group semantics")
		}
	}
}

func TestDestinationSimulationProtocolUncertaintyHasNoDefinitiveTermination(t *testing.T) {
	defs := domain.DestDefinitions{Policies: []domain.DestPolicy{
		{ID: 1, Action: domain.DestBlock, Scope: domain.DestScopeAll, Enabled: true, Priority: 1, Inline: domain.DestInline{Protocols: []string{"bittorrent"}}},
		{ID: 2, Action: domain.DestBlock, Scope: domain.DestScopeAll, Enabled: true, Priority: 2, Inline: domain.DestInline{Ports: "443"}},
	}}
	got, err := EvaluateDestinationTest(DestinationTestInput{Target: "example.test", Port: 443, Network: "tcp"}, destinationTestContext(t, defs), time.Minute, time.Now())
	if err != nil || got.Verdict != "untestable" || got.TerminatingStep != nil || !slices.Contains(got.Notes, "protocol_not_testable") {
		t.Fatal("destination-only simulation claimed a protocol-dependent conclusion")
	}
}

func TestDestinationSimulationAnonymousScopeAndMissingNativeClientAreDistinct(t *testing.T) {
	defs := domain.DestDefinitions{Policies: []domain.DestPolicy{{ID: 2, Action: domain.DestBlock, Scope: domain.DestScopeGroups, GroupIDs: []int64{8}, Enabled: true, Inline: domain.DestInline{Ports: "443"}}}}
	current := destinationTestContext(t, defs)
	current.SelectedPanelID = 0
	got, err := EvaluateDestinationTest(DestinationTestInput{Target: "example.test", Port: 443, Network: "tcp"}, current, time.Minute, time.Now())
	if err != nil || got.Verdict != "direct" || !slices.Contains(got.Notes, "virtual_scope") {
		t.Fatal("anonymous simulation inherited a group or exemption")
	}
	got, err = EvaluateDestinationTest(DestinationTestInput{Target: "example.test", Port: 443, Network: "tcp", UserID: 2}, current, time.Minute, time.Now())
	if err != nil || got.Verdict != "untestable" || len(got.Steps) != 0 || got.TerminatingStep != nil || !slices.Contains(got.Notes, "no_native_client") {
		t.Fatal("missing native-client scope became a fabricated anonymous verdict")
	}
}
