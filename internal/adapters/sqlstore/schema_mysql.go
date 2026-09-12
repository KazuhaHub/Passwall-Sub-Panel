package sqlstore

import (
	"fmt"

	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

func autoMigrateCurrentSchema(db *gorm.DB) error {
	if db.Dialector.Name() != "mysql" {
		return db.AutoMigrate(schemaModels...)
	}
	// Do not change the caller's dialect or normal query behavior. Only this
	// schema session suppresses MySQL's automatic unique-index deletion.
	migrationDB := db.Session(&gorm.Session{})
	migrationDB.Dialector = preservingMySQLSchemaDialect{db.Dialector}
	return migrationDB.AutoMigrate(schemaModels...)
}

type preservingMySQLSchemaDialect struct{ gorm.Dialector }

func (dialect preservingMySQLSchemaDialect) Migrator(db *gorm.DB) gorm.Migrator {
	return preservingMySQLSchemaMigrator{dialect.Dialector.Migrator(db)}
}

type preservingMySQLSchemaMigrator struct{ gorm.Migrator }

func (preservingMySQLSchemaMigrator) MigrateColumnUnique(_ any, field *schema.Field, _ gorm.ColumnType) error {
	// The MySQL driver calls any single-column unique index absent from this
	// field's tags redundant, and drops it before our bounded bridge sees it.
	// PSP uses named uniqueIndex tags, created by AutoMigrate's index phase.
	// Unique-index removal belongs only in explicit, definition-checked steps.
	if field.Unique {
		return fmt.Errorf("column UNIQUE migration for %s needs an explicit preserving migration; use uniqueIndex for PSP models", field.DBName)
	}
	return nil
}
