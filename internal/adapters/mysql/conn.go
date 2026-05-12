// Package mysql provides the GORM-backed implementation of ports.Repos.
// The name is historical — the package also supports SQLite (pure Go, via
// glebarez/sqlite) so the panel runs zero-config out of the box and only
// needs a real MySQL when the operator opts in.
package mysql

import (
	"fmt"
	"os"
	"path/filepath"

	sqlitedriver "github.com/glebarez/sqlite"
	mysqldriver "gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// Open builds a GORM connection. kind picks the driver:
//   - "mysql":  dsn is a standard go-sql-driver MySQL DSN.
//   - "sqlite": dsn is a filesystem path; parent dirs are created if missing.
//
// Call Migrate(db) separately to run schema migrations.
func Open(kind, dsn string) (*gorm.DB, error) {
	if dsn == "" {
		return nil, fmt.Errorf("empty database dsn")
	}
	cfg := &gorm.Config{Logger: logger.Default.LogMode(logger.Warn)}

	var db *gorm.DB
	var err error
	switch kind {
	case "sqlite":
		if dir := filepath.Dir(dsn); dir != "" && dir != "." {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return nil, fmt.Errorf("sqlite parent dir: %w", err)
			}
		}
		db, err = gorm.Open(sqlitedriver.Open(dsn), cfg)
	case "mysql":
		db, err = gorm.Open(mysqldriver.Open(dsn), cfg)
	default:
		return nil, fmt.Errorf("unknown db kind: %q", kind)
	}
	if err != nil {
		return nil, fmt.Errorf("gorm open (%s): %w", kind, err)
	}

	sqlDB, err := db.DB()
	if err != nil {
		return nil, err
	}
	// SQLite is happy with much smaller pools; MySQL tolerates these defaults.
	sqlDB.SetMaxIdleConns(10)
	sqlDB.SetMaxOpenConns(50)
	return db, nil
}

// NewRepos wires up every repository implementation and returns the
// aggregated ports.Repos for the service layer. Repositories are stateless
// and safely share a single *gorm.DB.
func NewRepos(db *gorm.DB) ports.Repos {
	return ports.Repos{
		User:      &userRepo{db: db},
		Group:     &groupRepo{db: db},
		Node:      &nodeRepo{db: db},
		Ownership: &ownershipRepo{db: db},
		Traffic:   &trafficRepo{db: db},
		Audit:     &auditRepo{db: db},
		SubLog:    &subLogRepo{db: db},
	}
}
