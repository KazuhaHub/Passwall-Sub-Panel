package nodesync

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-protocol/protocol"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

type failedCapabilityObservation struct{ ports.NodeAgentRepo }

func (f failedCapabilityObservation) UpdateProtocolObservation(context.Context, string, int, []string, time.Time) error {
	return domain.ErrUnavailable
}

func TestAllowlistResyncUsesOnlyCommittedPolicyCapabilityChanges(t *testing.T) {
	f := newConfigAppliedFixture(t)
	calls := 0
	expectedPolicy := true
	f.service.SetAllowlistResyncer(func(ctx context.Context, panelID int64) {
		calls++
		stored, err := f.repos.NodeAgent.GetByAgentID(ctx, f.agent.AgentID)
		if err != nil || panelID != f.agent.PanelID || slices.Contains(stored.ObservedCapabilities, protocol.CapabilityDestinationPolicy) != expectedPolicy {
			t.Fatalf("resync before observation committed: panel=%d agent=%+v / %v", panelID, stored, err)
		}
	})
	report := policySyncReport(f)
	if _, err := f.service.Sync(t.Context(), report); err != nil || calls != 1 {
		t.Fatalf("capability gained: calls=%d / %v", calls, err)
	}
	// Audit/task/reordering changes do not change allowlist eligibility.
	report.Capabilities = []string{"audit.usage.v1", "host.telemetry.v1", protocol.CapabilityDestinationPolicy}
	report.Partial = true
	if _, err := f.service.Sync(t.Context(), report); err != nil || calls != 1 {
		t.Fatalf("irrelevant capabilities resynced: calls=%d / %v", calls, err)
	}
	expectedPolicy = false
	report.Capabilities = nil
	if _, err := f.service.Sync(t.Context(), report); err != nil || calls != 2 {
		t.Fatalf("capability lost on partial report: calls=%d / %v", calls, err)
	}
	f.service.agents = failedCapabilityObservation{f.service.agents}
	report.Capabilities = []string{protocol.CapabilityDestinationPolicy}
	if _, err := f.service.Sync(t.Context(), report); !errors.Is(err, domain.ErrUnavailable) || calls != 2 {
		t.Fatalf("uncommitted observation resynced: calls=%d / %v", calls, err)
	}
	f.service.agents = f.repos.NodeAgent
	expectedPolicy = true
	if _, err := f.service.Sync(t.Context(), report); err != nil || calls != 3 {
		t.Fatalf("observation recovery: calls=%d / %v", calls, err)
	}
}

func TestAllowlistResyncRetainsCommittedCapabilityChangeWhenLaterSyncFails(t *testing.T) {
	f := newConfigAppliedFixture(t)
	f.service.policyCandidates = f.repos.NodeAgent.(ports.NodePolicyCandidateRepo)
	failure := errors.New("later compiler failure")
	coordinator := &testPolicyCoordinator{
		observe: func(context.Context, string, *protocol.PolicyStatus, []string) error { return nil },
		compile: func(context.Context, *domain.NodeAgent, *ports.NativeDesiredSnapshot, []string, protocol.ConfigBody) (PolicyCandidate, error) {
			return PolicyCandidate{}, failure
		},
	}
	f.service.policies = coordinator
	calls := 0
	f.service.SetAllowlistResyncer(func(context.Context, int64) { calls++ })
	report := policySyncReport(f)
	if _, err := f.service.Sync(t.Context(), report); !errors.Is(err, failure) || calls != 1 {
		t.Fatalf("committed capability lost with later sync error: calls=%d / %v", calls, err)
	}
	coordinator.compile = func(context.Context, *domain.NodeAgent, *ports.NativeDesiredSnapshot, []string, protocol.ConfigBody) (PolicyCandidate, error) {
		return policySyncCandidate(), nil
	}
	if _, err := f.service.Sync(t.Context(), report); err != nil || calls != 1 {
		t.Fatalf("replay duplicated capability transition: calls=%d / %v", calls, err)
	}
}
