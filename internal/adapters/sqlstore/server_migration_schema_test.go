package sqlstore

import "testing"

func TestServerMigrationSchemaCheckIsReadOnly(t *testing.T) {
	db, err := openTestDB(t)
	if err != nil {
		t.Fatal(err)
	}
	if err := RequireCurrentV4Schema(db); err == nil {
		t.Fatal("empty database accepted for maintenance")
	}
	if db.Migrator().HasTable("schema_migrations") {
		t.Fatal("read-only maintenance initialized schema")
	}
	if err := EnsureSchema(db); err != nil {
		t.Fatal(err)
	}
	if err := RequireCurrentV4Schema(db); err != nil {
		t.Fatal(err)
	}
	if err := db.Delete(&schemaMigrationRow{}, "id = ?", v3ToV4BaselineMigrationID).Error; err != nil {
		t.Fatal(err)
	}
	if err := RequireCurrentV4Schema(db); err == nil {
		t.Fatal("uncompleted V4 accepted for maintenance")
	}
	var count int64
	if err := db.Model(&schemaMigrationRow{}).Where("id = ?", v3ToV4BaselineMigrationID).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("maintenance repaired marker: count=%d err=%v", count, err)
	}
}
