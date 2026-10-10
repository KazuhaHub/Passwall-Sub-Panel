package destpolicy

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-protocol/protocol"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

func TestCollectionDisclosureRequiresCurrentProofAndSeparatesRuleFreeUsage(t *testing.T) {
	now := time.Now().UTC()
	for _, tc := range []struct {
		name   string
		change func(*domain.DestStatusPanel)
		kinds  []string
	}{
		{"hits", func(*domain.DestStatusPanel) {}, []string{"hits"}},
		{"trial", func(p *domain.DestStatusPanel) { p.Facts.Trial = true }, []string{"hits", "trial"}},
		{"usage without rules", func(p *domain.DestStatusPanel) {
			p.Collect = domain.AuditCollectHitsAndUsage
			p.Facts.Collect = "hits_and_usage"
			p.Facts.Hits = false
		}, []string{"usage"}},
		{"usage and hits", func(p *domain.DestStatusPanel) {
			p.Collect = domain.AuditCollectHitsAndUsage
			p.Facts.Collect = "hits_and_usage"
		}, []string{"hits", "usage"}},
		{"off", func(p *domain.DestStatusPanel) { p.Collect = domain.AuditCollectOff }, nil},
		{"revision changed", func(p *domain.DestStatusPanel) { p.CollectRevision++ }, nil},
		{"pending", func(p *domain.DestStatusPanel) { p.Runtime.ReportedSHA256 = "previous" }, nil},
		{"rejected", func(p *domain.DestStatusPanel) { p.Runtime.ReportedState = "rejected" }, nil},
		{"exhausted", func(p *domain.DestStatusPanel) { p.Runtime.FallbackExhausted = true }, nil},
		{"offline", func(p *domain.DestStatusPanel) { old := now.Add(-181 * time.Second); p.Agent.LastSeen = &old }, nil},
		{"sing box", func(p *domain.DestStatusPanel) { p.Agent.ObservedCoreEngine = domain.NodeCoreSingBox }, nil},
		{"unverified body", func(p *domain.DestStatusPanel) { p.Facts = domain.DestCollectionFacts{} }, nil},
		{"allow only", func(p *domain.DestStatusPanel) { p.Facts.Hits = false }, nil},
		{"lost usage capability", func(p *domain.DestStatusPanel) {
			p.Collect = domain.AuditCollectHitsAndUsage
			p.Facts.Collect = "hits_and_usage"
			p.Agent.ObservedCapabilities = []string{protocol.CapabilityDestinationPolicy, "audit.hits.v1"}
		}, nil},
		{"usage downgraded and applied", func(p *domain.DestStatusPanel) {
			p.Collect = domain.AuditCollectHitsAndUsage
			p.Agent.ObservedCapabilities = []string{protocol.CapabilityDestinationPolicy, "audit.hits.v1"}
		}, []string{"hits"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := statusFixture(now)
			tc.change(&p)
			got := BuildCollectionDisclosure(domain.DestStatusContext{Panels: []domain.DestStatusPanel{p, p}}, domain.DestinationSettings{}, 60, now)
			var kinds []string
			for _, row := range got {
				kinds = append(kinds, row.Kind)
				if row.Nodes != 1 || row.RetentionDays != map[string]int{"hits": 30, "trial": 7, "usage": 7}[row.Kind] {
					t.Fatalf("bad count or retention: %+v", got)
				}
			}
			if got == nil || !reflect.DeepEqual(kinds, tc.kinds) {
				t.Fatalf("got %+v; want %v", got, tc.kinds)
			}
		})
	}
}

func TestCollectionDisclosureCountsPhysicalNodesAndUsesEffectiveRetention(t *testing.T) {
	now := time.Now().UTC()
	one, two, three := statusFixture(now), statusFixture(now), statusFixture(now)
	two.ID, three.ID = 2, 3
	two.Collect, two.Facts.Collect, two.Facts.Hits = domain.AuditCollectHitsAndUsage, "hits_and_usage", false
	three.Facts.Trial = true
	got := BuildCollectionDisclosure(domain.DestStatusContext{Panels: []domain.DestStatusPanel{one, two, three, one}},
		domain.DestinationSettings{HitRetentionDays: 43, TrialRetentionDays: 12, UsageRetentionDays: 9}, 60, now)
	want := []domain.LegalAccessCollection{{Kind: "hits", Nodes: 2, RetentionDays: 43}, {Kind: "trial", Nodes: 1, RetentionDays: 12}, {Kind: "usage", Nodes: 1, RetentionDays: 9}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v; want %+v", got, want)
	}
}

func TestCollectionFactsTrialUsesOnlyTheCanonicalObserveFallback(t *testing.T) {
	for _, tc := range []struct {
		name  string
		rules []protocol.DestinationRule
		trial bool
	}{
		{"trial after block", []protocol.DestinationRule{{ID: "p1", Action: protocol.RuleBlock, Ports: "443"}, {ID: "g9", Action: protocol.RuleObserve, CatchAll: true, Subjects: []protocol.SubjectKey{"usr_1"}}}, true},
		{"enforced group", []protocol.DestinationRule{{ID: "g9", Action: protocol.RuleBlock, CatchAll: true, Subjects: []protocol.SubjectKey{"usr_1"}}}, false},
		{"group exception", []protocol.DestinationRule{{ID: "g9x1", Action: protocol.RuleAllow, Ports: "443", Subjects: []protocol.SubjectKey{"usr_1"}}}, false},
		{"observe policy", []protocol.DestinationRule{{ID: "p1", Action: protocol.RuleObserve, Ports: "443"}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			policy := &protocol.DestinationPolicy{Collect: protocol.CollectHits, CollectRevision: 1, Rules: tc.rules}
			body, err := json.Marshal(policy)
			if err != nil {
				t.Fatal(err)
			}
			var cache CollectionFactsCache
			facts, err := cache.Read("agt_trial", protocol.PolicyDigest(policy), func() ([]byte, error) { return body, nil })
			if err != nil || facts.Trial != tc.trial {
				t.Fatalf("facts %+v: %v", facts, err)
			}
		})
	}
}
