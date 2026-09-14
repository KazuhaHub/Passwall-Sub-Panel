package sqlstore

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

func appliedConfigNodeFixture(inboundID int) *domain.Node {
	captured := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	pending := captured.Add(time.Hour)
	health := captured.Add(2 * time.Hour)
	return &domain.Node{
		PanelID: 9, InboundID: inboundID, DisplayName: "fixture-node", ServerAddress: "node.example.test",
		DesiredProtocol: "vless", DesiredPort: 444, ObservedProtocol: "trojan", ObservedPort: 443,
		Flow: "xtls-rprx-vision", Region: "CA", Tags: []string{"fixture", "tag"}, SortOrder: 20,
		Enabled: true, Kind: domain.NodeKindReal, CreatedAt: captured.Add(-time.Hour),
		LifetimeUpBytes: 101, LifetimeDownBytes: 202, LifetimeTotalBytes: 303,
		LastTrafficUpBytes: 11, LastTrafficDownBytes: 22, LastTrafficTotalBytes: 33,
		LastInboundUpBytes: 44, LastInboundDownBytes: 55, LastInboundTotalBytes: 99,
		LastInboundCounterEpoch: 7, LastInboundSeeded: true,
		HealthState: domain.NodeHealthUnreachable, HealthCheckedAt: &health, HealthDetail: "fixture probe failed",
		InboundListen: "0.0.0.0", InboundRemark: "Fixture Listener",
		InboundSettings: `{"decryption":"none","secret":"FixtureOnly"}`,
		StreamSettings:  `{"security":"reality","realitySettings":{"privateKey":"FixtureOnly"}}`,
		Sniffing:        `{"enabled":true}`, Allocate: `{"strategy":"always"}`, InboundExpiryTime: 1893456000000,
		ConfigSyncedAt: &captured, ConfigSyncState: domain.ConfigSyncPending, ConfigPendingSince: &pending,
		CertSource: domain.CertSourceManaged, CertID: 17,
		Relays:     []domain.RelayLine{{Name: "fixture-relay", Address: "relay.example.test", Port: 8443, Enabled: true}},
		HideDirect: true, ShowRelayStatus: true,
		RelayHealth: []domain.RelayHealth{{Index: 0, Address: "relay.example.test", Port: 8443, State: domain.NodeHealthOK}},
	}
}

func appliedConfigStoredRow(t *testing.T, repo *nodeRepo, id int64) nodeRow {
	t.Helper()
	var row nodeRow
	if err := repo.db.First(&row, id).Error; err != nil {
		t.Fatalf("read stored node: %v", err)
	}
	return row
}

func assertAppliedConfigRowEqual(t *testing.T, got, want nodeRow) {
	t.Helper()
	if reflect.DeepEqual(got, want) {
		return
	}
	// Report only differing column names, never configuration or ciphertext.
	actual, expected := reflect.ValueOf(got), reflect.ValueOf(want)
	var fields []string
	for i := 0; i < actual.NumField(); i++ {
		if !reflect.DeepEqual(actual.Field(i).Interface(), expected.Field(i).Interface()) {
			fields = append(fields, actual.Type().Field(i).Name)
		}
	}
	t.Fatalf("unexpected node column changes: %s", strings.Join(fields, ", "))
}

