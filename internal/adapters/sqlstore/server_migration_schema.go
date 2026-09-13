package sqlstore

import (
	"fmt"

	"gorm.io/gorm"
)

// RequireCurrentV4Schema is deliberately read-only. Maintenance is not a
// substitute for successfully booting V4 and must never initialize or upgrade
// a database as a side effect of a dry run.
func RequireCurrentV4Schema(db *gorm.DB) error {
	empty, err := inspectSchemaBaseline(db)
	if err != nil {
		return err
	}
	if empty {
		return fmt.Errorf("migrate-server requires an existing, successfully initialized V4 database")
	}
	var count int64
	if err := db.Model(&schemaMigrationRow{}).Where("id = ?", v3ToV4BaselineMigrationID).Count(&count).Error; err != nil {
		return err
	}
	if count != 1 {
		return fmt.Errorf("migrate-server requires a completed V4 upgrade; start V4 normally first")
	}
	for table, columns := range map[string][]string{
		"node_agents":         {"agent_id", "panel_id", "epoch", "credential_sha256", "credential_ciphertext", "desired_core_engine", "desired_core_version", "allow_restricted_reality"},
		"node_agent_streams":  {"agent_id", "stream", "desired_version", "desired_etag", "desired_body", "applied_epoch"},
		"psp_clients":         {"desired_enable", "desired_expiry_time", "desired_minted"},
		"psp_client_inbounds": {"state", "applied_email", "applied_uuid", "applied_password"},
		"sync_tasks":          {"id", "type", "target_id", "payload", "status"},
	} {
		if !db.Migrator().HasTable(table) {
			return fmt.Errorf("migrate-server requires current V4 schema: missing %s", table)
		}
		for _, column := range columns {
			if !db.Migrator().HasColumn(table, column) {
				return fmt.Errorf("migrate-server requires current V4 schema: missing %s.%s", table, column)
			}
		}
	}
	return nil
}
