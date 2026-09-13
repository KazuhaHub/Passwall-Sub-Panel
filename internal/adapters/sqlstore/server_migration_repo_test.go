package sqlstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

type serverMigrationFixture struct {
	db       *gorm.DB
	repos    ports.Repos
	panelID  int64
	otherID  int64
	nodeIDs  []int64
	clientID int64
	raw      string
	agent    *domain.NodeAgent
}

func newServerMigrationFixture(t *testing.T) *serverMigrationFixture {
	t.Helper()
	previousKey := append([]byte(nil), dbSecretKey...)
	ConfigureSecretKey("server-migration-fixture-encryption-key")
	t.Cleanup(func() { dbSecretKey = previousKey })
	db, err := openTestDB(t)
	if err != nil {
		t.Fatal(err)
	}
	if err := EnsureSchema(db); err != nil {
		t.Fatal(err)
	}
	f := &serverMigrationFixture{db: db, repos: NewRepos(db), raw: "pspn_" + strings.Repeat("s", 43)}
	panel := &domain.Panel{Kind: domain.PanelKind3XUI, Name: "Canada BC Danika Home - Telus", URL: "https://old-panel.example.test/panel",
		APIToken: "migration-fixture-old-api-token", Username: "old-admin", Password: "migration-fixture-old-password", Remark: "preserved remark",
		AuthMethod: domain.XUIAuthToken, InsecureSkipVerify: true}
	if err := f.repos.XUIPanel.Save(t.Context(), panel); err != nil {
		t.Fatal(err)
	}
	f.panelID = panel.ID
	other := &domain.Panel{Kind: domain.PanelKind3XUI, Name: "unrelated server", URL: "https://other.example.test/panel"}
	if err := f.repos.XUIPanel.Save(t.Context(), other); err != nil {
		t.Fatal(err)
	}
	f.otherID = other.ID
	now := time.Date(2026, 9, 12, 9, 10, 0, 0, time.UTC)
	if err := db.Model(&xuiPanelRow{}).Where("id = ?", f.panelID).Updates(map[string]any{
		"panel_version": "3.7.0", "xray_version": "26.7.28", "version_checked_at": now,
		"ip_limit_enforcement": "inert", "ip_limit_probed_at": now,
	}).Error; err != nil {
		t.Fatal(err)
	}
	for i, port := range []int{443, 8443} {
		node := &domain.Node{
			PanelID: f.panelID, InboundID: 90 + i, DisplayName: fmt.Sprintf("original node %d", i), ServerAddress: "node.example.test",
			DesiredProtocol: "vless", DesiredPort: port, ObservedProtocol: "vless", ObservedPort: port,
			Region: "CA", Tags: []string{"region:CA", "existing-group"}, SortOrder: 20 + i*10, Enabled: i == 0,
			Flow: "xtls-rprx-vision", InboundListen: "0.0.0.0", InboundRemark: "original inbound",
			InboundSettings: `{"decryption":"none"}`, StreamSettings: `{"network":"tcp","security":"none"}`,
			Sniffing: `{"enabled":true,"destOverride":["http","tls"]}`, Allocate: `{"strategy":"always"}`,
			ConfigSyncedAt: &now, ConfigSyncState: domain.ConfigSyncSynced,
			LifetimeUpBytes: 1000 + int64(i), LifetimeDownBytes: 2000, LifetimeTotalBytes: 3000 + int64(i),
			LastTrafficUpBytes: 120, LastTrafficDownBytes: 230, LastTrafficTotalBytes: 350,
			LastInboundUpBytes: 400, LastInboundDownBytes: 600, LastInboundTotalBytes: 1000, LastInboundSeeded: true,
			HealthState: domain.NodeHealthOK, HealthCheckedAt: &now, HealthDetail: "previous health evidence",
		}
		if err := f.repos.Node.Create(t.Context(), node); err != nil {
			t.Fatal(err)
		}
		f.nodeIDs = append(f.nodeIDs, node.ID)
	}
	group := groupRow{Slug: "preserved-subscribers", Name: "preserved group", TagFilter: jsonTagFilter(domain.TagFilter{Tags: []string{"existing-group"}}),
		Layout: jsonLayout(domain.Layout{Sort: []domain.SortEntry{{NodeID: f.nodeIDs[1], Weight: 1}, {NodeID: f.nodeIDs[0], Weight: 2}}})}
	if err := db.Create(&group).Error; err != nil {
		t.Fatal(err)
	}
	user := userRow{UPN: "migration-user", SSOProvider: "local", SSOSubject: "migration-user", SubToken: "migration-fixture-subscription-token",
		UUID: "11111111-1111-4111-8111-111111111111", GroupID: group.ID, Enabled: true,
		LifetimeUpBytes: 1500, LifetimeDownBytes: 2500, LifetimeTotalBytes: 4000, PeriodBaselineBytes: 1700,
		TrafficResetPeriod: "monthly", TrafficPeriodStart: &now, ExpireAt: &now}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	client := pspClientRow{UserID: user.ID, PanelID: f.panelID, Email: "shared-original@example.test", UUID: user.UUID, Password: "original-user-password",
		DesiredEnable: false, DesiredExpiryTime: now.UnixMilli(), PanelQuotaHeadroom: 2300, PanelIPLimit: 2, PanelDeviceLimit: 3, DesiredMinted: true,
		LifetimeUpBytes: 1500, LifetimeDownBytes: 2500, LifetimeTotalBytes: 4000,
		LastRawUpBytes: 500, LastRawDownBytes: 800, LastRawTotalBytes: 1300,
		PeriodBaselineUpBytes: 700, PeriodBaselineDownBytes: 1000, PeriodBaselineTotalBytes: 1700}
	if err := db.Create(&client).Error; err != nil {
		t.Fatal(err)
	}
	f.clientID = client.ID
	for _, id := range f.nodeIDs {
		attachment := pspClientInboundRow{ClientID: client.ID, NodeID: id, State: string(domain.ClientApplyApplied), AppliedVersion: 7,
			AppliedEmail: client.Email, AppliedUUID: client.UUID, AppliedPassword: client.Password, FlowOverride: "xtls-rprx-vision"}
		if err := db.Create(&attachment).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, row := range []any{
		&trafficRow{UserID: user.ID, UpBytes: 1500, DownBytes: 2500, TotalBytes: 4000, CapturedAt: now},
		&nodeTrafficRow{NodeID: f.nodeIDs[0], UpBytes: 1000, DownBytes: 2000, TotalBytes: 3000, CapturedAt: now},
		&clientTrafficRow{UserID: user.ID, PanelID: f.panelID, InboundID: 90, ClientEmail: client.Email, UpBytes: 1500, DownBytes: 2500, TotalBytes: 4000, CapturedAt: now},
	} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	digest, err := nativeCredentialDigest(f.raw)
	if err != nil {
		t.Fatal(err)
	}
	f.agent = &domain.NodeAgent{PanelID: f.panelID, AgentID: "agt_server_migration_fixture", CredentialSHA256: digest,
		DesiredCoreEngine: domain.NodeCoreXray, DesiredCoreVersion: "26.6.27"}
	return f
}

func (f *serverMigrationFixture) fingerprint(t *testing.T) string {
	t.Helper()
	snapshot, err := f.repos.ServerMigration.Load(t.Context(), f.panelID)
	if err != nil {
		t.Fatal(err)
	}
	if blockers := snapshot.Blockers(); len(blockers) != 0 {
		t.Fatalf("valid fixture has blockers: %v", blockers)
	}
	return snapshot.Fingerprint(f.agent.DesiredCoreVersion, f.agent.AllowRestrictedReality)
}

func migrationStoredRows[T any](t *testing.T, db *gorm.DB) []T {
	t.Helper()
	var rows []T
	if err := db.Order("id").Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	return rows
}

type migrationStoredState struct {
	Panels        []xuiPanelRow
	Nodes         []nodeRow
	Clients       []pspClientRow
	Attachments   []pspClientInboundRow
	Groups        []groupRow
	Users         []userRow
	Traffic       []trafficRow
	NodeTraffic   []nodeTrafficRow
	ClientTraffic []clientTrafficRow
	Tasks         []syncTaskRow
	Agents        []nodeAgentRow
	Streams       []nodeAgentStreamRow
}

func migrationState(t *testing.T, db *gorm.DB) migrationStoredState {
	t.Helper()
	return migrationStoredState{
		Panels: migrationStoredRows[xuiPanelRow](t, db), Nodes: migrationStoredRows[nodeRow](t, db),
		Clients: migrationStoredRows[pspClientRow](t, db), Attachments: migrationStoredRows[pspClientInboundRow](t, db),
		Groups: migrationStoredRows[groupRow](t, db), Users: migrationStoredRows[userRow](t, db),
		Traffic: migrationStoredRows[trafficRow](t, db), NodeTraffic: migrationStoredRows[nodeTrafficRow](t, db),
		ClientTraffic: migrationStoredRows[clientTrafficRow](t, db), Tasks: migrationStoredRows[syncTaskRow](t, db),
		Agents: migrationStoredRows[nodeAgentRow](t, db), Streams: migrationStoredRows[nodeAgentStreamRow](t, db),
	}
}

func TestServerMigrationPreservesOriginalIdentityConfigurationAndHistory(t *testing.T) {
	f := newServerMigrationFixture(t)
	fingerprint := f.fingerprint(t)
	before := migrationState(t, f.db)
	if err := f.repos.ServerMigration.Apply(t.Context(), f.panelID, fingerprint, f.agent, f.raw); err != nil {
		t.Fatal(err)
	}
	after := migrationState(t, f.db)
	if len(after.Panels) != len(before.Panels) || len(after.Nodes) != len(before.Nodes) || len(after.Attachments) != len(before.Attachments) ||
		len(after.Agents) != 1 || len(after.Streams) != 3 || f.agent.ID == 0 || f.agent.PanelID != f.panelID || f.agent.Epoch != 1 {
		t.Fatal("conversion recreated identity rows or omitted new native state")
	}
	for i, old := range before.Panels {
		want := old
		if old.ID == f.panelID {
			want.Kind, want.URL = string(domain.PanelKindPSP), "psp://"+f.agent.AgentID
			want.APIToken, want.Username, want.Password, want.AuthMethod = "", "", "", ""
			want.InsecureSkipVerify = false
			want.PanelVersion, want.XrayVersion, want.IPLimitEnforcement = "", "", ""
			want.VersionCheckedAt, want.IPLimitProbedAt = nil, nil
			want.UpdatedAt = after.Panels[i].UpdatedAt
		}
		if !reflect.DeepEqual(after.Panels[i], want) {
			t.Fatal("conversion changed an unowned server field")
		}
	}
	for i, old := range before.Nodes {
		want := old
		want.ConfigSyncState = domain.ConfigSyncPending
		want.ConfigPendingSince = after.Nodes[i].ConfigPendingSince
		if want.ConfigPendingSince == nil || want.ConfigPendingSince.IsZero() || !reflect.DeepEqual(after.Nodes[i], want) {
			t.Fatal("conversion modified node identity/configuration/counters or omitted pending state")
		}
	}
	for i, old := range before.Attachments {
		want := old
		want.State, want.AppliedVersion, want.FirstFailedAt = string(domain.ClientApplyPending), 0, nil
		if !reflect.DeepEqual(after.Attachments[i], want) {
			t.Fatal("conversion replaced attachment identity or last confirmed credentials")
		}
	}
	if !reflect.DeepEqual(after.Clients, before.Clients) || !reflect.DeepEqual(after.Users, before.Users) ||
		!reflect.DeepEqual(after.Groups, before.Groups) || !reflect.DeepEqual(after.Traffic, before.Traffic) ||
		!reflect.DeepEqual(after.NodeTraffic, before.NodeTraffic) || !reflect.DeepEqual(after.ClientTraffic, before.ClientTraffic) {
		t.Fatal("conversion changed user/client credentials, membership, counters or historical snapshots")
	}
	stored := after.Agents[0]
	if stored.CredentialCiphertext == nil || !strings.HasPrefix(*stored.CredentialCiphertext, secretPrefix) || strings.Contains(*stored.CredentialCiphertext, f.raw) {
		t.Fatal("fixed node credential was not encrypted")
	}
	raw, err := decryptNativeCredential(*stored.CredentialCiphertext)
	if err != nil || raw != f.raw {
		t.Fatal("fixed credential cannot be recovered exactly")
	}
	for _, stream := range after.Streams {
		if stream.AgentID != f.agent.AgentID || stream.DesiredVersion != 0 || stream.AppliedVersion != 0 || stream.AppliedEpoch != 0 || stream.AppliedETag != "" {
			t.Fatal("old upstream state was treated as native acknowledgement")
		}
	}
	stable := migrationState(t, f.db)
	second := *f.agent
	second.ID, second.Epoch = 0, 0
	if err := f.repos.ServerMigration.Apply(t.Context(), f.panelID, fingerprint, &second, f.raw); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("repeat conversion = %v, want conflict", err)
	}
	if !reflect.DeepEqual(migrationState(t, f.db), stable) {
		t.Fatal("repeat conversion minted or rotated a native identity/credential")
	}
}