func TestNodeRepoConfirmAppliedConfigPreservesEntireRow(t *testing.T) {
	ConfigureSecretKey("applied-config-fixture-key")
	t.Cleanup(func() { ConfigureSecretKey("") })

	cases := []struct {
		name   string
		modify func(*domain.Node)
	}{
		{"pending", func(*domain.Node) {}},
		{"failed", func(n *domain.Node) { n.ConfigSyncState = domain.ConfigSyncFailed }},
		{"nil_capture", func(n *domain.Node) { n.ConfigSyncedAt = nil }},
		{"disabled", func(n *domain.Node) { n.Enabled = false }},
		{"synced_observed_drift", func(n *domain.Node) {
			n.ConfigSyncState, n.ConfigPendingSince = domain.ConfigSyncSynced, nil
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo, ctx := newNodeTestRepo(t)
			n := appliedConfigNodeFixture(1)
			tc.modify(n)
			if err := repo.Create(ctx, n); err != nil {
				t.Fatal(err)
			}
			before := appliedConfigStoredRow(t, repo, n.ID)
			if !strings.HasPrefix(before.InboundSettings, secretPrefix) || !strings.HasPrefix(before.StreamSettings, secretPrefix) {
				t.Fatal("fixture secrets were not encrypted")
			}
			changed, err := repo.ConfirmAppliedConfig(ctx, n.ID, n.PanelID, n.ConfigIntent())
			if err != nil || !changed {
				t.Fatalf("confirm: changed=%t error=%v", changed, err)
			}
			want := before
			want.ObservedPort, want.ObservedProtocol = n.DesiredPort, n.DesiredProtocol
			want.ConfigSyncState, want.ConfigPendingSince = domain.ConfigSyncSynced, nil
			assertAppliedConfigRowEqual(t, appliedConfigStoredRow(t, repo, n.ID), want)

			changed, err = repo.ConfirmAppliedConfig(ctx, n.ID, n.PanelID, n.ConfigIntent())
			if err != nil || changed {
				t.Fatalf("replayed confirm: changed=%t error=%v", changed, err)
			}
			assertAppliedConfigRowEqual(t, appliedConfigStoredRow(t, repo, n.ID), want)
		})
	}
}

func TestNodeRepoConfirmAppliedConfigRejectsEveryChangedIntentField(t *testing.T) {
	ConfigureSecretKey("applied-config-fixture-key")
	t.Cleanup(func() { ConfigureSecretKey("") })
	repo, ctx := newNodeTestRepo(t)
	cases := []struct {
		name   string
		modify func(*domain.Node)
	}{
		{"enabled", func(n *domain.Node) { n.Enabled = false }},
		{"listen", func(n *domain.Node) { n.InboundListen = "::" }},
		{"port", func(n *domain.Node) { n.DesiredPort++ }},
		{"protocol", func(n *domain.Node) { n.DesiredProtocol = "trojan" }},
		{"remark", func(n *domain.Node) { n.InboundRemark = "fixture Listener" }},
		{"settings_case_only", func(n *domain.Node) {
			n.InboundSettings = strings.ReplaceAll(n.InboundSettings, "FixtureOnly", "fixtureOnly")
		}},
		{"stream_settings_case_only", func(n *domain.Node) {
			n.StreamSettings = strings.ReplaceAll(n.StreamSettings, "FixtureOnly", "fixtureOnly")
		}},
		{"sniffing", func(n *domain.Node) { n.Sniffing = `{"enabled":false}` }},
		{"allocate", func(n *domain.Node) { n.Allocate = `{"strategy":"random"}` }},
		{"expiry_time", func(n *domain.Node) { n.InboundExpiryTime++ }},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			n := appliedConfigNodeFixture(i + 1)
			if err := repo.Create(ctx, n); err != nil {
				t.Fatal(err)
			}
			expected := n.ConfigIntent()
			capture := *n.ConfigSyncedAt
			tc.modify(n)
			n.ConfigSyncState = domain.ConfigSyncFailed
			if err := repo.UpdateInboundConfig(ctx, n); err != nil {
				t.Fatal(err)
			}
			if err := repo.UpdateEnabled(ctx, n.ID, n.Enabled); err != nil {
				t.Fatal(err)
			}
			before := appliedConfigStoredRow(t, repo, n.ID)
			if before.ConfigSyncedAt == nil || !before.ConfigSyncedAt.Equal(capture) {
				t.Fatal("fixture edit must preserve the capture timestamp")
			}
			changed, err := repo.ConfirmAppliedConfig(ctx, n.ID, n.PanelID, expected)
			if err != nil || changed {
				t.Fatalf("stale confirm: changed=%t error=%v", changed, err)
			}
			assertAppliedConfigRowEqual(t, appliedConfigStoredRow(t, repo, n.ID), before)
		})
	}
}

