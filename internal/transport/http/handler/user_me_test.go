package handler

import (
	"strings"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// TestCoarseNodeStatus pins the user-facing health bucketing. The security
// point: every distinct failure state (panel unreachable vs inbound missing vs
// inbound disabled) must collapse to the same "down" so the user-facing server
// status can't reveal WHERE in the stack a node failed — only that it's up,
// down, or not yet probed.
func TestCoarseNodeStatus(t *testing.T) {
	cases := []struct {
		in   domain.NodeHealthState
		want string
	}{
		{domain.NodeHealthOK, "ok"},
		{domain.NodeHealthPanelUnreachable, "down"},
		{domain.NodeHealthInboundMissing, "down"},
		{domain.NodeHealthInboundDisabled, "down"},
		{domain.NodeHealthUnreachable, "down"},
		{domain.NodeHealthUnknown, "unknown"},
		{domain.NodeHealthState("some-future-state"), "unknown"},
	}
	for _, tc := range cases {
		if got := coarseNodeStatus(tc.in); got != tc.want {
			t.Fatalf("coarseNodeStatus(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestUserStatusForNode_RelayVisibilityAndSanitization(t *testing.T) {
	checked := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	n := &domain.Node{
		DisplayName: "Tokyo", Region: "JP", DesiredPort: 443,
		HealthState: domain.NodeHealthOK, HealthCheckedAt: &checked,
		HideDirect: true,
		Relays: []domain.RelayLine{
			{Name: "Osaka relay", Address: "secret-relay.example", Port: 8443, Enabled: true},
			{Name: "disabled", Address: "disabled.example", Enabled: false},
		},
		RelayHealth: []domain.RelayHealth{{
			Index: 0, Address: "secret-relay.example", Port: 8443,
			State: domain.NodeHealthUnreachable, CheckedAt: &checked,
		}},
	}
	got := userStatusForNode(n)
	if got.ShowDirect {
		t.Fatal("hidden direct endpoint was exposed")
	}
	if len(got.RelayStatuses) != 1 || got.RelayStatuses[0].Status != "down" || got.RelayStatuses[0].Name != "Osaka relay" {
		t.Fatalf("relay statuses = %#v", got.RelayStatuses)
	}
	if strings.Contains(got.RelayStatuses[0].Name, "secret-relay.example") {
		t.Fatal("relay address leaked through user status")
	}
	n.HideDirect = false
	got = userStatusForNode(n)
	if !got.ShowDirect || len(got.RelayStatuses) != 1 {
		t.Fatal("relay status still depended on hiding the direct line")
	}
	n.Relays[0].Enabled = false
	if got := userStatusForNode(n); len(got.RelayStatuses) != 0 {
		t.Fatal("disabled relay exposed cached health")
	}
}

func TestUserStatusForNode_RoutesReuseEntryHealthAndNamingOptions(t *testing.T) {
	directTime := time.Date(2026, 10, 17, 1, 0, 0, 0, time.UTC)
	relayTime := directTime.Add(time.Minute)
	n := &domain.Node{Enabled: true, DisplayName: "Parent", DesiredProtocol: "vless", DesiredPort: 443,
		HealthState: domain.NodeHealthOK, HealthCheckedAt: &directTime,
		Relays:      []domain.RelayLine{{ID: "front", Name: "Front", OnlyLineName: true, Address: "private.example", Enabled: true}},
		RelayHealth: []domain.RelayHealth{{Index: 0, Address: "private.example", Port: 443, State: domain.NodeHealthUnreachable, CheckedAt: &relayTime}},
		RouteState: domain.NodeRouteState{Status: "applied", Lines: []domain.VlessRouteLine{
			{ID: "direct", Name: "Direct route", Enabled: true},
			{ID: "relay", OutboundTag: "warp", EntryRelayID: "front", OnlyLineName: true, Enabled: true},
			{ID: "disabled", Name: "Off", Enabled: false},
			{ID: "deleted", Name: "Gone", Enabled: true, Deleted: true},
			{ID: "missing", Name: "Missing", EntryRelayID: "missing", Enabled: true},
		}},
	}
	got := userStatusForNode(n)
	if len(got.RouteStatuses) != 2 || !got.RelayStatuses[0].OnlyLineName {
		t.Fatalf("wrong visible lines: %+v", got)
	}
	direct, relay := got.RouteStatuses[0], got.RouteStatuses[1]
	if direct.Name != "Direct route" || direct.OnlyLineName || direct.Status != "ok" || direct.CheckedAt != &directTime {
		t.Fatal(direct)
	}
	if relay.Name != "warp" || !relay.OnlyLineName || relay.Status != "down" || relay.CheckedAt != &relayTime {
		t.Fatal(relay)
	}
	n.HideDirect = true
	if status := userStatusForNode(n); status.ShowDirect || len(status.RouteStatuses) != 2 || status.RouteStatuses[0].CheckedAt != &directTime {
		t.Fatal("hidden direct affected route inbound health or visibility")
	}
	n.Relays[0].Enabled = false
	if got := userStatusForNode(n); len(got.RouteStatuses) != 1 || got.RouteStatuses[0].Name != "Direct route" || len(got.RelayStatuses) != 0 {
		t.Fatal("inactive dependency exposed cached status")
	}
	n.HideDirect = false
	n.RouteState.Status = "drift"
	if len(userStatusForNode(n).RouteStatuses) != 0 {
		t.Fatal("unconfirmed route exposed")
	}
}
