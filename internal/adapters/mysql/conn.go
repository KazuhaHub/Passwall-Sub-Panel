// Package mysql provides the GORM-backed implementation of ports.Repos.
// It supports both MySQL and SQLite; SQLite keeps local setups zero-config.
package mysql

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

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
// Call EnsureSchema(db) separately to create the required tables.
func Open(kind, dsn string) (*gorm.DB, error) {
	if dsn == "" {
		return nil, fmt.Errorf("empty database dsn")
	}
	// GORM's default logger treats `ErrRecordNotFound` as a warning and
	// prints the full SQL to stderr. That's fine for general debugging but
	// half this codebase calls First() with the explicit expectation that
	// the row may be absent (settings on fresh boot, saml/oidc/mail
	// config never set, defaults seeded on the fly). Silence that one
	// specific case so the log isn't drowned in normal-path noise.
	gormLogger := logger.New(
		log.New(os.Stderr, "\r\n", log.LstdFlags),
		logger.Config{
			SlowThreshold:             200 * time.Millisecond,
			LogLevel:                  logger.Warn,
			IgnoreRecordNotFoundError: true,
			Colorful:                  false,
		},
	)
	cfg := &gorm.Config{Logger: gormLogger}

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
		User:       &userRepo{db: db},
		Group:      &groupRepo{db: db},
		Node:       &nodeRepo{db: db},
		Ownership:  &ownershipRepo{db: db},
		Traffic:    &trafficRepo{db: db},
		NodeTraffic: &nodeTrafficRepo{db: db},
		Audit:      &auditRepo{db: db},
		SubLog:     &subLogRepo{db: db},
		SyncTask:   &syncTaskRepo{db: db},
		RuleSet:    &ruleSetRepo{db: db},
		XUIPanel:   &xuiPanelRepo{db: db},
		Settings:   &settingsRepo{db: db},
		Mail:       &mailRepo{db: db},
		SAMLConfig: &samlConfigRepo{db: db},
		OIDCConfig: &oidcConfigRepo{db: db},
	}
}