func TestNodeRepoConfirmAppliedConfigNewIntentWithSameTimestamp(t *testing.T) {
	repo, ctx := newNodeTestRepo(t)
	n := appliedConfigNodeFixture(1)
	if err := repo.Create(ctx, n); err != nil {
		t.Fatal(err)
	}
	old := n.ConfigIntent()
	n.DesiredPort = 8444
	if err := repo.UpdateInboundConfig(ctx, n); err != nil {
		t.Fatal(err)
	}
	before := appliedConfigStoredRow(t, repo, n.ID)
	changed, err := repo.ConfirmAppliedConfig(ctx, n.ID, n.PanelID, old)
	if err != nil || changed {
		t.Fatalf("old same-stamp confirm: changed=%t error=%v", changed, err)
	}
	assertAppliedConfigRowEqual(t, appliedConfigStoredRow(t, repo, n.ID), before)
	changed, err = repo.ConfirmAppliedConfig(ctx, n.ID, n.PanelID, n.ConfigIntent())
	if err != nil || !changed {
		t.Fatalf("current same-stamp confirm: changed=%t error=%v", changed, err)
	}
	want := before
	want.ObservedPort, want.ObservedProtocol = n.DesiredPort, n.DesiredProtocol
	want.ConfigSyncState, want.ConfigPendingSince = domain.ConfigSyncSynced, nil
	assertAppliedConfigRowEqual(t, appliedConfigStoredRow(t, repo, n.ID), want)
}

func TestNodeRepoConfirmAppliedConfigMissingOrWrongPanelIsNoop(t *testing.T) {
	repo, ctx := newNodeTestRepo(t)
	n := appliedConfigNodeFixture(1)
	if err := repo.Create(ctx, n); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name    string
		nodeID  int64
		panelID int64
	}{
		{"missing", n.ID + 100, n.PanelID},
		{"zero", 0, n.PanelID},
		{"wrong_panel", n.ID, n.PanelID + 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := appliedConfigStoredRow(t, repo, n.ID)
			changed, err := repo.ConfirmAppliedConfig(ctx, tc.nodeID, tc.panelID, n.ConfigIntent())
			if err != nil || changed {
				t.Fatalf("guarded confirm: changed=%t error=%v", changed, err)
			}
			assertAppliedConfigRowEqual(t, appliedConfigStoredRow(t, repo, n.ID), before)
		})
	}
	if err := repo.db.Model(&nodeRow{}).Where("id = ?", n.ID).Update("panel_id", n.PanelID+1).Error; err != nil {
		t.Fatal(err)
	}
	before := appliedConfigStoredRow(t, repo, n.ID)
	changed, err := repo.ConfirmAppliedConfig(ctx, n.ID, n.PanelID, n.ConfigIntent())
	if err != nil || changed {
		t.Fatalf("rebound panel confirm: changed=%t error=%v", changed, err)
	}
	assertAppliedConfigRowEqual(t, appliedConfigStoredRow(t, repo, n.ID), before)
	var count int64
	if err := repo.db.Model(&nodeRow{}).Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("guard inserted rows: count=%d error=%v", count, err)
	}
}

func TestNodeRepoConfirmAppliedConfigFailureDoesNotMutate(t *testing.T) {
	ConfigureSecretKey("applied-config-fixture-key")
	t.Cleanup(func() { ConfigureSecretKey("") })
	repo, ctx := newNodeTestRepo(t)
	n := appliedConfigNodeFixture(1)
	if err := repo.Create(ctx, n); err != nil {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	before := appliedConfigStoredRow(t, repo, n.ID)
	changed, err := repo.ConfirmAppliedConfig(canceled, n.ID, n.PanelID, n.ConfigIntent())
	if err == nil || changed {
		t.Fatalf("canceled confirm: changed=%t error=%v", changed, err)
	}
	assertAppliedConfigRowEqual(t, appliedConfigStoredRow(t, repo, n.ID), before)
	if err := repo.db.Model(&nodeRow{}).Where("id = ?", n.ID).
		Update("stream_settings", secretPrefix+"invalid-fixture-ciphertext").Error; err != nil {
		t.Fatal(err)
	}
	before = appliedConfigStoredRow(t, repo, n.ID)
	changed, err = repo.ConfirmAppliedConfig(ctx, n.ID, n.PanelID, n.ConfigIntent())
	if err == nil || changed {
		t.Fatalf("invalid ciphertext confirm: changed=%t error=%v", changed, err)
	}
	assertAppliedConfigRowEqual(t, appliedConfigStoredRow(t, repo, n.ID), before)
}
