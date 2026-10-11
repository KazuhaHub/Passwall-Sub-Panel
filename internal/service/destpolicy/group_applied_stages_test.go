package destpolicy

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-protocol/protocol"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

func groupAppliedFixture(t *testing.T) (domain.DestPolicyState, domain.DestTestPanel, time.Time, []byte) {
	t.Helper()
	now := time.UnixMilli(1_800_000_000_000).UTC()
	policy := &protocol.DestinationPolicy{Rules: []protocol.DestinationRule{
		{ID: "g7", CatchAll: true, Action: protocol.RuleObserve, Subjects: []protocol.SubjectKey{"usr_2"}},
		{ID: "g8", CatchAll: true, Action: protocol.RuleBlock, Subjects: []protocol.SubjectKey{"usr_3"}},
		{ID: "g9x1", Action: protocol.RuleAllow, Domains: []string{"domain:example.test"}, Subjects: []protocol.SubjectKey{"usr_4"}},
	}}
	body, err := json.Marshal(policy)
	if err != nil || protocol.ValidateDestinationPolicy(policy) != nil {
		t.Fatal("invalid receipt fixture")
	}
	digest := protocol.PolicyDigest(policy)
	p := domain.DestTestPanel{ID: 1, Kind: domain.PanelKindPSP, Agent: &domain.NodeAgent{AgentID: "agt_group_receipt", DesiredCoreEngine: domain.NodeCoreXray, ObservedCoreEngine: domain.NodeCoreXray, ObservedCapabilities: []string{protocol.CapabilityDestinationPolicy}, LastSeen: &now}, Runtime: &domain.DestAgentPolicy{AgentID: "agt_group_receipt", MintedKind: domain.DestCandidateDesired, MintedGeneration: 7, MintedAt: &now, DesiredSHA256: digest, MintedSHA256: digest, ReportedSHA256: digest, AppliedSHA256: digest, AppliedAt: &now, ReportedState: "applied", AppliedGroups: []int64{123456}}}
	return domain.DestPolicyState{Generation: 7, PublishedGeneration: 7}, p, now, body
}

func TestAppliedGroupStagesVerifyCurrentBodyInsteadOfHistoricalGroupIDs(t *testing.T) {
	state, panel, now, body := groupAppliedFixture(t)
	for _, engine := range []domain.NodeCoreEngine{domain.NodeCoreXray, domain.NodeCoreSingBox} {
		panel.Agent.DesiredCoreEngine, panel.Agent.ObservedCoreEngine = engine, engine
		calls := 0
		got, err := ReadAppliedGroupStages(state, panel, time.Minute, now, func() ([]byte, error) { calls++; return body, nil })
		if err != nil || calls != 1 || !reflect.DeepEqual(got, map[int64]string{7: "trial", 8: "enforce"}) {
			t.Fatal("current receipt lost its rule stage, required collection or trusted historical group IDs")
		}
	}
}

