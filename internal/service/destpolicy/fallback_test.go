package destpolicy

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/KazuhaHub/passwall-protocol/protocol"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

func TestPruneFallbackRetainsMatchesOrderAndOnlyExistingSubjects(t *testing.T) {
	lkg := &protocol.DestinationPolicy{Collect: protocol.CollectHits, CollectRevision: 1, Exempt: []protocol.SubjectKey{"usr_12"}, Rules: []protocol.DestinationRule{
		{ID: "p2x1", Action: protocol.RuleAllow, Subjects: []protocol.SubjectKey{"usr_2", "usr_99"}, Domains: []string{"domain:old.example.com"}},
		{ID: "p2x2", Action: protocol.RuleAllow, Subjects: []protocol.SubjectKey{"usr_2", "usr_99"}, Protocols: []string{"bittorrent"}},
		{ID: "p3", Action: protocol.RuleBlock, Ports: "443"},
		{ID: "g8x1", Action: protocol.RuleAllow, Subjects: []protocol.SubjectKey{"usr_2"}, Domains: []string{"domain:old-allow.example.com"}},
		{ID: "g8", Action: protocol.RuleBlock, Subjects: []protocol.SubjectKey{"usr_2"}, CatchAll: true},
	}}
	before, _ := json.Marshal(lkg)
	defs := domain.DestDefinitions{Policies: []domain.DestPolicy{rulePolicy(2, domain.DestBlock, 1), rulePolicy(3, domain.DestAllow, 2), rulePolicy(4, domain.DestBlock, 3)}, Groups: []domain.DestGroupMode{{GroupID: 8, Mode: "allowlist", Stage: "trial"}}, Exemptions: []domain.DestExemption{{UserID: 3}, {UserID: 999}}}
	input := testRoster()
	input.Collect, input.CollectRevision = domain.AuditCollectHitsAndUsage, 9
	p, err := PruneFallback(lkg, defs, input)
	if err != nil || p == nil {
		t.Fatalf("pruning failed: %+v / %v", p, err)
	}
	want := *lkg
	want.Rules = append([]protocol.DestinationRule(nil), lkg.Rules...)
	want.Rules[0].Subjects, want.Rules[1].Subjects = []protocol.SubjectKey{"usr_2"}, []protocol.SubjectKey{"usr_2"}
	want.Exempt, want.Collect, want.CollectRevision = []protocol.SubjectKey{"usr_3"}, protocol.CollectHitsAndUsage, 9
	if !reflect.DeepEqual(p, &want) {
		t.Fatalf("confirmed matches changed or new matches added: %+v", p)
	}
	// Returned nested slices must not alias the durable LKG.
	p.Rules[0].Domains[0] = "domain:mutated.example.com"
	p.Rules[1].Protocols[0] = "http"
	p.Rules[0].Subjects[0] = "usr_3"
	after, _ := json.Marshal(lkg)
	if string(after) != string(before) {
		t.Fatal("pruning mutated LKG")
	}
}

func TestPruneFallbackDropsRetiredPolicyGroupAndEmptyScopedRules(t *testing.T) {
	lkg := &protocol.DestinationPolicy{Rules: []protocol.DestinationRule{
		{ID: "p1", Action: protocol.RuleAllow, Ports: "443"},
		{ID: "p2", Action: protocol.RuleBlock, Ports: "80"},
		{ID: "p3", Action: protocol.RuleBlock, Subjects: []protocol.SubjectKey{"usr_99"}, Ports: "80"},
		{ID: "g8x1", Action: protocol.RuleAllow, Subjects: []protocol.SubjectKey{"usr_2"}, Ports: "443"},
		{ID: "g8x2", Action: protocol.RuleAllow, Subjects: []protocol.SubjectKey{"usr_2"}, Ports: "80"},
		{ID: "g8", Action: protocol.RuleBlock, Subjects: []protocol.SubjectKey{"usr_2"}, CatchAll: true},
		{ID: "g9", Action: protocol.RuleBlock, Subjects: []protocol.SubjectKey{"usr_2"}, CatchAll: true},
		{ID: "p4", Action: protocol.RuleBlock, Ports: "443"},
	}}
	disabled := rulePolicy(2, domain.DestBlock, 1)
	disabled.Enabled = false
	defs := domain.DestDefinitions{Policies: []domain.DestPolicy{disabled, rulePolicy(3, domain.DestBlock, 1), rulePolicy(4, domain.DestBlock, 2)}, Groups: []domain.DestGroupMode{{GroupID: 8, Mode: "open"}}}
	p, err := PruneFallback(lkg, defs, testRoster())
	if err != nil || p == nil || len(p.Rules) != 1 || p.Rules[0].ID != "p4" {
		t.Fatalf("retired rules retained: %+v / %v", p, err)
	}
}

