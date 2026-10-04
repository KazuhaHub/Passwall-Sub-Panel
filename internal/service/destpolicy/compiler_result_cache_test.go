package destpolicy

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-protocol/protocol"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

type cacheCompilerInputs struct {
	generation uint64
	tracked    bool
	collect    domain.AuditCollect
	revision   uint64
}

func (i *cacheCompilerInputs) ForNode(context.Context, int64, *ports.NativeDesiredSnapshot, bool) (RosterInput, RosterInput, error) {
	input := RosterInput{UserGroups: map[int64]int64{2: 8}, UserIDs: []int64{2}, Collect: i.collect, CollectRevision: i.revision, MembershipGeneration: i.generation, MembershipTracked: i.tracked}
	return input, input, nil
}

type cacheCompilerRuntime struct {
	ports.DestAgentPolicyRepo
	mu           sync.Mutex
	state        domain.DestAgentPolicy
	body         []byte
	calls, loads int
	fail         bool
}

func (r *cacheCompilerRuntime) Update(_ context.Context, _ string, _ time.Time, fn func(*domain.DestAgentPolicy, func() error) (bool, error)) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls++
	state := r.state
	state.AppliedBody = nil
	changed, err := fn(&state, func() error { r.loads++; state.AppliedBody = append([]byte(nil), r.body...); return nil })
	if err != nil {
		return false, err
	}
	if r.fail {
		return false, domain.ErrUnavailable
	}
	state.AppliedBody = nil
	r.state = state
	return changed, nil
}
func cachedCompilerFixture(t *testing.T) (*Compiler, *cacheCompilerInputs, *cacheCompilerRuntime, *domain.NodeAgent, *ports.NativeDesiredSnapshot, protocol.ConfigBody) {
	t.Helper()
	defs := domain.DestDefinitions{State: domain.DestPolicyState{Generation: 1, PublishedGeneration: 1}, Policies: []domain.DestPolicy{{ID: 1, Enabled: true, Action: domain.DestBlock, Scope: domain.DestScopeAll, Inline: domain.DestInline{Ports: "443"}}}}
	body, err := BuildDefinitionSnapshot(defs)
	if err != nil {
		t.Fatal(err)
	}
	store := &cachedPublicationStore{state: defs.State, snapshot: domain.DestPolicySnapshot{Generation: 1, Body: body}}
	inputs, runtime := &cacheCompilerInputs{tracked: true, collect: domain.AuditCollectOff, revision: 1}, &cacheCompilerRuntime{}
	c, err := NewCompiler(CompilerOptions{Definitions: store, Runtime: runtime, Inputs: inputs})
	if err != nil {
		t.Fatal(err)
	}
	return c, inputs, runtime, &domain.NodeAgent{AgentID: "cache-agent", PanelID: 81}, &ports.NativeDesiredSnapshot{Clients: []ports.NativeDesiredClient{{Client: &domain.PSPClient{UserID: 2}}}}, protocol.ConfigBody{}
}

func TestCompilerResultCacheAvoidsRebuildAndSelectionButStillChecksRuntime(t *testing.T) {
	c, _, runtime, agent, snapshot, base := cachedCompilerFixture(t)
	prepares, selects := 0, 0
	c.prepare = func(defs domain.DestDefinitions, r, q RosterInput, b protocol.ConfigBody) (*PreparedCandidate, error) {
		prepares++
		return PrepareCandidate(defs, r, q, b)
	}
	c.selectCandidate = func(p *PreparedCandidate, s *domain.DestAgentPolicy) (ports.DestPolicyCandidate, bool, error) {
		selects++
		return SelectCandidate(p, s)
	}
	first, err := c.Compile(t.Context(), agent, snapshot, []string{protocol.CapabilityDestinationPolicy}, base)
	if err != nil {
		t.Fatal(err)
	}
	first.Policy.Rules[0].Ports = "80"
	again, err := c.Compile(t.Context(), agent, snapshot, []string{protocol.CapabilityDestinationPolicy}, base)
	if err != nil || prepares != 1 || selects != 1 || runtime.calls != 2 || again.Policy.Rules[0].Ports != "443" {
		t.Fatalf("idle rebuild/alias/runtime check: prepares=%d selects=%d runtime=%d candidate=%+v / %v", prepares, selects, runtime.calls, again.Policy, err)
	}
	runtime.fail = true
	if _, err := c.Compile(t.Context(), agent, snapshot, []string{protocol.CapabilityDestinationPolicy}, base); !errors.Is(err, domain.ErrUnavailable) {
		t.Fatalf("cached result bypassed durable runtime failure: %v", err)
	}
}