func TestAppliedGroupStagesDoNotLoadStaleUnpublishedOrFallbackCandidates(t *testing.T) {
	for _, item := range []struct {
		name   string
		change func(*domain.DestPolicyState, *domain.DestTestPanel, time.Time)
	}{
		{"unpublished", func(s *domain.DestPolicyState, _ *domain.DestTestPanel, _ time.Time) { s.Generation++ }},
		{"paused", func(s *domain.DestPolicyState, _ *domain.DestTestPanel, _ time.Time) { s.Paused = true }},
		{"old-generation", func(_ *domain.DestPolicyState, p *domain.DestTestPanel, _ time.Time) { p.Runtime.MintedGeneration-- }},
		{"old-report", func(_ *domain.DestPolicyState, p *domain.DestTestPanel, _ time.Time) {
			p.Runtime.ReportedSHA256 = strings.Repeat("a", 64)
		}},
		{"old-applied", func(_ *domain.DestPolicyState, p *domain.DestTestPanel, _ time.Time) {
			p.Runtime.AppliedSHA256 = strings.Repeat("a", 64)
		}},
		{"old-desired", func(_ *domain.DestPolicyState, p *domain.DestTestPanel, _ time.Time) {
			p.Runtime.DesiredSHA256 = strings.Repeat("a", 64)
		}},
		{"rejected", func(_ *domain.DestPolicyState, p *domain.DestTestPanel, _ time.Time) {
			p.Runtime.ReportedState = "rejected"
		}},
		{"fallback", func(_ *domain.DestPolicyState, p *domain.DestTestPanel, _ time.Time) {
			p.Runtime.MintedKind = domain.DestCandidateFallback
		}},
		{"fallback-reason", func(_ *domain.DestPolicyState, p *domain.DestTestPanel, _ time.Time) {
			p.Runtime.FallbackReason = "sniffing"
		}},
		{"exhausted", func(_ *domain.DestPolicyState, p *domain.DestTestPanel, _ time.Time) {
			p.Runtime.FallbackExhausted = true
		}},
		{"over-limit", func(_ *domain.DestPolicyState, p *domain.DestTestPanel, _ time.Time) {
			p.Runtime.OverLimit = &domain.DestPublishError{Kind: "rules"}
		}},
		{"sniffing", func(_ *domain.DestPolicyState, p *domain.DestTestPanel, _ time.Time) {
			p.Runtime.PrecheckListeners = []string{"lst_1"}
		}},
		{"offline", func(_ *domain.DestPolicyState, p *domain.DestTestPanel, n time.Time) {
			old := n.Add(-3*time.Minute - time.Millisecond)
			p.Agent.LastSeen = &old
		}},
		{"future-heartbeat", func(_ *domain.DestPolicyState, p *domain.DestTestPanel, n time.Time) {
			next := n.Add(time.Minute)
			p.Agent.LastSeen = &next
		}},
		{"no-capability", func(_ *domain.DestPolicyState, p *domain.DestTestPanel, _ time.Time) {
			p.Agent.ObservedCapabilities = nil
		}},
		{"different-engine", func(_ *domain.DestPolicyState, p *domain.DestTestPanel, _ time.Time) {
			p.Agent.DesiredCoreEngine = domain.NodeCoreSingBox
		}},
		{"unknown-observed-engine", func(_ *domain.DestPolicyState, p *domain.DestTestPanel, _ time.Time) {
			p.Agent.ObservedCoreEngine = ""
		}},
		{"different-agent", func(_ *domain.DestPolicyState, p *domain.DestTestPanel, _ time.Time) { p.Runtime.AgentID = "agt_other" }},
		{"third-party", func(_ *domain.DestPolicyState, p *domain.DestTestPanel, _ time.Time) { p.Kind = domain.PanelKind3XUI }},
		{"missing-mint-time", func(_ *domain.DestPolicyState, p *domain.DestTestPanel, _ time.Time) { p.Runtime.MintedAt = nil }},
		{"missing-applied-time", func(_ *domain.DestPolicyState, p *domain.DestTestPanel, _ time.Time) { p.Runtime.AppliedAt = nil }},
	} {
		t.Run(item.name, func(t *testing.T) {
			s, p, now, _ := groupAppliedFixture(t)
			item.change(&s, &p, now)
			got, err := ReadAppliedGroupStages(s, p, time.Minute, now, func() ([]byte, error) { t.Fatal("unproven metadata loaded private body"); return nil, nil })
			if err != nil || got == nil || len(got) != 0 {
				t.Fatal("unproven receipt claimed applied groups")
			}
		})
	}
}

func TestAppliedGroupStagesDistinguishStageChangesWithTheSameGroupID(t *testing.T) {
	state, panel, now, body := groupAppliedFixture(t)
	var policy *protocol.DestinationPolicy
	if err := json.Unmarshal(body, &policy); err != nil {
		t.Fatal(err)
	}
	policy.Rules[0].Action = protocol.RuleBlock
	changed, err := json.Marshal(policy)
	if err != nil {
		t.Fatal(err)
	}
	got, err := ReadAppliedGroupStages(state, panel, time.Minute, now, func() ([]byte, error) { return changed, nil })
	if err == nil || got != nil {
		t.Fatal("same group ID with a different stage bypassed the candidate digest")
	}
	digest := protocol.PolicyDigest(policy)
	panel.Runtime.DesiredSHA256, panel.Runtime.MintedSHA256, panel.Runtime.ReportedSHA256, panel.Runtime.AppliedSHA256 = digest, digest, digest, digest
	got, err = ReadAppliedGroupStages(state, panel, time.Minute, now, func() ([]byte, error) { return changed, nil })
	if err != nil || got[7] != "enforce" || got[8] != "enforce" || len(got) != 2 {
		t.Fatal("new exact receipt retained the historical trial stage")
	}
}

func TestAppliedGroupStagesWithholdMalformedNoncanonicalAndFailedProofs(t *testing.T) {
	state, panel, now, body := groupAppliedFixture(t)
	for _, candidate := range [][]byte{[]byte(`{}`), append([]byte(" "), body...), []byte(`{"rules":`)} {
		got, err := ReadAppliedGroupStages(state, panel, time.Minute, now, func() ([]byte, error) { return candidate, nil })
		if err == nil || got != nil {
			t.Fatal("bad candidate returned partial stage proof")
		}
	}
	private := errors.New("private-proof-storage-detail")
	got, err := ReadAppliedGroupStages(state, panel, time.Minute, now, func() ([]byte, error) { return body, private })
	if err == nil || got != nil {
		t.Fatal("failed body read returned stages")
	}
	got, err = ReadAppliedGroupStages(state, panel, time.Minute, now, nil)
	if err == nil || got != nil {
		t.Fatal("missing body reader proved applied groups")
	}
}