func TestServerMigrationRetiresOnlyOriginalServerNodeTasks(t *testing.T) {
	f := newServerMigrationFixture(t)
	fingerprint := f.fingerprint(t)
	var targetIDs, otherIDs []int64
	finished := time.Date(2026, 9, 1, 1, 0, 0, 0, time.UTC)
	for _, typ := range []domain.SyncTaskType{domain.SyncTaskNodeCreate, domain.SyncTaskNodeDelete, domain.SyncTaskNodeSetEnabled, domain.SyncTaskNodeUpdate} {
		for _, status := range []domain.SyncTaskStatus{domain.SyncTaskPending, domain.SyncTaskRunning, domain.SyncTaskSucceeded, domain.SyncTaskCanceled} {
			task := &domain.SyncTask{Type: typ, Status: status, TargetType: "node", TargetID: f.nodeIDs[0], Summary: "preserve original summary",
				Payload: `{"previous_configuration":"preserved"}`, LastError: "preserve original error", Attempts: 4, NextRunAt: finished}
			if typ == domain.SyncTaskNodeCreate {
				task.TargetID = 0
				encoded, err := json.Marshal(struct {
					Node domain.Node `json:"node"`
				}{domain.Node{PanelID: f.panelID}})
				if err != nil {
					t.Fatal(err)
				}
				task.Payload = string(encoded)
			}
			if status == domain.SyncTaskSucceeded || status == domain.SyncTaskCanceled {
				task.FinishedAt = &finished
			}
			if err := f.repos.SyncTask.Create(t.Context(), task); err != nil {
				t.Fatal(err)
			}
			targetIDs = append(targetIDs, task.ID)
		}
	}
	for _, task := range []*domain.SyncTask{
		// An already-retired malformed record is inert, and must not block a
		// different server's maintenance or have its original timestamp changed.
		{Type: domain.SyncTaskNodeCreate, Status: domain.SyncTaskRetired, TargetType: "node", TargetID: 0, Payload: "unresolved retired payload", FinishedAt: &finished},
		{Type: domain.SyncTaskNodeCreate, TargetType: "node", TargetID: 0, Payload: fmt.Sprintf(`{"node":{"PanelID":%d},"spec":{}}`, f.otherID)},
		{Type: domain.SyncTaskNodeDelete, TargetType: "node", TargetID: 900000},
		{Type: domain.SyncTaskUserResync, TargetType: "user", TargetID: f.nodeIDs[0]},
		{Type: domain.SyncTaskMailNotify, TargetType: "user", TargetID: f.nodeIDs[0]},
		{Type: domain.SyncTaskCertRenew, TargetType: "certificate", TargetID: f.nodeIDs[0]},
	} {
		if err := f.repos.SyncTask.Create(t.Context(), task); err != nil {
			t.Fatal(err)
		}
		otherIDs = append(otherIDs, task.ID)
	}
	before := migrationStoredRows[syncTaskRow](t, f.db)
	if err := f.repos.ServerMigration.Apply(t.Context(), f.panelID, fingerprint, f.agent, f.raw); err != nil {
		t.Fatal(err)
	}
	after := migrationStoredRows[syncTaskRow](t, f.db)
	if len(after) != len(before) {
		t.Fatal("conversion deleted task history")
	}
	for i, old := range before {
		want := old
		if containsMigrationID(targetIDs, old.ID) {
			want.Status = string(domain.SyncTaskRetired)
			want.UpdatedAt = after[i].UpdatedAt
			if want.FinishedAt == nil {
				want.FinishedAt = after[i].FinishedAt
			}
			if after[i].FinishedAt == nil {
				t.Fatal("retired active task lacks terminal timestamp")
			}
		} else if !containsMigrationID(otherIDs, old.ID) {
			t.Fatal("test omitted task scope")
		}
		if !reflect.DeepEqual(after[i], want) {
			t.Fatal("task retirement changed payload/history or affected unrelated work")
		}
	}
	for _, id := range targetIDs {
		if err := f.repos.SyncTask.RetryNow(t.Context(), id); !errors.Is(err, domain.ErrConflict) {
			t.Fatalf("RetryNow retired task %d = %v", id, err)
		}
	}
}

