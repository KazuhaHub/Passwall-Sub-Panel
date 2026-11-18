package group

import (
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// A node whose tunnel is withheld by its probes is dropped like one whose
// tunnel is not ready, and a node that leaves from its own server is never
// affected.
func TestWithoutUnreadyEgressDropsWithheldNodes(t *testing.T) {
	direct := &domain.Node{ID: 1, Enabled: true}
	ready := &domain.Node{ID: 2, Enabled: true, EgressPanelID: 5, EgressReady: true}
	withheld := &domain.Node{ID: 3, Enabled: true, EgressPanelID: 5, EgressReady: true, EgressWithheld: true}
	unready := &domain.Node{ID: 4, Enabled: true, EgressPanelID: 5}

	got := withoutUnreadyEgress([]*domain.Node{direct, ready, withheld, unready})
	if len(got) != 2 || got[0].ID != 1 || got[1].ID != 2 {
		t.Fatalf("kept %v, want nodes 1 and 2", probeNodeIDs(got))
	}
	all := []*domain.Node{direct, ready}
	if got := withoutUnreadyEgress(all); len(got) != 2 {
		t.Fatalf("a node set with nothing to drop lost nodes: %v", probeNodeIDs(got))
	}
}

func probeNodeIDs(nodes []*domain.Node) []int64 {
	out := make([]int64, 0, len(nodes))
	for _, n := range nodes {
		out = append(out, n.ID)
	}
	return out
}
