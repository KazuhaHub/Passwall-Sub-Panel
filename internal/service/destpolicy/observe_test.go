package destpolicy

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-protocol/protocol"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

func observedCandidate(t *testing.T, kind domain.DestCandidateKind) (domain.DestAgentPolicy, time.Time) {
	t.Helper()
	now := time.UnixMilli(1791000000000).UTC()
	p := &protocol.DestinationPolicy{Rules: []protocol.DestinationRule{{ID: "g8", Action: protocol.RuleBlock, CatchAll: true, Subjects: []protocol.SubjectKey{"usr_2"}}}}
	body, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	return domain.DestAgentPolicy{AgentID: "agt_observe", MintedKind: kind, MintedGeneration: 7, MintedContext: strings.Repeat("a", 64), MintedAt: &now, MintedBody: body, MintedSHA256: protocol.PolicyDigest(p), DesiredSHA256: protocol.PolicyDigest(p)}, now
}
func observedCaps() []string { return []string{protocol.CapabilityDestinationPolicy} }

func TestObservePromotesOnlyMatchingExactCandidateAndIsIdle(t *testing.T) {
	for _, kind := range []domain.DestCandidateKind{domain.DestCandidateDesired, domain.DestCandidateFallback} {
		t.Run(string(kind), func(t *testing.T) {
			state, now := observedCandidate(t, kind)
			state.FallbackReason = "rejected"
			state.RejectedGeneration = 7
			status := &protocol.PolicyStatus{State: "applied", Digest: state.MintedSHA256}
			changed, err := ApplyPolicyStatus(&state, status, observedCaps(), now.Add(time.Minute))
			if err != nil || !changed || !bytes.Equal(state.AppliedBody, state.MintedBody) || state.AppliedSHA256 != state.MintedSHA256 || state.AppliedRuleCount != 1 || !reflect.DeepEqual(state.AppliedGroups, []int64{8}) {
				t.Fatalf("exact candidate not promoted: %+v / %v", state, err)
			}
			if kind == domain.DestCandidateDesired && (state.FallbackReason != "" || state.RejectedGeneration != 0) {
				t.Fatal("desired apply did not clear fallback")
			}
			if kind == domain.DestCandidateFallback && (state.FallbackReason != "rejected" || state.RejectedGeneration != 7) {
				t.Fatal("fallback success was treated as desired success")
			}
			first := state
			changed, err = ApplyPolicyStatus(&state, status, observedCaps(), now.Add(2*time.Minute))
			if err != nil || changed || !reflect.DeepEqual(state, first) {
				t.Fatalf("idle observation wrote timestamps/state: %+v / %v", state, err)
			}
		})
	}
}
func TestObserveFallbackRejectionUsesMintedDigestAfterPruning(t *testing.T) {
	state, now := observedCandidate(t, domain.DestCandidateFallback)
	state.AppliedSHA256, state.AppliedBody = strings.Repeat("b", 64), []byte("old-prior-to-pruning")
	state.FallbackReason = "sniffing"
	changed, err := ApplyPolicyStatus(&state, &protocol.PolicyStatus{State: "rejected", Digest: state.MintedSHA256, IssueCode: protocol.IssueDestinationPolicyRejected}, observedCaps(), now.Add(time.Minute))
	if err != nil || !changed || !state.FallbackExhausted || state.AppliedSHA256 != "" || len(state.AppliedBody) != 0 {
		t.Fatalf("pruned rejection did not exhaust LKG: %+v / %v", state, err)
	}
	first := state
	changed, err = ApplyPolicyStatus(&state, &protocol.PolicyStatus{State: "rejected", Digest: state.MintedSHA256, IssueCode: protocol.IssueDestinationPolicyRejected}, observedCaps(), now.Add(2*time.Minute))
	if err != nil || changed || !reflect.DeepEqual(state, first) {
		t.Fatalf("repeated rejection changed exhausted state: %+v / %v", state, err)
	}
}
func TestObserveDesiredRejectionRetainsPriorLKGAndActualGeneration(t *testing.T) {
	state, now := observedCandidate(t, domain.DestCandidateDesired)
	state.AppliedSHA256, state.AppliedBody = strings.Repeat("b", 64), []byte("prior")
	changed, err := ApplyPolicyStatus(&state, &protocol.PolicyStatus{State: "rejected", Digest: state.MintedSHA256, IssueCode: protocol.IssueDestinationPolicyRejected}, observedCaps(), now)
	if err != nil || !changed || state.FallbackReason != "rejected" || state.RejectedGeneration != 7 || state.AppliedSHA256 != strings.Repeat("b", 64) || string(state.AppliedBody) != "prior" {
		t.Fatalf("desired rejection state=%+v err=%v", state, err)
	}
}
func TestObserveStaleStatusRecordsObservationWithoutPromotingOrRejecting(t *testing.T) {
	for _, value := range []string{"applied", "rejected"} {
		state, now := observedCandidate(t, domain.DestCandidateDesired)
		status := &protocol.PolicyStatus{State: value, Digest: strings.Repeat("b", 64)}
		if value == "rejected" {
			status.IssueCode = protocol.IssueDestinationPolicyRejected
		}
		changed, err := ApplyPolicyStatus(&state, status, observedCaps(), now)
		if err != nil || !changed || state.ReportedSHA256 != status.Digest || state.AppliedSHA256 != "" || state.FallbackReason != "" || state.RejectedGeneration != 0 {
			t.Fatalf("stale status changed decisions: %+v / %v", state, err)
		}
	}
}
func TestObservePausedEmptyAndCollectionOnlyCannotReplaceLKG(t *testing.T) {
	for _, kind := range []domain.DestCandidateKind{domain.DestCandidatePaused, domain.DestCandidateEmpty, domain.DestCandidateDesired} {
		state, now := observedCandidate(t, kind)
		state.AppliedSHA256, state.AppliedBody = strings.Repeat("b", 64), []byte("prior")
		p := &protocol.DestinationPolicy{Collect: protocol.CollectHitsAndUsage, CollectRevision: 4}
		state.MintedBody, _ = json.Marshal(p)
		state.MintedSHA256 = protocol.PolicyDigest(p)
		changed, err := ApplyPolicyStatus(&state, &protocol.PolicyStatus{State: "applied", Digest: state.MintedSHA256}, observedCaps(), now)
		if err != nil || !changed || state.AppliedSHA256 != strings.Repeat("b", 64) || string(state.AppliedBody) != "prior" {
			t.Fatalf("%s overwrote LKG: %+v / %v", kind, state, err)
		}
	}
}
func TestObserveIgnoresInvalidOrUnsupportedStatus(t *testing.T) {
	state, now := observedCandidate(t, domain.DestCandidateDesired)
	original := state
	for _, tc := range []struct {
		s    *protocol.PolicyStatus
		caps []string
	}{{nil, observedCaps()}, {&protocol.PolicyStatus{State: "applied", Digest: state.MintedSHA256}, nil}, {&protocol.PolicyStatus{State: "wrong"}, observedCaps()}} {
		changed, err := ApplyPolicyStatus(&state, tc.s, tc.caps, now)
		if err != nil || changed || !reflect.DeepEqual(state, original) {
			t.Fatalf("invalid status changed state: %+v / %v", state, err)
		}
	}
}
func TestObserveCorruptMatchingCandidateReturnsStorageErrorWithoutMutation(t *testing.T) {
	state, now := observedCandidate(t, domain.DestCandidateDesired)
	state.MintedBody = []byte(`{}`)
	original := state
	changed, err := ApplyPolicyStatus(&state, &protocol.PolicyStatus{State: "applied", Digest: state.MintedSHA256}, observedCaps(), now)
	if !errors.Is(err, domain.ErrUnavailable) || changed || !reflect.DeepEqual(state, original) {
		t.Fatalf("corruption promoted or silently emptied: %+v / %v", state, err)
	}
}
