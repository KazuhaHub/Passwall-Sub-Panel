package group

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-protocol/protocol"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// ---- fakes: each embeds the full port (nil) and answers only what Eligible reads ----

type eligNodes struct {
	ports.NodeRepo
	nodes []*domain.Node
	err   error
	calls atomic.Int32
}

func (r *eligNodes) ListEnabled(context.Context) ([]*domain.Node, error) {
	r.calls.Add(1)
	return r.nodes, r.err
}

type eligGroups struct {
	ports.GroupRepo
	groups  []*domain.Group
	members map[int64]int64
	err     error
	counts  atomic.Int32
}

func (r *eligGroups) List(context.Context) ([]*domain.Group, error) { return r.groups, r.err }
func (r *eligGroups) CountMembers(_ context.Context, id int64) (int64, error) {
	r.counts.Add(1)
	return r.members[id], r.err
}

type eligPanels struct {
	kinds map[int64]domain.PanelKind
	err   error
	calls atomic.Int32
}

func (r *eligPanels) GetByID(_ context.Context, id int64) (*domain.XUIPanel, error) {
	r.calls.Add(1)
	if r.err != nil {
		return nil, r.err
	}
	kind, ok := r.kinds[id]
	if !ok {
		return nil, domain.ErrNotFound
	}
	return &domain.XUIPanel{ID: id, Kind: kind}, nil
}

type eligAgents struct {
	byPanel map[int64]*domain.NodeAgent
	err     error
	calls   atomic.Int32
}

func (r *eligAgents) GetByPanelID(_ context.Context, id int64) (*domain.NodeAgent, error) {
	r.calls.Add(1)
	if r.err != nil {
		return nil, r.err
	}
	a, ok := r.byPanel[id]
	if !ok {
		return nil, domain.ErrNotFound
	}
	return a, nil
}

type eligPolicies struct {
	byAgent map[string]*domain.DestAgentPolicy
	err     error
	calls   atomic.Int32
}

func (r *eligPolicies) GetMeta(_ context.Context, agentID string) (*domain.DestAgentPolicy, error) {
	r.calls.Add(1)
	if r.err != nil {
		return nil, r.err
	}
	m, ok := r.byAgent[agentID]
	if !ok {
		return nil, domain.ErrNotFound
	}
	return m, nil
}

// eligEnv is one native panel (id 1) with one enabled node, healthy by default.
type eligEnv struct {
	svc      *Service
	nodes    *eligNodes
	groups   *eligGroups
	panels   *eligPanels
	agents   *eligAgents
	policies *eligPolicies
	node     *domain.Node
	clock    time.Time
}

const sniffOK = `{"enabled":true,"destOverride":["http","tls","quic"]}`

func newEligEnv(t *testing.T) *eligEnv {
	t.Helper()
	node := &domain.Node{ID: 10, PanelID: 1, Enabled: true, Region: "TW", Sniffing: sniffOK}
	e := &eligEnv{
		node:   node,
		nodes:  &eligNodes{nodes: []*domain.Node{node}},
		groups: &eligGroups{members: map[int64]int64{}},
		panels: &eligPanels{kinds: map[int64]domain.PanelKind{1: domain.PanelKindPSP}},
		agents: &eligAgents{byPanel: map[int64]*domain.NodeAgent{1: {
			AgentID: "agt_1", PanelID: 1, DesiredCoreEngine: domain.NodeCoreXray,
			ObservedCapabilities: []string{protocol.CapabilityDestinationPolicy, protocol.CapabilityDestinationGroups},
		}}},
		policies: &eligPolicies{byAgent: map[string]*domain.DestAgentPolicy{}},
		clock:    time.Unix(1_700_000_000, 0),
	}
	e.svc = New(e.groups, e.nodes, nil)
	e.svc.now = func() time.Time { return e.clock }
	e.svc.SetEligibilityDeps(EligibilityDeps{Panels: e.panels, Agents: e.agents, Policies: e.policies})
	return e
}

func allowlistGroup(state domain.DestState) *domain.Group {
	return &domain.Group{ID: 5, TagFilter: domain.TagFilter{All: true}, DestAllowlist: domain.DestAllowlist{State: state}}
}

func (e *eligEnv) eligible(t *testing.T, g *domain.Group) bool {
	t.Helper()
	ok, err := e.svc.Eligible(context.Background(), e.node, g)
	if err != nil {
		t.Fatalf("Eligible: %v", err)
	}
	return ok
}

