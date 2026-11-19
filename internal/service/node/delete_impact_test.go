package node

import (
	"context"
	"reflect"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

func TestLinesEnteringServerListsForwardsAndTunnelsOnly(t *testing.T) {
	nodes := []*domain.Node{
		{ID: 1, DisplayName: "JP", Relays: []domain.RelayLine{
			{ID: "fwd", Name: "GZ", EntryPanelID: 9},
			{ID: "other", Name: "SH", EntryPanelID: 8},
			{ID: "ext", Name: "external"},
			{ID: "tun", Address: "hk.example", Mode: domain.RelayModeTunnel, EntryPanelID: 9, TunnelNodeID: 5},
			{ID: "cdn", Name: "cf", Mode: domain.RelayModeCDN},
		}},
		{ID: 5, DisplayName: "listener", PanelID: 9, ParentNodeID: 1, ParentLineID: "tun"},
	}
	got := LinesEnteringServer(nodes, 9)
	want := []DeleteImpactLine{
		{NodeID: 1, LineID: "fwd", LandingName: "JP", LineName: "GZ", LineType: "forward"},
		{NodeID: 1, LineID: "tun", LandingName: "JP", LineName: "hk.example", LineType: "tunnel"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}
	if got := LinesEnteringServer(nodes, 0); len(got) != 0 {
		t.Fatalf("server 0 means no entry, got %+v", got)
	}
}

type impactRepo struct {
	ports.NodeRepo
	nodes map[int64]*domain.Node
}

func (r impactRepo) GetByID(_ context.Context, id int64) (*domain.Node, error) {
	if n := r.nodes[id]; n != nil {
		return n, nil
	}
	return nil, domain.ErrNotFound
}

type impactFronts struct {
	ports.TransitFrontRepo
	fronts []domain.TransitFront
}

func (r impactFronts) List(context.Context) ([]domain.TransitFront, error) { return r.fronts, nil }

func TestNodeDeleteImpactListsLinesTunnelsAndBlockingRoutes(t *testing.T) {
	landing := &domain.Node{ID: 1, PanelID: 7, DisplayName: "JP", Relays: []domain.RelayLine{
		{ID: "fwd", Name: "GZ", EntryPanelID: 9},
		{ID: "tun", Name: "HK", Mode: domain.RelayModeTunnel, EntryPanelID: 9, TunnelNodeID: 2},
	}}
	child := &domain.Node{ID: 2, PanelID: 9, DisplayName: "JP · HK", ParentNodeID: 1, ParentLineID: "tun", DesiredPort: 24443}
	svc := &Service{
		nodes: impactRepo{nodes: map[int64]*domain.Node{1: landing, 2: child}},
		fronts: impactFronts{fronts: []domain.TransitFront{{ID: 1, PanelID: 9, Port: 443, Routes: []domain.FrontRoute{
			{ServerNames: []string{"a.example"}, Target: domain.FrontTarget{Kind: domain.FrontTargetForward, NodeID: 1}},
			{ServerNames: []string{"b.example"}, Target: domain.FrontTarget{Kind: domain.FrontTargetForward, NodeID: 99}},
		}}}},
	}
	got, err := svc.DeleteImpact(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	wantLines := []DeleteImpactLine{
		{NodeID: 1, LineID: "direct", LandingName: "JP", LineName: "JP", LineType: "direct"},
		{NodeID: 1, LineID: "fwd", LandingName: "JP", LineName: "GZ", LineType: "forward"},
		{NodeID: 1, LineID: "tun", LandingName: "JP", LineName: "HK", LineType: "tunnel"},
	}
	if !reflect.DeepEqual(got.Lines, wantLines) {
		t.Fatalf("lines %+v", got.Lines)
	}
	if len(got.Tunnels) != 1 || got.Tunnels[0].NodeID != 2 || got.Tunnels[0].Port != 24443 || got.Tunnels[0].LineID != "tun" {
		t.Fatalf("tunnels %+v", got.Tunnels)
	}
	if len(got.BlockedByRoutes) != 1 {
		t.Fatalf("routes %v", got.BlockedByRoutes)
	}
	// A hidden listener is not deleted as a node.
	if _, err := svc.DeleteImpact(context.Background(), 2); err == nil {
		t.Fatal("tunnel listener accepted")
	}
}