func TestCompilerResultCacheInvalidatesMembershipContextAndRuntimeDecision(t *testing.T) {
	c, inputs, runtime, agent, snapshot, base := cachedCompilerFixture(t)
	prepares := 0
	c.prepare = func(d domain.DestDefinitions, r, q RosterInput, b protocol.ConfigBody) (*PreparedCandidate, error) {
		prepares++
		return PrepareCandidate(d, r, q, b)
	}
	first, err := c.Compile(t.Context(), agent, snapshot, []string{protocol.CapabilityDestinationPolicy}, base)
	if err != nil {
		t.Fatal(err)
	}
	runtime.state.RejectedGeneration, runtime.state.RejectedContext = 1, first.Mint.Context
	runtime.state.FallbackReason = "rejected"
	empty, err := c.Compile(t.Context(), agent, snapshot, []string{protocol.CapabilityDestinationPolicy}, base)
	if err != nil || empty.Policy != nil || prepares != 1 {
		t.Fatalf("runtime rejection reused desired or rebuilt pure input: %+v prepares=%d / %v", empty, prepares, err)
	}
	inputs.generation++
	if _, err := c.Compile(t.Context(), agent, snapshot, []string{protocol.CapabilityDestinationPolicy}, base); err != nil || prepares != 2 {
		t.Fatalf("membership cache key: %d / %v", prepares, err)
	}
	base.Core.Version = "26.9.30"
	retry, err := c.Compile(t.Context(), agent, snapshot, []string{protocol.CapabilityDestinationPolicy}, base)
	if err != nil || retry.Policy == nil || reflect.DeepEqual(retry.Mint, first.Mint) || prepares != 3 {
		t.Fatalf("context retry cache key: %+v prepares=%d / %v", retry, prepares, err)
	}
	inputs.tracked = false
	for range 2 {
		if _, err := c.Compile(t.Context(), agent, snapshot, []string{protocol.CapabilityDestinationPolicy}, base); err != nil {
			t.Fatal(err)
		}
	}
	if prepares != 5 {
		t.Fatalf("untracked membership reused cached output: %d", prepares)
	}
}

func TestCompilerResultCacheDoesNotRetainUncommittedSelection(t *testing.T) {
	c, _, runtime, agent, snapshot, base := cachedCompilerFixture(t)
	selects := 0
	c.selectCandidate = func(p *PreparedCandidate, s *domain.DestAgentPolicy) (ports.DestPolicyCandidate, bool, error) {
		selects++
		return SelectCandidate(p, s)
	}
	runtime.fail = true
	if _, err := c.Compile(t.Context(), agent, snapshot, []string{protocol.CapabilityDestinationPolicy}, base); !errors.Is(err, domain.ErrUnavailable) {
		t.Fatalf("commit failure ignored: %v", err)
	}
	runtime.fail = false
	for range 2 {
		if _, err := c.Compile(t.Context(), agent, snapshot, []string{protocol.CapabilityDestinationPolicy}, base); err != nil {
			t.Fatal(err)
		}
	}
	if selects != 2 {
		t.Fatalf("uncommitted decision retained or committed result not cached: %d", selects)
	}
}

func TestCompilerResultCacheTracksCollectionRevisionCapabilitiesAndRoster(t *testing.T) {
	c, inputs, _, agent, snapshot, base := cachedCompilerFixture(t)
	base.Core.Engine = "xray"
	inputs.collect = domain.AuditCollectHitsAndUsage
	caps := []string{protocol.CapabilityDestinationPolicy, "audit.hits.v1", "audit.usage.v1"}
	first, err := c.Compile(t.Context(), agent, snapshot, caps, base)
	if err != nil || first.Policy.Collect != protocol.CollectHitsAndUsage {
		t.Fatalf("initial collection: %+v / %v", first, err)
	}
	inputs.revision++
	changed, err := c.Compile(t.Context(), agent, snapshot, caps, base)
	if err != nil || changed.Policy.CollectRevision != inputs.revision || changed.CacheKey == first.CacheKey {
		t.Fatalf("revision reused cache: %+v / %v", changed, err)
	}
	caps = caps[:2]
	hits, err := c.Compile(t.Context(), agent, snapshot, caps, base)
	if err != nil || hits.Policy.Collect != protocol.CollectHits || hits.CacheKey == changed.CacheKey {
		t.Fatalf("capability downgrade reused cache: %+v / %v", hits, err)
	}
	snapshot.Clients = nil
	empty, err := c.Compile(t.Context(), agent, snapshot, caps, base)
	if err != nil || empty.CacheKey == hits.CacheKey {
		t.Fatalf("roster removal reused cache: %+v / %v", empty, err)
	}
	inputs.collect = domain.AuditCollectOff
	off, err := c.Compile(t.Context(), agent, snapshot, caps, base)
	if err != nil || (off.Policy != nil && off.Policy.Collect != "") || off.CacheKey == empty.CacheKey {
		t.Fatalf("collection off reused cache: %+v / %v", off, err)
	}
}