func containsMigrationID(ids []int64, target int64) bool {
	for _, id := range ids {
		if id == target {
			return true
		}
	}
	return false
}

func TestServerMigrationRollbackLeavesNoHalfConvertedState(t *testing.T) {
	for _, failAt := range []string{"agent", "stream", "credential", "node", "attachment", "task"} {
		t.Run(failAt, func(t *testing.T) {
			f := newServerMigrationFixture(t)
			if err := f.repos.SyncTask.Create(t.Context(), &domain.SyncTask{Type: domain.SyncTaskNodeDelete, TargetType: "node", TargetID: f.nodeIDs[0]}); err != nil {
				t.Fatal(err)
			}
			fingerprint := f.fingerprint(t)
			before := migrationState(t, f.db)
			injected := errors.New("injected transaction failure with private payload")
			callback := "test:server_migration_rollback_" + failAt
			create := failAt == "agent" || failAt == "stream"
			fail := func(tx *gorm.DB) {
				if tx.Statement.Schema == nil {
					return
				}
				table := tx.Statement.Schema.Table
				if (failAt == "agent" || failAt == "credential") && table == (nodeAgentRow{}).TableName() ||
					failAt == "stream" && table == (nodeAgentStreamRow{}).TableName() ||
					failAt == "node" && table == (nodeRow{}).TableName() ||
					failAt == "attachment" && table == (pspClientInboundRow{}).TableName() ||
					failAt == "task" && table == (syncTaskRow{}).TableName() {
					tx.AddError(injected)
				}
			}
			if create {
				if err := f.db.Callback().Create().Before("gorm:create").Register(callback, fail); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = f.db.Callback().Create().Remove(callback) })
			} else {
				if err := f.db.Callback().Update().Before("gorm:update").Register(callback, fail); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = f.db.Callback().Update().Remove(callback) })
			}
			err := f.repos.ServerMigration.Apply(t.Context(), f.panelID, fingerprint, f.agent, f.raw)
			if !errors.Is(err, injected) || err == nil || strings.Contains(err.Error(), "private payload") {
				t.Fatalf("injected failure did not stay private/classifiable: %v", err)
			}
			if !reflect.DeepEqual(migrationState(t, f.db), before) || f.agent.ID != 0 {
				t.Fatal("failed transaction left half-converted state or mutated caller identity")
			}
		})
	}
}