// With the allowlist off the decision is exactly Matches, whatever the panel
// is and whether or not the dependencies are wired: a group with no allowlist
// must behave as it did before the allowlist existed.
func TestEligible_OffIsExactlyMatches(t *testing.T) {
	filters := []domain.TagFilter{
		{All: true},
		{Tags: []string{"region:TW"}},
		{Tags: []string{"region:JP"}},
		{Mode: "any", Tags: []string{"region:JP", "region:TW"}},
		{Mode: "any"},
		{},
	}
	for _, withDeps := range []bool{true, false} {
		e := newEligEnv(t)
		if !withDeps {
			e.svc.SetEligibilityDeps(EligibilityDeps{})
		}
		// The most hostile panel for an allowlist: 3X-UI, which can enforce nothing.
		e.panels.kinds[1] = domain.PanelKind3XUI
		for _, state := range []domain.DestState{"", domain.DestStateOff} {
			for _, f := range filters {
				g := &domain.Group{ID: 5, TagFilter: f, DestAllowlist: domain.DestAllowlist{State: state}}
				if got, want := e.eligible(t, g), Matches(e.node, f); got != want {
					t.Errorf("deps=%v state=%q filter=%+v: Eligible = %v, Matches = %v", withDeps, state, f, got, want)
				}
			}
		}
		if n := e.panels.calls.Load() + e.agents.calls.Load() + e.policies.calls.Load(); n != 0 {
			t.Errorf("an off group read %d eligibility rows; it must read none", n)
		}
	}
}

// Without the destination read side wired, the allowlist cannot be compiled
// either, so Eligible falls back to Matches rather than excluding every node.
func TestEligible_UnwiredDepsFallBackToMatches(t *testing.T) {
	e := newEligEnv(t)
	e.svc.SetEligibilityDeps(EligibilityDeps{})
	e.panels.kinds[1] = domain.PanelKind3XUI
	if !e.eligible(t, allowlistGroup(domain.DestStateOn)) {
		t.Fatal("with no eligibility dependencies Eligible must equal Matches")
	}
}

