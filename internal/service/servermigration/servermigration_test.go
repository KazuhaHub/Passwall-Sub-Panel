package servermigration

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

func migrationFixture() *domain.ServerMigrationSnapshot {
	stamp := time.Date(2026, 9, 12, 1, 2, 3, 0, time.UTC)
	c := &domain.PSPClient{ID: 3, UserID: 7, PanelID: 1, Email: "u7@example.invalid", UUID: "test-original-uuid", Password: "test-original-password", DesiredMinted: true, DesiredEnable: true}
	n := &domain.Node{ID: 2, PanelID: 1, InboundID: 9, DisplayName: "node", Enabled: true, DesiredPort: 443, ObservedPort: 443, DesiredProtocol: "vless", ObservedProtocol: "vless", ConfigSyncedAt: &stamp, ConfigSyncState: domain.ConfigSyncSynced, InboundSettings: `{"decryption":"none","counter":9007199254740993}`, StreamSettings: `{"network":"tcp","security":"reality","realitySettings":{"privateKey":"test-original-reality-key","shortIds":["abcd"],"serverNames":["example.invalid"],"target":"example.invalid:443"}}`, Sniffing: `{"enabled":true}`, Allocate: `{}`}
	a := domain.PSPClientInbound{ClientID: c.ID, NodeID: n.ID, State: domain.ClientApplyApplied, AppliedEmail: c.Email, AppliedUUID: c.UUID, AppliedPassword: c.Password}
	return &domain.ServerMigrationSnapshot{Panel: &domain.Panel{ID: 1, Kind: domain.PanelKind3XUI, Name: "server", XrayVersion: "26.7.28"}, Nodes: []*domain.Node{n}, Clients: []domain.ServerMigrationClient{{Client: c, Inbounds: []domain.PSPClientInbound{a}}}}
}

func hasIssue(issues []domain.MigrationIssue, code string) bool {
	for _, issue := range issues {
		if issue.Code == code {
			return true
		}
	}
	return false
}

func TestSnapshotPreservesSupportedSelfContainedConfigurations(t *testing.T) {
	for _, protocol := range []string{"vless", "vmess", "trojan", "shadowsocks"} {
		for _, enabled := range []bool{true, false} {
			t.Run(protocol+"/enabled="+map[bool]string{true: "true", false: "false"}[enabled], func(t *testing.T) {
				s := migrationFixture()
				s.Nodes[0].DesiredProtocol, s.Nodes[0].ObservedProtocol, s.Nodes[0].Enabled = protocol, protocol, enabled
				s.Nodes[0].StreamSettings = `{"network":"ws","security":"tls","wsSettings":{"path":"/a/b","headers":{"Host":"example.invalid"}},"tlsSettings":{"certificates":[{"certificate":["test-inline-cert"],"key":["test-inline-secret-key"]}]}}`
				if protocol == "shadowsocks" {
					s.Nodes[0].InboundSettings = `{"method":"2022-blake3-aes-128-gcm","password":"test-original-server-psk","network":"tcp,udp"}`
				}
				before, _ := json.Marshal(s)
				if issues := s.Blockers(); len(issues) != 0 {
					t.Fatalf("valid self-contained configuration rejected: %+v", issues)
				}
				_ = s.Fingerprint("26.7.28", false)
				after, _ := json.Marshal(s)
				if string(before) != string(after) {
					t.Fatal("policy mutated original configuration or secrets")
				}
			})
		}
	}
	if issues := migrationFixture().Blockers(); len(issues) != 0 {
		t.Fatalf("Reality rejected: %+v", issues)
	}
}

