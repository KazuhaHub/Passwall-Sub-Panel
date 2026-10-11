package app

import (
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/destpolicy"
)

type nativeStatusFact struct {
	name string
	pass bool
}

// Keep the original exact status comparison. Additional facts explain a failed
// snapshot using closed boolean categories, never identifiers or receipt data.
func nativeStatusCandidateMatches(r domain.DestAgentPolicy, status destpolicy.DestinationStatus, panelID int64, rules int) (bool, []nativeStatusFact) {
	if len(status.Nodes) != 1 {
		return false, []nativeStatusFact{{"rows", false}}
	}
	n := status.Nodes[0]
	wantState := "applied"
	if r.FallbackReason == "sniffing" {
		wantState = "sniffing"
	}
	if r.MintedKind == domain.DestCandidatePaused {
		wantState = "paused"
	}
	facts := []nativeStatusFact{
		{"rows", true}, {"panel", n.PanelID == panelID}, {"kind", n.MintedKind == r.MintedKind},
		{"reason", n.FallbackReason == r.FallbackReason}, {"exhausted", n.FallbackExhausted == r.FallbackExhausted},
		{"rules", n.AppliedRules == rules}, {"pending", n.PendingSince == nil}, {"applied_at", n.AppliedAt != nil},
		{"state", n.State == wantState}, {"paused", status.Paused == (r.MintedKind == domain.DestCandidatePaused)},
	}
	matches := true
	for _, fact := range facts {
		matches = matches && fact.pass
	}
	// These are diagnostic only; they do not replace any comparison above.
	facts = append(facts, nativeStatusFact{"generation", status.PublishedGeneration == r.MintedGeneration})
	for _, state := range []string{"applied", "sniffing", "paused", "pending", "offline", "none", "rejected", "over_limit"} {
		facts = append(facts, nativeStatusFact{"is_" + state, n.State == state})
	}
	return matches, facts
}

func TestNativeStatusCandidateRequiresEveryOriginalComparison(t *testing.T) {
	stamp := int64(1000)
	r := domain.DestAgentPolicy{MintedKind: domain.DestCandidateDesired, MintedGeneration: 7}
	base := destpolicy.DestinationStatus{PublishedGeneration: 7, Nodes: []destpolicy.DestinationNodeStatus{{PanelID: 42, MintedKind: r.MintedKind, AppliedRules: 1, AppliedAt: &stamp, State: "applied"}}}
	for _, tc := range []struct {
		name   string
		change func(*destpolicy.DestinationStatus)
	}{
		{"rows", func(s *destpolicy.DestinationStatus) { s.Nodes = nil }},
		{"duplicate-row", func(s *destpolicy.DestinationStatus) { s.Nodes = append(s.Nodes, s.Nodes[0]) }},
		{"panel", func(s *destpolicy.DestinationStatus) { s.Nodes[0].PanelID++ }},
		{"kind", func(s *destpolicy.DestinationStatus) { s.Nodes[0].MintedKind = domain.DestCandidateEmpty }},
		{"reason", func(s *destpolicy.DestinationStatus) { s.Nodes[0].FallbackReason = "sniffing" }},
		{"exhausted", func(s *destpolicy.DestinationStatus) { s.Nodes[0].FallbackExhausted = true }},
		{"rules", func(s *destpolicy.DestinationStatus) { s.Nodes[0].AppliedRules = 0 }},
		{"pending", func(s *destpolicy.DestinationStatus) { s.Nodes[0].PendingSince = &stamp }},
		{"applied_at", func(s *destpolicy.DestinationStatus) { s.Nodes[0].AppliedAt = nil }},
		{"state", func(s *destpolicy.DestinationStatus) { s.Nodes[0].State = "pending" }},
		{"paused", func(s *destpolicy.DestinationStatus) { s.Paused = true }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := base
			s.Nodes = append([]destpolicy.DestinationNodeStatus(nil), base.Nodes...)
			tc.change(&s)
			if matches, _ := nativeStatusCandidateMatches(r, s, 42, 1); matches {
				t.Fatal("status accepted a missing original comparison")
			}
		})
	}
	if matches, facts := nativeStatusCandidateMatches(r, base, 42, 1); !matches || len(facts) != 19 {
		t.Fatal("exact status did not match or lost its bounded facts")
	}
	r.FallbackReason = "sniffing"
	base.Nodes[0].FallbackReason, base.Nodes[0].State = "sniffing", "sniffing"
	if matches, _ := nativeStatusCandidateMatches(r, base, 42, 1); !matches {
		t.Fatal("exact sniffing status did not match")
	}
	r.MintedKind, base.Nodes[0].MintedKind = domain.DestCandidatePaused, domain.DestCandidatePaused
	base.Nodes[0].State, base.Paused = "paused", true
	if matches, _ := nativeStatusCandidateMatches(r, base, 42, 1); !matches {
		t.Fatal("exact paused status did not match")
	}
}
