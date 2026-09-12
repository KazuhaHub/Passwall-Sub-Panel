package sqlstore

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"gorm.io/gorm"
)

// Embed the real dialect/migrator and fail exactly one metadata query. SQL,
// transactions and every subsequent query still use the isolated real DB.
type onceColumnMetadataDialect struct {
	gorm.Dialector
	failed *atomic.Bool
	err    error
}

func (dialect onceColumnMetadataDialect) Migrator(db *gorm.DB) gorm.Migrator {
	return onceColumnMetadataMigrator{dialect.Dialector.Migrator(db), dialect.failed, dialect.err}
}

type onceColumnMetadataMigrator struct {
	gorm.Migrator
	failed *atomic.Bool
	err    error
}

func (migrator onceColumnMetadataMigrator) ColumnTypes(value any) ([]gorm.ColumnType, error) {
	if !migrator.failed.Swap(true) {
		return nil, migrator.err
	}
	return migrator.Migrator.ColumnTypes(value)
}

func TestV4ValueMigrationMetadataFailureDoesNotStampOrChangeSourceAndCanRetry(t *testing.T) {
	for _, name := range []string{"node-endpoint", "attachment-state"} {
		t.Run(name, func(t *testing.T) {
			db, err := openTestDB(t)
			if err != nil {
				t.Fatal(err)
			}
			seedV3Baseline(t, db)
			var migrate func(*gorm.DB) error
			var marker string
			if name == "node-endpoint" {
				if err := db.Create(&v392Beta20NodeRow{PanelID: 10, InboundID: 20, DisplayName: "source", Region: "JP", Port: 8443, Protocol: "vless"}).Error; err != nil {
					t.Fatal(err)
				}
				// Add only this migration's frozen beta1 target DDL, retaining
				// old source columns. This is not a relabeled historical DB.
				if err := db.AutoMigrate(&v400Beta1NodeRow{}); err != nil {
					t.Fatal(err)
				}
				migrate, marker = migrateNodeEndpointState, "node_endpoint_desired_observed_v4"
			} else {
				yes, no := true, false
				for _, row := range []legacyPSPClientInboundRow{{ClientID: 1, NodeID: 10, Provisioned: &yes}, {ClientID: 1, NodeID: 11, Provisioned: &no}} {
					if err := db.Create(&row).Error; err != nil {
						t.Fatal(err)
					}
				}
				if err := db.AutoMigrate(&v400Beta1AttachmentRow{}); err != nil {
					t.Fatal(err)
				}
				migrate, marker = migratePSPClientInboundState, "psp_client_inbound_state_v4"
			}
			before := schemaBaselineSnapshot(t, db)
			metadataError := errors.New("fixture transient column metadata failure")
			failed := &atomic.Bool{}
			faultDB := db.Session(&gorm.Session{})
			faultDB.Dialector = onceColumnMetadataDialect{Dialector: db.Dialector, failed: failed, err: metadataError}
			// Invoke the dedicated value migration, not EnsureSchema preflight.
			if err := migrate(faultDB); !errors.Is(err, metadataError) || !failed.Load() {
				t.Fatalf("value migration did not return the metadata failure: %v", err)
			}
			if schemaBaselineSnapshot(t, db) != before {
				t.Fatal("metadata failure changed source values, columns or migration markers")
			}
			if err := migrate(faultDB); err != nil {
				t.Fatalf("value migration did not recover after transient metadata error: %v", err)
			}
			var applied int64
			if err := db.Table("schema_migrations").Where("id = ?", marker).Count(&applied).Error; err != nil || applied != 1 {
				t.Fatal("successful retry did not stamp exactly one migration marker")
			}
			if name == "node-endpoint" {
				got := v392ReadRows[v400Beta1NodeRow](t, db)
				if len(got) != 1 || got[0].DesiredPort != 8443 || got[0].ObservedPort != 8443 || got[0].DesiredProtocol != "vless" || got[0].ObservedProtocol != "vless" {
					t.Fatal("retry failed to copy the original endpoint to both axes")
				}
			} else {
				got := v392ReadRows[v400Beta1AttachmentRow](t, db)
				if len(got) != 2 || got[0].State != "applied" || got[0].FirstFailedAt != nil || got[1].State != "pending" || got[1].FirstFailedAt == nil {
					t.Fatal("retry failed to preserve true/false confirmation semantics")
				}
			}
		})
	}
}

