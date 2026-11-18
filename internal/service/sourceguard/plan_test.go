package sourceguard

import (
	"context"
	"reflect"
	"testing"

	nodeprotocol "github.com/KazuhaHub/passwall-protocol/protocol"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

func landing() *domain.Node {
	return &domain.Node{
		ID: 5, Enabled: true,
		Relays: []domain.RelayLine{
			{ID: "fwd", Enabled: true, EntryPanelID: 9},
			{ID: "ext", Enabled: true, SourceCIDRs: []string{"192.0.2.0/24"}},
			{ID: "ext-unknown", Enabled: true},
			{ID: "cdn", Enabled: true, Mode: domain.RelayModeCDN},
			{ID: "off", Enabled: false, SourceCIDRs: []string{"192.0.2.0/24"}},
			{ID: "tun", Enabled: true, Mode: domain.RelayModeTunnel, EntryPanelID: 9},
		},
	}
}

func TestComputeSetsAllowAndDeny(t *testing.T) {
	in := domain.SourceInputs{
		EntryAddresses: map[int64][]string{9: {"93.184.216.34"}},
		Cloudflare:     []string{"173.245.48.0/20"},
	}
	plan := Compute([]*domain.Node{landing()}, in)

	want := map[string][]string{
		string(Key(5, "fwd")): {"93.184.216.34/32"},
		string(Key(5, "ext")): {"192.0.2.0/24"},
		string(Key(5, "cdn")): {"173.245.48.0/20"},
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
	if len(plan.Deny[5]) != 3 {
		t.Fatalf("deny = %v", plan.Deny[5])
	}
	for _, id := range []string{"ext-unknown", "off", "tun"} {
		if _, ok := plan.Allow[domain.LineKey{NodeID: 5, LineID: id}]; ok {
			t.Errorf("line %s is guarded", id)
		}
	}

	direct := plan.Guard(&domain.PSPClient{NodeID: 5, LineID: domain.DirectLineID}, 1)
	if direct == nil || direct.AllowFrom != "" || !reflect.DeepEqual(direct.DenyFrom, plan.Deny[5]) {
		t.Fatalf("direct guard = %+v", direct)
	}
	if err := nodeprotocol.ValidateClientSourceGuard(direct); err != nil {
		t.Fatal(err)
	}
	relay := plan.Guard(&domain.PSPClient{NodeID: 5, LineID: "ext"}, 1)
	if relay == nil || relay.AllowFrom != Key(5, "ext") || len(relay.DenyFrom) != 0 {
		t.Fatalf("relay guard = %+v", relay)
	}
	for name, c := range map[string]*domain.PSPClient{
		"tunnel":        {NodeID: 5, LineID: "tun"},
		"unknown set":   {NodeID: 5, LineID: "ext-unknown"},
		"legacy row":    {NodeID: 0, LineID: ""},
		"other landing": {NodeID: 6, LineID: domain.DirectLineID},
		"deleted line":  {NodeID: 5, LineID: "gone"},
		"no line":       {NodeID: 5, LineID: ""},
	} {
		if g := plan.Guard(c, 1); g != nil {
			t.Errorf("%s got a guard: %+v", name, g)
		}
	}
	if plan.Guard(&domain.PSPClient{NodeID: 5, LineID: domain.DirectLineID}, 2) != nil {
		t.Error("a client on several listeners got a guard")
	}
}

func TestComputeIsEmptyWhenNothingIsKnown(t *testing.T) {
	n := &domain.Node{ID: 5, Relays: []domain.RelayLine{{ID: "x", Enabled: true}, {ID: "fwd", Enabled: true, EntryPanelID: 9}}}
	plan := Compute([]*domain.Node{n, {ID: 6}}, domain.SourceInputs{})
	if !plan.Empty() || plan.Guard(&domain.PSPClient{NodeID: 5, LineID: domain.DirectLineID}, 1) != nil {
		t.Fatalf("plan = %+v", plan)
	}
}

func TestKeyAndNodeIDOf(t *testing.T) {
	key := Key(42, "line-a")
	if err := key.Validate(); err != nil {
		t.Fatal(err)
	}
	if NodeIDOf(string(key)) != 42 || NodeIDOf("src_x_y") != 0 || NodeIDOf("cli_3") != 0 || NodeIDOf("src_7") != 0 {
		t.Fatal("NodeIDOf misreads keys")
	}
	if Key(42, "line-a") != key || Key(42, "line-b") == key {
		t.Fatal("key is not deterministic per line")
	}
}

type fakeAgents struct{ agents []*domain.NodeAgent }

func (f fakeAgents) ListByPanelIDs(context.Context, []int64) ([]*domain.NodeAgent, error) {
	return f.agents, nil
}

func TestPlannerSendsNothingWithoutTheCapability(t *testing.T) {
	agents := fakeAgents{[]*domain.NodeAgent{{PanelID: 9, ObservedAddress: "93.184.216.34"}}}
	p := NewPlanner(agents, nil)
	nodes := []*domain.Node{landing()}
	plan, err := p.PlanFor(t.Context(), []string{nodeprotocol.CapabilityTransitForward}, nodes)
	if err != nil || !plan.Empty() {
		t.Fatalf("plan = %+v err=%v", plan, err)
	}
	plan, err = p.PlanFor(t.Context(), []string{nodeprotocol.CapabilitySourceGuard}, nodes)
	if err != nil || plan.Empty() {
		t.Fatalf("plan = %+v err=%v", plan, err)
	}
	// The built-in Cloudflare snapshot stands in for a keeper that is not wired.
	found := false
	for _, set := range plan.Sets {
		if set.Key == Key(5, "cdn") && len(set.CIDRs) == len(BuiltinCloudflare) {
			found = true
		}
	}
	if !found {
		t.Fatalf("cdn set missing: %+v", plan.Sets)
	}
}
