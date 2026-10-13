package destpolicy

import (
	"reflect"
	"testing"

	"github.com/KazuhaHub/passwall-protocol/protocol"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

func groupCoverageFixture(t *testing.T) (domain.DestDefinitions, GroupNodeInput) {
	t.Helper()
	defs := domain.DestDefinitions{Lists: []domain.DestList{readyList(1, "domain:example.test\n")}, Groups: []domain.DestGroupMode{{GroupID: 7, Mode: "allowlist", Stage: "trial", ListIDs: []int64{1}}}}
	input := GroupNodeInput{Panel: domain.DestTestPanel{ID: 1, Name: "native", Kind: domain.PanelKindPSP, Agent: &domain.NodeAgent{AgentID: "agt_coverage", ObservedCapabilities: []string{protocol.CapabilityDestinationPolicy}}}, Engine: "xray", Quota: RosterInput{UserIDs: []int64{10}, UserGroups: map[int64]int64{10: 7}, Collect: domain.AuditCollectOff}, Listeners: []protocol.Listener{sniffListener(t, 1, true, "{}")}}
	return defs, input
}

func TestGroupNodeCoverageChecksProspectivePolicyAndKeepsStableReasons(t *testing.T) {
	for _, item := range []struct {
		name, reason string
		change       func(*GroupNodeInput)
	}{
		{"native", "", func(*GroupNodeInput) {}},
		{"third party", "xui", func(p *GroupNodeInput) { p.Panel.Kind = domain.PanelKind3XUI }},
		{"sui", "sui", func(p *GroupNodeInput) { p.Panel.Kind = domain.PanelKindSUI }},
		{"old native", "capability", func(p *GroupNodeInput) { p.Panel.Agent = nil }},
		{"missing policy", "capability", func(p *GroupNodeInput) { p.Panel.Agent.ObservedCapabilities = []string{"audit.hits.v1"} }},
		{"current limit", "over_limit", func(p *GroupNodeInput) { p.Panel.Runtime = &domain.DestAgentPolicy{FallbackReason: "over_limit"} }},
		{"current rejection", "rejected", func(p *GroupNodeInput) { p.Panel.Runtime = &domain.DestAgentPolicy{FallbackReason: "rejected"} }},
		{"current sniffing", "sniffing", func(p *GroupNodeInput) { p.Panel.Runtime = &domain.DestAgentPolicy{FallbackReason: "sniffing"} }},
		{"exhausted", "rejected", func(p *GroupNodeInput) { p.Panel.Runtime = &domain.DestAgentPolicy{FallbackExhausted: true} }},
		{"prospective sniffing", "sniffing", func(p *GroupNodeInput) {
			p.Listeners = []protocol.Listener{sniffListener(t, 1, true, `{"enabled":true,"metadataOnly":true}`)}
		}},
	} {
		t.Run(item.name, func(t *testing.T) {
			defs, input := groupCoverageFixture(t)
			item.change(&input)
			got, err := AssessGroupNodes(defs, 7, []GroupNodeInput{input})
			if err != nil || got.TotalNodes != 1 {
				t.Fatalf("coverage: %+v %v", got, err)
			}
			if item.reason == "" {
				if got.EligibleNodes != 1 || len(got.Ineligible) != 0 || !reflect.DeepEqual(got.EligiblePanelIDs, []int64{1}) {
					t.Fatal("capable prospective node lost eligibility")
				}
			} else if got.EligibleNodes != 0 || len(got.Ineligible) != 1 || got.Ineligible[0].Reason != item.reason {
				t.Fatalf("wrong exclusion reason: %+v", got)
			}
		})
	}
}

func TestGroupNodeCoverageUsesFullQuotaAndDoesNotMutatePausedDefinitions(t *testing.T) {
	defs, input := groupCoverageFixture(t)
	defs.State.Paused = true
	input.Quota.UserIDs, input.Quota.UserGroups = nil, map[int64]int64{}
	// Disabled/tag-matched users can be absent from the actual roster; they
	// still count in both the allow rule and the catch-all rule.
	for id := int64(1); id <= protocol.MaxDestinationSubjects/2+1; id++ {
		input.Quota.UserIDs = append(input.Quota.UserIDs, id)
		input.Quota.UserGroups[id] = 7
	}
	got, err := AssessGroupNodes(defs, 7, []GroupNodeInput{input})
	if err != nil || got.EligibleNodes != 0 || len(got.Ineligible) != 1 || got.Ineligible[0].Reason != "over_limit" || !defs.State.Paused {
		t.Fatal("prospective quota used the reduced roster or a paused shortcut")
	}
}

func TestGroupNodeCoverageOrdersPanelsWithoutChangingInput(t *testing.T) {
	defs, native := groupCoverageFixture(t)
	xui, sui := native, native
	xui.Panel.ID, xui.Panel.Kind = 2, domain.PanelKind3XUI
	sui.Panel.ID, sui.Panel.Kind = 5, domain.PanelKindSUI
	inputs := []GroupNodeInput{sui, native, xui}
	got, err := AssessGroupNodes(defs, 7, inputs)
	if err != nil || got.TotalNodes != 3 || got.EligibleNodes != 1 || len(got.Ineligible) != 2 || got.Ineligible[0].PanelID != 2 || got.Ineligible[1].PanelID != 5 || inputs[0].Panel.ID != 5 {
		t.Fatal("coverage ordering, counts or input isolation changed")
	}
}

func TestGroupNodeCoverageRejectsCorruptionWithoutPartialCounts(t *testing.T) {
	for _, kind := range []string{"duplicate", "bad panel", "unknown kind", "bad fallback", "bad member", "unmapped member", "missing mode"} {
		t.Run(kind, func(t *testing.T) {
			defs, input := groupCoverageFixture(t)
			bad := input
			bad.Panel.ID = 2
			switch kind {
			case "duplicate":
				bad.Panel.ID = 1
			case "bad panel":
				bad.Panel.ID = 0
			case "unknown kind":
				bad.Panel.Kind = "unknown"
			case "bad fallback":
				bad.Panel.Runtime = &domain.DestAgentPolicy{FallbackReason: "private-unknown-reason"}
			case "bad member":
				bad.Quota.UserIDs = []int64{-1}
			case "unmapped member":
				bad.Quota.UserGroups = map[int64]int64{}
			case "missing mode":
				defs.Groups = nil
			}
			got, err := AssessGroupNodes(defs, 7, []GroupNodeInput{input, bad})
			if err == nil || !reflect.DeepEqual(got, GroupNodeCoverage{}) {
				t.Fatal("corrupt coverage became partial or permissive")
			}
		})
	}
}