func TestServerMigrationRejectsChangedPreviewAndIncompleteClosureWithoutWrites(t *testing.T) {
	for _, mutate := range []string{"config", "credential", "orphan_client", "cross_panel_client", "cross_panel_attachment", "legacy", "malformed_create", "ambiguous_create"} {
		t.Run(mutate, func(t *testing.T) {
			f := newServerMigrationFixture(t)
			fingerprint := f.fingerprint(t)
			switch mutate {
			case "config":
				if err := f.db.Model(&nodeRow{}).Where("id = ?", f.nodeIDs[0]).Update("inbound_remark", "changed after preview").Error; err != nil {
					t.Fatal(err)
				}
			case "credential":
				if err := f.db.Model(&pspClientRow{}).Where("id = ?", f.clientID).Update("uuid", "22222222-2222-4222-8222-222222222222").Error; err != nil {
					t.Fatal(err)
				}
			case "orphan_client":
				if err := f.db.Create(&pspClientInboundRow{ClientID: 900000, NodeID: f.nodeIDs[0], State: "applied"}).Error; err != nil {
					t.Fatal(err)
				}
			case "cross_panel_client":
				if err := f.db.Model(&pspClientRow{}).Where("id = ?", f.clientID).Update("panel_id", f.otherID).Error; err != nil {
					t.Fatal(err)
				}
			case "cross_panel_attachment":
				if err := f.db.Create(&pspClientInboundRow{ClientID: f.clientID, NodeID: 900001, State: "applied"}).Error; err != nil {
					t.Fatal(err)
				}
			case "legacy":
				if err := f.db.AutoMigrate(&ownershipRow{}); err != nil {
					t.Fatal(err)
				}
				if err := f.db.Create(&ownershipRow{PanelID: f.panelID, UserID: 1, InboundID: 90, ClientEmail: "legacy", ClientUUID: "old-uuid"}).Error; err != nil {
					t.Fatal(err)
				}
			case "malformed_create", "ambiguous_create":
				payload := `{"node":`
				if mutate == "ambiguous_create" {
					payload = fmt.Sprintf(`{"node":{"PanelID":%d,"panel_id":%d}}`, f.otherID, f.panelID)
				}
				if err := f.repos.SyncTask.Create(t.Context(), &domain.SyncTask{Type: domain.SyncTaskNodeCreate, TargetType: "node", Payload: payload}); err != nil {
					t.Fatal(err)
				}
			}
			before := migrationState(t, f.db)
			err := f.repos.ServerMigration.Apply(t.Context(), f.panelID, fingerprint, f.agent, f.raw)
			if !errors.Is(err, domain.ErrConflict) && !errors.Is(err, domain.ErrValidation) {
				t.Fatalf("invalid conversion = %v, want conflict/validation", err)
			}
			if !reflect.DeepEqual(migrationState(t, f.db), before) || f.agent.ID != 0 {
				t.Fatal("rejected conversion wrote database state")
			}
		})
	}
}

