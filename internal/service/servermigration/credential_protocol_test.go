package servermigration

import (
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// After the 4.3 per-line split 3X-UI keeps a stale password on VLESS clients;
// only the fields the attached protocol reads may block a migration.
func TestCredentialConfirmationIsProtocolAware(t *testing.T) {
	tests := []struct {
		name, protocol string
		change         func(*domain.ServerMigrationSnapshot)
		want           bool
	}{
		{"vless_stale_password", "vless", func(s *domain.ServerMigrationSnapshot) { s.Clients[0].Inbounds[0].AppliedPassword = "stale" }, false},
		{"vless_uuid_mismatch", "vless", func(s *domain.ServerMigrationSnapshot) { s.Clients[0].Inbounds[0].AppliedUUID = "other" }, true},
		{"vless_email_mismatch", "vless", func(s *domain.ServerMigrationSnapshot) { s.Clients[0].Inbounds[0].AppliedEmail = "other@x" }, true},
		{"shadowsocks_password_mismatch", "shadowsocks", func(s *domain.ServerMigrationSnapshot) { s.Clients[0].Inbounds[0].AppliedPassword = "stale" }, true},
		{"shadowsocks_stale_uuid", "shadowsocks", func(s *domain.ServerMigrationSnapshot) { s.Clients[0].Inbounds[0].AppliedUUID = "stale" }, false},
		{"unknown_protocol_fails_closed", "wireguard", func(s *domain.ServerMigrationSnapshot) { s.Clients[0].Inbounds[0].AppliedPassword = "stale" }, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := migrationFixture()
			s.Nodes[0].DesiredProtocol, s.Nodes[0].ObservedProtocol = tc.protocol, tc.protocol
			tc.change(s)
			if got := hasIssue(s.Blockers(), "credential_not_confirmed"); got != tc.want {
				t.Fatalf("credential_not_confirmed = %v, want %v", got, tc.want)
			}
		})
	}
}
