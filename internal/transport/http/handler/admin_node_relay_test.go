package handler

import "testing"

// TestParseRelays_Valid pins the happy path: fields are trimmed, port 0 (reuse
// the inbound port) is allowed, and the order is preserved.
func TestParseRelays_Valid(t *testing.T) {
	in := []relayLineDTO{
		{Name: "  广州移动中转 ", Address: " gz.relay.cn ", Port: 20001, Enabled: true},
		{Name: "L4", Address: "sh.relay.cn", Port: 0, Enabled: false},
	}
	got, err := parseRelays(in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 lines, got %d", len(got))
	}
	if got[0].Name != "广州移动中转" || got[0].Address != "gz.relay.cn" {
		t.Fatalf("trimming failed: %#v", got[0])
	}
	if got[1].Port != 0 {
		t.Fatalf("port 0 (reuse inbound) should be allowed, got %d", got[1].Port)
	}
}

// TestParseRelays_Rejects pins the 400-worthy cases: missing address, an
// out-of-range port, and exceeding the per-node line cap.
func TestParseRelays_Rejects(t *testing.T) {
	if _, err := parseRelays([]relayLineDTO{{Address: "   ", Port: 1}}); err == nil {
		t.Fatal("blank address should be rejected")
	}
	if _, err := parseRelays([]relayLineDTO{{Address: "a", Port: 70000}}); err == nil {
		t.Fatal("port > 65535 should be rejected")
	}
	if _, err := parseRelays([]relayLineDTO{{Address: "a", Port: -1}}); err == nil {
		t.Fatal("negative port should be rejected")
	}
	too := make([]relayLineDTO, maxRelayLines+1)
	for i := range too {
		too[i] = relayLineDTO{Address: "a"}
	}
	if _, err := parseRelays(too); err == nil {
		t.Fatalf("more than %d lines should be rejected", maxRelayLines)
	}
}

// TestParseRelays_Empty: no lines round-trips to a nil slice (relay-less node).
func TestParseRelays_Empty(t *testing.T) {
	got, err := parseRelays(nil)
	if err != nil || got != nil {
		t.Fatalf("empty input → (nil,nil), got (%#v, %v)", got, err)
	}
}
