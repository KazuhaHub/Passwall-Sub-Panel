package ports

import (
	"context"

	"github.com/KazuhaHub/passwall-protocol/protocol"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// DestPolicyCompiler owns durable status transitions and compiles only the
// destination subtree. The sync coordinator remains the sole config minter.
// Inputs are read-only and include the node's current reported capabilities.
type DestPolicyCompiler interface {
	ObserveStatus(context.Context, string, *protocol.PolicyStatus, []string) error
	Compile(context.Context, *domain.NodeAgent, *NativeDesiredSnapshot, []string, protocol.ConfigBody) (DestPolicyCandidate, error)
}

type DestPolicyCandidate struct {
	Policy *protocol.DestinationPolicy
	Mint   domain.DestPolicyMint
}
