package servermigration

import (
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

const ss2022Settings = `{"method":"2022-blake3-aes-128-gcm","password":"test-original-server-psk","network":"tcp,udp"}`

// After the 4.3 per-line split 3X-UI keeps a stale password on VLESS clients;
// only the fields the attached protocol reads may block a migration.
func TestCredentialConfirmationIsProtocolAware(t *testing.T) {
	stalePW := func(s *domain.ServerMigrationSnapshot) { s.Clients[0].Inbounds[0].AppliedPassword = "stale" }
	staleID := func(s *domain.ServerMigrationSnapshot) { s.Clients[0].Inbounds[0].AppliedUUID = "other" }
	tests := []struct {
		name, protocol, settings string
		change                   func(*domain.ServerMigrationSnapshot)
		want                     bool
	}{
		{"vless_stale_password", "vless", "", stalePW, false},
		{"vless_uuid_mismatch", "vless", "", staleID, true},
		{"vless_email_mismatch", "vless", "", func(s *domain.ServerMigrationSnapshot) { s.Clients[0].Inbounds[0].AppliedEmail = "other@x" }, true},
		{"shadowsocks_password_mismatch", "shadowsocks", "", stalePW, true},
		{"shadowsocks_stale_uuid", "shadowsocks", "", staleID, false},
		{"ss_alias_password_mismatch", "ss", "", stalePW, true},
		{"ss_alias_stale_uuid", "ss", "", staleID, false},
		{"ss2022_password_mismatch", "shadowsocks", ss2022Settings, stalePW, true},
		{"ss2022_stale_uuid", "shadowsocks", ss2022Settings, staleID, false},
		{"hysteria2_stale_id", "hysteria2", "", staleID, true},
		{"hysteria2_stale_password", "hysteria2", "", stalePW, false},
		{"hy2_alias_stale_id", "hy2", "", staleID, true},
		{"hy2_alias_stale_password", "hy2", "", stalePW, false},
		{"tuic_stale_id", "tuic", "", staleID, true},
		{"tuic_stale_password", "tuic", "", stalePW, true},
		{"unknown_protocol_fails_closed", "wireguard", "", stalePW, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := migrationFixture()
			s.Nodes[0].DesiredProtocol, s.Nodes[0].ObservedProtocol = tc.protocol, tc.protocol
			if tc.settings != "" {
				s.Nodes[0].InboundSettings = tc.settings
			}
			tc.change(s)
			if got := hasIssue(s.Blockers(), "credential_not_confirmed"); got != tc.want {
				t.Fatalf("credential_not_confirmed = %v, want %v", got, tc.want)
			}
		})
	}
}
