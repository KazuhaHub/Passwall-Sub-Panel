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
	// CacheKey is produced by the compiler for immutable compiled output.
	// Empty disables canonical-config caching. Source minting is never skipped.
	CacheKey string
}

// UserMembershipRepo reads only identity/group columns. Quota membership includes
// disabled members and is independent of roster presence and node eligibility.
type UserMembershipRepo interface {
	GroupIDsByIDs(context.Context, []int64) (map[int64]int64, error)
	MembersByGroupIDs(context.Context, []int64) (map[int64][]int64, error)
}

type PanelAuditSettings struct {
	Collect  domain.AuditCollect
	Revision uint64
}
type PanelAuditSettingsRepo interface {
	GetAuditSettings(context.Context, int64) (PanelAuditSettings, error)
}

// DestinationEligibilityRepo projects only mode and enforcement facts. Readers
// must return errors for corrupt or failed reads, never convert them to a verdict.
type DestinationEligibilityRepo interface {
	GroupEligibilityMode(context.Context, int64) (string, error)
	PanelDestinationEligibility(context.Context, int64) (DestinationPanelEligibility, error)
}

type DestinationPanelEligibility struct {
	Native, PolicyCapable, FallbackBlocked bool
}

type GroupNodeEligibility interface {
	Eligible(context.Context, *domain.Node, *domain.Group) (bool, error)
}
