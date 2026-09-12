package sqlstore

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/migrator"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

const (
	pspClientInboundAppliedCredentialsMigrationID = "psp_client_inbound_applied_credentials_v1"
	pspClientInboundStateMigrationID              = "psp_client_inbound_state_v4"
	nodeEndpointStateMigrationID                  = "node_endpoint_desired_observed_v4"
)

// migrateV3ToV4 is the bounded bridge from the supported final V3 schema to
// V4. It also accepts beta1, which executed these individual migrations before
// this consolidated completion marker existed. A current V4 restart does not
// scan or run the previous major's cleanup collection.
// Later V4 data migrations must have their own dispatch and markers, outside
// this completed previous-major bridge.
//
// DDL stays outside applyOnce transactions: MySQL implicitly commits DDL. Each
// structural step is independently repeatable, the value transformations keep
// their original transaction+marker identities, and completion is recorded only
// after every required step succeeds. Failure is not an automatic DB rollback.
func migrateV3ToV4(db *gorm.DB) error {
	var completed int64
	if err := db.Model(&schemaMigrationRow{}).Where("id = ?", v3ToV4BaselineMigrationID).Count(&completed).Error; err != nil {
		return fmt.Errorf("check V4 schema baseline: %w", err)
	}
	if completed != 0 {
		return nil
	}
	for _, step := range []func(*gorm.DB) error{
		migratePSPClientIdentityIndex,
		migratePSPClientInboundState,
		backfillPSPClientInboundAppliedCredentials,
		migrateNodeEndpointState,
		dropV3EndpointAndAttachmentColumns,
		normalizeV3BaselineIndexes,
	} {
		if err := step(db); err != nil {
			return err
		}
	}
	return applyOnce(db, v3ToV4BaselineMigrationID, func(*gorm.DB) error { return nil })
}

// Email is an upstream projection, not a durable client identity. AutoMigrate
// creates the current non-unique lookup but does not remove this V3 uniqueness.
// Preserve IDs, credentials and every traffic baseline while removing it.
func migratePSPClientIdentityIndex(db *gorm.DB) error {
	const legacyIndex = "uk_psp_client"
	indexes, err := migrationIndexes(db, &pspClientRow{})
	if err != nil {
		return fmt.Errorf("inspect legacy psp_clients identity index: %w", err)
	}
	for _, index := range indexes {
		if index.Name() != legacyIndex {
			continue
		}
		unique, known := index.Unique()
		columns := index.Columns()
		// PostgreSQL's metadata does not guarantee index-column ordering.
		// Uniqueness of this pair is independent of that ordering.
		identityPair := len(columns) == 2 && ((columns[0] == "panel_id" && columns[1] == "email") || (columns[0] == "email" && columns[1] == "panel_id"))
		if !known || !unique || !identityPair {
			return fmt.Errorf("legacy index %s has an unexpected definition; preserve it for operator review", legacyIndex)
		}
		if err := dropMigrationIndex(db, &pspClientRow{}, index); err != nil {
			return fmt.Errorf("drop legacy psp_clients email identity index: %w", err)
		}
	}
	return nil
}

// A false/NULL provisioned value proves neither rejection nor application. It
// becomes pending with a clock; true becomes applied. Reusing the beta1 marker
// is essential: a restart must not overwrite subsequent convergence state.
func migratePSPClientInboundState(db *gorm.DB) error {
	legacyExists, err := migrationHasColumn(db, "psp_client_inbounds", "provisioned")
	if err != nil {
		return err
	}
	return applyOnce(db, pspClientInboundStateMigrationID, func(tx *gorm.DB) error {
		if !legacyExists {
			return nil
		}
		now := time.Now().UTC()
		return tx.Exec(`
UPDATE psp_client_inbounds
SET
	state = CASE WHEN provisioned = ? THEN ? ELSE ? END,
	applied_version = 0,
	first_failed_at = CASE
		WHEN provisioned = ? THEN NULL
		ELSE COALESCE(first_failed_at, ?)
	END
`, true, string(domain.ClientApplyApplied), string(domain.ClientApplyPending), true, now).Error
	})
}