func TestV4IdentityMigrationPreservesUnexpectedOperatorIndexDefinition(t *testing.T) {
	db, err := openTestDB(t)
	if err != nil {
		t.Fatal(err)
	}
	seedV3Baseline(t, db)
	if err := db.Migrator().DropIndex(&v392Beta20ClientRow{}, "uk_psp_client"); err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("CREATE UNIQUE INDEX uk_psp_client ON psp_clients(uuid)").Error; err != nil {
		t.Fatal(err)
	}
	client := v392Beta20ClientRow{UserID: 1, PanelID: 10, Email: "existing@example.invalid", UUID: "operator-unique-uuid", Password: "existing-password", LastRawTotalBytes: 99}
	if err := db.Create(&client).Error; err != nil {
		t.Fatal(err)
	}
	before := v392ReadRows[v392Beta20ClientRow](t, db)
	if err := EnsureSchema(db); err == nil {
		t.Fatal("ambiguous reused identity index was not held for operator review")
	}
	if !db.Migrator().HasIndex(&v392Beta20ClientRow{}, "uk_psp_client") || !reflect.DeepEqual(v392ReadRows[v392Beta20ClientRow](t, db), before) {
		t.Fatal("identity migration removed operator uniqueness or changed stable client/source data")
	}
	var complete int64
	if err := db.Table("schema_migrations").Where("id = ?", "v3_to_v4_baseline_v1").Count(&complete).Error; err != nil || complete != 0 {
		t.Fatal("ambiguous operator identity constraint stamped V4 completion")
	}
	duplicate := client
	duplicate.ID, duplicate.UserID, duplicate.PanelID, duplicate.Email = 0, 2, 11, "other@example.invalid"
	v392RejectDuplicate(t, db, &duplicate, "operator UUID uniqueness")
}

func TestV4BaselineIndexNormalizationPreservesReusedOperatorUniqueIndex(t *testing.T) {
	db, err := openTestDB(t)
	if err != nil {
		t.Fatal(err)
	}
	seedV3Baseline(t, db)
	if err := db.Exec("CREATE UNIQUE INDEX idx_users_email ON users(uuid)").Error; err != nil {
		t.Fatal(err)
	}
	user := v392Beta20UserRow{UPN: "existing", SubToken: "existing-token", UUID: "operator-unique-uuid", Role: "user", Enabled: false}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	before := v392ReadRows[v392Beta20UserRow](t, db)
	for boot := 1; boot <= 2; boot++ {
		if err := EnsureSchema(db); err != nil {
			t.Fatal(err)
		}
		if !db.Migrator().HasIndex(&v392Beta20UserRow{}, "idx_users_email") || !reflect.DeepEqual(v392ReadRows[v392Beta20UserRow](t, db), before) {
			t.Fatal("obsolete-index normalization removed operator uniqueness or changed source user")
		}
	}
	duplicate := user
	duplicate.ID, duplicate.UPN, duplicate.SubToken = 0, "another", "another-token"
	v392RejectDuplicate(t, db, &duplicate, "operator UUID uniqueness")
}

func TestCurrentSchemaUsesUniqueIndexesInsteadOfColumnUniqueTags(t *testing.T) {
	db, err := openTestDB(t)
	if err != nil {
		t.Fatal(err)
	}
	for _, model := range schemaModels {
		statement := &gorm.Statement{DB: db}
		if err := statement.Parse(model); err != nil {
			t.Fatal(err)
		}
		for _, field := range statement.Schema.Fields {
			if field.Unique && !field.IgnoreMigration {
				t.Fatalf("%s.%s uses column UNIQUE; MySQL schema migration requires explicit preserving support or uniqueIndex", statement.Schema.Table, field.DBName)
			}
		}
	}
}

