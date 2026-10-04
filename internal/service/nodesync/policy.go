package nodesync

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"time"

	"github.com/KazuhaHub/passwall-protocol/protocol"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// PolicyCoordinator owns durable status transitions and compiles only the
// destination subtree. The sync coordinator remains the sole config minter.
type PolicyCoordinator interface {
	ObserveStatus(context.Context, string, *protocol.PolicyStatus, []string) error
	Compile(context.Context, *domain.NodeAgent, *ports.NativeDesiredSnapshot, []string, protocol.ConfigBody) (PolicyCandidate, error)
}

type PolicyCandidate struct {
	Policy *protocol.DestinationPolicy
	Mint   domain.DestPolicyMint
}

func (s *Service) mintConfig(ctx context.Context, agent *domain.NodeAgent, snapshot *ports.NativeDesiredSnapshot, capabilities []string, body protocol.ConfigBody, now time.Time) (protocol.ConfigBody, *domain.NodeAgentStream, error) {
	if s.policies == nil {
		stream, err := s.mint(ctx, agent, domain.NodeAgentStreamConfig, body, now)
		return body, stream, err
	}
	if s.policyCandidates == nil {
		return body, nil, fmt.Errorf("%w: missing atomic policy candidate repository", domain.ErrUnavailable)
	}
	candidate, err := s.policies.Compile(ctx, agent, snapshot, capabilities, body)
	if err != nil {
		return body, nil, fmt.Errorf("nodesync: compile destination policy: %w", err)
	}
	if candidate.Policy != nil && !slices.Contains(capabilities, protocol.CapabilityDestinationPolicy) {
		return body, nil, fmt.Errorf("%w: destination policy requires node capability", domain.ErrValidation)
	}
	body.Policy = candidate.Policy
	canonical, err := json.Marshal(body)
	if err != nil {
		return body, nil, fmt.Errorf("nodesync: encode policy config: %w", err)
	}
	stream, _, err := s.policyCandidates.MintConfigWithPolicyCandidate(ctx, agent.AgentID, canonical, candidate.Mint, now)
	if err != nil {
		return body, nil, fmt.Errorf("nodesync: mint config and destination candidate: %w", err)
	}
	return body, stream, nil
}
