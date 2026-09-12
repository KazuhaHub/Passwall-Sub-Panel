package sqlstore

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"
)

// Frozen source: v3.9.2-beta.20, commit
// 081b209d67873f8ab78f69aba5d6a24cf294404e,
// internal/adapters/sqlstore/schema.go: userRow, groupRow, xuiPanelRow,
// separatorRow and schemaMigrationRow. Field types/tags/table names are copied
// verbatim; only Go names change and historical prose comments are omitted.
// The SQL representations of the JSON wrappers are unchanged from that source.
// Together with the frozen node/client/attachment models in v3_v4_upgrade_test,
// these are a real V3 core-schema baseline, not current rows relabeled historical.
type v392Beta20UserRow struct {
	ID                     int64  `gorm:"primaryKey;autoIncrement"`
	UPN                    string `gorm:"size:255;uniqueIndex;not null"`
	SSOProvider            string `gorm:"size:64;not null;default:local;index:idx_user_sso,priority:1"`
	SSOSubject             string `gorm:"size:255;not null;default:'';index:idx_user_sso,priority:2"`
	Email                  string `gorm:"size:255"`
	PasswordHash           string `gorm:"size:255"`
	Role                   string `gorm:"size:16;not null;default:user"`
	SubToken               string `gorm:"size:64;uniqueIndex;not null"`
	UUID                   string `gorm:"size:36;not null"`
	GroupID                int64  `gorm:"index;not null"`
	EnabledRuleSets        jsonStrings
	PersonalRules          string `gorm:"type:text"`
	ExpireAt               *time.Time
	TrafficLimitBytes      *int64
	IPLimit                *int
	DeviceLimit            *int
	TrafficResetPeriod     string `gorm:"size:16;default:never"`
	TrafficPeriodStart     *time.Time
	LifetimeUpBytes        int64 `gorm:"default:0"`
	LifetimeDownBytes      int64 `gorm:"default:0"`
	LifetimeTotalBytes     int64 `gorm:"default:0"`
	PeriodBaselineBytes    int64 `gorm:"default:0"`
	LifetimeBaselineAt     *time.Time
	DisplayName            string `gorm:"size:128"`
	Remark                 string `gorm:"size:255"`
	Enabled                bool   `gorm:"not null"`
	AutoDisabledReason     string `gorm:"size:32"`
	DisableDetail          string `gorm:"type:text"`
	ServiceDisabledReason  string `gorm:"size:32;not null;default:''"`
	ServiceDisableDetail   string `gorm:"type:text"`
	ServiceDisabledAt      *time.Time
	SelfRegistered         bool `gorm:"not null;default:false"`
	BlockViolationCount    int  `gorm:"default:0"`
	LastBlockViolationAt   *time.Time
	EmergencyUsedCount     int
	EmergencyUntil         *time.Time
	EmergencyBaselineBytes int64             `gorm:"default:0"`
	TokenVersion           int               `gorm:"default:0;not null"`
	TOTPSecret             string            `gorm:"column:totp_secret;size:255;not null;default:''"`
	TOTPEnabled            bool              `gorm:"column:totp_enabled;not null;default:false"`
	RecoveryCodes          jsonStrings       `gorm:"column:recovery_codes"`
	PermissionOverrides    jsonPermOverrides `gorm:"column:permission_overrides"`
	LastOnlineAt           *time.Time
	CreatedAt              time.Time
	UpdatedAt              time.Time
}

func (v392Beta20UserRow) TableName() string { return "users" }

type v392Beta20GroupRow struct {
	ID                int64  `gorm:"primaryKey;autoIncrement"`
	Slug              string `gorm:"size:64;uniqueIndex;not null"`
	Name              string `gorm:"size:128;not null"`
	TagFilter         jsonTagFilter
	Layout            jsonLayout
	Remark            string `gorm:"size:255"`
	Require2FA        bool   `gorm:"column:require_2fa;not null;default:false"`
	TrafficLimitBytes *int64
	IPLimit           *int
	DeviceLimit       *int
	CreatedAt         time.Time
}

func (v392Beta20GroupRow) TableName() string { return "groups_" }