func TestSnapshotFingerprintStableAndExcludesObservations(t *testing.T) {
	s := migrationFixture()
	want := s.Fingerprint("26.7.28", false)
	if len(want) != 64 || strings.Contains(want, "test-original") {
		t.Fatal("fingerprint is not an opaque SHA-256")
	}
	n, c := s.Nodes[0], s.Clients[0].Client
	n.LifetimeUpBytes, n.LifetimeDownBytes, n.LifetimeTotalBytes = 1, 2, 3
	n.LastTrafficUpBytes, n.LastTrafficDownBytes, n.LastTrafficTotalBytes = 4, 5, 6
	n.LastInboundUpBytes, n.LastInboundDownBytes, n.LastInboundTotalBytes, n.LastInboundCounterEpoch, n.LastInboundSeeded = 7, 8, 9, 3, true
	n.HealthState, n.HealthDetail = "healthy", "observation"
	n.CreatedAt = time.Now()
	n.HealthCheckedAt = &n.CreatedAt
	c.LifetimeUpBytes, c.LifetimeDownBytes, c.LifetimeTotalBytes = 10, 11, 12
	c.LastRawUpBytes, c.LastRawDownBytes, c.LastRawTotalBytes, c.LastCounterEpoch = 13, 14, 15, 4
	c.PeriodBaselineUpBytes, c.PeriodBaselineDownBytes, c.PeriodBaselineTotalBytes = 16, 17, 18
	c.PanelQuotaHeadroom = 999
	c.CreatedAt = time.Now()
	s.Panel.VersionCheckedAt, s.Panel.IPLimitProbedAt = &c.CreatedAt, &c.CreatedAt
	s.Clients[0].Inbounds[0].FirstFailedAt = &c.CreatedAt
	n.InboundSettings = "{\n \"counter\":9007199254740993, \"decryption\":\"none\" }"
	if got := s.Fingerprint("26.7.28", false); got != want {
		t.Fatal("counter/health observations or JSON formatting changed fingerprint")
	}
	second := *n
	second.ID, second.InboundID = 8, 10
	s.Nodes = append(s.Nodes, &second)
	secondClient := *c
	secondClient.ID, secondClient.UserID, secondClient.Email = 9, 10, "u10@example.invalid"
	s.Clients = append(s.Clients, domain.ServerMigrationClient{Client: &secondClient})
	want = s.Fingerprint("26.7.28", false)
	s.Nodes[0], s.Nodes[1] = s.Nodes[1], s.Nodes[0]
	s.Clients[0], s.Clients[1] = s.Clients[1], s.Clients[0]
	if got := s.Fingerprint("26.7.28", false); got != want {
		t.Fatal("database result ordering changed fingerprint")
	}
}

func TestSnapshotFingerprintBindsIntentAndConfirmation(t *testing.T) {
	changes := map[string]func(*domain.ServerMigrationSnapshot){
		"config": func(s *domain.ServerMigrationSnapshot) { s.Nodes[0].StreamSettings = `{"network":"grpc"}` },
		"exact_integer": func(s *domain.ServerMigrationSnapshot) {
			s.Nodes[0].InboundSettings = `{"decryption":"none","counter":9007199254740992}`
		},
		"enable": func(s *domain.ServerMigrationSnapshot) { s.Nodes[0].Enabled = false },
		"snapshot_stamp": func(s *domain.ServerMigrationSnapshot) {
			stamp := s.Nodes[0].ConfigSyncedAt.Add(time.Second)
			s.Nodes[0].ConfigSyncedAt = &stamp
		},
		"snapshot_state":     func(s *domain.ServerMigrationSnapshot) { s.Nodes[0].ConfigSyncState = "pending" },
		"client_enable":      func(s *domain.ServerMigrationSnapshot) { s.Clients[0].Client.DesiredEnable = false },
		"client_expiry":      func(s *domain.ServerMigrationSnapshot) { s.Clients[0].Client.DesiredExpiryTime = 123 },
		"client_credential":  func(s *domain.ServerMigrationSnapshot) { s.Clients[0].Client.Password = "rotated" },
		"applied_credential": func(s *domain.ServerMigrationSnapshot) { s.Clients[0].Inbounds[0].AppliedPassword = "rotated" },
		"applied_version":    func(s *domain.ServerMigrationSnapshot) { s.Clients[0].Inbounds[0].AppliedVersion = 10 },
		"applied_state":      func(s *domain.ServerMigrationSnapshot) { s.Clients[0].Inbounds[0].State = domain.ClientApplyPending },
		"attachment":         func(s *domain.ServerMigrationSnapshot) { s.Clients[0].Inbounds[0].NodeID = 12 },
		"legacy":             func(s *domain.ServerMigrationSnapshot) { s.LegacyOwnershipCount = 1 },
	}
	for name, change := range changes {
		t.Run(name, func(t *testing.T) {
			s := migrationFixture()
			want := s.Fingerprint("26.7.28", false)
			change(s)
			if s.Fingerprint("26.7.28", false) == want {
				t.Fatal("intent change was not bound")
			}
		})
	}
	s := migrationFixture()
	if s.Fingerprint("26.7.28", false) == s.Fingerprint("26.6.27", false) || s.Fingerprint("26.7.28", false) == s.Fingerprint("26.7.28", true) {
		t.Fatal("core choice/acknowledgment not bound")
	}
}

