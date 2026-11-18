package sourceguard

import (
	"context"
	"reflect"
	"testing"

	nodeprotocol "github.com/KazuhaHub/passwall-protocol/protocol"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

func milli(v int) *int { return &v }

func landing() *domain.Node {
	return &domain.Node{
		ID: 5, PanelID: 1, Enabled: true,
		Relays: []domain.RelayLine{
			{ID: "same", Enabled: true}, // costs what the direct line costs, no addresses
			{ID: "fwd", Enabled: true, EntryPanelID: 9, TrafficFactorMilli: milli(2000)},
			{ID: "ext", Enabled: true, SourceCIDRs: []string{"192.0.2.0/24"}, TrafficFactorMilli: milli(2000)},
			{ID: "ext-unknown", Enabled: true, TrafficFactorMilli: milli(3000)},
			{ID: "cdn", Enabled: true, Mode: domain.RelayModeCDN, TrafficFactorMilli: milli(4000)},
			{ID: "off", Enabled: false, SourceCIDRs: []string{"192.0.2.0/24"}, TrafficFactorMilli: milli(5000)},
			{ID: "tun", Enabled: true, Mode: domain.RelayModeTunnel, EntryPanelID: 9, TrafficFactorMilli: milli(6000)},
		},
	}
}

func capableInputs() domain.SourceInputs {
	return domain.SourceInputs{
		EntryAddresses: map[int64][]string{9: {"93.184.216.34"}},
		Cloudflare:     []string{"173.245.48.0/20"},
		Servers:        map[int64]domain.SourceGuardServer{1: {Capable: true}},
	}
}

func TestComputeGroupsLinesIntoFactorClasses(t *testing.T) {
	plan := Compute([]*domain.Node{landing()}, capableInputs())

	want := map[string][]string{
		string(Key(5, 2000)): {"93.184.216.34/32", "192.0.2.0/24"},
		string(Key(5, 4000)): {"173.245.48.0/20"},
	}
	got := map[string][]string{}
	for _, set := range plan.Sets {
		got[string(set.Key)] = set.CIDRs
		if err := nodeprotocol.ValidateSourceSet(set); err != nil {
			t.Fatal(err)
		}
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("sets = %v", got)
	}
	for i := 1; i < len(plan.Sets); i++ {
		if plan.Sets[i-1].Key >= plan.Sets[i].Key {
			t.Fatal("sets are not sorted by key")
		}
	}
	// Class 3000 has no known address: its key is referenced, never sent.
	if !reflect.DeepEqual(plan.Deny[5], []nodeprotocol.SourceSetKey{Key(5, 2000), Key(5, 4000)}) {
		t.Fatalf("deny = %v", plan.Deny[5])
	}
	for _, id := range []string{"same", "tun"} {
		if _, ok := plan.Allow[domain.LineKey{NodeID: 5, LineID: id}]; ok {
			t.Errorf("line %s is priced", id)
		}
	}
	if len(plan.Withheld) != 1 || plan.Withheld[0].Line.ID != "ext-unknown" || plan.Withheld[0].Reason != domain.SourceWithheldUnknown {
		t.Fatalf("withheld = %+v", plan.Withheld)
	}

	allow := func(line string) *nodeprotocol.ClientSourceGuard {
		g, ok := plan.Guard(&domain.PSPClient{NodeID: 5, LineID: line}, []int64{5})
		if !ok {
			t.Fatalf("%s: guard not sendable", line)
		}
		return g
	}
	// A priced client is allowed from its class only.
	if g := allow("fwd"); g == nil || g.AllowFrom != Key(5, 2000) || len(g.DenyFrom) != 0 {
		t.Fatalf("fwd = %+v", g)
	}
	// Two lines of one class share the key.
	if allow("ext").AllowFrom != Key(5, 2000) {
		t.Fatal("ext is not in the fwd class")
	}
	// An unknown set still guards: allow_from names a key that is not sent.
	if g := allow("ext-unknown"); g == nil || g.AllowFrom != Key(5, 3000) {
		t.Fatalf("unknown = %+v", g)
	}
	for _, set := range plan.Sets {
		if set.Key == Key(5, 3000) {
			t.Fatal("the unknown class was sent")
		}
	}
	// The direct client, and a relay client at the direct factor, are refused from
	// every priced class's set.
	for _, line := range []string{domain.DirectLineID, "same"} {
		g := allow(line)
		if g == nil || g.AllowFrom != "" || !reflect.DeepEqual(g.DenyFrom, plan.Deny[5]) {
			t.Fatalf("%s guard = %+v", line, g)
		}
		if err := nodeprotocol.ValidateClientSourceGuard(g); err != nil {
			t.Fatal(err)
		}
	}
	for name, c := range map[string]*domain.PSPClient{
		"tunnel":        {NodeID: 5, LineID: "tun"},
		"other landing": {NodeID: 6, LineID: domain.DirectLineID},
	} {
		nodes := []int64{5}
		if name == "tunnel" {
			nodes = []int64{77} // a tunnel line's client sits on its own listener
		}
		if name == "other landing" {
			nodes = []int64{6}
		}
		if g, ok := plan.Guard(c, nodes); !ok || g != nil {
			t.Errorf("%s got a guard: %+v", name, g)
		}
	}
}

func TestGuardOfALegacyRowAttachedToSeveralLandings(t *testing.T) {
	a, b := landing(), landing()
	b.ID = 6
	plan := Compute([]*domain.Node{a, b}, capableInputs())
	g, ok := plan.Guard(&domain.PSPClient{}, []int64{5, 6})
	if !ok || g == nil || len(g.DenyFrom) != 4 {
		t.Fatalf("legacy row guard = %+v %v", g, ok)
	}
	if err := nodeprotocol.ValidateClientSourceGuard(g); err != nil {
		t.Fatal(err)
	}
	// More deny keys than a client holds are never cut: the guard is not sent.
	many := Plan{Deny: map[int64][]nodeprotocol.SourceSetKey{}, Allow: map[domain.LineKey]nodeprotocol.SourceSetKey{}}
	for i := int64(1); i <= 2; i++ {
		for f := 1; f <= 40; f++ {
			many.Deny[i] = append(many.Deny[i], Key(i, f))
		}
	}
	if g, ok := many.Guard(&domain.PSPClient{}, []int64{1, 2}); ok || g != nil {
		t.Fatalf("80 deny keys: %+v %v", g, ok)
	}
}

func TestComputeKeepsAnUnknownClassGuardedEvenWhenNothingIsSent(t *testing.T) {
	n := &domain.Node{ID: 5, PanelID: 1, Relays: []domain.RelayLine{{ID: "x", Enabled: true, TrafficFactorMilli: milli(2000)}}}
	plan := Compute([]*domain.Node{n}, capableInputs())
	if len(plan.Sets) != 0 || plan.Empty() {
		t.Fatalf("plan = %+v", plan)
	}
	g, ok := plan.Guard(&domain.PSPClient{NodeID: 5, LineID: "x"}, []int64{5})
	if !ok || g == nil || g.AllowFrom != Key(5, 2000) {
		t.Fatalf("guard = %+v: a priced client is never left unguarded", g)
	}
}

func TestComputeIsEmptyWhenEveryLineCostsTheDirectFactor(t *testing.T) {
	n := &domain.Node{ID: 5, PanelID: 1, Relays: []domain.RelayLine{{ID: "x", Enabled: true}, {ID: "fwd", Enabled: true, EntryPanelID: 9}}}
	plan := Compute([]*domain.Node{n, {ID: 6}}, capableInputs())
	if !plan.Empty() || len(plan.Withheld) != 0 {
		t.Fatalf("plan = %+v", plan)
	}
	if g, ok := plan.Guard(&domain.PSPClient{NodeID: 5, LineID: domain.DirectLineID}, []int64{5}); g != nil || !ok {
		t.Fatalf("guard = %+v", g)
	}
}

func TestComputeWithholdsClassesThatOverlapAndKeepsTheirClientsGuarded(t *testing.T) {
	n := &domain.Node{ID: 5, PanelID: 1, Relays: []domain.RelayLine{
		{ID: "a", Enabled: true, Mode: domain.RelayModeCDN, TrafficFactorMilli: milli(2000)},
		{ID: "b", Enabled: true, Mode: domain.RelayModeCDN, TrafficFactorMilli: milli(3000)},
	}}
	plan := Compute([]*domain.Node{n}, capableInputs())
	if len(plan.Sets) != 0 || len(plan.Deny[5]) != 0 || len(plan.Withheld) != 2 {
		t.Fatalf("plan = %+v", plan)
	}
	for _, id := range []string{"a", "b"} {
		g, ok := plan.Guard(&domain.PSPClient{NodeID: 5, LineID: id}, []int64{5})
		if !ok || g == nil || g.AllowFrom == "" {
			t.Fatalf("%s: %+v", id, g)
		}
	}
}

func TestKeyAndNodeIDOf(t *testing.T) {
	key := Key(42, 2500)
	if err := key.Validate(); err != nil {
		t.Fatal(err)
	}
	if NodeIDOf(string(key)) != 42 || NodeIDOf("src_x_y") != 0 || NodeIDOf("cli_3") != 0 || NodeIDOf("src_7") != 0 {
		t.Fatal("NodeIDOf misreads keys")
	}
	if f, ok := FactorOf(string(key)); !ok || f != 2500 {
		t.Fatal("FactorOf misreads keys")
	}
	if Key(42, 2500) != key || Key(42, 2000) == key {
		t.Fatal("key is not deterministic per factor")
	}
}

type fakeAgents struct{ agents []*domain.NodeAgent }

func (f fakeAgents) ListByPanelIDs(context.Context, []int64) ([]*domain.NodeAgent, error) {
	return f.agents, nil
}

func TestPlannerSendsSetsOnlyToAnAgentThatDeclaredTheCapability(t *testing.T) {
	agents := fakeAgents{[]*domain.NodeAgent{{PanelID: 9, ObservedAddress: "93.184.216.34"}}}
	p := NewPlanner(agents, nil)
	nodes := []*domain.Node{landing()}
	plan, err := p.PlanFor(t.Context(), AgentFacts{PanelID: 1, Capabilities: []string{nodeprotocol.CapabilityTransitForward}}, nodes)
	if err != nil || len(plan.Sets) != 0 || !plan.Empty() {
		t.Fatalf("plan = %+v err=%v", plan, err)
	}
	// The lines it cannot keep apart are withheld.
	if len(plan.Withheld) != 5 {
		t.Fatalf("withheld = %d lines", len(plan.Withheld))
	}
	plan, err = p.PlanFor(t.Context(), AgentFacts{PanelID: 1, Capabilities: []string{nodeprotocol.CapabilitySourceGuard}}, nodes)
	if err != nil || plan.Empty() {
		t.Fatalf("plan = %+v err=%v", plan, err)
	}
	// The built-in Cloudflare snapshot stands in for a keeper that is not wired.
	found := false
	for _, set := range plan.Sets {
		if set.Key == Key(5, 4000) && len(set.CIDRs) == len(BuiltinCloudflare) {
			found = true
		}
	}
	if !found {
		t.Fatalf("cdn set missing: %+v", plan.Sets)
	}
}

func TestPlannerDoesNotGuardAListenerTheCoreReadsAHeaderFor(t *testing.T) {
	n := landing()
	n.StreamSettings = `{"network":"ws","security":"tls"}`
	p := NewPlanner(fakeAgents{}, nil)
	facts := AgentFacts{PanelID: 1, Capabilities: []string{nodeprotocol.CapabilitySourceGuard}, Engines: []domain.NodeCoreEngine{domain.NodeCoreSingBox}}
	plan, err := p.PlanFor(t.Context(), facts, []*domain.Node{n})
	if err != nil || !plan.Empty() || len(plan.Withheld) == 0 {
		t.Fatalf("sing-box ws: plan = %+v err=%v", plan, err)
	}
	for _, w := range plan.Withheld {
		if w.Reason != domain.SourceWithheldFactor {
			t.Fatalf("withheld %+v", w)
		}
	}
	// The same listener on Xray, which does not trust the header, is guarded.
	facts.Engines = []domain.NodeCoreEngine{domain.NodeCoreXray}
	if plan, _ = p.PlanFor(t.Context(), facts, []*domain.Node{n}); plan.Empty() {
		t.Fatal("xray ws is guardable")
	}
}
