package domain

import "testing"

func TestRouteEntryAndDefaultLabel(t *testing.T) {
	n := &Node{HideDirect: true, Relays: []RelayLine{{ID: "on", Enabled: true}, {ID: "off"}}}
	line := VlessRouteLine{OutboundTag: "warp"}
	if relay, ok := line.ResolveEntry(n); !ok || relay != nil || line.Label() != "warp" {
		t.Fatal("legacy route did not use the direct entry and outbound label")
	}
	line.Name = "custom"
	if line.Label() != "custom" {
		t.Fatal("custom name was replaced")
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
