package render

import (
	"context"
	"encoding/base64"
	"net/url"
	"strings"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

func TestRoutesChooseOneVisibleEntryAcrossFormats(t *testing.T) {
	for _, relayEntry := range []bool{false, true} {
		s, n, user, _ := routeRenderFixture(t)
		n.HideDirect = relayEntry
		n.Relays = []domain.RelayLine{{ID: "front", Name: "R", Address: "relay.example", Port: 8443, SNI: "relay-sni.example", Enabled: true}}
		line := &n.RouteState.Lines[0]
		line.Name, line.OutboundTag, line.OnlyLineName = "", "warp", true
		wantHost, wantPort := n.ServerAddress, n.DesiredPort
		if relayEntry {
			line.EntryRelayID = "front"
			wantHost, wantPort = "relay.example", 8443
		}
		items := s.filterRouteItems(context.Background(), user, expandNodeLines([]renderItem{{node: n, name: n.DisplayName}}))
		var routes []renderItem
		for _, item := range items {
			if item.route != nil {
				routes = append(routes, item)
			} else if n.HideDirect && item.relay == nil {
				t.Fatal("hidden standalone direct entry was published")
			}
		}
		if len(routes) != 1 || routes[0].name != "warp" || routes[0].lineID() != line.ID {
			t.Fatal("route was hidden, renamed incorrectly or expanded as a cross product")
		}
		proxy := s.buildProxies(context.Background(), user, routes, ports.UISettings{})[0]
		if proxy["server"] != wantHost || proxy["port"] != wantPort || proxy["uuid"] != "12345678-1234-03e9-8def-123456789abc" {
			t.Fatalf("Mihomo did not preserve route credentials and selected entry: %+v", proxy)
		}
		found := false
		for _, outbound := range s.buildSingBoxOutbounds(context.Background(), user, routes, nil, nil, nil, ports.UISettings{}) {
			if outbound["tag"] == "warp" {
				found = outbound["server"] == wantHost && outbound["server_port"] == wantPort && outbound["uuid"] == proxy["uuid"]
			}
		}
		if !found {
			t.Fatal("sing-box lost the entry or route credentials")
		}
		rendered, err := s.renderURIList(context.Background(), user, routes, ports.UISettings{})
		if err != nil {
			t.Fatal(err)
		}
		body, err := base64.StdEncoding.DecodeString(string(rendered.Body))
		if err != nil {
			t.Fatal(err)
		}
		uri, err := url.Parse(strings.TrimSpace(string(body)))
		if err != nil || uri.Hostname() != wantHost || uri.Fragment != "warp" || uri.User.Username() != proxy["uuid"] {
			t.Fatalf("URI entry/name/credentials mismatch: %s %v", body, err)
		}
		catalog := inspectNodeLines([]*domain.Node{n})
		if !catalog[len(catalog)-1].Available || catalog[len(catalog)-1].Name != "warp" {
			t.Fatal("hidden direct leaked into route availability or default name")
		}
	}
}

func TestRoutesWithUnavailableEntriesStayWithdrawn(t *testing.T) {
	for _, entryID := range []string{"", "disabled", "deleted"} {
		s, n, user, _ := routeRenderFixture(t)
		n.Relays = []domain.RelayLine{{ID: "disabled", Address: "relay.example"}}
		n.HideDirect = true
		n.RouteState.Lines[0].EntryRelayID = entryID
		items := s.filterRouteItems(context.Background(), user, expandNodeLines([]renderItem{{node: n, name: n.DisplayName}}))
		for _, item := range items {
			if item.route != nil {
				t.Fatal("unavailable relay route fell back to direct")
			}
		}
		if len(s.PublishedRouteLines(context.Background(), user, []*domain.Node{n})[n.ID]) != 0 {
			t.Fatal("status page published an unavailable route")
		}
		catalog := inspectNodeLines([]*domain.Node{n})
		if line := catalog[len(catalog)-1]; line.Available || line.Enabled || line.Reason != "entry_unavailable" {
			t.Fatalf("unavailable route was selectable: %+v", line)
		}
		for _, kind := range []string{"node", "line"} {
			member := domain.ProxyGroupMember{Kind: kind, NodeID: n.ID}
			if kind == "line" {
				member.LineID = n.RouteState.Lines[0].ID
			}
			inspection := InspectProxyGroups("- MATCH,Pick", map[string][]domain.ProxyGroupMember{"Pick": {member}}, nil, []*domain.Node{n})
			for _, issue := range inspection.Issues {
				if issue.Level == "error" {
					t.Fatal(issue)
				}
			}
			for _, group := range inspection.Groups {
				for _, name := range group.Preview {
					if name == "A B" {
						t.Fatal("node/line selection preview published inactive route")
					}
				}
			}
		}
		proxies := s.buildProxies(context.Background(), user, items, ports.UISettings{})
		for _, proxy := range proxies {
			if proxy["type"] == "vless" {
				t.Fatal("Mihomo published inactive entry")
			}
		}
		for _, outbound := range s.buildSingBoxOutbounds(context.Background(), user, items, nil, nil, nil, ports.UISettings{}) {
			if outbound["tag"] == "A B" {
				t.Fatal("sing-box published inactive entry")
			}
		}
		body, err := s.renderURIList(context.Background(), user, items, ports.UISettings{})
		if err != nil {
			t.Fatal(err)
		}
		decoded, _ := base64.StdEncoding.DecodeString(string(body.Body))
		if strings.Contains(string(decoded), "vless://") {
			t.Fatal("URI list published inactive entry")
		}
		if !n.RouteState.Lines[0].Enabled {
			t.Fatal("dependency changed configured intent")
		}
	}
}