func TestSnapshotBlockersFailClosedWithoutSecrets(t *testing.T) {
	tests := []struct {
		name, code string
		change     func(*domain.ServerMigrationSnapshot)
	}{
		{"source", "source_not_3xui", func(s *domain.ServerMigrationSnapshot) { s.Panel.Kind = domain.PanelKindSUI }},
		{"no_nodes", "missing_snapshot", func(s *domain.ServerMigrationSnapshot) { s.Nodes = nil }},
		{"uncaptured", "missing_snapshot", func(s *domain.ServerMigrationSnapshot) { s.Nodes[0].ConfigSyncedAt = nil }},
		{"pending", "config_not_synced", func(s *domain.ServerMigrationSnapshot) { s.Nodes[0].ConfigSyncState = "pending" }},
		{"port_drift", "endpoint_not_confirmed", func(s *domain.ServerMigrationSnapshot) { s.Nodes[0].ObservedPort = 8443 }},
		{"protocol_drift", "endpoint_not_confirmed", func(s *domain.ServerMigrationSnapshot) { s.Nodes[0].ObservedProtocol = "trojan" }},
		{"disabled_unsupported", "unsupported_protocol", func(s *domain.ServerMigrationSnapshot) {
			s.Nodes[0].Enabled = false
			s.Nodes[0].DesiredProtocol = "hysteria2"
		}},
		{"expiry", "inbound_expiry_unsupported", func(s *domain.ServerMigrationSnapshot) { s.Nodes[0].InboundExpiryTime = 123 }},
		{"file_cert", "external_file_dependency", func(s *domain.ServerMigrationSnapshot) {
			s.Nodes[0].StreamSettings = `{"tlsSettings":{"certificates":[{"certificateFile":"/root/test-original-cert","keyFile":"/root/test-original-key"}]}}`
		}},
		{"global_outbound", "global_config_dependency", func(s *domain.ServerMigrationSnapshot) {
			s.Nodes[0].StreamSettings = `{"sockopt":{"dialerProxy":"test-original-outbound-tag"}}`
		}},
		{"global_routing", "global_config_dependency", func(s *domain.ServerMigrationSnapshot) { s.Nodes[0].InboundSettings = `{"routing":{"rules":[{}]}}` }},
		{"local_fallback", "local_fallback_dependency", func(s *domain.ServerMigrationSnapshot) {
			s.Nodes[0].InboundSettings = `{"fallbacks":[{"dest":"127.0.0.1:8080"}]}`
		}},
		{"numeric_fallback", "local_fallback_dependency", func(s *domain.ServerMigrationSnapshot) { s.Nodes[0].InboundSettings = `{"fallbacks":[{"dest":8080}]}` }},
		{"unix_fallback", "local_fallback_dependency", func(s *domain.ServerMigrationSnapshot) {
			s.Nodes[0].InboundSettings = `{"fallbacks":[{"dest":"/test-original-socket"}]}`
		}},
		{"json_array", "invalid_config", func(s *domain.ServerMigrationSnapshot) { s.Nodes[0].InboundSettings = `[]` }},
		{"json_null", "invalid_config", func(s *domain.ServerMigrationSnapshot) { s.Nodes[0].InboundSettings = `null` }},
		{"json_trailing", "invalid_config", func(s *domain.ServerMigrationSnapshot) { s.Nodes[0].InboundSettings = `{} {}` }},
		{"json_clients", "snapshot_contains_clients", func(s *domain.ServerMigrationSnapshot) { s.Nodes[0].InboundSettings = `{"clients":[]}` }},
		{"lifecycle", "lifecycle_not_minted", func(s *domain.ServerMigrationSnapshot) { s.Clients[0].Client.DesiredMinted = false }},
		{"credential_mismatch", "credential_not_confirmed", func(s *domain.ServerMigrationSnapshot) {
			s.Clients[0].Inbounds[0].AppliedPassword = "test-original-unconfirmed"
		}},
		{"pending_attachment", "credential_not_confirmed", func(s *domain.ServerMigrationSnapshot) { s.Clients[0].Inbounds[0].State = domain.ClientApplyPending }},
		{"cross_panel_client", "cross_panel_attachment", func(s *domain.ServerMigrationSnapshot) { s.Clients[0].Client.PanelID = 10 }},
		{"cross_panel_node", "cross_panel_attachment", func(s *domain.ServerMigrationSnapshot) { s.Nodes[0].PanelID = 10 }},
		{"orphan_attachment", "orphan_attachment", func(s *domain.ServerMigrationSnapshot) { s.Clients[0].Inbounds[0].NodeID = 10 }},
		{"wrong_client", "orphan_attachment", func(s *domain.ServerMigrationSnapshot) { s.Clients[0].Inbounds[0].ClientID = 10 }},
		{"nil_client", "missing_client", func(s *domain.ServerMigrationSnapshot) { s.Clients[0].Client = nil }},
		{"no_attachment", "missing_attachment", func(s *domain.ServerMigrationSnapshot) { s.Clients[0].Inbounds = nil }},
		{"duplicate_attachment", "duplicate_attachment", func(s *domain.ServerMigrationSnapshot) {
			s.Clients[0].Inbounds = append(s.Clients[0].Inbounds, s.Clients[0].Inbounds[0])
		}},
		{"legacy", "legacy_ownership_pending", func(s *domain.ServerMigrationSnapshot) { s.LegacyOwnershipCount = 1 }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			s := migrationFixture()
			test.change(s)
			issues := s.Blockers()
			if !hasIssue(issues, test.code) {
				t.Fatalf("missing %s: %+v", test.code, issues)
			}
			encoded, _ := json.Marshal(issues)
			if strings.Contains(string(encoded), "test-original") {
				t.Fatal("issues leaked secret configuration")
			}
		})
	}
}