func TestEligible_AllowlistedGroups(t *testing.T) {
	cases := []struct {
		name  string
		setup func(e *eligEnv)
		want  bool
	}{
		{"healthy native node", func(e *eligEnv) {}, true},
		{"3x-ui panel", func(e *eligEnv) { e.panels.kinds[1] = domain.PanelKind3XUI }, false},
		{"legacy panel kind", func(e *eligEnv) { e.panels.kinds[1] = "" }, false},
		{"s-ui panel", func(e *eligEnv) { e.panels.kinds[1] = domain.PanelKindSUI }, false},
		{"no destination policy capability", func(e *eligEnv) {
			e.agents.byPanel[1].ObservedCapabilities = []string{protocol.CapabilityDestinationGroups}
		}, false},
		{"no groups capability", func(e *eligEnv) {
			e.agents.byPanel[1].ObservedCapabilities = []string{protocol.CapabilityDestinationPolicy}
		}, false},
		{"no capabilities at all", func(e *eligEnv) { e.agents.byPanel[1].ObservedCapabilities = nil }, false},
		{"panel without an agent", func(e *eligEnv) { delete(e.agents.byPanel, 1) }, false},
		{"policy rejected by the node", func(e *eligEnv) {
			e.policies.byAgent["agt_1"] = &domain.DestAgentPolicy{FallbackReason: domain.DestFallbackRejected}
		}, false},
		{"policy over a limit", func(e *eligEnv) {
			e.policies.byAgent["agt_1"] = &domain.DestAgentPolicy{FallbackReason: domain.DestFallbackOverLimit}
		}, false},
		// A6: the sniffing fallback depends on the roster, which R5 itself
		// shrinks, so it must not be read: judging by it would oscillate.
		{"sniffing fallback reason is not read", func(e *eligEnv) {
			e.policies.byAgent["agt_1"] = &domain.DestAgentPolicy{FallbackReason: domain.DestFallbackSniffing}
		}, true},
		{"policy row with no fallback", func(e *eligEnv) {
			e.policies.byAgent["agt_1"] = &domain.DestAgentPolicy{}
		}, true},
		{"xray inbound sniffing metadata only", func(e *eligEnv) {
			e.node.Sniffing = `{"enabled":true,"metadataOnly":true}`
		}, false},
		{"xray inbound sniffing lacks tls", func(e *eligEnv) {
			e.node.Sniffing = `{"enabled":true,"destOverride":["http"]}`
		}, false},
		{"xray inbound sniffing route only", func(e *eligEnv) {
			e.node.Sniffing = `{"enabled":true,"destOverride":["http","tls","quic"],"routeOnly":true}`
		}, false},
		{"xray inbound sniffing malformed", func(e *eligEnv) { e.node.Sniffing = `{not json` }, false},
		// The node turns sniffing on itself when it is off.
		{"xray inbound sniffing off", func(e *eligEnv) { e.node.Sniffing = `{"enabled":false}` }, true},
		{"xray inbound sniffing empty", func(e *eligEnv) { e.node.Sniffing = `` }, true},
		{"insufficient sniffing on a disabled sibling is ignored", func(e *eligEnv) {
			e.nodes.nodes = append(e.nodes.nodes, &domain.Node{ID: 11, PanelID: 1, Enabled: false, Sniffing: `{"enabled":true,"metadataOnly":true}`})
		}, true},
		{"insufficient sniffing on another panel is ignored", func(e *eligEnv) {
			e.nodes.nodes = append(e.nodes.nodes, &domain.Node{ID: 12, PanelID: 2, Enabled: true, Sniffing: `{"enabled":true,"metadataOnly":true}`})
		}, true},
		{"insufficient sniffing on a sibling inbound of the same panel", func(e *eligEnv) {
			e.nodes.nodes = append(e.nodes.nodes, &domain.Node{ID: 13, PanelID: 1, Enabled: true, Sniffing: `{"enabled":true,"destOverride":["http"]}`})
		}, false},
		// sing-box takes its sniff rule from the route, which the node writes.
		{"sing-box ignores inbound sniffing", func(e *eligEnv) {
			e.agents.byPanel[1].DesiredCoreEngine = domain.NodeCoreSingBox
			e.node.Sniffing = `{"enabled":true,"metadataOnly":true}`
		}, true},
		// D2: a node that can enforce counts even though it records nothing.
		{"enforces but records nothing", func(e *eligEnv) {
			e.agents.byPanel[1].DesiredCoreEngine = domain.NodeCoreSingBox
		}, true},
	}
	for _, state := range []domain.DestState{domain.DestStateReport, domain.DestStateOn} {
		for _, tc := range cases {
			t.Run(string(state)+"/"+tc.name, func(t *testing.T) {
				e := newEligEnv(t)
				tc.setup(e)
				if got := e.eligible(t, allowlistGroup(state)); got != tc.want {
					t.Fatalf("Eligible = %v, want %v", got, tc.want)
				}
			})
		}
	}
}

// Matches stays a precondition: a node the group's tags do not select is not
// eligible however healthy it is.
func TestEligible_TagFilterStillApplies(t *testing.T) {
	e := newEligEnv(t)
	g := allowlistGroup(domain.DestStateOn)
	g.TagFilter = domain.TagFilter{Tags: []string{"region:JP"}}
	if e.eligible(t, g) {
		t.Fatal("a node the tag filter does not select must not be eligible")
	}
}

// Any failed read is an error, never a verdict in either direction.
func TestEligible_ReadFailuresAreErrors(t *testing.T) {
	boom := errors.New("db down")
	cases := map[string]func(e *eligEnv){
		"panel":    func(e *eligEnv) { e.panels.err = boom },
		"agent":    func(e *eligEnv) { e.agents.err = boom },
		"policy":   func(e *eligEnv) { e.policies.err = boom },
		"nodelist": func(e *eligEnv) { e.nodes.err = boom },
	}
	for name, fail := range cases {
		t.Run(name, func(t *testing.T) {
			e := newEligEnv(t)
			fail(e)
			ok, err := e.svc.Eligible(context.Background(), e.node, allowlistGroup(domain.DestStateOn))
			if !errors.Is(err, boom) {
				t.Fatalf("err = %v, want the read failure", err)
			}
			if ok {
				t.Fatal("a failed read must not come back eligible")
			}
		})
	}
}

