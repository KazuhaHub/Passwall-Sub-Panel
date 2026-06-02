package handler

import "testing"

// TestRedactInboundForRole pins the operator-secret guard: the node-detail
// inbound DTO carries the raw Settings (client UUIDs / SS-Trojan passwords) and
// StreamSettings (Reality privateKey / TLS keys). Operators may read node
// metadata but must NOT see those — only admins do.
func TestRedactInboundForRole(t *testing.T) {
	full := inboundDTO{
		ID: 1, Protocol: "vless", Port: 443, Listen: "0.0.0.0", Remark: "r",
		Settings:       `{"clients":[{"id":"uuid-secret","email":"u1-n1"}]}`,
		StreamSettings: `{"realitySettings":{"privateKey":"REALITY-PRIVATE-KEY"}}`,
		Sniffing:       `{"enabled":true}`,
	}

	// Admin sees everything.
	if got := redactInboundForRole(full, true); got.Settings == "" || got.StreamSettings == "" {
		t.Fatalf("admin must see full settings, got settings=%q stream=%q", got.Settings, got.StreamSettings)
	}

	// Operator: the two secret-bearing blobs blanked, non-secret fields kept.
	op := redactInboundForRole(full, false)
	if op.Settings != "" {
		t.Errorf("operator leaked Settings (client creds): %q", op.Settings)
	}
	if op.StreamSettings != "" {
		t.Errorf("operator leaked StreamSettings (Reality privateKey): %q", op.StreamSettings)
	}
	if op.Protocol != "vless" || op.Port != 443 || op.Remark != "r" || op.Sniffing == "" {
		t.Errorf("operator should still see non-secret fields: %+v", op)
	}
}
