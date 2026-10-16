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
