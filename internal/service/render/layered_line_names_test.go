package render

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

func TestLayeredNamesReserveTypesThenExtendShortIdentities(t *testing.T) {
	for _, tc := range []struct {
		name  string
		lines []subscriptionLineName
		fixed []string
		want  []string
	}{
		{"different types", []subscriptionLineName{{nodeID: 1, lineID: "direct", kind: "直连"}, {nodeID: 1, lineID: "52787c21-62ab", kind: "中转"}, {nodeID: 1, lineID: "6b752549-4a51", kind: "VLESS 路由"}}, nil, []string{"same [直连]", "same [中转]", "same [VLESS 路由]"}},
		{"same type", []subscriptionLineName{{nodeID: 1, lineID: "52787c21-62ab", kind: "中转"}, {nodeID: 1, lineID: "6b752549-4a51", kind: "中转"}}, nil, []string{"same [中转:52787c21]", "same [中转:6b752549]"}},
		{"shared prefix", []subscriptionLineName{{nodeID: 1, lineID: "52787c21-62ab", kind: "VLESS 路由"}, {nodeID: 1, lineID: "52787c21-72ab", kind: "VLESS 路由"}}, nil, []string{"same [VLESS 路由:52787c21-6]", "same [VLESS 路由:52787c21-7]"}},
		{"fixed type and prefix", []subscriptionLineName{{nodeID: 1, lineID: "52787c21-62ab", kind: "中转"}}, []string{"same", "same [中转]", "same [中转:52787c21]"}, []string{"same [中转:52787c21-]"}},
		{"same ID across nodes", []subscriptionLineName{{nodeID: 1, lineID: "52787c21", kind: "中转"}, {nodeID: 2, lineID: "52787c21", kind: "中转"}}, []string{"same [中转:52787c21:1]", "same [中转:52787c21:1] [2]"}, []string{"same [中转:52787c21:1] [3]", "same [中转:52787c21:2]"}},
		{"direct has no UUID", []subscriptionLineName{{nodeID: 1, lineID: "direct", kind: "直连"}, {nodeID: 2, lineID: "direct", kind: "直连"}}, nil, []string{"same [直连:1]", "same [直连:2]"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := &lineNameCatalog{lines: map[string]subscriptionLineName{}}
			kept, fixed, want := map[string]bool{}, map[string]bool{}, map[string]string{}
			for i, line := range tc.lines {
				line.base, line.token = "same", fmt.Sprint(i)
				c.lines[line.token], kept[line.token], want[line.token] = line, true, tc.want[i]
			}
			for _, name := range tc.fixed {
				fixed[name] = true
			}
			for i := 0; i < 30; i++ {
				if got := c.allocate(kept, fixed); !reflect.DeepEqual(got, want) {
					t.Fatalf("got %v want %v", got, want)
				}
			}
			if !fixed["same"] {
				if got := c.allocate(map[string]bool{"0": true}, fixed); got["0"] != "same" {
					t.Fatalf("unreferenced siblings still caused a suffix: %v", got)
				}
			}
		})
	}
}

func TestLayeredNamesSameTypeSubscriptionsAndPreviewShareIdentity(t *testing.T) {
	for _, ct := range []domain.ClientType{domain.ClientMihomo, domain.ClientSingBox} {
		for _, kind := range []string{"中转", "VLESS 路由"} {
			t.Run(string(ct)+kind, func(t *testing.T) {
				s, node, user, rule := collidingNameFixture(t)
				user.GroupID = 1
				first, second := "52787c21-62ab-4496-94fb-6f4b4fb51312", "52787c21-72ab-4496-94fb-6f4b4fb51312"
				if kind == "中转" {
					node.Relays[0].ID = first
					other := node.Relays[0]
					other.ID, other.Address = second, "other.example"
					node.Relays = append(node.Relays, other)
				} else {
					node.RouteState.Lines[0].ID = first
					other := node.RouteState.Lines[0]
					other.ID, other.Route = second, 1002
					node.RouteState.Lines = append(node.RouteState.Lines, other)
				}
				rule.ProxyGroupMembers["国外媒体"] = []domain.ProxyGroupMember{{Kind: "line", NodeID: node.ID, LineID: first}, {Kind: "line", NodeID: node.ID, LineID: second}}
				output, err := s.RenderForUser(context.Background(), user, ct)
				if err != nil {
					t.Fatal(err)
				}
				got := namedVLESSProxies(t, output, ct)
				want := []string{"same [" + kind + ":52787c21-6]", "same [" + kind + ":52787c21-7]"}
				if len(got) != 2 || got[want[0]] == nil || got[want[1]] == nil {
					t.Fatalf("wrong lines: %v", got)
				}
				if kind == "中转" && (got[want[0]]["server"] != "relay.example" || got[want[1]]["server"] != "other.example") {
					t.Fatal("short names switched endpoints")
				}
				if kind == "VLESS 路由" && (got[want[0]]["uuid"] != "12345678-1234-03e9-8def-123456789abc" || got[want[1]]["uuid"] != "12345678-1234-03ea-8def-123456789abc") {
					t.Fatal("short names switched route credentials")
				}
				preview, err := s.PreviewMihomoGroup(context.Background(), 1, rule)
				if err != nil || !reflect.DeepEqual(preview.Groups[0].Members, want) {
					t.Fatalf("wrong preview: %+v %v", preview, err)
				}
				// Reordering members changes order, never the identity suffix.
				rule.ProxyGroupMembers["国外媒体"][0], rule.ProxyGroupMembers["国外媒体"][1] = rule.ProxyGroupMembers["国外媒体"][1], rule.ProxyGroupMembers["国外媒体"][0]
				output, err = s.RenderForUser(context.Background(), user, ct)
				if err != nil || !reflect.DeepEqual(namedVLESSProxies(t, output, ct), got) {
					t.Fatalf("reordering changed identity: %v", err)
				}
				// New generated names can be handwritten references too. A
				// shortened reference cannot silently choose a same-prefix sibling.
				rule.Content = "- MATCH," + want[0]
				output, err = s.RenderForUser(context.Background(), user, ct)
				if err != nil || len(namedVLESSProxies(t, output, ct)) != 1 || namedVLESSProxies(t, output, ct)["same"] == nil {
					t.Fatalf("new alias failed: %v", err)
				}
				rule.Content = "- MATCH,same [" + kind + ":52787c21]"
				if _, err = s.RenderForUser(context.Background(), user, ct); err == nil || !strings.Contains(err.Error(), "ambiguous") {
					t.Fatalf("ambiguous short alias accepted: %v", err)
				}
			})
		}
	}
}

func TestLayeredTypedAliasPreservesUnavailableTargetAndOldIdentityAliases(t *testing.T) {
	for _, name := range []string{"same [VLESS 路由]", "same [7:route-B]"} {
		s, node, user, rule := collidingNameFixture(t)
		rule.Content = "- MATCH," + name
		node.RouteState.Lines[0].Enabled = false
		if _, err := s.RenderForUser(context.Background(), user, domain.ClientMihomo); err == nil || !strings.Contains(err.Error(), "unavailable") {
			t.Fatalf("unavailable alias %q selected a sibling: %v", name, err)
		}
	}
}
