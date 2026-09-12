package sqlstore

import (
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// Frozen source: v3.9.2-beta.20, commit
// 081b209d67873f8ab78f69aba5d6a24cf294404e:
// internal/adapters/sqlstore/schema.go (nodeRow) and
// internal/adapters/sqlstore/psp_client_repo.go (both client rows).
// Field types/tags and table names are copied verbatim; only Go type names
// change and historical prose comments are omitted. The existing JSON SQL
// wrappers used below have the same representation as that tag.
//
// This is a historical three-table migration fixture, not a current-model
// database relabeled v3, and not a claim of full deployed-panel E2E coverage.
type v392Beta20NodeRow struct {
	ID                    int64  `gorm:"primaryKey;autoIncrement"`
	PanelID               int64  `gorm:"not null;index;uniqueIndex:uk_panel_inbound,priority:1"`
	InboundID             int    `gorm:"not null;uniqueIndex:uk_panel_inbound,priority:2"`
	DisplayName           string `gorm:"size:255;not null"`
	ServerAddress         string `gorm:"size:255"`
	Flow                  string `gorm:"size:64"`
	Protocol              string `gorm:"size:32;default:''"`
	Port                  int    `gorm:"default:0"`
	Region                string `gorm:"size:16;not null"`
	Tags                  jsonStrings
	SortOrder             int    `gorm:"default:0"`
	Enabled               *bool  `gorm:"default:true"`
	Kind                  string `gorm:"size:16;default:'real'"`
	LifetimeUpBytes       int64  `gorm:"default:0"`
	LifetimeDownBytes     int64  `gorm:"default:0"`
	LifetimeTotalBytes    int64  `gorm:"default:0"`
	LastTrafficUpBytes    int64  `gorm:"default:0"`
	LastTrafficDownBytes  int64  `gorm:"default:0"`
	LastTrafficTotalBytes int64  `gorm:"default:0"`
	LastInboundUpBytes    int64  `gorm:"default:0"`
	LastInboundDownBytes  int64  `gorm:"default:0"`
	LastInboundTotalBytes int64  `gorm:"default:0"`
	LastInboundSeeded     bool   `gorm:"default:false"`
	HealthState           string `gorm:"size:32;default:''"`
	HealthCheckedAt       *time.Time
	HealthDetail          string `gorm:"size:512;default:''"`
	InboundListen         string `gorm:"size:64;default:''"`
	InboundRemark         string `gorm:"size:255;default:''"`
	InboundSettings       string `gorm:"type:text"`
	StreamSettings        string `gorm:"type:text"`
	Sniffing              string `gorm:"type:text"`
	Allocate              string `gorm:"type:text"`
	InboundExpiryTime     int64  `gorm:"default:0"`
	ConfigSyncedAt        *time.Time
	ConfigSyncState       string          `gorm:"size:32;default:''"`
	CertSource            string          `gorm:"size:16;default:''"`
	CertID                int64           `gorm:"default:0;index"`
	Relays                jsonRelays      `gorm:"column:relays"`
	HideDirect            bool            `gorm:"default:false"`
	ShowRelayStatus       bool            `gorm:"default:false"`
	RelayHealth           jsonRelayHealth `gorm:"column:relay_health"`
	CreatedAt             time.Time
}

func (v392Beta20NodeRow) TableName() string { return "nodes" }

type v392Beta20ClientRow struct {
	ID        int64  `gorm:"primaryKey;autoIncrement"`
	UserID    int64  `gorm:"index;not null"`
	PanelID   int64  `gorm:"not null;uniqueIndex:uk_psp_client,priority:1"`
	Email     string `gorm:"size:255;not null;uniqueIndex:uk_psp_client,priority:2"`
	CredClass int    `gorm:"not null;default:0"`
	UUID      string `gorm:"size:36;not null;default:''"`
	Password  string `gorm:"size:128;not null;default:''"`
	CreatedAt time.Time

	LifetimeUpBytes    int64 `gorm:"default:0"`
	LifetimeDownBytes  int64 `gorm:"default:0"`
	LifetimeTotalBytes int64 `gorm:"default:0"`

	LastRawUpBytes    int64 `gorm:"default:0"`
	LastRawDownBytes  int64 `gorm:"default:0"`
	LastRawTotalBytes int64 `gorm:"default:0"`

	PeriodBaselineUpBytes    int64 `gorm:"default:0"`
	PeriodBaselineDownBytes  int64 `gorm:"default:0"`
	PeriodBaselineTotalBytes int64 `gorm:"default:0"`
}

func (v392Beta20ClientRow) TableName() string { return "psp_clients" }

type v392Beta20AttachmentRow struct {
	ID           int64  `gorm:"primaryKey;autoIncrement"`
	ClientID     int64  `gorm:"not null;index;uniqueIndex:uk_psp_client_inbound,priority:1"`
	NodeID       int64  `gorm:"not null;uniqueIndex:uk_psp_client_inbound,priority:2"`
	FlowOverride string `gorm:"size:64;not null;default:''"`
	Provisioned  bool   `gorm:"default:false"`
}

func (v392Beta20AttachmentRow) TableName() string { return "psp_client_inbounds" }

func TestV392Beta20SchemaUpgradePreservesNodeAndClientData(t *testing.T) {
	db, err := openTestDB(t)
	if err != nil {
		t.Fatal(err)
	}
	ConfigureSecretKey("v392-to-v4-fixture-key")
	t.Cleanup(func() { ConfigureSecretKey("") })

	// Only the three historical tables under test are created here. In
	// particular, no current model or current EnsureSchema builds the source.
	if err := db.AutoMigrate(&v392Beta20NodeRow{}, &v392Beta20ClientRow{}, &v392Beta20AttachmentRow{}); err != nil {
		t.Fatalf("create frozen v3.9.2-beta.20 tables: %v", err)
	}
	if !db.Migrator().HasIndex(&v392Beta20ClientRow{}, "uk_psp_client") {
		t.Fatal("fixture lost the historical unique email index")
	}
	for _, column := range []string{"desired_port", "observed_port", "desired_protocol", "observed_protocol"} {
		if db.Migrator().HasColumn(&v392Beta20NodeRow{}, column) {
			t.Fatalf("source fixture already contains v4 node column %s", column)
		}
	}
	for _, column := range []string{"state", "applied_email", "applied_uuid", "applied_password"} {
		if db.Migrator().HasColumn(&v392Beta20AttachmentRow{}, column) {
			t.Fatalf("source fixture already contains v4 attachment column %s", column)
		}
	}

	when := time.Date(2026, 8, 1, 2, 3, 4, 123456000, time.UTC)
	inboundPlain := `{"method":"2022-blake3-aes-128-gcm","password":"fixture-server-key"}`
	streamPlain := `{"security":"reality","realitySettings":{"privateKey":"fixture-private-key"}}`
	inboundCipher, err := encryptSecret(inboundPlain)
	if err != nil {
		t.Fatal(err)
	}
	streamCipher, err := encryptSecret(streamPlain)
	if err != nil {
		t.Fatal(err)
	}
	yes, no := true, false
	nodes := []v392Beta20NodeRow{
		{
			PanelID: 10, InboundID: 21, DisplayName: "旧节点 / return-home", ServerAddress: "node.example",
			Port: 8443, Protocol: "vless", Flow: "xtls-rprx-vision", Region: "JP",
			Tags: jsonStrings{"return-home", "udp"}, SortOrder: 8, Enabled: &no, Kind: "real",
			LifetimeUpBytes: 9007199254740993, LifetimeDownBytes: 42, LifetimeTotalBytes: 9007199254741035,
			LastTrafficUpBytes: 101, LastTrafficDownBytes: 202, LastTrafficTotalBytes: 303,
			LastInboundUpBytes: 401, LastInboundDownBytes: 502, LastInboundTotalBytes: 903, LastInboundSeeded: true,
			HealthState: "healthy", HealthCheckedAt: &when, HealthDetail: "fixture detail",
			InboundListen: "0.0.0.0", InboundRemark: "existing inbound", InboundSettings: inboundCipher,
			StreamSettings: streamCipher, Sniffing: `{"enabled":true}`, Allocate: `{"strategy":"always"}`,
			InboundExpiryTime: 1893456000000, ConfigSyncedAt: &when, ConfigSyncState: "synced",
			CertSource: "psp_managed", CertID: 88,
			Relays:     jsonRelays{{Name: "中转", Address: "relay.example", Port: 9443, SNI: "sni.example", Host: "host.example", Enabled: true}},
			HideDirect: true, ShowRelayStatus: true,
			RelayHealth: jsonRelayHealth{{Index: 0, Address: "relay.example", Port: 9443, State: "healthy", CheckedAt: &when}},
			CreatedAt:   when,
		},
		{
			PanelID: 10, InboundID: 22, DisplayName: "enabled legacy", ServerAddress: "other.example",
			Port: 443, Protocol: "trojan", Region: "US", Enabled: &yes, CreatedAt: when,
		},
	}
	for i := range nodes {
		if err := db.Create(&nodes[i]).Error; err != nil {
			t.Fatal(err)
		}
	}
	client := v392Beta20ClientRow{
		UserID: 7, PanelID: 10, Email: "u7@old.example", CredClass: 1,
		UUID: "00000000-0000-0000-0000-000000000007", Password: "fixture-client-password", CreatedAt: when,
		LifetimeUpBytes: 1234, LifetimeDownBytes: 5678, LifetimeTotalBytes: 6912,
		LastRawUpBytes: 34, LastRawDownBytes: 78, LastRawTotalBytes: 112,
		PeriodBaselineUpBytes: 234, PeriodBaselineDownBytes: 678, PeriodBaselineTotalBytes: 912,
	}
	if err := db.Create(&client).Error; err != nil {
		t.Fatal(err)
	}
	attachments := []v392Beta20AttachmentRow{
		{ClientID: client.ID, NodeID: nodes[0].ID, FlowOverride: "xtls-rprx-vision", Provisioned: true},
		{ClientID: client.ID, NodeID: nodes[1].ID, Provisioned: false},
	}
	for i := range attachments {
		if err := db.Create(&attachments[i]).Error; err != nil {
			t.Fatal(err)
		}
	}
	verifyCustom := v4SQLiteOperatorObjects(t, db, "nodes")
	// Reload the source first: compare database values, not driver-specific
	// timestamp precision or GORM-applied create defaults.
	oldNodes := v392ReadRows[v392Beta20NodeRow](t, db)
	oldClients := v392ReadRows[v392Beta20ClientRow](t, db)
	oldAttachments := v392ReadRows[v392Beta20AttachmentRow](t, db)
	started := time.Now().UTC()
	if err := EnsureSchema(db); err != nil {
		t.Fatalf("v3.9.2-beta.20 -> v4 EnsureSchema: %v", err)
	}
	finished := time.Now().UTC()

	// Selecting * into a frozen row ignores newly added columns. Exclude only
	// the three intentionally removed fields from the shared-value comparison.
	afterNodes := v392ReadRows[v392Beta20NodeRow](t, db)
	expectedNodes := append([]v392Beta20NodeRow(nil), oldNodes...)
	for i := range expectedNodes {
		expectedNodes[i].Port, expectedNodes[i].Protocol = 0, ""
	}
	if !reflect.DeepEqual(afterNodes, expectedNodes) {
		t.Fatal("migration changed historical node identity, counters, flags, TLS/relay/config data or timestamps")
	}
	if !reflect.DeepEqual(v392ReadRows[v392Beta20ClientRow](t, db), oldClients) {
		t.Fatal("migration changed historical client ID, credentials, counters or period baselines")
	}
	expectedAttachments := append([]v392Beta20AttachmentRow(nil), oldAttachments...)
	for i := range expectedAttachments {
		expectedAttachments[i].Provisioned = false
	}
	if !reflect.DeepEqual(v392ReadRows[v392Beta20AttachmentRow](t, db), expectedAttachments) {
		t.Fatal("migration changed historical attachment IDs, foreign IDs or flow overrides")
	}

	repos := NewRepos(db)
	for _, before := range oldNodes {
		got, err := repos.Node.GetByID(t.Context(), before.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.DesiredPort != before.Port || got.ObservedPort != before.Port ||
			got.DesiredProtocol != before.Protocol || got.ObservedProtocol != before.Protocol || !got.EndpointInSync() {
			t.Fatal("legacy endpoint did not become identical desired and observed state")
		}
		if before.ID == nodes[0].ID && (got.InboundSettings != inboundPlain || got.StreamSettings != streamPlain) {
			t.Fatal("unchanged encryption key no longer decrypts historical server identity")
		}
	}
	applied := v392ReadRows[pspClientInboundRow](t, db)
	if len(applied) != 2 || applied[0].State != string(domain.ClientApplyApplied) || applied[0].FirstFailedAt != nil ||
		applied[0].AppliedVersion != 0 || applied[0].AppliedEmail != client.Email ||
		applied[0].AppliedUUID != client.UUID || applied[0].AppliedPassword != client.Password {
		t.Fatal("confirmed attachment lost applied credentials or changed confirmation meaning")
	}
	if applied[1].State != string(domain.ClientApplyPending) || applied[1].FirstFailedAt == nil ||
		applied[1].FirstFailedAt.Before(started.Add(-time.Second)) || applied[1].FirstFailedAt.After(finished.Add(time.Second)) ||
		applied[1].AppliedEmail != "" || applied[1].AppliedUUID != "" || applied[1].AppliedPassword != "" {
		t.Fatal("unconfirmed attachment must remain pending, not acquire fabricated applied credentials")
	}
	for _, column := range []string{"port", "protocol"} {
		if db.Migrator().HasColumn(&nodeRow{}, column) {
			t.Fatalf("obsolete node column %s survived", column)
		}
	}
	if db.Migrator().HasColumn(&pspClientInboundRow{}, "provisioned") {
		t.Fatal("obsolete provisioned column survived")
	}
	if db.Migrator().HasIndex(&pspClientRow{}, "uk_psp_client") {
		t.Fatal("obsolete unique email identity index survived")
	}
	for _, index := range []struct {
		model any
		name  string
	}{
		{&pspClientRow{}, "idx_psp_client_panel_email"},
		{&nodeRow{}, "uk_panel_inbound"},
		{&nodeRow{}, "idx_nodes_panel_id"},
		{&nodeRow{}, "idx_nodes_cert_id"},
		{&pspClientInboundRow{}, "uk_psp_client_inbound"},
		{&pspClientInboundRow{}, "idx_psp_client_inbounds_client_id"},
	} {
		if !db.Migrator().HasIndex(index.model, index.name) {
			t.Errorf("migration lost required index %s", index.name)
		}
	}
	duplicateNode := v392ReadRows[nodeRow](t, db)[0]
	duplicateNode.ID = 0
	v392RejectDuplicate(t, db, &duplicateNode, "(panel,inbound) node")
	duplicateAttachment := applied[0]
	duplicateAttachment.ID = 0
	v392RejectDuplicate(t, db, &duplicateAttachment, "(client,node) attachment")
	verifyCustom()
	if db.Dialector.Name() == "sqlite" {
		if err := db.Exec("UPDATE nodes SET display_name = ? WHERE id = ?", "operator trigger verified", nodes[0].ID).Error; err != nil {
			t.Fatal(err)
		}
		var count int64
		if err := db.Table("operator_nodes_name_updates").Count(&count).Error; err != nil || count != 1 {
			t.Fatal("operator-owned node trigger no longer executes after column cleanup")
		}
	}

	// A second boot must not restart the pending clock or overwrite values that
	// have subsequently diverged on the independently owned axes.
	if err := repos.Node.UpdateObservedEndpoint(t.Context(), nodes[0].ID, domain.NodeObservedEndpoint{Port: 9443, Protocol: "trojan"}); err != nil {
		t.Fatal(err)
	}
	updated, err := repos.PSPClient.GetByID(t.Context(), client.ID)
	if err != nil {
		t.Fatal(err)
	}
	updated.Email, updated.UUID, updated.Password = "u7@new.example", "00000000-0000-0000-0000-000000000008", "next-password"
	if err := repos.PSPClient.UpdateDefinition(t.Context(), updated); err != nil {
		t.Fatal(err)
	}
	newNodes := v392ReadRows[nodeRow](t, db)
	newClients := v392ReadRows[pspClientRow](t, db)
	newAttachments := v392ReadRows[pspClientInboundRow](t, db)
	if err := EnsureSchema(db); err != nil {
		t.Fatalf("second boot: %v", err)
	}
	if !reflect.DeepEqual(v392ReadRows[nodeRow](t, db), newNodes) ||
		!reflect.DeepEqual(v392ReadRows[pspClientRow](t, db), newClients) ||
		!reflect.DeepEqual(v392ReadRows[pspClientInboundRow](t, db), newAttachments) {
		t.Fatal("repeat migration overwrote independent observed state, desired credentials, applied snapshot or pending clock")
	}
	verifyCustom()
}

func v392ReadRows[T any](t *testing.T, db *gorm.DB) []T {
	t.Helper()
	var rows []T
	// PostgreSQL/pgx caches SELECT * result shapes. These fixtures deliberately
	// read both sides of DDL on one connection, unlike a normal panel boot.
	// Use the same per-query simple protocol as the PostgreSQL GORM migrator;
	// retain SELECT * so retired frozen fields become zero after column removal.
	query := db.Order("id")
	if db.Dialector.Name() == "postgres" {
		query = query.Scopes(func(d *gorm.DB) *gorm.DB {
			d.Statement.Vars = append([]any{pgx.QueryExecModeSimpleProtocol}, d.Statement.Vars...)
			return d
		})
	}
	if err := query.Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	return rows
}

func v392RejectDuplicate(t *testing.T, db *gorm.DB, duplicate any, identity string) {
	t.Helper()
	rollback := errors.New("fixture duplicate probe rollback")
	var createErr error
	// A probe that unexpectedly succeeds must not leave an extra row behind.
	// Avoid logging the deliberately private fixture's SQL parameter values.
	err := db.Session(&gorm.Session{Logger: logger.Discard}).Transaction(func(tx *gorm.DB) error {
		createErr = tx.Create(duplicate).Error
		return rollback
	})
	if !errors.Is(err, rollback) {
		t.Fatal("cannot roll back duplicate constraint probe")
	}
	if createErr == nil {
		t.Errorf("first v4 boot accepted duplicate %s identity", identity)
	}
}

// A custom index/trigger unrelated to a retired column belongs to the operator,
// not the migration. Recreating a table and restoring only model indexes loses
// these objects; native SQLite DROP COLUMN must preserve them too.
func v4SQLiteOperatorObjects(t *testing.T, db *gorm.DB, table string) func() {
	t.Helper()
	if db.Dialector.Name() != "sqlite" {
		return func() {}
	}
	var engine string
	if err := db.Raw("SELECT sqlite_version()").Scan(&engine).Error; err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(engine, ".")
	if len(parts) < 2 {
		t.Fatal("SQLite engine version is not parseable")
	}
	major, majorErr := strconv.Atoi(parts[0])
	minor, minorErr := strconv.Atoi(parts[1])
	if majorErr != nil || minorErr != nil || major < 3 || (major == 3 && minor < 35) {
		t.Fatal("native DROP COLUMN requires SQLite >=3.35")
	}
	t.Logf("pure-Go SQLite engine %s supports native DROP COLUMN", engine)
	index, trigger, audit := "operator_"+table+"_name", "operator_"+table+"_name_trigger", "operator_"+table+"_name_updates"
	for _, statement := range []string{
		fmt.Sprintf("CREATE INDEX %s ON %s(display_name)", index, table),
		fmt.Sprintf("CREATE TABLE %s (node_id INTEGER NOT NULL, display_name TEXT NOT NULL)", audit),
		fmt.Sprintf("CREATE TRIGGER %s AFTER UPDATE OF display_name ON %s BEGIN INSERT INTO %s(node_id, display_name) VALUES (NEW.id, NEW.display_name); END", trigger, table, audit),
	} {
		if err := db.Exec(statement).Error; err != nil {
			t.Fatal(err)
		}
	}
	return func() {
		t.Helper()
		if !db.Migrator().HasIndex(table, index) {
			t.Errorf("migration lost operator-owned index on %s", table)
		}
		var count int64
		if err := db.Raw("SELECT COUNT(*) FROM sqlite_master WHERE type = 'trigger' AND name = ? AND tbl_name = ?", trigger, table).Scan(&count).Error; err != nil || count != 1 {
			t.Errorf("migration lost operator-owned trigger on %s", table)
		}
	}
}

// Frozen source: v3.0.0-rc.3, commit
// 425e2257a7b4d1050cd446cf999541e46a282e10,
// internal/adapters/mysql/schema.go: separatorRow. These legacy columns could
// still be present in a later v3 database. This model has no independent
// declared indexes; its primary key and unrelated operator objects must survive.
type v30RC3SeparatorRow struct {
	ID              int64  `gorm:"primaryKey;autoIncrement"`
	DisplayName     string `gorm:"size:255;not null"`
	SortOrder       int    `gorm:"default:0"`
	Enabled         bool   `gorm:"default:true"`
	ShowInAllGroups bool   `gorm:"default:true"`
	GroupIDs        jsonInt64s
	CreatedAt       time.Time
}

func (v30RC3SeparatorRow) TableName() string { return "nodes_separator" }

func TestLegacySeparatorCleanupPreservesPrimaryKeyAndOperatorObjects(t *testing.T) {
	db, err := openTestDB(t)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&v30RC3SeparatorRow{}); err != nil {
		t.Fatal(err)
	}
	row := v30RC3SeparatorRow{
		DisplayName: "legacy separator", SortOrder: 31, Enabled: true, ShowInAllGroups: true,
		GroupIDs: jsonInt64s{7, 8}, CreatedAt: time.Date(2026, 7, 1, 2, 3, 4, 0, time.UTC),
	}
	if err := db.Create(&row).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&v30RC3SeparatorRow{}).Where("id = ?", row.ID).Updates(map[string]any{"enabled": false, "show_in_all_groups": false}).Error; err != nil {
		t.Fatal(err)
	}
	before := v392ReadRows[v30RC3SeparatorRow](t, db)[0]
	verifyCustom := v4SQLiteOperatorObjects(t, db, "nodes_separator")
	if err := EnsureSchema(db); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"show_in_all_groups", "group_ids"} {
		if db.Migrator().HasColumn(&separatorRow{}, name) {
			t.Fatalf("legacy separator column %s survived", name)
		}
	}
	after := v392ReadRows[separatorRow](t, db)
	if len(after) != 1 || after[0].ID != before.ID || after[0].DisplayName != before.DisplayName ||
		after[0].SortOrder != before.SortOrder || after[0].Enabled == nil || *after[0].Enabled ||
		!after[0].CreatedAt.Equal(before.CreatedAt) || after[0].Mode != "global" || len(after[0].NodeIDs) != 0 {
		t.Fatal("separator cleanup changed retained data or legacy safe-default behavior")
	}
	// Unlike node/attachment probes, this intentionally keeps ID to test the PK.
	duplicate := after[0]
	v392RejectDuplicate(t, db, &duplicate, "separator primary key")
	verifyCustom()
	if db.Dialector.Name() == "sqlite" {
		if err := db.Exec("UPDATE nodes_separator SET display_name = ? WHERE id = ?", "operator separator trigger verified", row.ID).Error; err != nil {
			t.Fatal(err)
		}
		var count int64
		if err := db.Table("operator_nodes_separator_name_updates").Count(&count).Error; err != nil || count != 1 {
			t.Fatal("operator-owned separator trigger no longer executes after cleanup")
		}
	}
	if err := EnsureSchema(db); err != nil {
		t.Fatal(err)
	}
	verifyCustom()
}