type fakeRepo struct {
	snapshot       *domain.ServerMigrationSnapshot
	err            error
	loads, applies int
}

func (r *fakeRepo) Load(context.Context, int64) (*domain.ServerMigrationSnapshot, error) {
	r.loads++
	return r.snapshot, r.err
}
func (r *fakeRepo) Apply(context.Context, int64, string, *domain.NodeAgent, string) error {
	r.applies++
	return errors.New("preview must not apply")
}

func TestPreviewCoreChoiceIsExplicitAndReadOnly(t *testing.T) {
	for _, test := range []struct {
		name, current, explicit    string
		ack                        bool
		wantCore, blocker, warning string
	}{
		{"preserve", "26.7.28", "", false, "26.7.28", "", "reality_compatibility_normalization"},
		{"unknown_not_downgraded", "26.8.0", "", false, "26.8.0", "core_not_verified", ""},
		{"missing_recommended", "", "", false, "26.6.27", "", ""},
		{"explicit_recommended", "26.8.0", "26.6.27", false, "26.6.27", "", "core_version_changed"},
		{"restricted_no_ack", "26.9.9", "", false, "26.9.9", "core_ack_required", ""},
		{"restricted_ack", "26.9.9", "", true, "26.9.9", "", "restricted_core"},
		{"canonical", "v26.7.28", "", false, "26.7.28", "", ""},
		{"broad_extra_ack_canonicalized", "26.7.28", "", true, "26.7.28", "", ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			snapshot := migrationFixture()
			snapshot.Panel.XrayVersion = test.current
			repo := &fakeRepo{snapshot: snapshot}
			before, _ := json.Marshal(snapshot)
			p, err := New(repo).Preview(context.Background(), 1, test.explicit, test.ack)
			if err != nil {
				t.Fatal(err)
			}
			if p.CoreVersion != test.wantCore || p.RecommendedCoreVersion != "26.6.27" {
				t.Fatalf("unexpected core: %+v", p)
			}
			if test.blocker != "" && !hasIssue(p.Blockers, test.blocker) {
				t.Fatalf("missing blocker: %+v", p)
			}
			if test.blocker == "" && !p.CanMigrate {
				t.Fatalf("valid migration blocked: %+v", p)
			}
			if test.warning != "" && !hasIssue(p.Warnings, test.warning) {
				t.Fatalf("missing warning: %+v", p)
			}
			if !hasIssue(p.Warnings, "managed_scope") || repo.loads != 1 || repo.applies != 0 {
				t.Fatal("preview is not one read-only managed-scope operation")
			}
			canonicalAck := test.ack && p.CoreRequiresAck
			if p.Fingerprint != snapshot.Fingerprint(p.CoreVersion, canonicalAck) {
				t.Fatal("fingerprint does not match exact choice")
			}
			encoded, _ := json.Marshal(p)
			if strings.Contains(string(encoded), "test-original") {
				t.Fatal("preview leaked secrets")
			}
			after, _ := json.Marshal(snapshot)
			if string(before) != string(after) {
				t.Fatal("preview mutated source")
			}
			if p.CoreRequiresAck != (test.wantCore == "26.9.9") || p.AllowRestrictedReality != canonicalAck {
				t.Fatal("acknowledgment DTO mismatch")
			}
		})
	}
}

