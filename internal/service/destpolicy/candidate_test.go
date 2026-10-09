package destpolicy

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/KazuhaHub/passwall-protocol/protocol"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

func candidateFixture(t *testing.T) (*PreparedCandidate, domain.DestDefinitions, RosterInput, protocol.ConfigBody) {
	t.Helper()
	defs := domain.DestDefinitions{State: domain.DestPolicyState{Generation: 7, PublishedGeneration: 7}, Policies: []domain.DestPolicy{rulePolicy(1, domain.DestBlock, 1)}}
	input := testRoster()
	base := protocol.ConfigBody{Core: protocol.CoreSelection{Engine: "xray", Version: "26.6.27"}}
	p, err := PrepareCandidate(defs, input, input, base)
	if err != nil {
		t.Fatal(err)
	}
	return p, defs, input, base
}
func confirmedPortLKG(t *testing.T) domain.DestAgentPolicy {
	t.Helper()
	p := &protocol.DestinationPolicy{Rules: []protocol.DestinationRule{{ID: "p1", Action: protocol.RuleBlock, Ports: "80"}}}
	raw, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	return domain.DestAgentPolicy{AgentID: "agt_candidate", AppliedSHA256: protocol.PolicyDigest(p), AppliedBody: raw}
}

func TestCandidateDesiredAndRepeatedPendingRetryPreserveRejectionSource(t *testing.T) {
	prepared, _, _, _ := candidateFixture(t)
	state := confirmedPortLKG(t)
	state.FallbackReason, state.RejectedGeneration, state.RejectedContext = "rejected", 7, strings.Repeat("a", 64)
	state.MintedContext = state.RejectedContext
	got, changed, err := SelectCandidate(prepared, &state)
	if err != nil || got.Mint.Kind != domain.DestCandidateDesired || got.Policy == nil || got.Policy.Rules[0].Ports != "443" || changed || state.RejectedGeneration != 7 || state.FallbackReason != "rejected" {
		t.Fatalf("new context did not retry without erasing rejection: %+v state=%+v / %v", got, state, err)
	}
	// Minting the pending desired candidate must not recreate the old lock.
	state.MintedContext = got.Mint.Context
	again, changed, err := SelectCandidate(prepared, &state)
	if err != nil || changed || again.Mint.Kind != domain.DestCandidateDesired {
		t.Fatalf("pending retry immediately fell back: %+v / %v", again, err)
	}
	state.RejectedContext = got.Mint.Context
	got, _, err = SelectCandidate(prepared, &state)
	if err != nil || got.Mint.Kind != domain.DestCandidateFallback || got.Policy.Rules[0].Ports != "80" || got.Mint.DesiredSHA256 == protocol.PolicyDigest(got.Policy) {
		t.Fatalf("current-context rejection did not select exact LKG: %+v / %v", got, err)
	}
}

func TestCandidateExhaustionSurvivesRosterAndResetsOnRetryContext(t *testing.T) {
	prepared, defs, input, base := candidateFixture(t)
	state := confirmedPortLKG(t)
	state.FallbackReason, state.RejectedGeneration, state.RejectedContext = "rejected", 7, prepared.context
	state.MintedContext, state.FallbackExhausted = prepared.context, true
	input.UserIDs = append(input.UserIDs, 123)
	changedRoster, err := PrepareCandidate(defs, input, input, base)
	if err != nil {
		t.Fatal(err)
	}
	got, changed, err := SelectCandidate(changedRoster, &state)
	if err != nil || changed || got.Policy != nil || got.Mint.Kind != domain.DestCandidateEmpty || !state.FallbackExhausted {
		t.Fatalf("roster unlocked exhausted fallback: %+v / %v", got, err)
	}
	base.Core.Version = "26.9.9"
	newContext, err := PrepareCandidate(defs, input, input, base)
	if err != nil {
		t.Fatal(err)
	}
	got, changed, err = SelectCandidate(newContext, &state)
	if err != nil || !changed || got.Mint.Kind != domain.DestCandidateDesired || state.FallbackExhausted {
		t.Fatalf("core change failed to retry: %+v / %v", got, err)
	}
}

