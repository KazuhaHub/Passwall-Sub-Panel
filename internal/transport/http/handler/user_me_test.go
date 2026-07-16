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
		DisplayName: "Tokyo", Region: "JP", Port: 443,
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
}