func TestEligible_AFailedReadIsNotCached(t *testing.T) {
	e := newEligEnv(t)
	e.policies.err = errors.New("db down")
	if _, err := e.svc.Eligible(context.Background(), e.node, allowlistGroup(domain.DestStateOn)); err == nil {
		t.Fatal("want the read failure")
	}
	e.policies.err = nil
	if !e.eligible(t, allowlistGroup(domain.DestStateOn)) {
		t.Fatal("after the read recovers the node must be eligible: the failure was cached")
	}
}

func TestEligible_CachePerPanelWithTTL(t *testing.T) {
	e := newEligEnv(t)
	g := allowlistGroup(domain.DestStateOn)
	e.eligible(t, g)
	e.eligible(t, g)
	e.eligible(t, allowlistGroup(domain.DestStateReport)) // same panel, other group: same answer
	if got := e.agents.calls.Load(); got != 1 {
		t.Fatalf("agent read %d times inside the TTL, want 1", got)
	}
	e.clock = e.clock.Add(29 * time.Second)
	e.eligible(t, g)
	if got := e.agents.calls.Load(); got != 1 {
		t.Fatalf("agent read %d times at 29s, want 1", got)
	}
	e.clock = e.clock.Add(2 * time.Second)
	e.eligible(t, g)
	if got := e.agents.calls.Load(); got != 2 {
		t.Fatalf("agent read %d times after the TTL, want 2", got)
	}
}

func TestEligible_InvalidateEligibility(t *testing.T) {
	e := newEligEnv(t)
	g := allowlistGroup(domain.DestStateOn)
	if !e.eligible(t, g) {
		t.Fatal("want eligible")
	}
	// The capability disappears; the cache still says yes until it is told.
	e.agents.byPanel[1].ObservedCapabilities = nil
	if !e.eligible(t, g) {
		t.Fatal("inside the TTL the cached answer stands")
	}
	e.svc.InvalidateEligibility(2) // another panel: no effect
	if !e.eligible(t, g) {
		t.Fatal("invalidating another panel must not drop this one")
	}
	e.svc.InvalidateEligibility(1)
	if e.eligible(t, g) {
		t.Fatal("after InvalidateEligibility(panel) the new capabilities must be read")
	}
	e.agents.byPanel[1].ObservedCapabilities = []string{protocol.CapabilityDestinationPolicy, protocol.CapabilityDestinationGroups}
	e.svc.InvalidateEligibility(0)
	if !e.eligible(t, g) {
		t.Fatal("InvalidateEligibility(0) must drop every panel")
	}
}

// A read that began before an invalidation must not put its (stale) answer
// back: the resyncer invalidates and then reads, and a late put would pin the
// old answer for the whole TTL.
func TestEligible_InvalidateRejectsInFlightPut(t *testing.T) {
	c := newEligibilityCache(30 * time.Second)
	t0 := time.Unix(1_700_000_000, 0)
	version := c.currentVersion()
	c.invalidate(1)
	if c.putIfVersion(1, true, t0, version) {
		t.Fatal("a put from before the invalidation must be rejected")
	}
	if _, ok := c.get(1, t0); ok {
		t.Fatal("the rejected put must not populate the cache")
	}
}

func TestNodesFor_FiltersIneligibleEvenForAll(t *testing.T) {
	e := newEligEnv(t)
	xui := &domain.Node{ID: 20, PanelID: 2, Enabled: true}
	e.panels.kinds[2] = domain.PanelKind3XUI
	e.nodes.nodes = []*domain.Node{e.node, xui}

	for _, state := range []domain.DestState{domain.DestStateReport, domain.DestStateOn} {
		got, err := e.svc.NodesFor(context.Background(), allowlistGroup(state))
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 1 || got[0].ID != e.node.ID {
			t.Fatalf("state %s: NodesFor = %v, want only the native node", state, ids(got))
		}
	}
}

func TestNodesFor_ReturnsEligibilityErrors(t *testing.T) {
	e := newEligEnv(t)
	boom := errors.New("db down")
	e.agents.err = boom
	if _, err := e.svc.NodesFor(context.Background(), allowlistGroup(domain.DestStateOn)); !errors.Is(err, boom) {
		t.Fatalf("NodesFor err = %v, want the read failure passed through", err)
	}
}

