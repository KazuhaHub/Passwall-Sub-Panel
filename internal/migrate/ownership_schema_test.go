package migrate

import (
	"path/filepath"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/adapters/sqlstore"
)

// Regression: v3.9.0 retired the per-node ownership model and deliberately
// dropped ownershipRow from sqlstore.schemaModels, so EnsureSchema no longer
// creates `user_xui_clients`. That is correct for a running panel — a fresh
// install has no legacy clients, and an upgrade keeps its existing table
// because AutoMigrate leaves untracked tables alone.
//
// But the v2 -> v3 offline migrator calls EnsureSchema on a BLANK destination
// (runner.go) and then copyOwnerships writes straight into that table
// (migrate.go). On a fresh destination the table does not exist, so importing
// any v2 database that has legacy clients fails outright — the exact data the
// migration exists to carry across.
//
// Nothing caught it: the migrator's only other test covers settings, and the
// ownership repo's own test sidesteps the gap by calling CreateTable itself
// (ownership_repo_test.go, with a comment naming the v3.9.0 change). The
// schema-model removal was a code change; the breakage was a release-
// engineering one, and there was no test spanning the two.
//
// This asserts the property the migrator actually needs: after the schema step,
// the destination can accept an ownership row.
func TestMigratorDestinationAcceptsOwnershipRows(t *testing.T) {
	dst, err := sqlstore.Open("sqlite", filepath.Join(t.TempDir(), "dst.db"))
	if err != nil {
		t.Fatalf("open dst: %v", err)
	}
	defer closeGorm(t, dst)

	if err := sqlstore.EnsureSchema(dst); err != nil {
		t.Fatalf("EnsureSchema: %v", err)
	}
	if err := sqlstore.EnsureLegacyOwnershipTable(dst); err != nil {
		t.Fatalf("ensureOwnershipTable: %v", err)
	}

	// The shape copyOwnerships writes: a map insert against the raw table name.
	row := []map[string]any{{
		"user_id": int64(1), "panel_id": int64(10), "inbound_id": 100,
		"client_email": "u1-n1@psp.local", "client_uuid": "uuid-1",
	}}
	if err := dst.Table("user_xui_clients").Create(&row).Error; err != nil {
		t.Fatalf("the migrator's ownership insert must succeed after the schema step: %v", err)
	}
}

// Importing a v2 database with NO legacy clients must not leave an empty
// ownership table behind on the destination: that would make the install look
// un-migrated to DropIfMigrated's row-count probe forever, and a fresh v3
// install has no such table at all.
func TestMigratorLeavesNoOwnershipTableWhenSourceHasNone(t *testing.T) {
	dst, err := sqlstore.Open("sqlite", filepath.Join(t.TempDir(), "dst.db"))
	if err != nil {
		t.Fatalf("open dst: %v", err)
	}
	defer closeGorm(t, dst)
	if err := sqlstore.EnsureSchema(dst); err != nil {
		t.Fatalf("EnsureSchema: %v", err)
	}
	if dst.Migrator().HasTable("user_xui_clients") {
		t.Fatal("EnsureSchema must not create user_xui_clients — v3.9.0 retired it from schemaModels")
	}
}
