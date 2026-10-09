package nodesync

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-protocol/protocol"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

type serializedPolicyRetrier struct {
	entered chan struct{}
	release chan struct{}
	retries chan string
}

func (p *serializedPolicyRetrier) ObserveStatus(context.Context, string, *protocol.PolicyStatus, []string) error {
	return nil
}
func (p *serializedPolicyRetrier) Compile(ctx context.Context, _ *domain.NodeAgent, _ *ports.NativeDesiredSnapshot, _ []string, _ protocol.ConfigBody) (ports.DestPolicyCandidate, error) {
	close(p.entered)
	select {
	case <-p.release:
		return policySyncCandidate(), nil
	case <-ctx.Done():
		return ports.DestPolicyCandidate{}, ctx.Err()
	}
}
func (p *serializedPolicyRetrier) RetryDestinationPolicy(_ context.Context, id string) (bool, error) {
	p.retries <- id
	return true, nil
}

func TestDestinationRetrySerializesWithWholeSyncAndKeepsOtherAgentsConcurrent(t *testing.T) {
	f := newConfigAppliedFixture(t)
	p := &serializedPolicyRetrier{entered: make(chan struct{}), release: make(chan struct{}), retries: make(chan string, 3)}
	f.service.policies = p
	f.service.policyCandidates = f.repos.NodeAgent.(ports.NodePolicyCandidateRepo)
	synced := make(chan error, 1)
	go func() { _, err := f.service.Sync(t.Context(), policySyncReport(f)); synced <- err }()
	select {
	case <-p.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("sync did not enter compiler")
	}
	defer func() {
		select {
		case <-p.release:
		default:
			close(p.release)
		}
	}()
	retried := make(chan error, 1)
	go func() { _, err := f.service.RetryDestinationPolicy(t.Context(), f.agent.AgentID); retried <- err }()
	if changed, err := f.service.RetryDestinationPolicy(t.Context(), "agt_another"); err != nil || !changed {
		t.Fatal("another agent was blocked by active sync")
	}
	if id := <-p.retries; id != "agt_another" {
		t.Fatal("retry crossed its active agent sync owner")
	}
	select {
	case <-p.retries:
		t.Fatal("same-agent retry crossed observe/compile/mint")
	case <-time.After(30 * time.Millisecond):
	}
	close(p.release)
	select {
	case err := <-synced:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("sync did not finish")
	}
	select {
	case err := <-retried:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("retry did not run after sync")
	}
	if id := <-p.retries; id != f.agent.AgentID {
		t.Fatal("retry used another agent identity")
	}
}

func TestDestinationRetryChecksCancellationAfterSyncOwnerWait(t *testing.T) {
	f := newConfigAppliedFixture(t)
	p := &serializedPolicyRetrier{retries: make(chan string, 1)}
	f.service.policies = p
	unlock := f.service.agentLocks.Lock(f.agent.AgentID)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := f.service.RetryDestinationPolicy(ctx, f.agent.AgentID); done <- err }()
	select {
	case <-done:
		t.Fatal("retry did not wait for its sync owner")
	case <-time.After(30 * time.Millisecond):
	}
	cancel()
	unlock()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal("canceled retry mutated state")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("retry did not return")
	}
	select {
	case <-p.retries:
		t.Fatal("canceled retry reached persistence")
	default:
	}
	if _, err := (&Service{}).RetryDestinationPolicy(t.Context(), "agt_test"); !errors.Is(err, domain.ErrUnavailable) {
		t.Fatal("legacy service pretended to support policy retry")
	}
}