func TestCandidateSniffingChecksDesiredAndFallbackBeforeMint(t *testing.T) {
	_, defs, input, base := candidateFixture(t)
	defs.Lists = []domain.DestList{readyList(1, "domain:example.com\n")}
	defs.Policies[0].ListIDs = []int64{1}
	base.Listeners = contextConfig().Listeners
	prepared, err := PrepareCandidate(defs, input, input, base)
	if err != nil {
		t.Fatal(err)
	}
	state := confirmedPortLKG(t)
	got, changed, err := SelectCandidate(prepared, &state)
	if err != nil || !changed || got.Mint.Kind != domain.DestCandidateFallback || state.FallbackReason != "sniffing" || !reflect.DeepEqual(state.PrecheckListeners, []string{"lis_1"}) {
		t.Fatalf("port LKG unavailable for sniffing failure: %+v state=%+v / %v", got, state, err)
	}
	lkg := &protocol.DestinationPolicy{Rules: []protocol.DestinationRule{{ID: "p1", Action: protocol.RuleBlock, Domains: []string{"domain:old.example.com"}}}}
	state.AppliedBody, _ = json.Marshal(lkg)
	state.AppliedSHA256 = protocol.PolicyDigest(lkg)
	got, _, err = SelectCandidate(prepared, &state)
	if err != nil || got.Mint.Kind != domain.DestCandidateEmpty || got.Policy != nil || !state.FallbackExhausted {
		t.Fatalf("insufficient LKG was minted: %+v / %v", got, err)
	}
}

func TestCandidatePauseWinsAndLazyLKGCorruptionDoesNotMutate(t *testing.T) {
	prepared, defs, input, base := candidateFixture(t)
	state := confirmedPortLKG(t)
	state.FallbackReason, state.RejectedGeneration, state.RejectedContext = "rejected", 7, prepared.context
	state.MintedContext = prepared.context
	state.AppliedBody = nil
	before := state
	if _, _, err := SelectCandidate(prepared, &state); !errors.Is(err, errPolicyBodiesRequired) || !reflect.DeepEqual(state, before) {
		t.Fatalf("lazy body request mutated state: %+v / %v", state, err)
	}
	state.AppliedBody = []byte("{}")
	before = state
	if _, _, err := SelectCandidate(prepared, &state); !errors.Is(err, domain.ErrUnavailable) || !reflect.DeepEqual(state, before) {
		t.Fatalf("corrupt body masqueraded as empty: %+v / %v", state, err)
	}
	defs.State.Paused = true
	input.Collect = domain.AuditCollectHitsAndUsage
	prepared, err := PrepareCandidate(defs, input, input, base)
	if err != nil {
		t.Fatal(err)
	}
	got, changed, err := SelectCandidate(prepared, &state)
	if err != nil || changed || got.Mint.Kind != domain.DestCandidatePaused || got.Policy == nil || len(got.Policy.Rules) != 0 || got.Policy.CollectRevision != input.CollectRevision || !reflect.DeepEqual(state, before) {
		t.Fatalf("pause did not dominate LKG: %+v / %v", got, err)
	}
}

func TestCandidateQuotaUsesAllTagMatchedMembersWhenRosterShrinks(t *testing.T) {
	_, defs, roster, base := candidateFixture(t)
	defs.Policies = nil
	defs.Groups = []domain.DestGroupMode{{GroupID: 8, Mode: "allowlist", Stage: "enforce"}}
	quota := roster
	quota.UserIDs = nil
	quota.UserGroups = map[int64]int64{}
	for id := int64(1); id <= protocol.MaxDestinationSubjects+1; id++ {
		quota.UserIDs = append(quota.UserIDs, id)
		quota.UserGroups[id] = 8
	}
	prepared, err := PrepareCandidate(defs, roster, quota, base)
	if err != nil {
		t.Fatal(err)
	}
	state := domain.DestAgentPolicy{AgentID: "agt_candidate"}
	got, changed, err := SelectCandidate(prepared, &state)
	if err != nil || !changed || got.Mint.Kind != domain.DestCandidateEmpty || state.FallbackReason != "over_limit" || state.OverLimit == nil || state.OverLimit.Kind != "subjects" || state.OverLimit.Used != protocol.MaxDestinationSubjects+1 {
		t.Fatalf("quota ignored full membership: %+v state=%+v / %v", got, state, err)
	}
	roster.UserIDs = nil
	prepared, err = PrepareCandidate(defs, roster, quota, base)
	if err != nil {
		t.Fatal(err)
	}
	got, _, err = SelectCandidate(prepared, &state)
	if err != nil || got.Mint.Kind != domain.DestCandidateEmpty || state.FallbackReason != "over_limit" || state.OverLimit == nil {
		t.Fatalf("removing ineligible clients cleared over-limit state: %+v / %v", got, err)
	}
}