func TestPreviewErrorsAndValidation(t *testing.T) {
	if _, err := New(nil).Preview(context.Background(), 1, "", false); err == nil {
		t.Fatal("nil repo accepted")
	}
	repo := &fakeRepo{snapshot: migrationFixture()}
	if _, err := New(repo).Preview(context.Background(), 0, "", false); !errors.Is(err, domain.ErrValidation) || repo.loads != 0 {
		t.Fatal("invalid identity loaded repository")
	}
	repo.err = domain.ErrNotFound
	if _, err := New(repo).Preview(context.Background(), 1, "", false); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal("repository error lost")
	}
	if _, err := ValidateCore("1.14.0", false); !errors.Is(err, domain.ErrValidation) {
		t.Fatal("sing-box engine switch accepted")
	}
	if _, err := ValidateCore("26.9.9", false); !errors.Is(err, domain.ErrValidation) {
		t.Fatal("restricted core accepted without ack")
	}
	if _, err := ValidateCore("26.9.9", true); err != nil {
		t.Fatal(err)
	}
}

func TestSnapshotRawTransportsAndDependencies(t *testing.T) {
	for _, network := range []string{"tcp", "ws", "grpc", "httpupgrade", "xhttp"} {
		s := migrationFixture()
		s.Nodes[0].StreamSettings = `{"network":"` + network + `","security":"tls","tlsSettings":{"certificates":[{"certificate":["test-inline-cert"],"key":["test-inline-key"]}]},"sockopt":{"tcpFastOpen":true}}`
		if issues := s.Blockers(); len(issues) != 0 {
			t.Fatalf("forwarded network %s rejected: %+v", network, issues)
		}
		if !hasIssue(s.Warnings(), "socket_environment_dependency") {
			t.Fatal("socket environmental requirements hidden")
		}
	}
	s := migrationFixture()
	s.Nodes[0].InboundSettings = `{"fallbacks":[{"dest":"fallback.example.invalid:443"}]}`
	if len(s.Blockers()) != 0 || !hasIssue(s.Warnings(), "fallback_environment_dependency") {
		t.Fatal("external fallback not preserved with explicit environmental warning")
	}
	s.Clients[0].Client.PanelIPLimit = 2
	if !hasIssue(s.Warnings(), "connection_limits_not_enforced") {
		t.Fatal("native connection-enforcement gap hidden")
	}
	for _, dest := range []string{"localhost:443", "[::1]:443", "0.0.0.0:443", "10.0.0.1:443", "unix:/tmp/socket", "@abstract", "8080", "malformed"} {
		s := migrationFixture()
		s.Nodes[0].InboundSettings = `{"fallbacks":[{"dest":"` + dest + `"}]}`
		if !hasIssue(s.Blockers(), "local_fallback_dependency") {
			t.Fatalf("dependent fallback %s accepted", dest)
		}
	}
}

func TestSnapshotRejectsAmbiguousClientClosure(t *testing.T) {
	s := migrationFixture()
	second := *s.Nodes[0]
	second.ID, second.InboundID = 10, 11
	second.DesiredPort, second.ObservedPort = 8443, 8443
	s.Nodes = append(s.Nodes, &second)
	a := s.Clients[0].Inbounds[0]
	a.NodeID = second.ID
	a.FlowOverride = "xtls-rprx-vision"
	s.Clients[0].Inbounds = append(s.Clients[0].Inbounds, a)
	if !hasIssue(s.Blockers(), "flow_conflict") {
		t.Fatal("mixed VLESS flow would silently change PN listener behavior")
	}
	s = migrationFixture()
	s.Clients = append(s.Clients, s.Clients[0])
	if !hasIssue(s.Blockers(), "duplicate_client") || !hasIssue(s.Blockers(), "duplicate_username") {
		t.Fatal("duplicate identity accepted")
	}
	s = migrationFixture()
	s.Clients[0].Inbounds[0].FlowOverride = "unverified"
	if !hasIssue(s.Blockers(), "unsupported_flow") {
		t.Fatal("unsupported flow accepted")
	}
	s = migrationFixture()
	s.Clients[0].Client.UUID = ""
	if !hasIssue(s.Blockers(), "invalid_credentials") {
		t.Fatal("missing VLESS UUID accepted")
	}
	s = migrationFixture()
	s.Clients[0].Client = nil
	if !hasIssue(s.Blockers(), "orphan_attachment") {
		t.Fatal("attachment referencing deleted client lost")
	}
	if len((*domain.ServerMigrationSnapshot)(nil).Blockers()) == 0 {
		t.Fatal("nil closure accepted")
	}
}