// With no allowlist the output is exactly what it was before: the same nodes in
// the same order, and not one eligibility row read.
func TestNodesFor_UnchangedWhenOff(t *testing.T) {
	e := newEligEnv(t)
	e.panels.kinds[2] = domain.PanelKind3XUI
	e.nodes.nodes = []*domain.Node{
		e.node,
		{ID: 20, PanelID: 2, Enabled: true, Region: "TW"},
		{ID: 21, PanelID: 3, Enabled: true, Region: "JP"},
	}
	filters := []domain.TagFilter{{All: true}, {Tags: []string{"region:TW"}}, {Mode: "any", Tags: []string{"region:JP"}}}
	for _, state := range []domain.DestState{"", domain.DestStateOff} {
		for _, f := range filters {
			g := &domain.Group{ID: 5, TagFilter: f, DestAllowlist: domain.DestAllowlist{State: state}}
			got, err := e.svc.NodesFor(context.Background(), g)
			if err != nil {
				t.Fatal(err)
			}
			var want []int64
			for _, n := range e.nodes.nodes {
				if Matches(n, f) {
					want = append(want, n.ID)
				}
			}
			if !equalIDs(ids(got), want) {
				t.Errorf("state %q filter %+v: NodesFor = %v, want %v", state, f, ids(got), want)
			}
		}
	}
	if n := e.panels.calls.Load() + e.agents.calls.Load() + e.policies.calls.Load(); n != 0 {
		t.Fatalf("NodesFor on off groups read %d eligibility rows, want 0", n)
	}
}

func TestTagMatchedMembers(t *testing.T) {
	e := newEligEnv(t)
	e.groups.groups = []*domain.Group{
		{ID: 5, TagFilter: domain.TagFilter{All: true}},
		{ID: 6, TagFilter: domain.TagFilter{Tags: []string{"region:JP"}}},
		{ID: 7, TagFilter: domain.TagFilter{Tags: []string{"region:TW"}}},
	}
	e.groups.members = map[int64]int64{5: 40, 6: 9, 7: 3}
	e.svc.groups = e.groups
	e.nodes.nodes = []*domain.Node{e.node, {ID: 11, PanelID: 2, Enabled: true, Region: "JP"}}
	ctx := context.Background()
	for _, tc := range []struct {
		panel, group int64
		want         int
	}{
		{1, 5, 40}, // tags select the panel: every member counts, eligible or not
		{1, 6, 0},  // no node of panel 1 is in JP
		{2, 6, 9},
		{1, 7, 3},
		{2, 7, 0},
		{9, 5, 0}, // a panel with no enabled node selects nobody
	} {
		got, err := e.svc.TagMatchedMembers(ctx, tc.panel, tc.group)
		if err != nil || got != tc.want {
			t.Errorf("TagMatchedMembers(panel %d, group %d) = %d, %v; want %d", tc.panel, tc.group, got, err, tc.want)
		}
	}
}

// It runs before destpolicy's compile cache key on every sync of every node,
// so the member count is read once per TTL, not once per call.
func TestTagMatchedMembers_CountsAreCached(t *testing.T) {
	e := newEligEnv(t)
	e.groups.groups = []*domain.Group{{ID: 5, TagFilter: domain.TagFilter{All: true}}}
	e.groups.members = map[int64]int64{5: 40}
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		if _, err := e.svc.TagMatchedMembers(ctx, 1, 5); err != nil {
			t.Fatal(err)
		}
	}
	if got := e.groups.counts.Load(); got != 1 {
		t.Fatalf("member count read %d times, want 1", got)
	}
	e.groups.members[5] = 41
	e.clock = e.clock.Add(31 * time.Second)
	if got, _ := e.svc.TagMatchedMembers(ctx, 1, 5); got != 41 {
		t.Fatalf("after the TTL the count is %d, want 41", got)
	}
}

func TestTagMatchedMembers_ReadFailureIsAnError(t *testing.T) {
	e := newEligEnv(t)
	e.groups.groups = []*domain.Group{{ID: 5, TagFilter: domain.TagFilter{All: true}}}
	e.groups.err = errors.New("db down")
	if _, err := e.svc.TagMatchedMembers(context.Background(), 1, 5); err == nil {
		t.Fatal("a failed read must be an error")
	}
}

// ---- the resync trigger ----

type resyncLog struct{ events *[]string }

func (r resyncLog) ResyncGroupMembersInBackground(id int64) {
	*r.events = append(*r.events, "resync:"+itoa(id))
}

