package destpolicy

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/KazuhaHub/passwall-protocol/protocol"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"testing"
	"time"
)

func statusFixture(now time.Time) domain.DestStatusPanel {
	return domain.DestStatusPanel{DestTestPanel: domain.DestTestPanel{ID: 1, Kind: domain.PanelKindPSP, Agent: &domain.NodeAgent{AgentID: "agt_status", ObservedCoreEngine: domain.NodeCoreXray, ObservedCapabilities: []string{protocol.CapabilityDestinationPolicy, "audit.hits.v1", "audit.usage.v1"}, LastSeen: &now}, Runtime: &domain.DestAgentPolicy{MintedAt: &now, MintedGeneration: 2, MintedSHA256: "candidate", ReportedSHA256: "candidate", ReportedState: "applied", MintedKind: domain.DestCandidateDesired, AppliedRuleCount: 2, AppliedGroups: []int64{17}}}, Collect: domain.AuditCollectHits, CollectRevision: 3, Facts: domain.DestCollectionFacts{Collect: "hits", Revision: 3, Hits: true}}
}

func TestDestinationStatusCollectionRequiresCurrentVerifiedExecutableCandidate(t *testing.T) {
	now := time.Date(2026, 10, 5, 1, 2, 3, 456000000, time.UTC)
	for _, tc := range []struct {
		name   string
		change func(*domain.DestStatusPanel)
		want   bool
	}{
		{"confirmed", func(*domain.DestStatusPanel) {}, true},
		{"saved off", func(p *domain.DestStatusPanel) { p.Collect = domain.AuditCollectOff }, false},
		{"old revision", func(p *domain.DestStatusPanel) { p.CollectRevision++ }, false},
		{"allow only", func(p *domain.DestStatusPanel) { p.Facts.Hits = false }, false},
		{"no verified body", func(p *domain.DestStatusPanel) { p.Facts = domain.DestCollectionFacts{} }, false},
		{"pending digest", func(p *domain.DestStatusPanel) { p.Runtime.ReportedSHA256 = "old" }, false},
		{"rejected", func(p *domain.DestStatusPanel) { p.Runtime.ReportedState = "rejected" }, false},
		{"fallback exhausted", func(p *domain.DestStatusPanel) { p.Runtime.FallbackExhausted = true }, false},
		{"sing box", func(p *domain.DestStatusPanel) { p.Agent.ObservedCoreEngine = domain.NodeCoreSingBox }, false},
		{"unknown engine", func(p *domain.DestStatusPanel) { p.Agent.ObservedCoreEngine = "" }, false},
		{"offline", func(p *domain.DestStatusPanel) { old := now.Add(-181 * time.Second); p.Agent.LastSeen = &old }, false},
		{"no hits", func(p *domain.DestStatusPanel) {
			p.Agent.ObservedCapabilities = []string{protocol.CapabilityDestinationPolicy}
		}, false},
		{"usage downgrade", func(p *domain.DestStatusPanel) {
			p.Collect = domain.AuditCollectHitsAndUsage
			p.Facts.Collect = "hits_and_usage"
			p.Agent.ObservedCapabilities = []string{protocol.CapabilityDestinationPolicy, "audit.hits.v1"}
		}, false},
		{"fallback still executing", func(p *domain.DestStatusPanel) {
			p.Runtime.MintedKind = domain.DestCandidateFallback
			p.Runtime.FallbackReason = "rejected"
		}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := statusFixture(now)
			tc.change(&p)
			view, err := BuildDestinationStatus(domain.DestStatusContext{State: domain.DestPolicyState{Generation: 2, PublishedGeneration: 2}, Panels: []domain.DestStatusPanel{p}}, 60, 60, now)
			if err != nil || view.Nodes[0].Collecting != tc.want {
				t.Fatalf("collecting=%t want=%t err=%v", view.Nodes[0].Collecting, tc.want, err)
			}
			if view.Nodes[0].AllowlistGroups[0].Name != nil || view.Nodes[0].Losses != nil || view.Nodes[0].Hits24h != nil {
				t.Fatal("fabricated missing group or telemetry")
			}
		})
	}
}

