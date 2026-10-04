package nodesync

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/KazuhaHub/passwall-protocol/protocol"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

type PolicyCoordinator = ports.DestPolicyCompiler
type PolicyCandidate = ports.DestPolicyCandidate

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
	canonical, err := s.encodePolicyConfig(body, candidate.CacheKey)
	if err != nil {
		return body, nil, fmt.Errorf("nodesync: encode policy config: %w", err)
	}
	stream, _, err := s.policyCandidates.MintConfigWithPolicyCandidate(ctx, agent.AgentID, canonical, candidate.Mint, now)
	if err != nil {
		return body, nil, fmt.Errorf("nodesync: mint config and destination candidate: %w", err)
	}
	return body, stream, nil
}