func TestServerMigrationCoreAcknowledgementAndSecretsFailClosed(t *testing.T) {
	for _, invalid := range []string{"unsupported", "noncanonical", "restricted_unacknowledged", "broad_with_ack", "wrong_verifier", "no_encryption", "wrong_panel"} {
		t.Run(invalid, func(t *testing.T) {
			f := newServerMigrationFixture(t)
			fingerprint := f.fingerprint(t)
			switch invalid {
			case "unsupported":
				f.agent.DesiredCoreVersion = "99.99.99"
			case "noncanonical":
				f.agent.DesiredCoreVersion = "v26.6.27"
			case "restricted_unacknowledged":
				f.agent.DesiredCoreVersion = "26.9.9"
			case "broad_with_ack":
				f.agent.AllowRestrictedReality = true
			case "wrong_verifier":
				f.agent.CredentialSHA256 = strings.Repeat("0", 64)
			case "no_encryption":
				ConfigureSecretKey("")
			case "wrong_panel":
				f.agent.PanelID = f.otherID
			}
			before := migrationState(t, f.db)
			if err := f.repos.ServerMigration.Apply(t.Context(), f.panelID, fingerprint, f.agent, f.raw); err == nil {
				t.Fatal("invalid core/credential boundary accepted")
			}
			if !reflect.DeepEqual(migrationState(t, f.db), before) {
				t.Fatal("invalid core/credential boundary changed database")
			}
		})
	}
	t.Run("restricted acknowledged", func(t *testing.T) {
		f := newServerMigrationFixture(t)
		f.agent.DesiredCoreVersion, f.agent.AllowRestrictedReality = "26.9.9", true
		if err := f.repos.ServerMigration.Apply(t.Context(), f.panelID, f.fingerprint(t), f.agent, f.raw); err != nil {
			t.Fatalf("explicit acknowledged exact core rejected: %v", err)
		}
	})
}