// The order is the point: a resync run before the invalidation would read the
// 30s cache and decide on the old eligibility.
func TestResyncAllowlistGroups_InvalidatesBeforeResyncing(t *testing.T) {
	e := newEligEnv(t)
	e.groups.groups = []*domain.Group{
		{ID: 1}, // never had an allowlist
		{ID: 2, DestAllowlist: domain.DestAllowlist{State: domain.DestStateOff}},
		{ID: 3, DestAllowlist: domain.DestAllowlist{State: domain.DestStateReport}},
		{ID: 4, DestAllowlist: domain.DestAllowlist{State: domain.DestStateOn}},
	}
	e.svc.groups = e.groups
	g := allowlistGroup(domain.DestStateOn)
	if !e.eligible(t, g) { // fill the cache
		t.Fatal("want eligible")
	}
	e.agents.byPanel[1].ObservedCapabilities = nil

	var events []string
	e.svc.SetMemberResyncer(resyncLogChecking{
		resyncLog: resyncLog{&events},
		check: func() {
			if e.eligible(t, g) {
				t.Error("the resync ran before the eligibility cache was invalidated")
			}
		},
	})
	e.svc.ResyncAllowlistGroups(context.Background(), 1)
	if want := []string{"resync:3", "resync:4"}; !equalStrings(events, want) {
		t.Fatalf("resynced %v, want %v (only the groups with an allowlist)", events, want)
	}
}

type resyncLogChecking struct {
	resyncLog
	check func()
}

func (r resyncLogChecking) ResyncGroupMembersInBackground(id int64) {
	r.check()
	r.resyncLog.ResyncGroupMembersInBackground(id)
}

func TestResyncAllowlistGroups_UnwiredIsANoop(t *testing.T) {
	e := newEligEnv(t)
	e.groups.groups = []*domain.Group{{ID: 3, DestAllowlist: domain.DestAllowlist{State: domain.DestStateOn}}}
	e.svc.groups = e.groups
	e.svc.ResyncAllowlistGroups(context.Background(), 1) // no resyncer: heal covers it; must not panic
}

func TestResyncAllowlistGroups_ListFailureStillInvalidates(t *testing.T) {
	e := newEligEnv(t)
	g := allowlistGroup(domain.DestStateOn)
	e.eligible(t, g)
	e.agents.byPanel[1].ObservedCapabilities = nil
	e.groups.err = errors.New("db down")
	e.svc.groups = e.groups
	var events []string
	e.svc.SetMemberResyncer(resyncLog{&events})
	e.svc.ResyncAllowlistGroups(context.Background(), 1)
	if len(events) != 0 {
		t.Fatalf("resynced %v after a failed group list", events)
	}
	e.groups.err = nil
	if e.eligible(t, g) {
		t.Fatal("the cache must have been invalidated even though the group list failed")
	}
}

func ids(nodes []*domain.Node) []int64 {
	out := make([]int64, len(nodes))
	for i, n := range nodes {
		out[i] = n.ID
	}
	return out
}

func equalIDs(a, b []int64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for ; n > 0; n /= 10 {
		b = append([]byte{byte('0' + n%10)}, b...)
	}
	return string(b)
}

// An inbound edit on a 3X-UI panel cannot change what that panel enforces (it
// enforces nothing), so it must not resync every allowlist group's members.
func TestInboundsChanged_OnlyNativePanelsResync(t *testing.T) {
	e := newEligEnv(t)
	e.panels.kinds[2] = domain.PanelKind3XUI
	e.groups.groups = []*domain.Group{{ID: 4, DestAllowlist: domain.DestAllowlist{State: domain.DestStateOn}}}
	e.svc.groups = e.groups
	var events []string
	e.svc.SetMemberResyncer(resyncLog{&events})

	e.svc.InboundsChanged(context.Background(), 2)
	if len(events) != 0 {
		t.Fatalf("a 3X-UI panel's inbound edit resynced %v", events)
	}
	e.svc.InboundsChanged(context.Background(), 1)
	if !equalStrings(events, []string{"resync:4"}) {
		t.Fatalf("a native panel's inbound edit resynced %v, want group 4", events)
	}
	// An unreadable panel is not a reason to resync (and the heal sweep covers it).
	events = nil
	e.panels.err = errors.New("db down")
	e.svc.InboundsChanged(context.Background(), 1)
	if len(events) != 0 {
		t.Fatalf("resynced %v after a failed panel read", events)
	}
}