func TestPruneFallbackEmptyPausedAndCapabilityPrecedence(t *testing.T) {
	for _, collect := range []domain.AuditCollect{domain.AuditCollectOff, domain.AuditCollectHits, domain.AuditCollectHitsAndUsage} {
		for _, paused := range []bool{false, true} {
			input := testRoster()
			input.Collect = collect
			defs := domain.DestDefinitions{State: domain.DestPolicyState{Paused: paused}, Exemptions: []domain.DestExemption{{UserID: 2}}}
			lkg := (*protocol.DestinationPolicy)(nil)
			if paused {
				lkg = &protocol.DestinationPolicy{Rules: []protocol.DestinationRule{{ID: "bad"}}}
			}
			p, err := PruneFallback(lkg, defs, input)
			if err != nil {
				t.Fatal(err)
			}
			if collect != domain.AuditCollectHitsAndUsage {
				if p != nil {
					t.Fatalf("empty policy changed wire: %+v", p)
				}
			} else if p == nil || len(p.Rules) != 0 || len(p.Exempt) != 0 || p.Collect != protocol.CollectHitsAndUsage || p.CollectRevision != input.CollectRevision {
				t.Fatalf("collection-only candidate wrong: %+v", p)
			}
		}
	}
	input := testRoster()
	input.Capabilities = nil
	if p, err := PruneFallback(&protocol.DestinationPolicy{Rules: []protocol.DestinationRule{{ID: "bad"}}}, domain.DestDefinitions{}, input); err != nil || p != nil {
		t.Fatalf("capability gate lost: %+v / %v", p, err)
	}
}

func TestPruneFallbackRejectsCorruptLKGAndInvalidRoster(t *testing.T) {
	if _, err := PruneFallback(&protocol.DestinationPolicy{Rules: []protocol.DestinationRule{{ID: "bad"}}}, domain.DestDefinitions{}, testRoster()); !errors.Is(err, domain.ErrUnavailable) {
		t.Fatalf("corrupt LKG not observable: %v", err)
	}
	input := testRoster()
	input.UserIDs = append(input.UserIDs, 0)
	if _, err := PruneFallback(&protocol.DestinationPolicy{Rules: []protocol.DestinationRule{{ID: "p1", Action: protocol.RuleBlock, Ports: "443"}}}, domain.DestDefinitions{Policies: []domain.DestPolicy{rulePolicy(1, domain.DestBlock, 1)}}, input); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("invalid roster accepted: %v", err)
	}
}

func TestPruneFallbackCurrentExemptionsCannotSilentlyExceedLimit(t *testing.T) {
	input := testRoster()
	input.UserIDs = nil
	defs := domain.DestDefinitions{Policies: []domain.DestPolicy{rulePolicy(1, domain.DestBlock, 1)}}
	for id := int64(1); id <= protocol.MaxDestinationSubjects; id++ {
		input.UserIDs = append(input.UserIDs, id)
		defs.Exemptions = append(defs.Exemptions, domain.DestExemption{UserID: id})
	}
	lkg := &protocol.DestinationPolicy{Rules: []protocol.DestinationRule{{ID: "p1", Action: protocol.RuleBlock, Subjects: []protocol.SubjectKey{"usr_1"}, Ports: "443"}}}
	if p, err := PruneFallback(lkg, defs, input); !errors.Is(err, domain.ErrValidation) || p != nil {
		t.Fatalf("over-limit exemption candidate escaped: %+v / %v", p, err)
	}
}
