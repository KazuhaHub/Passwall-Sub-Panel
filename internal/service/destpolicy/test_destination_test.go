package destpolicy

import (
	"slices"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

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
