package domain

import "testing"

func TestVlessRouteChangesOnlyTheTwoRouteBytes(t *testing.T) {
	const original = "12345678-1234-4abc-8def-123456789abc"
	got, err := WithUUIDRoute(original, 1001)
	if err != nil {
		t.Fatal(err)
	}
	if got != "12345678-1234-03e9-8def-123456789abc" {
		t.Fatal(got)
	}
	route, err := UUIDRoute(got)
	if err != nil || route != 1001 {
		t.Fatalf("%d %v", route, err)
	}
	if _, err := WithUUIDRoute("not-a-uuid", 1001); err == nil {
		t.Fatal("accepted invalid UUID")
	}
}

func TestVlessRouteDirectInboundDoesNotDependOnDirectLineVisibility(t *testing.T) {
	for _, hidden := range []bool{false, true} {
		n := &Node{Enabled: true, HideDirect: hidden, DesiredProtocol: "vless", RouteState: NodeRouteState{Status: "applied"}}
		line := VlessRouteLine{ID: "route", Enabled: true}
		if relay, available := line.ResolveEntry(n); relay != nil || !available || !line.LocallyAvailable(n) {
			t.Fatalf("direct inbound was coupled to hidden=%v", hidden)
		}
		line.Enabled = false
		if line.LocallyAvailable(n) {
			t.Fatal("disabled route became available")
		}
		line.Enabled = true
		for _, state := range []string{"unknown", "drift", "applying"} {
			n.RouteState.Status = state
			if line.LocallyAvailable(n) {
				t.Fatalf("unconfirmed state %q published a route", state)
			}
		}
		n.RouteState.Status = "applied"
		n.Enabled = false
		if line.LocallyAvailable(n) {
			t.Fatal("disabled node published a route")
		}
		if line.Enabled != true || line.EntryRelayID != "" || n.HideDirect != hidden {
			t.Fatal("publication mutated saved intent")
		}
	}
	if _, available := (VlessRouteLine{}).ResolveEntry(nil); available {
		t.Fatal("nil node has no inbound")
	}
}
