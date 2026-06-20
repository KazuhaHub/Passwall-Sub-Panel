// Package sqlstore provides the GORM-backed implementation of ports.Repos.
// It supports both MySQL and SQLite; SQLite keeps local setups zero-config.
package sqlstore

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	sqlitedriver "github.com/glebarez/sqlite"
	mysqldriver "gorm.io/driver/mysql"
	postgresdriver "gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// Open builds a GORM connection. kind picks the driver:
//   - "mysql":    dsn is a standard go-sql-driver MySQL DSN.
//   - "postgres": dsn is a pq/pgx connection string (URL or keyword form).
//   - "sqlite":   dsn is a filesystem path; parent dirs are created if missing.
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
	case "postgres":
		db, err = gorm.Open(postgresdriver.Open(dsn), cfg)
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
	// SQLite serializes writes, so a multi-connection pool just invites
	// "database is locked" when the traffic poll and an HTTP request write
	// concurrently. Cap it at a single connection — the database driver then
	// queues writers instead of failing them. MySQL/Postgres handle real
	// concurrency and get a proper pool.
	if kind == "sqlite" {
		sqlDB.SetMaxOpenConns(1)
		sqlDB.SetMaxIdleConns(1)
	} else {
		sqlDB.SetMaxIdleConns(10)
		sqlDB.SetMaxOpenConns(50)
		// Recycle connections so a fronting proxy (RDS Proxy, PgBouncer,
		// ProxySQL, a cloud LB) or the server's own wait_timeout closing an idle
		// socket doesn't leave database/sql handing a dead connection to the next
		// query — the background loops fire only every few minutes on quiet
		// panels, so idle windows easily exceed common server timeouts. Lifetime
		// below typical wait_timeout; idle cap reaps unused sockets sooner.
		sqlDB.SetConnMaxLifetime(30 * time.Minute)
		sqlDB.SetConnMaxIdleTime(5 * time.Minute)
	}
	return db, nil
}

// NewRepos wires up every repository implementation and returns the
// aggregated ports.Repos for the service layer. Repositories are stateless
// and safely share a single *gorm.DB.
func NewRepos(db *gorm.DB) ports.Repos {
	// Settings is wrapped in the in-process cache decorator (see below); the
	// ScopedSettings resolver layers each group's sparse overrides on top of it.
	cachedSettings := NewCachingSettingsRepo(newKVSettingsRepo(db))
	// One cached scope repo shared by BOTH the write side (ScopeSettings: admin
	// override CRUD, group-delete cleanup) and the read side (the ScopedSettings
	// resolver). Sharing the instance is what makes invalidate-on-write correct —
	// a SetOverride/DeleteScope clears the same cache the /sub resolver reads.
	scopeSettings := NewCachingScopeRepo(newKVScopeSettingsRepo(db))
	return ports.Repos{
		User:        &userRepo{db: db},
		Group:       &groupRepo{db: db},
		Node:        &nodeRepo{db: db},
		Separator:   &separatorRepo{db: db},
		Ownership:   &ownershipRepo{db: db},
		PSPClient:   &pspClientRepo{db: db},
		Traffic:     &trafficRepo{db: db},
		NodeTraffic: &nodeTrafficRepo{db: db},
		Audit:       &auditRepo{db: db},
		AuthEvent:   &authEventRepo{db: db},
		AuthToken:   &authTokenRepo{db: db},
		WebAuthn:    &webauthnCredentialRepo{db: db},
		SubLog:      &subLogRepo{db: db},
		SyncTask:    &syncTaskRepo{db: db},
		// RuleSet is intentionally absent here: production wires the
		// yamladapter.RuleSetRepo in app.go (rule sets live in
		// config/rulesets/*.yaml, not the DB). A previous MySQL repo
		// existed but was never actually injected, so it was dead code
		// and got removed during the v3 schema cleanup.
		XUIPanel: &xuiPanelRepo{db: db},
		// Settings is wrapped in the in-process cache decorator so the
		// hot paths (render, traffic poll, reconcile, paneltz) don't
		// fan into the DB for the same row dozens of times per request
		// / cycle. Save is invalidate-on-write so admin edits become
		// visible immediately (no TTL window). See settings_cache.go.
		Settings:       cachedSettings,
		ScopeSettings:  scopeSettings,
		ScopedSettings: NewScopedSettings(cachedSettings, scopeSettings),
		Mail:           &mailRepo{db: db},
		SAMLConfig:     &samlConfigRepo{db: db},
		OIDCConfig:     &oidcConfigRepo{db: db},

		Certificate:   &certificateRepo{db: db},
		DNSCredential: &dnsCredentialRepo{db: db},
		ACMEAccount:   &acmeAccountRepo{db: db},
		CertEvent:     &certEventRepo{db: db},
	}
}
