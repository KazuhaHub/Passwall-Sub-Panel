package render

import (
	"context"
	"encoding/base64"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

func TestLineOnlyNamesAcrossFormatsAndParentRegionFlags(t *testing.T) {
	for _, onlyName := range []bool{false, true} {
		for _, flags := range []bool{false, true} {
			s, n, user, _ := routeRenderFixture(t)
			n.DisplayName, n.Region = "Parent", "HK"
			n.Relays = []domain.RelayLine{{ID: "relay", Name: "JP relay", Address: "relay.example", Enabled: true, OnlyLineName: onlyName}}
			n.RouteState.Lines[0].Name = "US route"
			n.RouteState.Lines[0].OnlyLineName = onlyName
			want := []string{"Parent", "Parent JP relay", "Parent US route"}
			if onlyName {
				want[1], want[2] = "JP relay", "US route"
			}
			catalog := inspectNodeLines([]*domain.Node{n})
			for i, line := range catalog {
				if line.DisplayName != want[i] {
					t.Fatalf("catalog disagrees with subscription label: %+v", line)
				}
			}
			items := s.filterRouteItems(context.Background(), user, expandNodeLines([]renderItem{{node: n, name: n.DisplayName}}))
			if flags {
				applyRegionFlagPrefix(items)
				for i := range want {
					want[i] = "🇭🇰 " + want[i]
				}
			}
			var names []string
			for _, item := range items {
				names = append(names, item.name)
				if item.node != n {
					t.Fatal("line lost its parent node")
				}
			}
			if !reflect.DeepEqual(names, want) {
				t.Fatalf("onlyName=%v flags=%v: got %v want %v", onlyName, flags, names, want)
			}
			proxies := s.buildProxies(context.Background(), user, items, ports.UISettings{})
			outbounds := s.buildSingBoxOutbounds(context.Background(), user, items, nil, nil, nil, ports.UISettings{})
			var singboxNames []string
			for _, outbound := range outbounds {
				if outbound["type"] == "vless" {
					singboxNames = append(singboxNames, outbound["tag"].(string))
				}
			}
			if len(proxies) != len(want) || !reflect.DeepEqual(singboxNames, want) {
				t.Fatalf("unexpected proxy names/count: Mihomo=%v sing-box=%v", proxies, singboxNames)
			}
			for i, name := range want {
				if proxies[i]["name"] != name {
					t.Fatalf("Mihomo/sing-box label mismatch at %d", i)
				}
			}
			rendered, err := s.renderURIList(context.Background(), user, items, ports.UISettings{})
			if err != nil {
				t.Fatal(err)
			}
			body, err := base64.StdEncoding.DecodeString(string(rendered.Body))
			if err != nil {
				t.Fatal(err)
			}
			uris := strings.Split(strings.TrimSpace(string(body)), "\n")
			if len(uris) != len(want) {
				t.Fatalf("unexpected URI count: %d", len(uris))
			}
			for i, raw := range uris {
				uri, err := url.Parse(raw)
				if err != nil || uri.Fragment != want[i] {
					t.Fatalf("URI label mismatch: %s %v", raw, err)
				}
			}
		}
	}
}

func TestLineOnlyNamingKeepsRelayFallbackAndUniqueIdentities(t *testing.T) {
	if got := relayEntryName("Parent", domain.RelayLine{Address: "relay.example", OnlyLineName: true}); got != "relay.example" {
		t.Fatal(got)
	}
	n := &domain.Node{ID: 1, Enabled: true, DesiredProtocol: "vless",
		Relays:     []domain.RelayLine{{ID: "relay", Name: "same", OnlyLineName: true, Enabled: true}},
		RouteState: domain.NodeRouteState{Status: "applied", Lines: []domain.VlessRouteLine{{ID: "route", Name: "same", OnlyLineName: true, Enabled: true}}},
	}
	items := expandNodeLines([]renderItem{{node: n, name: "Parent"}})
	if items[1].name == items[2].name || items[1].lineID() != "relay" || items[2].lineID() != "route" {
		t.Fatal("line-only labels broke disambiguation or stable references")
	}
}

func TestLineNameOptionsRemainIndependent(t *testing.T) {
	n := &domain.Node{ID: 1, Enabled: true, DesiredProtocol: "vless",
		Relays: []domain.RelayLine{
			{ID: "r1", Name: "relay-off", Enabled: true},
			{ID: "r2", Name: "relay-on", OnlyLineName: true, Enabled: true},
		},
		RouteState: domain.NodeRouteState{Status: "applied", Lines: []domain.VlessRouteLine{
			{ID: "v1", Name: "route-off", Enabled: true},
			{ID: "v2", Name: "route-on", OnlyLineName: true, Enabled: true},
		}},
	}
	var names []string
	for _, item := range expandNodeLines([]renderItem{{node: n, name: "Parent"}}) {
		names = append(names, item.name)
	}
	if want := []string{"Parent", "Parent relay-off", "relay-on", "Parent route-off", "route-on"}; !reflect.DeepEqual(names, want) {
		t.Fatalf("line options affected another line: %v", names)
	}
}
