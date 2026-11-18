package destpolicy

import (
	"reflect"
	"testing"

	"github.com/KazuhaHub/passwall-protocol/protocol"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

const pinnedUnroutedDigest = "9319c1c202b9ec98a14e3d9df29c019077fc45562576be858902f70795732770"

func routedBase() *base {
	return &base{block: []baseRule{
		{id: "p1", action: protocol.RuleBlock, scope: domain.DestScopePanels, panels: []int64{1}, domains: []string{"domain:entry.example"}},
		{id: "p2", action: protocol.RuleBlock, scope: domain.DestScopePanels, panels: []int64{2}, domains: []string{"domain:tokyo.example"}},
		{id: "p3", action: protocol.RuleBlock, scope: domain.DestScopePanels, panels: []int64{1, 2}, domains: []string{"domain:both.example"}},
		{id: "p4", action: protocol.RuleBlock, scope: domain.DestScopePanels, panels: []int64{1, 2, 3}, domains: []string{"domain:every.example"}},
		{id: "p5", action: protocol.RuleBlock, scope: domain.DestScopeAll, domains: []string{"domain:all.example"}},
	}}
}

// Node 11 routes some destinations to server 2 and some to server 3 and leaves
// the rest from its own server (1); node 10 has no routes.
func routedView() nodeView {
	return nodeView{panelID: 1, nodeIDs: map[int64]bool{10: true, 11: true},
		routes: map[int64][]RouteExit{11: {
			{Key: protocol.LocalExit, PanelID: 1},
			{Key: "lnk_5", PanelID: 2},
			{Key: "lnk_6", PanelID: 3},
		}}}
}

type copyShape struct {
	inbounds []protocol.ListenerKey
	exits    []protocol.LinkKey
}

func copiesOf(policy *protocol.DestinationPolicy) map[string][]copyShape {
	out := map[string][]copyShape{}
	for _, r := range policy.Rules {
		base := r.ID
		if i := len(base); i > 2 && base[2] == 'x' {
			base = base[:2]
		}
		out[base] = append(out[base], copyShape{r.Inbounds, r.Exits})
	}
	return out
}

// A panel-scoped policy follows the traffic's exit on a routed listener, exit by
// exit: a listener whose exits are only partly in the policy's scope gets a copy
// confined to those exits, and one whose exits all are keeps the plain rule.
func TestPanelScopedPoliciesSplitByExitOnARoutedListener(t *testing.T) {
	policy := routedBase().assemble(routedView())
	if err := protocol.ValidateDestinationPolicy(policy); err != nil {
		t.Fatal(err)
	}
	l10, l11 := protocol.NewListenerKey(10), protocol.NewListenerKey(11)
	got := copiesOf(policy)
	want := map[string][]copyShape{
		// Scoped to the entry server: node 10 leaves from it; on node 11 only the
		// local exit is the entry's.
		"p1": {{[]protocol.ListenerKey{l10}, nil}, {[]protocol.ListenerKey{l11}, []protocol.LinkKey{protocol.LocalExit}}},
		// Scoped to server 2 only: nothing of node 10, and only the exit through it
		// on node 11.
		"p2": {{[]protocol.ListenerKey{l11}, []protocol.LinkKey{"lnk_5"}}},
		// Scoped to servers 1 and 2: node 10 entirely, node 11 on two of its three exits.
		"p3": {{[]protocol.ListenerKey{l10}, nil}, {[]protocol.ListenerKey{l11}, []protocol.LinkKey{"lnk_5", protocol.LocalExit}}},
		// Scoped to every server in play: no confinement anywhere, so one rule on every inbound.
		"p4": {{nil, nil}},
		"p5": {{nil, nil}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("copies =\n%v\nwant\n%v", got, want)
	}
	ids := map[string]bool{}
	for _, r := range policy.Rules {
		if ids[r.ID] {
			t.Fatalf("rule id %s is used twice", r.ID)
		}
		ids[r.ID] = true
	}
}

// A node whose listeners are not routed assembles exactly as it did before
// routes existed, whether the routes map is nil or empty.
func TestAssembleIsUnchangedWithoutRoutes(t *testing.T) {
	plain := nodeView{panelID: 1, nodeIDs: map[int64]bool{10: true, 11: true}, egress: map[int64]int64{11: 2}}
	empty := plain
	empty.routes = map[int64][]RouteExit{}
	a, b := protocol.PolicyDigest(routedBase().assemble(plain)), protocol.PolicyDigest(routedBase().assemble(empty))
	if a != b {
		t.Fatalf("digest moved: %s vs %s", a, b)
	}
	// Pinned: what the node-with-a-tunnel policy of this base digests to. It must
	// not change when the compiler learns something new about routes.
	if a != pinnedUnroutedDigest {
		t.Fatalf("digest of a node without routes = %s, pinned %s", a, pinnedUnroutedDigest)
	}
	for _, r := range routedBase().assemble(plain).Rules {
		if len(r.Exits) != 0 {
			t.Fatalf("a node without routes got a rule confined to exits: %+v", r)
		}
	}
}

func TestCompileKeyIsUnchangedWithoutRoutes(t *testing.T) {
	v := nodeView{panelID: 1, nodeIDs: map[int64]bool{10: true}, egress: map[int64]int64{10: 2}}
	meta := &domain.DestAgentPolicy{}
	nodes := []*domain.Node{{ID: 10, Enabled: true}}
	base := compileKey(3, []string{"x"}, "xray", protocol.CollectHits, protocol.ResolveAsIs, v, nodes, meta, "")
	// Pinned to what it was before routes existed: a node without any keeps its
	// cached compile across the upgrade.
	if base != "c28f9b2f752d6a0265351bef55d18be46bb86dc8045060fd7b291f234c2171b3" {
		t.Fatalf("the compile key of a node without routes moved: %s", base)
	}
	v.routes = map[int64][]RouteExit{}
	if got := compileKey(3, []string{"x"}, "xray", protocol.CollectHits, protocol.ResolveAsIs, v, nodes, meta, ""); got != base {
		t.Fatal("an empty routes map moved the compile key")
	}
	v.routes = map[int64][]RouteExit{10: {{Key: protocol.LocalExit, PanelID: 1}, {Key: "lnk_5", PanelID: 2}}}
	routed := compileKey(3, []string{"x"}, "xray", protocol.CollectHits, protocol.ResolveAsIs, v, nodes, meta, "")
	if routed == base {
		t.Fatal("routes did not move the compile key")
	}
	v.routes[10][1].PanelID = 3
	if compileKey(3, []string{"x"}, "xray", protocol.CollectHits, protocol.ResolveAsIs, v, nodes, meta, "") == routed {
		t.Fatal("an exit moving to another server did not move the compile key")
	}
}

// What the last applied policy keeps when the node falls back is only ever
// narrower: a rule survives on the listeners whose traffic its scope still
// covers through every exit the rule takes.
func TestFallbackNarrowsAnExitConfinedRule(t *testing.T) {
	v := routedView()
	l10, l11 := protocol.NewListenerKey(10), protocol.NewListenerKey(11)
	cases := []struct {
		name   string
		panels []int64
		rule   protocol.DestinationRule
		want   []protocol.ListenerKey
	}{
		{"unconfined rule of a policy that now covers both", []int64{1, 2, 3}, protocol.DestinationRule{ID: "p4"}, []protocol.ListenerKey{l10, l11}},
		{"a rule that covered node 11 on all exits, now only partly covered", []int64{1, 2}, protocol.DestinationRule{ID: "p4"}, []protocol.ListenerKey{l10}},
		{"an exit-confined copy still covered", []int64{1, 2}, protocol.DestinationRule{ID: "p3x1", Inbounds: []protocol.ListenerKey{l11}, Exits: []protocol.LinkKey{protocol.LocalExit, "lnk_5"}}, []protocol.ListenerKey{l11}},
		{"an exit-confined copy whose exit left the policy's scope", []int64{1}, protocol.DestinationRule{ID: "p3x1", Inbounds: []protocol.ListenerKey{l11}, Exits: []protocol.LinkKey{"lnk_5"}}, nil},
		{"a confined copy never applies to an unrouted listener", []int64{1, 2}, protocol.DestinationRule{ID: "p3x1", Inbounds: []protocol.ListenerKey{l10}, Exits: []protocol.LinkKey{protocol.LocalExit}}, nil},
	}
	for _, tc := range cases {
		if got := v.covered(tc.panels, tc.rule); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: covered = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestAuditEgressNeedsAnExitThroughALink(t *testing.T) {
	caps := []string{capabilityAuditHitsEgress}
	local := map[int64][]RouteExit{11: {{Key: protocol.LocalExit, PanelID: 1}}}
	tunnel := map[int64][]RouteExit{11: {{Key: protocol.LocalExit, PanelID: 1}, {Key: "lnk_5", PanelID: 2}}}
	if auditEgressFor(CompileInput{Capabilities: caps, RouteExits: local}, xrayEngine) {
		t.Fatal("routes that all leave locally have nothing to tell apart")
	}
	if !auditEgressFor(CompileInput{Capabilities: caps, RouteExits: tunnel}, xrayEngine) {
		t.Fatal("a route through a link should ask for the egress of each hit")
	}
	if auditEgressFor(CompileInput{Capabilities: caps, RouteExits: tunnel}, "sing-box") {
		t.Fatal("sing-box cannot tell links apart per rule")
	}
}