type v392Beta20PanelRow struct {
	ID                 int64  `gorm:"primaryKey;autoIncrement"`
	Kind               string `gorm:"size:32;not null;default:'';index"`
	Name               string `gorm:"size:128;uniqueIndex;not null"`
	URL                string `gorm:"size:512;not null"`
	APIToken           string `gorm:"type:text"`
	Username           string `gorm:"size:255"`
	Password           string `gorm:"type:text"`
	Remark             string `gorm:"size:255"`
	AuthMethod         string `gorm:"size:16;default:''"`
	InsecureSkipVerify bool   `gorm:"default:false"`
	PanelVersion       string `gorm:"size:32;default:''"`
	XrayVersion        string `gorm:"size:32;default:''"`
	VersionCheckedAt   *time.Time
	IPLimitEnforcement string `gorm:"size:24;default:''"`
	IPLimitProbedAt    *time.Time
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

func (v392Beta20PanelRow) TableName() string { return "xui_panels" }

type v392Beta20SeparatorRow struct {
	ID          int64  `gorm:"primaryKey;autoIncrement"`
	DisplayName string `gorm:"size:255;not null"`
	SortOrder   int    `gorm:"default:0"`
	Enabled     *bool  `gorm:"default:true"`
	Mode        string `gorm:"size:32;not null;default:global"`
	NodeIDs     jsonInt64s
	CreatedAt   time.Time
}

func (v392Beta20SeparatorRow) TableName() string { return "nodes_separator" }

type v392Beta20SchemaMigrationRow struct {
	ID        string `gorm:"primaryKey;size:64"`
	AppliedAt time.Time
}

func (v392Beta20SchemaMigrationRow) TableName() string { return "schema_migrations" }

const v392LimitsBaselineMarker = "limits_tristate_v3.9.3"

func seedV3Baseline(t *testing.T, db *gorm.DB) {
	t.Helper()
	seedV3BaselineWithSeparator(t, db, &v392Beta20SeparatorRow{})
}

// The override is only for refusal fixtures: their other seven core tables
// must remain a valid baseline so the old separator cannot fail for another
// missing table and make the test accidentally green.
func seedV3BaselineWithSeparator(t *testing.T, db *gorm.DB, separator any) {
	t.Helper()
	if err := db.AutoMigrate(
		&v392Beta20SchemaMigrationRow{}, &v392Beta20UserRow{},
		&v392Beta20GroupRow{}, &v392Beta20PanelRow{}, &v392Beta20NodeRow{},
		&v392Beta20ClientRow{}, &v392Beta20AttachmentRow{}, separator,
	); err != nil {
		t.Fatalf("create frozen V3 baseline: %v", err)
	}
	if err := db.Create(&v392Beta20SchemaMigrationRow{
		ID: v392LimitsBaselineMarker, AppliedAt: time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC),
	}).Error; err != nil {
		t.Fatalf("record historical tri-state baseline: %v", err)
	}
}

