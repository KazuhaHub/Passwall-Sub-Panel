package destpolicy

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"time"

	"github.com/KazuhaHub/passwall-protocol/protocol"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

type CompilerDefinitions interface {
	PublicationStore
	PublishedState(context.Context) (domain.DestPolicyState, domain.DestPolicySnapshot, bool, error)
}

// CompilerInputs supplies panel collection settings, membership and independent
// tag-matched quota membership. It must not derive quotas from eligibility.
// includeMembers is false while paused or when the current node lacks policy
// capability; only collection settings are needed in those paths.
type CompilerInputs interface {
	ForNode(ctx context.Context, panelID int64, snapshot *ports.NativeDesiredSnapshot, includeMembers bool) (RosterInput, RosterInput, error)
}

type CompilerOptions struct {
	Definitions CompilerDefinitions
	Runtime     ports.DestAgentPolicyRepo
	Inputs      CompilerInputs
	MinSeconds  func(context.Context) (int, error)
	Now         func() time.Time
}

type Compiler struct {
	definitions CompilerDefinitions
	runtime     ports.DestAgentPolicyRepo
	inputs      CompilerInputs
	minSeconds  func(context.Context) (int, error)
	now         func() time.Time
	publisher   *Publisher
	observer    *Observer
	invalidate  func(string)
}

func NewCompiler(options CompilerOptions) (*Compiler, error) {
	if options.Definitions == nil || options.Runtime == nil || options.Inputs == nil {
		return nil, domain.ErrValidation
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.MinSeconds == nil {
		options.MinSeconds = func(context.Context) (int, error) { return 60, nil }
	}
	c := &Compiler{definitions: options.Definitions, runtime: options.Runtime, inputs: options.Inputs, minSeconds: options.MinSeconds, now: options.Now}
	c.publisher = NewPublisher(options.Definitions, nil)
	c.publisher.now = options.Now
	c.observer = NewObserver(options.Runtime, options.Now)
	c.observer.SetInvalidator(func(agentID string) {
		if c.invalidate != nil {
			c.invalidate(agentID)
		}
	})
	return c, nil
}

func (c *Compiler) SetInvalidator(invalidate func(string)) { c.invalidate = invalidate }
func (c *Compiler) ObserveStatus(ctx context.Context, agentID string, status *protocol.PolicyStatus, caps []string) error {
	if c == nil || c.observer == nil {
		return domain.ErrUnavailable
	}
	return c.observer.ObserveStatus(ctx, agentID, status, caps)
}

func (c *Compiler) Compile(ctx context.Context, agent *domain.NodeAgent, snapshot *ports.NativeDesiredSnapshot, caps []string, base protocol.ConfigBody) (ports.DestPolicyCandidate, error) {
	if c == nil || c.publisher == nil {
		return ports.DestPolicyCandidate{}, domain.ErrUnavailable
	}
	if agent == nil || snapshot == nil || agent.AgentID == "" || agent.PanelID <= 0 {
		return ports.DestPolicyCandidate{}, domain.ErrValidation
	}
	minimum, err := c.minSeconds(ctx)
	if err != nil {
		return ports.DestPolicyCandidate{}, err
	}
	if err := c.publisher.EnsurePublished(ctx, minimum, false); err != nil {
		return ports.DestPolicyCandidate{}, err
	}
	defs, state, err := c.publishedDefinitions(ctx)
	if err != nil {
		return ports.DestPolicyCandidate{}, err
	}
	if defs.State.Paused != state.Paused {
		if err := c.publisher.EnsurePublished(ctx, minimum, true); err != nil {
			return ports.DestPolicyCandidate{}, err
		}
		defs, state, err = c.publishedDefinitions(ctx)
		if err != nil {
			return ports.DestPolicyCandidate{}, err
		}
		// Invalid new definitions retain the prior snapshot, but cannot veto
		// the operator's emergency pause or resume of that valid snapshot.
		defs.State.Paused = state.Paused
	}
	roster, quota, err := c.inputs.ForNode(ctx, agent.PanelID, snapshot, !defs.State.Paused && slices.Contains(caps, protocol.CapabilityDestinationPolicy))
	if err != nil {
		return ports.DestPolicyCandidate{}, err
	}
	roster.Capabilities = append([]string(nil), caps...)
	roster.UserIDs = nil
	for _, client := range snapshot.Clients {
		if client.Client == nil || client.Client.UserID <= 0 {
			return ports.DestPolicyCandidate{}, fmt.Errorf("%w: invalid native policy roster", domain.ErrUnavailable)
		}
		roster.UserIDs = append(roster.UserIDs, client.Client.UserID)
	}
	// Every actual roster subject must also exist in the independent quota
	// candidate; tag-matched members can additionally survive roster removal.
	quota.UserIDs = append(append([]int64(nil), quota.UserIDs...), roster.UserIDs...)
	quota.UserGroups = maps.Clone(quota.UserGroups)
	if quota.UserGroups == nil {
		quota.UserGroups = map[int64]int64{}
	}
	for id, groupID := range roster.UserGroups {
		quota.UserGroups[id] = groupID
	}
	prepared, err := PrepareCandidate(defs, roster, quota, base)
	if err != nil {
		return ports.DestPolicyCandidate{}, err
	}
	var result ports.DestPolicyCandidate
	changed, err := c.runtime.Update(ctx, agent.AgentID, c.now().UTC(), func(runtime *domain.DestAgentPolicy, loadBodies func() error) (bool, error) {
		candidate, changed, err := SelectCandidate(prepared, runtime)
		if errors.Is(err, errPolicyBodiesRequired) {
			if err := loadBodies(); err != nil {
				return false, err
			}
			candidate, changed, err = SelectCandidate(prepared, runtime)
			if errors.Is(err, errPolicyBodiesRequired) {
				return false, fmt.Errorf("%w: missing exact confirmed policy body", domain.ErrUnavailable)
			}
		}
		if err == nil {
			result = candidate
		}
		return changed, err
	})
	if err != nil {
		return ports.DestPolicyCandidate{}, err
	}
	if changed && c.invalidate != nil {
		c.invalidate(agent.AgentID)
	}
	return result, nil
}

func (c *Compiler) publishedDefinitions(ctx context.Context) (domain.DestDefinitions, domain.DestPolicyState, error) {
	state, snapshot, found, err := c.definitions.PublishedState(ctx)
	if err != nil {
		return domain.DestDefinitions{}, domain.DestPolicyState{}, err
	}
	if !found {
		return domain.DestDefinitions{}, state, nil
	}
	defs, err := DecodeDefinitionSnapshot(snapshot)
	return defs, state, err
}

var _ ports.DestPolicyCompiler = (*Compiler)(nil)
