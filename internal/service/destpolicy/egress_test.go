package destpolicy

import (
	"reflect"
	"testing"

	"github.com/KazuhaHub/passwall-protocol/protocol"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// A panel-scoped policy follows the traffic's exit: on a listener that leaves
// through a tunnel, the exit server's scope decides, not the entry's.
func TestPanelScopedPoliciesFollowTheExit(t *testing.T) {
	b := &base{block: []baseRule{
		{id: "p1", action: protocol.RuleBlock, scope: domain.DestScopePanels, panels: []int64{1}, domains: []string{"domain:entry.example"}},
		{id: "p2", action: protocol.RuleBlock, scope: domain.DestScopePanels, panels: []int64{2}, domains: []string{"domain:exit.example"}},
		{id: "p3", action: protocol.RuleBlock, scope: domain.DestScopePanels, panels: []int64{1, 2}, domains: []string{"domain:both.example"}},
	}}
	inbounds := func(policy *protocol.DestinationPolicy) map[string][]protocol.ListenerKey {
		out := map[string][]protocol.ListenerKey{}
		for _, r := range policy.Rules {
			out[r.ID] = r.Inbounds
		}
		return out
	}

	// Without tunnels the entry runs its own policies everywhere, as before.
	plain := b.assemble(nodeView{panelID: 1, nodeIDs: map[int64]bool{10: true, 11: true}})
	if got, want := inbounds(plain), map[string][]protocol.ListenerKey{"p1": nil, "p3": nil}; !reflect.DeepEqual(got, want) {
		t.Fatalf("without tunnels = %v, want %v", got, want)
	}

	// Listener 11 leaves through server 2: the entry's policy no longer covers
	// it, the exit's does, and one scoped to both still covers everything.
	tunnelled := b.assemble(nodeView{panelID: 1, nodeIDs: map[int64]bool{10: true, 11: true}, egress: map[int64]int64{11: 2}})
	want := map[string][]protocol.ListenerKey{
		"p1": {protocol.NewListenerKey(10)},
		"p2": {protocol.NewListenerKey(11)},
		"p3": nil,
	}
	if got := inbounds(tunnelled); !reflect.DeepEqual(got, want) {
		t.Fatalf("with a tunnel = %v, want %v", got, want)
	}
	if err := protocol.ValidateDestinationPolicy(tunnelled); err != nil {
		t.Fatal(err)
	}
}