func TestV4BaselineDoesNotReinterpretHistoricalLimitsIdentityAndDisableState(t *testing.T) {
	db, err := openTestDB(t)
	if err != nil {
		t.Fatal(err)
	}
	seedV3Baseline(t, db)
	when := time.Date(2026, 7, 1, 2, 3, 4, 123456000, time.UTC)
	zeroTraffic, quota := int64(0), int64(100<<30)
	zeroCap, ipCap, deviceCap := 0, 3, 2
	group := v392Beta20GroupRow{
		Slug: "limited-group", Name: "Existing group", Require2FA: true,
		TrafficLimitBytes: &quota, IPLimit: &ipCap, DeviceLimit: &deviceCap, CreatedAt: when,
	}
	if err := db.Create(&group).Error; err != nil {
		t.Fatal(err)
	}
	users := []v392Beta20UserRow{
		{
			UPN: "explicit-unlimited", SSOProvider: "oidc:fixture", SSOSubject: "persistent-subject",
			Role: "admin", SubToken: "explicit-token", UUID: "00000000-0000-0000-0000-000000000001", GroupID: group.ID,
			TrafficLimitBytes: &zeroTraffic, IPLimit: &zeroCap, DeviceLimit: &zeroCap, Enabled: true,
		},
		{
			UPN: "inherit", Role: "user", SubToken: "inherit-token", UUID: "00000000-0000-0000-0000-000000000002", GroupID: group.ID,
			Enabled: false, AutoDisabledReason: "quota", DisableDetail: "historical account hold",
		},
		{
			UPN: "explicit-quota", Role: "operator", SubToken: "quota-token", UUID: "00000000-0000-0000-0000-000000000003", GroupID: group.ID,
			TrafficLimitBytes: &quota, IPLimit: &zeroCap, DeviceLimit: &deviceCap, Enabled: true,
			ServiceDisabledReason: "manual", ServiceDisableDetail: "existing service hold", ServiceDisabledAt: &when,
		},
	}
	for index := range users {
		row := &users[index]
		row.Email, row.PasswordHash = row.UPN+"@example.invalid", "historical-password-hash"
		row.EnabledRuleSets, row.PersonalRules = jsonStrings{"existing"}, "DOMAIN,example.invalid,DIRECT"
		row.ExpireAt, row.TrafficPeriodStart, row.LifetimeBaselineAt = &when, &when, &when
		row.TrafficResetPeriod = "month"
		row.LifetimeUpBytes, row.LifetimeDownBytes, row.LifetimeTotalBytes = 9007199254740993, 42, 9007199254741035
		row.PeriodBaselineBytes, row.EmergencyBaselineBytes = 9007199254740000, 99
		row.BlockViolationCount, row.EmergencyUsedCount, row.TokenVersion = 3, 2, 11
		row.LastBlockViolationAt, row.EmergencyUntil, row.LastOnlineAt = &when, &when, &when
		row.TOTPSecret, row.TOTPEnabled, row.RecoveryCodes = "existing-totp-ciphertext", true, jsonStrings{"existing-recovery-hash"}
		row.CreatedAt, row.UpdatedAt = when, when
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	// Database reload handles each driver's timestamp precision and defaults.
	beforeUsers := v392ReadRows[v392Beta20UserRow](t, db)
	beforeGroups := v392ReadRows[v392Beta20GroupRow](t, db)
	beforeMarker := v392ReadRows[v392Beta20SchemaMigrationRow](t, db)
	for boot := 1; boot <= 2; boot++ {
		if err := EnsureSchema(db); err != nil {
			t.Fatalf("V4 boot %d: %v", boot, err)
		}
		if !reflect.DeepEqual(v392ReadRows[v392Beta20UserRow](t, db), beforeUsers) ||
			!reflect.DeepEqual(v392ReadRows[v392Beta20GroupRow](t, db), beforeGroups) {
			t.Fatalf("V4 boot %d reinterpreted tri-state limits, disable axes, identity, counts or timestamps", boot)
		}
		var marker v392Beta20SchemaMigrationRow
		if err := db.First(&marker, "id = ?", v392LimitsBaselineMarker).Error; err != nil || !reflect.DeepEqual(marker, beforeMarker[0]) {
			t.Fatalf("V4 boot %d rewrote historical limits marker: %v", boot, err)
		}
		admins, err := NewRepos(db).User.CountEnabledAdmins(t.Context())
		if err != nil || admins != 1 {
			t.Fatalf("V4 boot %d changed enabled admin identity/count: %d, %v", boot, admins, err)
		}
	}
}

// Capture schema plus exact driver-returned cell values, not just row counts:
// a 0 -> NULL rewrite would leave the count unchanged. Operator tables are
// included. These fixtures contain no live credentials; do not log snapshots.
func schemaBaselineSnapshot(t *testing.T, db *gorm.DB) string {
	t.Helper()
	tables, err := db.Migrator().GetTables()
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(tables)
	state := make(map[string][]string, len(tables))
	for _, table := range tables {
		columns, err := db.Migrator().ColumnTypes(table)
		if err != nil {
			t.Fatal(err)
		}
		for _, column := range columns {
			nullable, knownNullable := column.Nullable()
			primary, knownPrimary := column.PrimaryKey()
			state[table] = append(state[table], fmt.Sprintf("column:%s:%s:%t:%t:%t:%t", column.Name(), column.DatabaseTypeName(), nullable, knownNullable, primary, knownPrimary))
		}
		statement := &gorm.Statement{DB: db}
		rows, err := db.Raw("SELECT * FROM " + statement.Quote(table)).Rows()
		if err != nil {
			t.Fatal(err)
		}
		fields, err := rows.Columns()
		if err != nil {
			rows.Close()
			t.Fatal(err)
		}
		for rows.Next() {
			values, pointers := make([]any, len(fields)), make([]any, len(fields))
			for index := range values {
				pointers[index] = &values[index]
			}
			if err := rows.Scan(pointers...); err != nil {
				rows.Close()
				t.Fatal(err)
			}
			cells := make([]string, len(fields))
			for index, value := range values {
				switch typed := value.(type) {
				case []byte:
					cells[index] = "bytes:" + string(typed)
				case time.Time:
					cells[index] = "time:" + typed.UTC().Format(time.RFC3339Nano)
				default:
					cells[index] = fmt.Sprintf("%T:%v", value, value)
				}
			}
			encoded, err := json.Marshal(cells)
			if err != nil {
				rows.Close()
				t.Fatal(err)
			}
			state[table] = append(state[table], "row:"+string(encoded))
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		rows.Close()
		sort.Strings(state[table])
	}
	encoded, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

func refuseBaselineWithoutWrites(t *testing.T, db *gorm.DB) {
	t.Helper()
	before := schemaBaselineSnapshot(t, db)
	if err := EnsureSchema(db); err == nil || !strings.Contains(err.Error(), "unsupported PSP database baseline") {
		t.Fatalf("unsupported baseline was not refused: %v", err)
	}
	if schemaBaselineSnapshot(t, db) != before {
		t.Fatal("unsupported baseline refusal changed a table, column, primary key, marker or stored cell")
	}
}

func TestV4BaselineRefusesMissingCoreTablesAndMarkerWithoutWrites(t *testing.T) {
	for _, table := range []string{"schema_migrations", "users", "groups_", "xui_panels", "nodes", "psp_clients", "psp_client_inbounds", "nodes_separator"} {
		t.Run(table, func(t *testing.T) {
			db, err := openTestDB(t)
			if err != nil {
				t.Fatal(err)
			}
			seedV3Baseline(t, db)
			if err := db.Migrator().DropTable(table); err != nil {
				t.Fatal(err)
			}
			refuseBaselineWithoutWrites(t, db)
		})
	}
	t.Run("limits-marker", func(t *testing.T) {
		db, err := openTestDB(t)
		if err != nil {
			t.Fatal(err)
		}
		seedV3Baseline(t, db)
		if err := db.Where("id = ?", v392LimitsBaselineMarker).Delete(&v392Beta20SchemaMigrationRow{}).Error; err != nil {
			t.Fatal(err)
		}
		refuseBaselineWithoutWrites(t, db)
	})
}

// Deliberately unsupported contract mutations, not another claimed release
// fixture. Alter just one limit column so each nullability check is exercised.
type nonNullableBaselineUserLimits struct {
	TrafficLimitBytes int64 `gorm:"not null;default:0"`
	IPLimit           int   `gorm:"not null;default:0"`
	DeviceLimit       int   `gorm:"not null;default:0"`
}

func (nonNullableBaselineUserLimits) TableName() string { return "users" }

type nonNullableBaselineGroupLimits nonNullableBaselineUserLimits

func (nonNullableBaselineGroupLimits) TableName() string { return "groups_" }

func TestV4BaselineRefusesNonNullableLimitsWithoutWrites(t *testing.T) {
	for _, table := range []struct {
		name  string
		model any
	}{{"users", &nonNullableBaselineUserLimits{}}, {"groups_", &nonNullableBaselineGroupLimits{}}} {
		for _, field := range []string{"TrafficLimitBytes", "IPLimit", "DeviceLimit"} {
			t.Run(table.name+"/"+field, func(t *testing.T) {
				db, err := openTestDB(t)
				if err != nil {
					t.Fatal(err)
				}
				seedV3Baseline(t, db)
				if err := db.Migrator().AlterColumn(table.model, field); err != nil {
					t.Fatal(err)
				}
				refuseBaselineWithoutWrites(t, db)
			})
		}
	}
}

func TestV4BaselineRefusesMissingCoreColumnsAndContradictoryEndpointOrAttachmentWithoutWrites(t *testing.T) {
	for _, test := range []struct{ table, column string }{
		{"users", "service_disabled_reason"}, {"users", "period_baseline_bytes"},
		{"xui_panels", "ip_limit_enforcement"}, {"xui_panels", "ip_limit_probed_at"},
		{"nodes_separator", "mode"}, {"nodes_separator", "node_ids"},
		{"nodes", "port"}, {"nodes", "protocol"}, {"psp_client_inbounds", "provisioned"},
	} {
		t.Run(test.table+"/"+test.column, func(t *testing.T) {
			db, err := openTestDB(t)
			if err != nil {
				t.Fatal(err)
			}
			seedV3Baseline(t, db)
			// All identifiers are fixed refusal cases. Native ALTER is also
			// valid on SQLite; its GORM table-string DropColumn has no Schema.
			statement := &gorm.Statement{DB: db}
			if err := db.Exec("ALTER TABLE " + statement.Quote(test.table) + " DROP COLUMN " + statement.Quote(test.column)).Error; err != nil {
				t.Fatal(err)
			}
			refuseBaselineWithoutWrites(t, db)
		})
	}
}

func TestV4BaselineRefusesUnfinishedSeparatorRowsWithoutWrites(t *testing.T) {
	db, err := openTestDB(t)
	if err != nil {
		t.Fatal(err)
	}
	seedV3Baseline(t, db)
	if err := db.Create(&v392Beta20NodeRow{
		PanelID: 3, InboundID: -8, DisplayName: "old separator row", Region: "US", Kind: "separator",
	}).Error; err != nil {
		t.Fatal(err)
	}
	refuseBaselineWithoutWrites(t, db)
}

func TestV4FreshSchemaDoesNotInventHistoricalLimitsMigration(t *testing.T) {
	db, err := openTestDB(t)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("CREATE TABLE operator_owned_state (value VARCHAR(64) NOT NULL)").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("INSERT INTO operator_owned_state (value) VALUES (?)", "keep-existing").Error; err != nil {
		t.Fatal(err)
	}
	for boot := 1; boot <= 2; boot++ {
		if err := EnsureSchema(db); err != nil {
			t.Fatalf("fresh V4 boot %d: %v", boot, err)
		}
		var limits, complete int64
		if err := db.Table("schema_migrations").Where("id = ?", v392LimitsBaselineMarker).Count(&limits).Error; err != nil || limits != 0 {
			t.Fatalf("fresh V4 invented a historical limits reinterpretation marker: %d, %v", limits, err)
		}
		if err := db.Table("schema_migrations").Where("id = ?", "v3_to_v4_baseline_v1").Count(&complete).Error; err != nil || complete != 1 {
			t.Fatalf("fresh V4 did not complete its own baseline: %d, %v", complete, err)
		}
		var value string
		if err := db.Table("operator_owned_state").Select("value").Scan(&value).Error; err != nil || value != "keep-existing" {
			t.Fatal("fresh V4 changed an unrelated operator table")
		}
	}
}

func TestV4InterruptedEmptyInitializationCanResume(t *testing.T) {
	db, err := openTestDB(t)
	if err != nil {
		t.Fatal(err)
	}
	// Contract simulation of interruption after the durable fresh marker and
	// the users DDL, before completion. Users are unchanged between beta20 and
	// beta1; this table is empty, not a historical backup labeled as fresh.
	if err := db.AutoMigrate(&v392Beta20SchemaMigrationRow{}, &v392Beta20UserRow{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&v392Beta20SchemaMigrationRow{ID: "v4_schema_initializing_v1", AppliedAt: time.Now().UTC()}).Error; err != nil {
		t.Fatal(err)
	}
	if err := EnsureSchema(db); err != nil {
		t.Fatalf("empty fresh initialization did not resume: %v", err)
	}
	var complete, oldLimits int64
	if err := db.Table("schema_migrations").Where("id = ?", "v3_to_v4_baseline_v1").Count(&complete).Error; err != nil || complete != 1 {
		t.Fatal("resumed fresh initialization lacks its completion marker")
	}
	if err := db.Table("schema_migrations").Where("id = ?", v392LimitsBaselineMarker).Count(&oldLimits).Error; err != nil || oldLimits != 0 {
		t.Fatal("resumed fresh initialization invented historical limits semantics")
	}
}

func TestV4EarliestEmptySchemaMigrationsDDLInterruptionCanResume(t *testing.T) {
	db, err := openTestDB(t)
	if err != nil {
		t.Fatal(err)
	}
	// Contract simulation of failure between the first metadata-table DDL
	// and its initializing marker. No prior state or marker is invented.
	if err := db.AutoMigrate(&v392Beta20SchemaMigrationRow{}); err != nil {
		t.Fatal(err)
	}
	if err := EnsureSchema(db); err != nil {
		t.Fatalf("earliest empty initialization could not resume: %v", err)
	}
	var complete int64
	if err := db.Table("schema_migrations").Where("id = ?", "v3_to_v4_baseline_v1").Count(&complete).Error; err != nil || complete != 1 {
		t.Fatal("earliest empty initialization did not complete V4 baseline")
	}
}

func TestV4CompletedMigrationMarkerWithMissingTargetColumnRefusesBeforeDDLOrWrites(t *testing.T) {
	for _, test := range []struct {
		table, column, marker string
		target                any
	}{
		{"nodes", "desired_port", "node_endpoint_desired_observed_v4", &v400Beta1NodeRow{}},
		{"nodes", "observed_port", "node_endpoint_desired_observed_v4", &v400Beta1NodeRow{}},
		{"nodes", "desired_protocol", "node_endpoint_desired_observed_v4", &v400Beta1NodeRow{}},
		{"nodes", "observed_protocol", "node_endpoint_desired_observed_v4", &v400Beta1NodeRow{}},
		{"psp_client_inbounds", "state", "psp_client_inbound_state_v4", &v400Beta1AttachmentRow{}},
		{"psp_client_inbounds", "applied_version", "psp_client_inbound_state_v4", &v400Beta1AttachmentRow{}},
		{"psp_client_inbounds", "first_failed_at", "psp_client_inbound_state_v4", &v400Beta1AttachmentRow{}},
		{"psp_client_inbounds", "applied_email", "psp_client_inbound_applied_credentials_v1", &v400Beta1AttachmentRow{}},
		{"psp_client_inbounds", "applied_uuid", "psp_client_inbound_applied_credentials_v1", &v400Beta1AttachmentRow{}},
		{"psp_client_inbounds", "applied_password", "psp_client_inbound_applied_credentials_v1", &v400Beta1AttachmentRow{}},
	} {
		t.Run(test.table+"/"+test.column, func(t *testing.T) {
			db, err := openTestDB(t)
			if err != nil {
				t.Fatal(err)
			}
			seedV3Baseline(t, db)
			if err := db.Create(&v392Beta20NodeRow{PanelID: 1, InboundID: 2, DisplayName: "original source", Region: "US", Port: 8443, Protocol: "vless"}).Error; err != nil {
				t.Fatal(err)
			}
			if err := db.Create(&v392Beta20AttachmentRow{ClientID: 1, NodeID: 2, Provisioned: true}).Error; err != nil {
				t.Fatal(err)
			}
			// Known partial-DDL contract fixture: retain the actual old source,
			// add frozen target fields, then create a contradictory marker.
			if err := db.AutoMigrate(test.target); err != nil {
				t.Fatal(err)
			}
			statement := &gorm.Statement{DB: db}
			if err := db.Exec("ALTER TABLE " + statement.Quote(test.table) + " DROP COLUMN " + statement.Quote(test.column)).Error; err != nil {
				t.Fatal(err)
			}
			if err := db.Create(&v392Beta20SchemaMigrationRow{ID: test.marker, AppliedAt: time.Now().UTC()}).Error; err != nil {
				t.Fatal(err)
			}
			refuseBaselineWithoutWrites(t, db)
		})
	}
}
