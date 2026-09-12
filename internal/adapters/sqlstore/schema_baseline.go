package sqlstore

import (
	"fmt"

	"gorm.io/gorm"
)

// These markers describe database semantics, not a previously installed binary
// version. beta20 and the final v3.9.2 share this baseline, but older binaries
// can have the same schema: there is no historical version column to recover.
const (
	v3LimitsBaselineMigrationID = "limits_tristate_v3.9.3"
	v4InitializingMigrationID   = "v4_schema_initializing_v1"
	v3ToV4BaselineMigrationID   = "v3_to_v4_baseline_v1"
)

// Only columns needed to recognize the supported schema/semantic floor belong
// here. AutoMigrate remains responsible for the complete current model. The
// endpoint and attachment representations are checked separately so a known
// interrupted V3 -> V4 migration can resume without guessing missing values.
var baselineColumns = map[string][]string{
	"schema_migrations": {"id", "applied_at"},
	"users": {
		"id", "upn", "uuid", "sub_token", "role", "group_id", "enabled",
		"service_disabled_reason", "traffic_limit_bytes", "ip_limit", "device_limit",
		"lifetime_up_bytes", "lifetime_down_bytes", "lifetime_total_bytes", "period_baseline_bytes",
	},
	"groups_":    {"id", "traffic_limit_bytes", "ip_limit", "device_limit"},
	"xui_panels": {"id", "url", "api_token", "password", "kind", "ip_limit_enforcement", "ip_limit_probed_at"},
	"nodes": {
		"id", "panel_id", "inbound_id", "display_name", "region", "enabled", "kind",
		"lifetime_up_bytes", "lifetime_down_bytes", "lifetime_total_bytes", "last_inbound_seeded",
		"inbound_settings", "stream_settings", "cert_source", "cert_id", "relays",
	},
	"psp_clients": {
		"id", "user_id", "panel_id", "email", "cred_class", "uuid", "password",
		"lifetime_up_bytes", "lifetime_down_bytes", "lifetime_total_bytes",
		"last_raw_up_bytes", "last_raw_down_bytes", "last_raw_total_bytes",
		"period_baseline_up_bytes", "period_baseline_down_bytes", "period_baseline_total_bytes",
	},
	"psp_client_inbounds": {"id", "client_id", "node_id", "flow_override"},
	"nodes_separator":     {"id", "mode", "node_ids", "enabled"},
}

func unsupportedSchemaBaseline(reason string) error {
	return fmt.Errorf("unsupported PSP database baseline: %s; first upgrade and successfully start v3.9.2 (or v3.9.2-beta.20), then back up the database and configuration before starting V4; no schema changes were performed", reason)
}

// inspectSchemaBaseline performs no writes, including no AutoMigrate. An old
// database must not be made to look current before its data semantics have been
// checked: notably, a deliberate unlimited 0 must never become inheritance.
// The bool is true only for a new, empty PSP database.
func inspectSchemaBaseline(db *gorm.DB) (bool, error) {
	tables, err := db.Migrator().GetTables()
	if err != nil {
		return false, fmt.Errorf("inspect PSP database tables: %w", err)
	}
	known, err := currentPSPTables(db)
	if err != nil {
		return false, err
	}
	present := make(map[string]bool, len(tables))
	pspTables := 0
	for _, table := range tables {
		present[table] = true
		if known[table] || table == "user_xui_clients" {
			pspTables++
		}
	}
	if pspTables == 0 {
		return true, nil // Unrelated operator tables are not PSP state.
	}
	if !present["schema_migrations"] {
		return false, unsupportedSchemaBaseline("missing schema_migrations")
	}
	columns := make(map[string]map[string]gorm.ColumnType)
	for table := range baselineColumns {
		if !present[table] {
			continue
		}
		fields, err := db.Migrator().ColumnTypes(table)
		if err != nil {
			return false, fmt.Errorf("inspect PSP database columns for %s: %w", table, err)
		}
		columns[table] = make(map[string]gorm.ColumnType, len(fields))
		for _, field := range fields {
			columns[table][field.Name()] = field
		}
	}
	for _, column := range baselineColumns["schema_migrations"] {
		if columns["schema_migrations"][column] == nil {
			return false, unsupportedSchemaBaseline("incomplete schema_migrations")
		}
	}
	var migrations []schemaMigrationRow
	if err := db.Find(&migrations).Error; err != nil {
		return false, fmt.Errorf("inspect PSP database migration markers: %w", err)
	}
	if pspTables == 1 && len(migrations) == 0 {
		// CREATE TABLE can commit before the initial marker INSERT on MySQL.
		// The sole, empty, structurally valid migration table is still an empty
		// PSP database; allow retrying this earliest initialization boundary.
		return true, nil
	}
	applied := make(map[string]bool, len(migrations))
	for _, migration := range migrations {
		applied[migration.ID] = true
	}
	if applied[v4InitializingMigrationID] && !applied[v3ToV4BaselineMigrationID] {
		// This marker is written only after the completely-empty check, before
		// the first V4 DDL. It permits retrying failed fresh initialization, not
		// importing an old or populated database under a new-schema marker.
		return false, inspectEmptyV4Initialization(db, present, known, columns)
	}
	if !applied[v3LimitsBaselineMigrationID] && !applied[v3ToV4BaselineMigrationID] {
		return false, unsupportedSchemaBaseline("V3 tri-state limits migration is not recorded")
	}
	for table, required := range baselineColumns {
		if !present[table] {
			return false, unsupportedSchemaBaseline("missing " + table)
		}
		for _, column := range required {
			if columns[table][column] == nil {
				return false, unsupportedSchemaBaseline("missing " + table + "." + column)
			}
		}
	}
	if err := inspectNullableLimits(columns); err != nil {
		return false, err
	}
	if err := inspectRetiredV3Separators(db, columns); err != nil {
		return false, err
	}
	// A completed data marker with missing target columns is not a resumable
	// DDL interruption. AutoMigrate would otherwise add blank target columns,
	// skip the marked value transformation and delete the only original value.
	for _, migration := range []struct {
		id      string
		table   string
		columns []string
	}{
		{nodeEndpointStateMigrationID, "nodes", []string{"desired_port", "observed_port", "desired_protocol", "observed_protocol"}},
		{pspClientInboundStateMigrationID, "psp_client_inbounds", []string{"state", "applied_version", "first_failed_at"}},
		{pspClientInboundAppliedCredentialsMigrationID, "psp_client_inbounds", []string{"applied_email", "applied_uuid", "applied_password"}},
	} {
		if !applied[migration.id] {
			continue
		}
		for _, column := range migration.columns {
			if columns[migration.table][column] == nil {
				return false, unsupportedSchemaBaseline("completed " + migration.id + " is missing " + migration.table + "." + column)
			}
		}
	}
	if applied[pspClientInboundAppliedCredentialsMigrationID] && !applied[pspClientInboundStateMigrationID] {
		return false, unsupportedSchemaBaseline("applied credential migration lacks its state migration")
	}
	for _, endpoint := range []string{"port", "protocol"} {
		legacy := columns["nodes"][endpoint] != nil
		if legacy && applied[v3ToV4BaselineMigrationID] {
			return false, unsupportedSchemaBaseline("completed V4 baseline still has nodes." + endpoint)
		}
		if !legacy && (!applied[nodeEndpointStateMigrationID] || columns["nodes"]["desired_"+endpoint] == nil || columns["nodes"]["observed_"+endpoint] == nil) {
			return false, unsupportedSchemaBaseline("nodes." + endpoint + " is missing without its completed V4 migration")
		}
	}
	if columns["psp_client_inbounds"]["provisioned"] != nil {
		if applied[v3ToV4BaselineMigrationID] {
			return false, unsupportedSchemaBaseline("completed V4 baseline still has provisioned")
		}
	} else {
		if !applied[pspClientInboundStateMigrationID] || !applied[pspClientInboundAppliedCredentialsMigrationID] {
			return false, unsupportedSchemaBaseline("provisioned is missing without its completed V4 migrations")
		}
		for _, column := range []string{"state", "applied_email", "applied_uuid", "applied_password", "first_failed_at"} {
			if columns["psp_client_inbounds"][column] == nil {
				return false, unsupportedSchemaBaseline("missing psp_client_inbounds." + column)
			}
		}
	}
	return false, nil
}