func TestDestinationStatusDeadlineAndNodeStates(t *testing.T) {
	now := time.Now().UTC()
	first := now.Add(-270 * time.Second)
	last := now.Add(-10 * time.Second)
	state := domain.DestPolicyState{Generation: 3, PublishedGeneration: 2, LastWriteAt: &last, FirstUnpublishedAt: &first}
	view, err := BuildDestinationStatus(domain.DestStatusContext{State: state}, 60, 15, now)
	if err != nil || view.ApplyETAMS != 45000 || view.NextPublishAt == nil || *view.NextPublishAt != now.Add(30*time.Second).UnixMilli() {
		t.Fatal("maximum debounce deadline or effective poll ETA wrong")
	}
	state.FirstUnpublishedAt = nil
	if _, err := BuildDestinationStatus(domain.DestStatusContext{State: state}, 60, 15, now); !errors.Is(err, domain.ErrUnavailable) {
		t.Fatal("missing publication times accepted")
	}
	for _, tc := range []struct {
		name   string
		change func(*domain.DestStatusPanel, *domain.DestPolicyState)
	}{
		{"applied", func(*domain.DestStatusPanel, *domain.DestPolicyState) {}},
		{"pending", func(p *domain.DestStatusPanel, s *domain.DestPolicyState) {
			s.Generation = 3
			s.PublishedGeneration = 3
		}},
		{"rejected", func(p *domain.DestStatusPanel, _ *domain.DestPolicyState) { p.Runtime.FallbackExhausted = true }},
		{"over_limit", func(p *domain.DestStatusPanel, _ *domain.DestPolicyState) {
			p.Runtime.OverLimit = &domain.DestPublishError{Kind: "rules", Used: 5001, Limit: 5000}
		}},
		{"sniffing", func(p *domain.DestStatusPanel, _ *domain.DestPolicyState) {
			p.Runtime.PrecheckListeners = []string{"lst_1", "lst_1"}
			id := int64(1)
			p.Listeners = map[string]domain.DestStatusListener{"lst_1": {Listener: "lst_1", Label: "vless-443", NodeID: &id}}
		}},
		{"offline", func(p *domain.DestStatusPanel, _ *domain.DestPolicyState) { p.Agent.LastSeen = nil }},
		{"unsupported_kind", func(p *domain.DestStatusPanel, _ *domain.DestPolicyState) { p.Kind = domain.PanelKind3XUI }},
		{"unsupported_version", func(p *domain.DestStatusPanel, _ *domain.DestPolicyState) { p.Agent.ObservedCapabilities = nil }},
		{"paused", func(_ *domain.DestStatusPanel, s *domain.DestPolicyState) { s.Paused = true }},
		{"none", func(p *domain.DestStatusPanel, _ *domain.DestPolicyState) {
			p.Runtime.MintedKind = domain.DestCandidateEmpty
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := statusFixture(now)
			s := domain.DestPolicyState{Generation: 2, PublishedGeneration: 2}
			tc.change(&p, &s)
			v, err := BuildDestinationStatus(domain.DestStatusContext{State: s, Panels: []domain.DestStatusPanel{p}}, 60, 60, now)
			if err != nil || v.Nodes[0].State != tc.name || v.Totals[tc.name] != 1 || v.Totals["total"] != 1 {
				t.Fatalf("wrong node status: %v", v)
			}
			if tc.name == "sniffing" && (len(v.Nodes[0].SniffingInsufficient) != 1 || v.Nodes[0].SniffingInsufficient[0].Label != "vless-443") {
				t.Fatal("listener metadata not resolved/deduplicated")
			}
			if tc.name == "none" && (v.Nodes[0].AppliedRules != 0 || len(v.Nodes[0].AllowlistGroups) != 0) {
				t.Fatal("empty acknowledgement presented retained LKG as executing")
			}
		})
	}
}

func TestDestinationStatusCollectionFactsAreCanonicalDigestVerifiedAndBounded(t *testing.T) {
	policy := &protocol.DestinationPolicy{Collect: protocol.CollectHits, CollectRevision: 3, Rules: []protocol.DestinationRule{{ID: "p1", Action: protocol.RuleBlock, Ports: "443"}}}
	body, _ := json.Marshal(policy)
	digest := protocol.PolicyDigest(policy)
	var cache CollectionFactsCache
	loads := 0
	load := func() ([]byte, error) { loads++; return body, nil }
	for i := 0; i < 2; i++ {
		fact, err := cache.Read("agt_one", digest, load)
		if err != nil || !fact.Hits || fact.Revision != 3 {
			t.Fatal("verified candidate facts missing")
		}
	}
	if loads != 1 {
		t.Fatal("warm status reread a candidate body")
	}
	for i := 0; i < 300; i++ {
		if _, err := cache.Read(fmt.Sprintf("agt_%d", i), digest, load); err != nil {
			t.Fatal(err)
		}
	}
	if len(cache.facts) != 256 || len(cache.order) != 256 {
		t.Fatal("unbounded status proof cache")
	}
	for _, bad := range [][]byte{[]byte(`null`), []byte(`{}`), append([]byte(" "), body...)} {
		if _, err := cache.Read("agt_bad", digest, func() ([]byte, error) { return bad, nil }); !errors.Is(err, domain.ErrUnavailable) {
			t.Fatal("corrupt/noncanonical policy accepted")
		}
	}
	policy.Rules[0].Action = protocol.RuleAllow
	body, _ = json.Marshal(policy)
	fact, err := cache.Read("agt_allow", protocol.PolicyDigest(policy), load)
	if err != nil || fact.Hits {
		t.Fatal("allow-only candidate proves hit recording")
	}
}
