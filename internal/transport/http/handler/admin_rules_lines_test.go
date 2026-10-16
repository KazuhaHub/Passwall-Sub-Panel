package handler

import (
	"context"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

type linePreviewPanels struct {
	ports.XUIPanelRepo
	panel domain.XUIPanel
}

func (p linePreviewPanels) GetByID(context.Context, int64) (*domain.XUIPanel, error) {
	copy := p.panel
	return &copy, nil
}

func TestLineInspectionBindingGateDoesNotMutateCachedNodes(t *testing.T) {
	n := &domain.Node{ID: 1, PanelID: 1, InboundID: 1, DesiredPort: 443, DesiredProtocol: "vless", Enabled: true, RouteState: domain.NodeRouteState{Status: "applied", Lines: []domain.VlessRouteLine{{ID: "route", Route: 1001, Name: "B", OutboundTag: "warp", Enabled: true}}}}
	p := domain.XUIPanel{ID: 1, Kind: domain.PanelKind3XUI, URL: "https://panel.example"}
	n.RouteState.Binding = domain.RouteBinding(p.URL, n)
	h := &AdminRuleSetsHandler{routePanels: linePreviewPanels{panel: p}}
	if got := h.routePreviewNodes(context.Background(), []*domain.Node{n}); got[0].RouteState.Status != "applied" {
		t.Fatal("confirmed binding unavailable")
	}
	p.URL = "https://replaced.example"
	h.routePanels = linePreviewPanels{panel: p}
	got := h.routePreviewNodes(context.Background(), []*domain.Node{n})
	if got[0].RouteState.Status != "drift" || n.RouteState.Status != "applied" || got[0] == n {
		t.Fatal("preview bypassed binding or mutated cached node")
	}
}