func TestCompilerResultCachePrunesLKGOnceAndInvalidatesConfirmedDigest(t *testing.T) {
	c, _, runtime, agent, snapshot, base := cachedCompilerFixture(t)
	caps := []string{protocol.CapabilityDestinationPolicy}
	desired, err := c.Compile(t.Context(), agent, snapshot, caps, base)
	if err != nil {
		t.Fatal(err)
	}
	runtime.body, err = json.Marshal(desired.Policy)
	if err != nil {
		t.Fatal(err)
	}
	runtime.state.AppliedSHA256 = protocol.PolicyDigest(desired.Policy)
	runtime.state.RejectedGeneration, runtime.state.RejectedContext = 1, desired.Mint.Context
	runtime.state.FallbackReason = "rejected"
	for range 2 {
		fallback, err := c.Compile(t.Context(), agent, snapshot, caps, base)
		if err != nil || fallback.Mint.Kind != domain.DestCandidateFallback || fallback.Policy.Rules[0].Ports != "443" {
			t.Fatalf("fallback: %+v / %v", fallback, err)
		}
		fallback.Policy.Rules[0].Ports = "caller-mutated"
	}
	if runtime.loads != 1 {
		t.Fatalf("idle fallback reloaded body: %d", runtime.loads)
	}
	desired.Policy.Rules[0].Ports = "80"
	good, err := json.Marshal(desired.Policy)
	if err != nil {
		t.Fatal(err)
	}
	runtime.state.AppliedSHA256 = protocol.PolicyDigest(desired.Policy)
	runtime.body = []byte("corrupt confirmed policy")
	if _, err := c.Compile(t.Context(), agent, snapshot, caps, base); !errors.Is(err, domain.ErrUnavailable) {
		t.Fatalf("changed confirmed digest bypassed validation: %v", err)
	}
	runtime.body = good
	fallback, err := c.Compile(t.Context(), agent, snapshot, caps, base)
	if err != nil || fallback.Policy.Rules[0].Ports != "80" || runtime.loads != 3 {
		t.Fatalf("changed LKG/error recovery: %+v loads=%d / %v", fallback, runtime.loads, err)
	}
}

type panelScopedCacheInputs struct{ cacheCompilerInputs }

func (i *panelScopedCacheInputs) ForNode(ctx context.Context, panelID int64, snapshot *ports.NativeDesiredSnapshot, members bool) (RosterInput, RosterInput, error) {
	r, q, err := i.cacheCompilerInputs.ForNode(ctx, panelID, snapshot, members)
	if panelID != 81 {
		r.UserGroups = map[int64]int64{2: 9}
		q.UserGroups = map[int64]int64{2: 9}
	}
	return r, q, err
}
func TestCompilerResultCacheKeepsPanelScopeInIdentity(t *testing.T) {
	c, inputs, _, agent, snapshot, base := cachedCompilerFixture(t)
	c.inputs = &panelScopedCacheInputs{*inputs}
	store := c.definitions.(*cachedPublicationStore)
	defs := domain.DestDefinitions{State: store.state, Policies: []domain.DestPolicy{{ID: 1, Enabled: true, Action: domain.DestBlock, Scope: domain.DestScopeGroups, GroupIDs: []int64{8}, Inline: domain.DestInline{Ports: "443"}}}}
	body, err := BuildDefinitionSnapshot(defs)
	if err != nil {
		t.Fatal(err)
	}
	store.snapshot.Body = body
	first, err := c.Compile(t.Context(), agent, snapshot, []string{protocol.CapabilityDestinationPolicy}, base)
	if err != nil || first.Policy == nil || len(first.Policy.Rules) != 1 {
		t.Fatalf("scoped initial rule: %+v / %v", first, err)
	}
	agent.PanelID = 82
	other, err := c.Compile(t.Context(), agent, snapshot, []string{protocol.CapabilityDestinationPolicy}, base)
	if err != nil || (other.Policy != nil && len(other.Policy.Rules) != 0) {
		t.Fatalf("old panel membership reused: %+v / %v", other, err)
	}
}