func TestPostgresV4IdentityIndexesAreIsolatedToCurrentSchema(t *testing.T) {
	for _, currentSchema := range []string{"public", "psp.current"} {
		for _, baseline := range []string{"v3", "fresh"} {
			t.Run(currentSchema+"/"+baseline, func(t *testing.T) {
				db, err := openTestDB(t)
				if err != nil {
					t.Fatal(err)
				}
				if db.Dialector.Name() != "postgres" {
					t.Skip("PostgreSQL namespace-specific index regression")
				}
				// Keep session-local search_path deterministic across all GORM
				// metadata queries and transactions in this isolated database.
				pool, err := db.DB()
				if err != nil {
					t.Fatal(err)
				}
				pool.SetMaxOpenConns(1)
				pool.SetMaxIdleConns(1)
				quote := func(name string) string { return `"` + strings.ReplaceAll(name, `"`, `""`) + `"` }
				if currentSchema != "public" {
					if err := db.Exec("CREATE SCHEMA " + quote(currentSchema)).Error; err != nil {
						t.Fatal(err)
					}
				}
				if err := db.Exec("SET search_path TO " + quote(currentSchema)).Error; err != nil {
					t.Fatal(err)
				}
				if baseline == "v3" {
					seedV3Baseline(t, db)
				}
				foreignSchema := "operator.test"
				for _, statement := range []string{
					"CREATE SCHEMA " + quote(foreignSchema),
					"CREATE TABLE " + quote(foreignSchema) + `.psp_clients (id bigint PRIMARY KEY, panel_id bigint, email text, uuid text)`,
					"CREATE UNIQUE INDEX uk_psp_client ON " + quote(foreignSchema) + ".psp_clients (uuid)",
					"INSERT INTO " + quote(foreignSchema) + `.psp_clients VALUES (1, 17, 'operator@example.invalid', 'operator-uuid')`,
					"SET search_path TO " + quote(currentSchema) + ", " + quote(foreignSchema),
				} {
					if err := db.Exec(statement).Error; err != nil {
						t.Fatal(err)
					}
				}
				var schemaName string
				if err := db.Raw("SELECT current_schema()").Scan(&schemaName).Error; err != nil || schemaName != currentSchema {
					t.Fatalf("wrong active schema: %q (%v)", schemaName, err)
				}
				readForeignIndex := func() string {
					var definition string
					if err := db.Raw(`SELECT pg_get_indexdef(indexrelid) FROM pg_index JOIN pg_class ON pg_class.oid = indexrelid JOIN pg_namespace ON pg_namespace.oid = pg_class.relnamespace WHERE pg_namespace.nspname = ? AND pg_class.relname = 'uk_psp_client'`, foreignSchema).Scan(&definition).Error; err != nil {
						t.Fatal(err)
					}
					if definition == "" {
						t.Fatal("operator identity index is absent")
					}
					return definition
				}
				before := readForeignIndex()
				for boot := 1; boot <= 2; boot++ {
					if err := EnsureSchema(db); err != nil {
						t.Fatalf("%s baseline boot %d confused an operator index with current PSP identity: %v", baseline, boot, err)
					}
					if readForeignIndex() != before {
						t.Fatal("V4 identity migration changed an index belonging to another schema")
					}
					var currentIndexCount, foreignRowCount int64
					if err := db.Raw(`SELECT count(*) FROM pg_class JOIN pg_namespace ON pg_namespace.oid = pg_class.relnamespace WHERE pg_namespace.nspname = ? AND pg_class.relname = 'uk_psp_client' AND pg_class.relkind = 'i'`, currentSchema).Scan(&currentIndexCount).Error; err != nil || currentIndexCount != 0 {
						t.Fatal("current PSP identity index was not retired independently")
					}
					if err := db.Raw(fmt.Sprintf("SELECT count(*) FROM %s.psp_clients WHERE id = 1 AND panel_id = 17 AND email = 'operator@example.invalid' AND uuid = 'operator-uuid'", quote(foreignSchema))).Scan(&foreignRowCount).Error; err != nil || foreignRowCount != 1 {
						t.Fatal("V4 upgrade changed another schema's operator client")
					}
				}
			})
		}
	}
}