func TestServerMigrationTrafficOnlyChangeDoesNotInvalidatePreview(t *testing.T) {
	f := newServerMigrationFixture(t)
	fingerprint := f.fingerprint(t)
	if err := f.db.Model(&nodeRow{}).Where("id = ?", f.nodeIDs[0]).Update("lifetime_total_bytes", 7777).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.db.Model(&pspClientRow{}).Where("id = ?", f.clientID).Update("last_raw_total_bytes", 5555).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.db.Model(&pspClientRow{}).Where("id = ?", f.clientID).Update("panel_quota_headroom", 2222).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.repos.ServerMigration.Apply(t.Context(), f.panelID, fingerprint, f.agent, f.raw); err != nil {
		t.Fatal(err)
	}
	var node nodeRow
	var client pspClientRow
	if err := f.db.First(&node, f.nodeIDs[0]).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.db.First(&client, f.clientID).Error; err != nil {
		t.Fatal(err)
	}
	if node.LifetimeTotalBytes != 7777 || client.LastRawTotalBytes != 5555 || client.PanelQuotaHeadroom != 2222 {
		t.Fatal("conversion clobbered counters changed after preview")
	}
}

func TestServerMigrationLoadIsPrivateAndMissingServerIsNotFound(t *testing.T) {
	f := newServerMigrationFixture(t)
	if _, err := f.repos.ServerMigration.Load(context.Background(), 900000); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("missing server = %v", err)
	}
	if _, err := f.repos.ServerMigration.Load(context.Background(), 0); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("invalid server = %v", err)
	}
	var log strings.Builder
	f.db.Config.Logger = logger.New(&migrationLogWriter{builder: &log}, logger.Config{LogLevel: logger.Info})
	if err := f.repos.ServerMigration.Apply(t.Context(), f.panelID, f.fingerprint(t), f.agent, f.raw); err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{f.raw, "migration-fixture-old-api-token", "migration-fixture-old-password", "original-user-password"} {
		if strings.Contains(log.String(), secret) {
			t.Fatal("migration secret was exposed in SQL logging")
		}
	}
}

type migrationLogWriter struct{ builder *strings.Builder }

func (w *migrationLogWriter) Printf(format string, args ...any) {
	fmt.Fprintf(w.builder, format, args...)
}

func TestMigrationCreatePanelIDRejectsAmbiguousOrUnknownIdentity(t *testing.T) {
	for _, raw := range []string{`null`, `[]`, `{}`, `{"node":null}`, `{"node":{"PanelID":0}}`,
		`{"node":{"PanelID":"2"}}`, `{"node":{"PanelID":2,"panelid":3}}`, `{"node":{"PanelID":2,"panel_id":3}}`,
		`{"node":{"PanelID":2},"Node":{"PanelID":3}}`, `{"node":{"PanelID":2}} {}`, `{"node":{"PanelID":2.5}}`} {
		if _, err := migrationCreatePanelID(raw); err == nil {
			t.Fatalf("unresolved/ambiguous identity accepted: %s", raw)
		}
	}
	for _, raw := range []string{`{"node":{"PanelID":2},"spec":{}}`, `{"node":{"panel_id":2},"spec":{}}`} {
		if id, err := migrationCreatePanelID(raw); err != nil || id != 2 {
			t.Fatalf("exact identity = %d, %v", id, err)
		}
	}
}
