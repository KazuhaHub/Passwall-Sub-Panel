package render

import (
	"reflect"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

func TestSpecificLineAndLegacyNodeMembership(t *testing.T) {
	n := &domain.Node{ID: 7, DisplayName: "A", Enabled: true, Relays: []domain.RelayLine{{ID: "r1", Name: "R1", Enabled: true}, {ID: "r2", Name: "R2", Enabled: true}}}
	items := expandRelays([]renderItem{{node: n, name: "A"}})
	for _, tc := range []struct {
		name    string
		members []domain.ProxyGroupMember
		want    []string
	}{
		{"legacy node", []domain.ProxyGroupMember{{Kind: "node", NodeID: 7}}, []string{"A", "A R1", "A R2"}},
		{"direct only", []domain.ProxyGroupMember{{Kind: "line", NodeID: 7, LineID: "direct"}}, []string{"A"}},
		{"one relay", []domain.ProxyGroupMember{{Kind: "line", NodeID: 7, LineID: "r2"}}, []string{"A R2"}},
		{"overlap order", []domain.ProxyGroupMember{{Kind: "line", NodeID: 7, LineID: "r2"}, {Kind: "node", NodeID: 7}}, []string{"A R2", "A", "A R1"}},
		{"wrong parent", []domain.ProxyGroupMember{{Kind: "line", NodeID: 8, LineID: "r2"}}, []string{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := resolveConfiguredMembers(tc.members, items)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %v want %v", got, tc.want)
			}
		})
	}
	n.HideDirect = true
	if got := resolveConfiguredMembers([]domain.ProxyGroupMember{{Kind: "line", NodeID: 7, LineID: "direct"}}, expandRelays([]renderItem{{node: n, name: "A"}})); len(got) != 0 {
		t.Fatal("precise reference bypassed hidden direct")
	}
}

func TestLineCatalogAndPreviewUseExpandedRelays(t *testing.T) {
	n := &domain.Node{ID: 7, DisplayName: "A", Enabled: true, HideDirect: true, Relays: []domain.RelayLine{{ID: "r1", Name: "R1", Enabled: true}, {ID: "r2", Name: "R2"}}}
	inspection := InspectProxyGroups("MATCH,Group", map[string][]domain.ProxyGroupMember{"Group": {{Kind: "line", NodeID: 7, LineID: "r1"}}}, nil, []*domain.Node{n})
	if len(inspection.Lines) != 3 || inspection.Lines[0].Available || inspection.Lines[2].Available {
		t.Fatalf("bad availability: %+v", inspection.Lines)
	}
	if got := resolveConfiguredMembers([]domain.ProxyGroupMember{{Kind: "line", NodeID: 7, LineID: "r1"}}, expandNodeLines([]renderItem{{node: n, name: "A"}})); !reflect.DeepEqual(got, []string{"A R1"}) {
		t.Fatalf("expanded members: %+v", got)
	}
}

func TestLineCatalogKeepsOwnNamesSeparateFromSubscriptionLabels(t *testing.T) {
	n := &domain.Node{ID: 7, DisplayName: "Parent Node", Enabled: true,
		Relays:     []domain.RelayLine{{ID: "relay", Name: "Parent Node relay", Enabled: true}, {ID: "fallback", Address: "relay.example", Enabled: true}},
		RouteState: domain.NodeRouteState{Lines: []domain.VlessRouteLine{{ID: "route", Name: "my-vlessroute-line", Enabled: true}}},
	}
	got := inspectNodeLines([]*domain.Node{n})
	wantNames := []string{"Parent Node", "Parent Node relay", "relay.example", "my-vlessroute-line"}
	wantLabels := []string{"Parent Node", "Parent Node Parent Node relay", "Parent Node relay.example", "Parent Node my-vlessroute-line"}
	if len(got) != len(wantNames) {
		t.Fatal(got)
	}
	for i, line := range got {
		if line.Name != wantNames[i] || line.DisplayName != wantLabels[i] {
			t.Fatalf("line %d lost its own name or changed its subscription label: %+v", i, line)
		}
	}
}
