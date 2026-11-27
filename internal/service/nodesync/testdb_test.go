package nodesync

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/adapters/sqlstore"
	"gorm.io/gorm"
)

// These service tests start from a freshly initialized schema. Build it once
// and copy the closed database so every test owns its rows and connection.
var migratedSQLite = sync.OnceValues(func() ([]byte, error) {
	dir, err := os.MkdirTemp("", "nodesync-template-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "schema.db")
	db, err := sqlstore.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	conn, err := db.DB()
	if err != nil {
		return nil, err
	}
	if err := errors.Join(sqlstore.EnsureSchema(db), conn.Close()); err != nil {
		return nil, err
	}
	return os.ReadFile(path)
})

func openMigratedTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	template, err := migratedSQLite()
	if err != nil {
		t.Fatalf("initialize SQLite test schema: %v", err)
	}
	path := filepath.Join(t.TempDir(), "panel.db")
	if err := os.WriteFile(path, template, 0o600); err != nil {
		t.Fatal(err)
	}
	db, err := sqlstore.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := conn.Close(); err != nil {
			t.Errorf("close SQLite fixture: %v", err)
		}
	})
	return db
}
