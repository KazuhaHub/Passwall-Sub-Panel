// Package destpolicy compiles the access-control definitions (lists, policies,
// exemptions, per-inbound source rules) into the destination policy a native
// node receives in its config, publishes them with a trailing debounce, and
// keeps each node's delivery state: what was minted, what the node reported,
// and the last policy known to have applied, which a node falls back to when a
// new one is refused.
//
// THE SHAPE. A definition write advances a generation (in the same transaction
// as the write, see sqlstore). Publishing snapshots every definition at one
// generation into dest_policy_snapshots; Compile reads the PUBLISHED snapshot,
// never the live rows, so an admin's burst of edits is one change to the
// fleet and a half-finished edit is never sent. Compile then specialises the
// snapshot for one node: who is on it (the roster), which inbounds it has,
// what its inbounds' sniffing allows, whether the node refused the last
// policy. Membership and roster changes are NOT part of the snapshot, so they
// take effect on the node's next sync with no debounce.
//
// WITHOUT A POLICY NOTHING CHANGES. Compile returns nil for a node that has no
// capability, while paused, and while nothing is in force, and a nil policy
// leaves ConfigBody byte-identical to before this feature existed: the config
// ETag does not move and no core restarts. That is the property that lets a
// panel upgrade ship without touching a single node.
package destpolicy

import (
	"context"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/destlist"
)

// Definitions reads every definition at one generation (sqlstore.DestRepos).
type Definitions interface {
	ReadDefinitions(ctx context.Context) (*domain.DestDefinitions, error)
	GroupIDsByUserIDs(ctx context.Context, ids []int64) (map[int64]int64, error)
}

// StateStore is the publish bookkeeping (sqlstore.DestStateRepo).
type StateStore interface {
	Get(ctx context.Context) (*domain.DestPolicyState, error)
	CompletePublish(ctx context.Context, gen, expectedPublished int64) (bool, error)
	SetPaused(ctx context.Context, paused bool) error
	SetPublishError(ctx context.Context, e *domain.DestPublishError, at time.Time) error
	SaveSnapshot(ctx context.Context, gen int64, body []byte) error
	Snapshot(ctx context.Context, gen int64) (*domain.DestPolicySnapshot, error)
	PruneSnapshotsExcept(ctx context.Context, keep ...int64) (int64, error)
}

// AgentStore is the per-agent delivery state (sqlstore.DestAgentPolicyRepo).
type AgentStore interface {
	GetMeta(ctx context.Context, agentID string) (*domain.DestAgentPolicy, error)
	AppliedBody(ctx context.Context, agentID string) ([]byte, error)
	Patch(ctx context.Context, agentID string, patch domain.DestAgentPolicyPatch) error
	List(ctx context.Context) ([]*domain.DestAgentPolicy, error)
}

// GroupMemberCounter counts the accounts of a group that a panel's tags select,
// whether or not they have a client on the panel right now.
type GroupMemberCounter interface {
	TagMatchedMembers(ctx context.Context, panelID, groupID int64) (int, error)
}

// ListExpander turns a list into the entries a rule carries (destlist.Service).
type ListExpander interface {
	Expand(ctx context.Context, l *domain.DestList) (*destlist.Expanded, error)
}

// CountryExpander expands source countries (destlist.CountryExpander).
type CountryExpander interface {
	CIDRs(ctx context.Context, countries []string) ([]string, error)
}

// SettingsReader is the slice of the settings store publication reads.
type SettingsReader interface {
	Load(ctx context.Context, defaults ports.UISettings) (ports.UISettings, error)
}

// Metrics is where the service reports what it did. Every field may be nil.
type Metrics struct {
	// Compile is told the result of each Compile call: "cache_hit",
	// "compiled", "fallback_rejected",
	// "fallback_over_limit", "fallback_nil" or "invalid".
	Compile func(result string)
	// CompileDuration is told how long a full (non-cached) compile took.
	CompileDuration func(d time.Duration)
	// Publish is told the trigger of each publish: "debounced", "max_wait",
	// "manual" or "pause".
	Publish func(trigger string)
	// PublishRejected is told of each publish the pre-publish validation
	// refused.
	PublishRejected func()
}

// Deps is everything a Service is built from. Definitions, State, Agents,
// Lists and Countries are required.
type Deps struct {
	Definitions Definitions
	State       StateStore
	Agents      AgentStore
	Lists       ListExpander
	Countries   CountryExpander
	Settings    SettingsReader
	// GroupMembers counts the members of an allowlist group that a panel's tags
	// select, for the per-node limit. Nil: the roster's members, which is the
	// same until members can be made ineligible for a panel.
	GroupMembers GroupMemberCounter
	// FallbackChanged is called (after the change is stored) when an agent
	// enters or leaves a refused or over-limit fallback, the two that stop a
	// node enforcing a group allowlist. Nil: nobody is told. It must not call
	// back into the Service.
	FallbackChanged func(ctx context.Context, agentID string)
	Metrics         Metrics
	// Now is the clock; nil is time.Now.
	Now func() time.Time
}

// Publish triggers, the metric's label.
const (
	TriggerDebounced = "debounced"
	TriggerMaxWait   = "max_wait"
	TriggerManual    = "manual"
	TriggerPause     = "pause"
)

// Compile results, the metric's label.
const (
	ResultCacheHit        = "cache_hit"
	ResultCompiled        = "compiled"
	ResultFallbackReject  = "fallback_rejected"
	ResultFallbackLimit   = "fallback_over_limit"
	ResultFallbackNil     = "fallback_nil"
	ResultInvalid         = "invalid"
	maxWaitFactor         = 5
	snapshotFormatVersion = 1
)
