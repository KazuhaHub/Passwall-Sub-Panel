package destpolicy

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-protocol/protocol"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

type fallbackIssueStore struct {
	ports.NodeAgentIssueRepo
	calls  int
	fail   bool
	issues []domain.NodeAgentIssue
}

func (s *fallbackIssueStore) RecordBatch(_ context.Context, agent string, issues []domain.NodeAgentIssue, _ time.Time) error {
	s.calls++
	if s.fail {
		return domain.ErrUnavailable
	}
	for _, issue := range issues {
		if issue.AgentID != agent {
			return domain.ErrValidation
		}
		s.issues = append(s.issues, issue)
	}
	return nil
}

func TestObserverRecordsOnlyMatchingCommittedFallbackRejection(t *testing.T) {
	for _, name := range []string{"matching", "pruned", "stale", "desired", "applied", "unsupported", "invalid", "rollback"} {
		t.Run(name, func(t *testing.T) {
			state, now := observedCandidate(t, domain.DestCandidateFallback)
			state.AppliedSHA256 = state.MintedSHA256
			status := &protocol.PolicyStatus{State: "rejected", Digest: state.MintedSHA256, IssueCode: protocol.IssueDestinationPolicyRejected}
			caps := observedCaps()
			switch name {
			case "pruned":
				state.AppliedSHA256 = strings.Repeat("b", 64)
			case "stale":
				status.Digest = strings.Repeat("b", 64)
			case "desired":
				state.MintedKind = domain.DestCandidateDesired
			case "applied":
				status.State, status.IssueCode = "applied", ""
			case "unsupported":
				caps = nil
			case "invalid":
				status.Digest = "private-invalid-digest"
			}
			runtime := &cacheCompilerRuntime{state: state, fail: name == "rollback"}
			issues := &fallbackIssueStore{}
			observer := NewObserver(runtime, func() time.Time { return now })
			observer.issues = issues
			err := observer.ObserveStatus(t.Context(), state.AgentID, status, caps)
			if (err != nil) != (name == "rollback") {
				t.Fatalf("unexpected observation error: %v", err)
			}
			want := name == "matching" || name == "pruned"
			if (issues.calls == 1) != want {
				t.Fatalf("issue writes = %d for %s", issues.calls, name)
			}
			if want {
				issue := issues.issues[0]
				if !runtime.state.FallbackExhausted || issue.Code != "destination_policy_lkg_rejected" || issue.Key != state.MintedSHA256 || issue.AgentID != state.AgentID || issue.Detail != "The core rejected the previous access policy; an empty policy is being dispatched." {
					t.Fatal("issue did not identify the exact committed rejected fallback")
				}
			}
		})
	}
}

func TestObserverRetriesIssueFailureWithoutRearmingFallback(t *testing.T) {
	state, now := observedCandidate(t, domain.DestCandidateFallback)
	runtime := &cacheCompilerRuntime{state: state}
	issues := &fallbackIssueStore{fail: true}
	observer := NewObserver(runtime, func() time.Time { return now })
	observer.issues = issues
	status := &protocol.PolicyStatus{State: "rejected", Digest: state.MintedSHA256, IssueCode: protocol.IssueDestinationPolicyRejected}
	if err := observer.ObserveStatus(t.Context(), state.AgentID, status, observedCaps()); !errors.Is(err, domain.ErrUnavailable) || !runtime.state.FallbackExhausted {
		t.Fatal("failed issue persistence lost the committed exhaustion or its error")
	}
	exhausted := runtime.state
	issues.fail = false
	if err := observer.ObserveStatus(t.Context(), state.AgentID, status, observedCaps()); err != nil || issues.calls != 2 || len(issues.issues) != 1 || !reflect.DeepEqual(runtime.state, exhausted) {
		t.Fatal("matching replay did not retry the issue independently of policy state")
	}
	// After the successful issue write, Sync mints empty. Old reports cannot
	// manufacture a new fallback issue or rearm the exhausted candidate.
	runtime.state.MintedKind, runtime.state.MintedSHA256, runtime.state.MintedBody = domain.DestCandidateEmpty, "", nil
	if err := observer.ObserveStatus(t.Context(), state.AgentID, status, observedCaps()); err != nil || issues.calls != 2 || !runtime.state.FallbackExhausted {
		t.Fatal("stale rejected report regenerated an issue or rearmed fallback")
	}
}