func TestSQLiteLegacyCleanupRefusesOperatorIndexOnRetiredColumn(t *testing.T) {
	for _, tc := range []struct {
		table, column string
		model         any
	}{
		{"nodes", "port", &v392Beta20NodeRow{PanelID: 3, InboundID: 2, DisplayName: "guard", Region: "US", Port: 8443, Protocol: "vless"}},
		{"nodes", "protocol", &v392Beta20NodeRow{PanelID: 3, InboundID: 2, DisplayName: "guard", Region: "US", Port: 8443, Protocol: "vless"}},
		{"psp_client_inbounds", "provisioned", &v392Beta20AttachmentRow{ClientID: 1, NodeID: 2, Provisioned: true}},
		{"nodes_separator", "show_in_all_groups", &v30RC3SeparatorRow{DisplayName: "guard", Enabled: true, ShowInAllGroups: true}},
		{"nodes_separator", "group_ids", &v30RC3SeparatorRow{DisplayName: "guard", Enabled: true, GroupIDs: jsonInt64s{7, 8}}},
	} {
		t.Run(tc.table+"/"+tc.column, func(t *testing.T) {
			db, err := openTestDB(t)
			if err != nil {
				t.Fatal(err)
			}
			if db.Dialector.Name() != "sqlite" {
				t.Skip("SQLite-specific operator schema dependency guard")
			}
			if err := db.AutoMigrate(tc.model); err != nil {
				t.Fatal(err)
			}
			if err := db.Create(tc.model).Error; err != nil {
				t.Fatal(err)
			}
			// These identifiers are fixed test cases, never external input.
			if err := db.Exec(fmt.Sprintf("CREATE INDEX operator_retired_guard ON %s(%s)", tc.table, tc.column)).Error; err != nil {
				t.Fatal(err)
			}
			if err := EnsureSchema(db.Session(&gorm.Session{Logger: logger.Discard})); err == nil {
				t.Fatal("cleanup silently destroyed or bypassed an operator dependency on a retired column")
			}
			if !db.Migrator().HasColumn(tc.model, tc.column) || !db.Migrator().HasIndex(tc.model, "operator_retired_guard") {
				t.Fatal("failed cleanup removed the operator index or its required column")
			}
			var count int64
			if err := db.Table(tc.table).Count(&count).Error; err != nil || count != 1 {
				t.Fatal("failed cleanup lost its historical row")
			}
		})
	}
}
