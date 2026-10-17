package render

import (
	"context"
	"encoding/base64"
	"strings"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

type routeRenderPanelRepo struct {
	ports.XUIPanelRepo
	panel domain.XUIPanel
}

func (r *routeRenderPanelRepo) GetByID(context.Context, int64) (*domain.XUIPanel, error) {
	p := r.panel
	return &p, nil
}

func routeRenderFixture(t *testing.T) (*Service, *domain.Node, *domain.User, *routeRenderPanelRepo) {
	t.Helper()
	n := vlessRealityNode(true)
	n.DisplayName = "A"
	p := &routeRenderPanelRepo{panel: domain.XUIPanel{ID: n.PanelID, Kind: domain.PanelKind3XUI, URL: "https://panel.example"}}
	n.RouteState = domain.NodeRouteState{Status: "applied", Binding: domain.RouteBinding(p.panel.URL, n), Lines: []domain.VlessRouteLine{{ID: "route-B", Name: "B", Route: 1001, OutboundTag: "warp", Enabled: true}}}
	u := &domain.User{ID: 5, UUID: "87654321-4321-4def-8abc-cba987654321"}
	repo := renderCredentialRepo{client: &domain.PSPClient{ID: 11, UserID: 5, UUID: u.UUID}, attachments: []domain.PSPClientInbound{{ClientID: 11, NodeID: n.ID, AppliedUUID: "12345678-1234-4abc-8def-123456789abc", AppliedEmail: "u5@example.test"}}}
	s := &Service{repos: ports.Repos{Settings: fakeSettings{}, PSPClient: repo, XUIPanel: p}, pool: panicPool{}}
	return s, n, u, p
}

func TestRouteAllSubscriptionFormatsUseAppliedUUIDAndZeroRemoteReads(t *testing.T) {
	for _, matched := range []bool{false, true} {
		t.Run(map[bool]string{false: "legacy", true: "optional_match"}[matched], func(t *testing.T) { testRouteFormatsWithOptionalMatch(t, matched) })
	}
}

func testRouteFormatsWithOptionalMatch(t *testing.T, matched bool) {
	t.Helper()
	s, n, u, _ := routeRenderFixture(t)
	if matched {
		line := &n.RouteState.Lines[0]
		line.Network, line.Protocols, line.IP, line.Domain, line.Port = "tcp", []string{"tls"}, []string{"192.0.2.0/24"}, []string{"domain:example.com"}, "443"
	}
	items := s.filterRouteItems(context.Background(), u, expandNodeLines([]renderItem{{node: n, name: "A"}}))
	if len(items) != 2 {
		t.Fatalf("route not expanded: %+v", items)
	}
	proxies := s.buildProxies(context.Background(), u, items, ports.UISettings{})
	if len(proxies) != 2 || proxies[0]["uuid"] != "12345678-1234-4abc-8def-123456789abc" || proxies[1]["uuid"] != "12345678-1234-03e9-8def-123456789abc" {
		t.Fatalf("wrong mihomo credentials: %+v", proxies)
	}
	singbox := s.buildSingBoxOutbounds(context.Background(), u, items, nil, nil, nil, ports.UISettings{})
	found := false
	for _, outbound := range singbox {
		if outbound["tag"] == "A B" {
			found = true
			if outbound["uuid"] != "12345678-1234-03e9-8def-123456789abc" {
				t.Fatal(outbound)
			}
		}
	}
	if !found {
		t.Fatal("sing-box route missing")
	}
	uri, err := s.renderURIList(context.Background(), u, items, ports.UISettings{})
	if err != nil {
		t.Fatal(err)
	}
	body, err := base64.StdEncoding.DecodeString(string(uri.Body))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "vless://12345678-1234-03e9-8def-123456789abc@") || strings.Contains(string(body), u.UUID) {
		t.Fatal("URI list used desired rather than applied identity")
	}
}

func TestRoutePublicationRequiresExactBackendAndAppliedCredential(t *testing.T) {
	for _, kind := range []string{"unknown", "changed_backend", "unsupported_backend", "missing_applied", "original_route_collision", "hidden_direct"} {
		t.Run(kind, func(t *testing.T) {
			s, n, u, p := routeRenderFixture(t)
			switch kind {
			case "unknown":
				n.RouteState.Status = "unknown"
			case "hidden_direct":
				n.HideDirect = true
			case "changed_backend":
				p.panel.URL = "https://different.example"
			case "unsupported_backend":
				p.panel.Kind = domain.PanelKindSUI
			case "missing_applied":
				s.repos.PSPClient = nil
			case "original_route_collision":
				n.RouteState.Lines[0].Route = 0x4abc
			}
			items := s.filterRouteItems(context.Background(), u, expandNodeLines([]renderItem{{node: n, name: "A"}}))
			for _, item := range items {
				if item.route != nil {
					t.Fatalf("published unconfirmed route for %s", kind)
				}
			}
			if len(s.PublishedRouteLines(context.Background(), u, []*domain.Node{n})[n.ID]) != 0 {
				t.Fatal("status metadata bypassed the subscription publication gates")
			}
			if kind != "hidden_direct" && len(items) != 1 {
				t.Fatal("withdrawing a route also removed original/relay node")
			}
		})
	}
}
