package destpolicy

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"sync"
	"time"

	"github.com/KazuhaHub/passwall-protocol/protocol"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/boundedcache"
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
	definitions            CompilerDefinitions
	runtime                ports.DestAgentPolicyRepo
	inputs                 CompilerInputs
	minSeconds             func(context.Context) (int, error)
	now                    func() time.Time
	publisher              *Publisher
	observer               *Observer
	invalidate             func(string)
	allowlistResync        func(context.Context, string)
	publicationMu          sync.Mutex
	publicationCached      bool
	publicationDefinitions domain.DestDefinitions
	prepare                func(domain.DestDefinitions, RosterInput, RosterInput, protocol.ConfigBody) (*PreparedCandidate, error)
	selectCandidate        func(*PreparedCandidate, *domain.DestAgentPolicy) (ports.DestPolicyCandidate, bool, error)
	preparedCache          *boundedcache.Cache[*PreparedCandidate]
	selectionCache         *boundedcache.Cache[cachedSelection]
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
	c.prepare, c.selectCandidate = PrepareCandidate, SelectCandidate
	c.preparedCache = boundedcache.New[*PreparedCandidate](64, 32<<20)
	c.selectionCache = boundedcache.New[cachedSelection](64, 32<<20)
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

// SetAllowlistResyncer receives an agent identity only after a committed change
// of allowlist eligibility. Assembly resolves the panel, invalidates its cache
// and enqueues asynchronous member resync; nil leaves periodic heal as fallback.
func (c *Compiler) SetAllowlistResyncer(resync func(context.Context, string)) {
	c.allowlistResync = resync
	c.observer.SetAllowlistResyncer(resync)
}
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
	publicationErr := c.publisher.EnsurePublished(ctx, minimum, false)
	defs, state, err := c.publishedDefinitions(ctx)
	if err != nil {
		return ports.DestPolicyCandidate{}, err
	}
	if publicationErr != nil && !state.Paused {
		return ports.DestPolicyCandidate{}, publicationErr
	}
	if defs.State.Paused != state.Paused {
		if err := c.publisher.EnsurePublished(ctx, minimum, true); err != nil {
			if !state.Paused {
				return ports.DestPolicyCandidate{}, err
			}
		} else {
			defs, state, err = c.publishedDefinitions(ctx)
			if err != nil {
				return ports.DestPolicyCandidate{}, err
			}
		}
		// Invalid new definitions retain the prior snapshot, but cannot veto
		// the operator's emergency pause or resume of that valid snapshot.
		// A failed snapshot write likewise cannot veto a saved emergency pause.
		// publishedDefinitions above still verifies the durable prior snapshot;
		// missing/corrupt publication storage never becomes a fabricated policy.
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
	prepared, inputKey, err := c.prepareCached(agent.AgentID, agent.PanelID, defs, roster, quota, base)
	if err != nil {
		return ports.DestPolicyCandidate{}, err
	}
	var result ports.DestPolicyCandidate
	var beforeKey, afterKey string
	var pending *cachedSelection
	resync := false
	changed, err := c.runtime.Update(ctx, agent.AgentID, c.now().UTC(), func(runtime *domain.DestAgentPolicy, loadBodies func() error) (bool, error) {
		pending = nil
		resync = false
		blocked := fallbackBlocksAllowlist(*runtime)
		key := selectionKey(inputKey, *runtime)
		if key != "" {
			if cached, found := c.selectionCache.Get(key); found {
				changed := cached.decision.apply(runtime)
				result = cached.candidate
				resync = blocked != fallbackBlocksAllowlist(*runtime)
				return changed, nil
			}
		}
		candidate, changed, err := c.selectCandidate(prepared, runtime)
		if errors.Is(err, errPolicyBodiesRequired) {
			if err := loadBodies(); err != nil {
				return false, err
			}
			candidate, changed, err = c.selectCandidate(prepared, runtime)
			if errors.Is(err, errPolicyBodiesRequired) {
				return false, fmt.Errorf("%w: missing exact confirmed policy body", domain.ErrUnavailable)
			}
		}
		if err == nil {
			result = candidate
			resync = blocked != fallbackBlocksAllowlist(*runtime)
			if key != "" {
				beforeKey, afterKey = key, selectionKey(inputKey, *runtime)
				result.CacheKey = afterKey
				value := cachedSelection{candidate: cloneCandidate(result), decision: cloneDecision(candidateDecision(*runtime))}
				pending = &value
			}
		}
		return changed, err
	})
	if err != nil {
		return ports.DestPolicyCandidate{}, err
	}
	if pending != nil {
		weight := policyWeight(pending.candidate.Policy) + 512
		for _, listener := range pending.decision.Listeners {
			weight += len(listener) + 32
		}
		c.selectionCache.Put(beforeKey, *pending, weight)
		if afterKey != beforeKey {
			c.selectionCache.Put(afterKey, *pending, weight)
		}
	}
	if changed && c.invalidate != nil {
		c.invalidate(agent.AgentID)
	}
	if resync && c.allowlistResync != nil {
		c.allowlistResync(ctx, agent.AgentID)
	}
	return cloneCandidate(result), nil
}

func (c *Compiler) publishedDefinitions(ctx context.Context) (domain.DestDefinitions, domain.DestPolicyState, error) {
	state, err := c.definitions.State(ctx)
	if err != nil {
		return domain.DestDefinitions{}, domain.DestPolicyState{}, err
	}
	if state.Generation < 0 || state.PublishedGeneration < 0 || state.PublishedGeneration > state.Generation {
		return domain.DestDefinitions{}, domain.DestPolicyState{}, fmt.Errorf("%w: invalid destination publication state", domain.ErrUnavailable)
	}
	// Publication generations identify immutable executable snapshots. Read the
	// small live state on every call so pause/publication changes stay visible,
	// but load and decode the selected body only once per generation. Serialize
	// cold loads to avoid one multi-MiB read per concurrent node. Cached slices
	// stay private to read-only compiler helpers; only the value state is edited.
	c.publicationMu.Lock()
	defer c.publicationMu.Unlock()
	if c.publicationCached && c.publicationDefinitions.State.PublishedGeneration == state.PublishedGeneration {
		return c.publicationDefinitions, state, nil
	}
	state, snapshot, found, err := c.definitions.PublishedState(ctx)
	if err != nil {
		return domain.DestDefinitions{}, domain.DestPolicyState{}, err
	}
	if !found {
		if state.PublishedGeneration != 0 {
			return domain.DestDefinitions{}, state, fmt.Errorf("%w: missing destination snapshot", domain.ErrUnavailable)
		}
		c.publicationDefinitions = domain.DestDefinitions{}
		c.publicationCached = true
		return domain.DestDefinitions{}, state, nil
	}
	defs, err := DecodeDefinitionSnapshot(snapshot)
	if err != nil {
		return domain.DestDefinitions{}, state, err
	}
	if defs.State.Generation != state.PublishedGeneration {
		return domain.DestDefinitions{}, state, fmt.Errorf("%w: mismatched destination snapshot", domain.ErrUnavailable)
	}
	c.publicationDefinitions = defs
	c.publicationCached = true
	return defs, state, nil
}

var _ ports.DestPolicyCompiler = (*Compiler)(nil)