func currentPSPTables(db *gorm.DB) (map[string]bool, error) {
	tables := make(map[string]bool, len(schemaModels))
	for _, model := range schemaModels {
		statement := &gorm.Statement{DB: db}
		if err := statement.Parse(model); err != nil {
			return nil, fmt.Errorf("inspect current PSP model: %w", err)
		}
		tables[statement.Schema.Table] = true
	}
	return tables, nil
}

func inspectNullableLimits(columns map[string]map[string]gorm.ColumnType) error {
	for _, table := range []string{"users", "groups_"} {
		for _, name := range []string{"traffic_limit_bytes", "ip_limit", "device_limit"} {
			column := columns[table][name]
			if column == nil {
				continue // Fresh initialization may not have reached this DDL.
			}
			if nullable, known := column.Nullable(); !known || !nullable {
				return unsupportedSchemaBaseline(table + "." + name + " is not verifiably nullable")
			}
		}
	}
	return nil
}

func inspectRetiredV3Separators(db *gorm.DB, columns map[string]map[string]gorm.ColumnType) error {
	for _, name := range []string{"show_in_all_groups", "group_ids"} {
		if columns["nodes_separator"][name] != nil {
			return unsupportedSchemaBaseline("unfinished V3 separator migration: " + name)
		}
	}
	if columns["nodes"]["kind"] != nil {
		var legacy int64
		if err := db.Table("nodes").Where("kind = ?", "separator").Count(&legacy).Error; err != nil {
			return fmt.Errorf("inspect V3 separator rows: %w", err)
		}
		if legacy != 0 {
			return unsupportedSchemaBaseline("unfinished V3 separator rows")
		}
	}
	return nil
}

func inspectEmptyV4Initialization(db *gorm.DB, present, known map[string]bool, columns map[string]map[string]gorm.ColumnType) error {
	if present["user_xui_clients"] {
		return unsupportedSchemaBaseline("legacy ownership table in a fresh V4 initialization")
	}
	for table, names := range map[string][]string{
		"nodes":               {"port", "protocol"},
		"psp_client_inbounds": {"provisioned"},
		"nodes_separator":     {"show_in_all_groups", "group_ids"},
	} {
		for _, name := range names {
			if columns[table][name] != nil {
				return unsupportedSchemaBaseline("legacy " + table + "." + name + " in a fresh V4 initialization")
			}
		}
	}
	if err := inspectNullableLimits(columns); err != nil {
		return err
	}
	for table := range known {
		if !present[table] || table == "schema_migrations" {
			continue
		}
		var rows int64
		if err := db.Table(table).Count(&rows).Error; err != nil {
			return fmt.Errorf("inspect interrupted V4 initialization for %s: %w", table, err)
		}
		if rows != 0 {
			return unsupportedSchemaBaseline("populated " + table + " in an incomplete fresh V4 initialization")
		}
	}
	return nil
}

func beginEmptyV4Initialization(db *gorm.DB) error {
	if err := db.AutoMigrate(&schemaMigrationRow{}); err != nil {
		return err
	}
	return applyOnce(db, v4InitializingMigrationID, func(*gorm.DB) error { return nil })
}