// Snapshot the credentials confirmed at the V3 -> V4 boundary. Future desired
// rotations must not change what render serves until application is confirmed.
// Row-wise updates are portable across all supported database dialects.
func backfillPSPClientInboundAppliedCredentials(db *gorm.DB) error {
	return applyOnce(db, pspClientInboundAppliedCredentialsMigrationID, func(tx *gorm.DB) error {
		var attachments []pspClientInboundRow
		if err := tx.Where("state = ? AND applied_uuid = ?", string(domain.ClientApplyApplied), "").Find(&attachments).Error; err != nil {
			return err
		}
		if len(attachments) == 0 {
			return nil
		}
		ids := make([]int64, 0, len(attachments))
		seen := make(map[int64]struct{}, len(attachments))
		for _, attachment := range attachments {
			if _, ok := seen[attachment.ClientID]; !ok {
				seen[attachment.ClientID] = struct{}{}
				ids = append(ids, attachment.ClientID)
			}
		}
		var clients []pspClientRow
		if err := tx.Where("id IN ?", ids).Find(&clients).Error; err != nil {
			return err
		}
		byID := make(map[int64]pspClientRow, len(clients))
		for _, client := range clients {
			byID[client.ID] = client
		}
		for _, attachment := range attachments {
			client, ok := byID[attachment.ClientID]
			if !ok {
				continue
			}
			if err := tx.Model(&pspClientInboundRow{}).Where("id = ?", attachment.ID).Updates(map[string]any{
				"applied_email": client.Email, "applied_uuid": client.UUID, "applied_password": client.Password,
			}).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

// At the boundary the old endpoint is the only known value, so copying it to
// both axes preserves connectivity. The marker prevents later observed drift
// from being replaced by the administrator's desired state on restart.
func migrateNodeEndpointState(db *gorm.DB) error {
	legacyPort, err := migrationHasColumn(db, "nodes", "port")
	if err != nil {
		return err
	}
	legacyProtocol, err := migrationHasColumn(db, "nodes", "protocol")
	if err != nil {
		return err
	}
	return applyOnce(db, nodeEndpointStateMigrationID, func(tx *gorm.DB) error {
		assignments := make([]string, 0, 4)
		if legacyPort {
			assignments = append(assignments, "desired_port = port", "observed_port = port")
		}
		if legacyProtocol {
			assignments = append(assignments, "desired_protocol = protocol", "observed_protocol = protocol")
		}
		if len(assignments) == 0 {
			return nil
		}
		return tx.Exec("UPDATE nodes SET " + strings.Join(assignments, ", ")).Error
	})
}

func dropV3EndpointAndAttachmentColumns(db *gorm.DB) error {
	for _, retired := range []struct {
		model  any
		table  string
		column string
	}{
		{&pspClientInboundRow{}, "psp_client_inbounds", "provisioned"},
		{&nodeRow{}, "nodes", "port"},
		{&nodeRow{}, "nodes", "protocol"},
	} {
		exists, err := migrationHasColumn(db, retired.table, retired.column)
		if err != nil {
			return err
		}
		if !exists {
			continue
		}
		if err := dropLegacyColumn(db, retired.model, retired.column); err != nil {
			return fmt.Errorf("retire V3 %s.%s: %w", retired.table, retired.column, err)
		}
	}
	return nil
}

// Final V3 treated these app-owned obsolete indexes as best-effort cleanup,
// so their presence does not make a valid V3 baseline unsupported. Normalize
// that fixed remainder once; do not carry every V3 separator transformation or
// repeat this previous-major index scan on completed V4 databases.
func normalizeV3BaselineIndexes(db *gorm.DB) error {
	for _, obsolete := range []struct {
		model  any
		name   string
		column string
	}{
		{&subLogRow{}, "idx_sub_logs_user_id", "user_id"},
		{&subLogRow{}, "idx_sub_logs_accessed_at", "accessed_at"},
		{&userRow{}, "idx_users_email", "email"},
	} {
		indexes, err := migrationIndexes(db, obsolete.model)
		if err != nil {
			return fmt.Errorf("inspect V3 baseline index %s: %w", obsolete.name, err)
		}
		for _, index := range indexes {
			if index.Name() != obsolete.name {
				continue
			}
			unique, known := index.Unique()
			columns := index.Columns()
			if !known || unique || len(columns) != 1 || columns[0] != obsolete.column {
				// An operator reused the app's old name for a different index or
				// constraint. Its definition is not ours to normalize.
				continue
			}
			if err := dropMigrationIndex(db, obsolete.model, index); err != nil {
				return fmt.Errorf("normalize V3 baseline index %s: %w", obsolete.name, err)
			}
		}
	}
	return nil
}

type postgresMigrationIndex struct {
	migrator.Index
	schema string
}

// GORM's PostgreSQL index lookup filters relation names, not namespaces, and
// its DROP INDEX follows search_path. Bind both operations to the current
// PSP schema, so another schema's same-named operator index is never touched.
func migrationIndexes(db *gorm.DB, model any) ([]gorm.Index, error) {
	if db.Dialector.Name() != "postgres" {
		return db.Migrator().GetIndexes(model)
	}
	statement := &gorm.Statement{DB: db}
	if err := statement.Parse(model); err != nil {
		return nil, err
	}
	var rows []struct {
		SchemaName string
		IndexName  string
		ColumnName string
		IsUnique   bool
		IsPrimary  bool
	}
	if err := db.Raw(`
SELECT ns.nspname AS schema_name, ci.relname AS index_name,
       COALESCE(a.attname, '') AS column_name,
       i.indisunique AS is_unique, i.indisprimary AS is_primary
FROM pg_catalog.pg_index i
JOIN pg_catalog.pg_class ct ON ct.oid = i.indrelid
JOIN pg_catalog.pg_namespace ns ON ns.oid = ct.relnamespace
JOIN pg_catalog.pg_class ci ON ci.oid = i.indexrelid
CROSS JOIN LATERAL unnest(i.indkey) WITH ORDINALITY AS k(attnum, position)
LEFT JOIN pg_catalog.pg_attribute a ON a.attrelid = ct.oid AND a.attnum = k.attnum
WHERE ns.nspname = current_schema() AND ct.relname = ?
ORDER BY ci.relname, k.position
`, statement.Schema.Table).Scan(&rows).Error; err != nil {
		return nil, err
	}
	indexes := make([]gorm.Index, 0)
	byName := make(map[string]*postgresMigrationIndex)
	for _, row := range rows {
		index := byName[row.IndexName]
		if index == nil {
			index = &postgresMigrationIndex{
				Index: migrator.Index{
					TableName: statement.Schema.Table, NameValue: row.IndexName,
					UniqueValue:     sql.NullBool{Bool: row.IsUnique, Valid: true},
					PrimaryKeyValue: sql.NullBool{Bool: row.IsPrimary, Valid: true},
				},
				schema: row.SchemaName,
			}
			byName[row.IndexName] = index
			indexes = append(indexes, index)
		}
		index.ColumnList = append(index.ColumnList, row.ColumnName)
	}
	return indexes, nil
}

func dropMigrationIndex(db *gorm.DB, model any, index gorm.Index) error {
	if scoped, ok := index.(*postgresMigrationIndex); ok {
		// Quote each literal identifier; GORM interprets dots in a schema
		// name as qualification rather than part of the identifier.
		quote := func(value string) string { return `"` + strings.ReplaceAll(value, `"`, `""`) + `"` }
		return db.Exec("DROP INDEX " + quote(scoped.schema) + "." + quote(scoped.Name())).Error
	}
	return db.Migrator().DropIndex(model, index.Name())
}

// GORM HasColumn/HasIndex discard metadata query errors and return false. In a
// value migration that is unsafe: false could stamp a skipped transformation,
// and a later successful lookup could delete the uncopied original value.
func migrationHasColumn(db *gorm.DB, table, name string) (bool, error) {
	columns, err := db.Migrator().ColumnTypes(table)
	if err != nil {
		return false, fmt.Errorf("inspect V4 migration column %s.%s: %w", table, name, err)
	}
	for _, column := range columns {
		if column.Name() == name {
			return true, nil
		}
	}
	return false, nil
}

// GORM's SQLite DropColumn rebuilds a table and discards independent operator
// indexes/triggers. Native ALTER keeps them and rejects dependencies on the
// retired column. Only curated application columns reach this helper.
func dropLegacyColumn(db *gorm.DB, model any, column string) error {
	if db.Dialector.Name() != "sqlite" {
		return db.Migrator().DropColumn(model, column)
	}
	statement := &gorm.Statement{DB: db}
	if err := statement.Parse(model); err != nil {
		return err
	}
	return db.Exec("ALTER TABLE " + statement.Quote(statement.Schema.Table) + " DROP COLUMN " + statement.Quote(column)).Error
}
