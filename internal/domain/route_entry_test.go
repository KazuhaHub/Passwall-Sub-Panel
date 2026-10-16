package domain

import "testing"

func TestRouteEntryAndDefaultLabel(t *testing.T) {
	n := &Node{Relays: []RelayLine{{ID: "on", Enabled: true}, {ID: "off"}}}
	line := VlessRouteLine{OutboundTag: "warp"}
	if relay, ok := line.ResolveEntry(n); !ok || relay != nil || line.Label() != "warp" {
		t.Fatal("legacy route did not use the direct entry and outbound label")
	}
	line.Name = "custom"
	if line.Label() != "custom" {
		t.Fatal("custom name was replaced")
	}
	n.HideDirect = true
	if _, ok := line.ResolveEntry(n); ok {
		t.Fatal("hidden direct entry remained available")
	}
	line.EntryRelayID = "on"
	if relay, ok := line.ResolveEntry(n); !ok || relay.ID != "on" {
		t.Fatal("enabled relay not selected")
	}
	for _, id := range []string{"off", "deleted"} {
		line.EntryRelayID = id
		if _, ok := line.ResolveEntry(n); ok {
			t.Fatal("missing/disabled relay fell back to direct")
		}
	}
}

func TestRouteEffectiveAvailabilityPreservesConfiguredIntent(t *testing.T) {
	n := &Node{Enabled: true, DesiredProtocol: "vless", RouteState: NodeRouteState{Status: "applied"}, Relays: []RelayLine{{ID: "front", Enabled: true}}}
	line := VlessRouteLine{Enabled: true}
	if !line.LocallyAvailable(n) {
		t.Fatal("visible direct entry unavailable")
	}
	n.HideDirect = true
	if line.LocallyAvailable(n) || !line.Enabled {
		t.Fatal("hidden direct route was enabled or configuration was lost")
	}
	line.EntryRelayID = "front"
	if !line.LocallyAvailable(n) {
		t.Fatal("hiding direct also disabled a relay route")
	}
	n.Relays[0].Enabled = false
	if line.LocallyAvailable(n) {
		t.Fatal("disabled relay route available")
	}
	n.Relays[0].Enabled = true
	if !line.LocallyAvailable(n) {
		t.Fatal("restoring dependency failed to restore route")
	}
	for _, status := range []string{"unknown", "applying", "drift"} {
		n.RouteState.Status = status
		if line.LocallyAvailable(n) {
			t.Fatal("unconfirmed route available")
		}
	}
}
