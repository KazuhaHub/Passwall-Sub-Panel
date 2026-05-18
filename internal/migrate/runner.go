// Package migrate implements the one-shot side-by-side database migration
// from the legacy (≤ v2.5.x) schema to v3.0.0. It is wired into the main
// panel binary as the `migrate` subcommand (`psp migrate ...`) so admins
// don't need to ship or build a second binary.
//
// Runtime safety: the panel's normal startup path never imports this
// package's logic — Run() is only called when os.Args[1] == "migrate" in
// cmd/panel/main.go, so a normal `psp` invocation pays zero cost.
//
// Lifecycle: this code lives in the binary forever. Unlike a separate
// migration cmd that can be deleted after a one-shot upgrade, embedding
// here is the trade-off for a single-binary release. Code size is small
// (~600 LOC) and the surface area is fully encapsulated.
//
// Usage:
//
//	# SQLite-to-SQLite:
//	psp migrate --driver=sqlite \
//	    --src=data/panel.db --dst=data/panel_v3.db
//
//	# MySQL-to-MySQL:
//	psp migrate --driver=mysql \
//	    --src='user:pw@tcp(host:3306)/panel?charset=utf8mb4&parseTime=true' \
//	    --dst='user:pw@tcp(host:3306)/panel_v3?charset=utf8mb4&parseTime=true'
//
//	# Dry-run mode (count source rows, do not write):
//	psp migrate --driver=mysql --src=... --dst=... --dry-run
//
// On success the program prints next-steps (edit config.yaml's `database`
// field to point at the new DB, restart the panel).
//
// Re-running protection: the migration seeds a sentinel row in
// `settings` before any other write, so a partial-failed run blocks
// re-runs at guardDstEmpty until the operator drops and recreates the
// destination DB. Source DB is read-only throughout.
package migrate

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	sqlitedriver "github.com/glebarez/sqlite"
	mysqldriver "gorm.io/driver/mysql"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"github.com/KazuhaHub/passwall-sub-panel/internal/adapters/mysql"
)

// Run executes the migrate subcommand. args is os.Args after the
// "migrate" subcommand name (i.e. os.Args[2:]). Returns the process exit
// code — caller is expected to os.Exit(Run(...)).
func Run(args []string) int {
	fs := flag.NewFlagSet("migrate", flag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: psp migrate --driver=sqlite|mysql --src=<SRC> --dst=<DST> [--dry-run]\n\n")
		fmt.Fprintf(os.Stderr, "One-shot migration from the legacy schema (≤ v2.5.x) to v3.0.0.\n")
		fmt.Fprintf(os.Stderr, "Source DB is opened read-only; destination must be an empty pre-created DB.\n\n")
		fs.PrintDefaults()
	}
	driver := fs.String("driver", "sqlite", "db driver: sqlite | mysql (both src and dst share the driver)")
	src := fs.String("src", "", "source DB: SQLite path or MySQL DSN")
	dst := fs.String("dst", "", "destination DB: SQLite path or MySQL DSN (must already exist for mysql)")
	dryRun := fs.Bool("dry-run", false, "open both DBs, report what would be copied, but do not write to dst")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	// Note: no --secret flag. AES-GCM-encrypted columns (mail_settings.smtp_password,
	// saml_settings.sp_key_pem, oidc_settings.client_secret, xui_panels.api_token /
	// password) carry their ciphertext as opaque strings through this migration
	// — we never decrypt or re-encrypt. The v3.0.0 panel decrypts at Load
	// time using config.yaml's SecretKeyMaterial, which must match what the
	// legacy panel used.
	// If it doesn't, the panel surfaces a clear "decrypt secret" error on boot
	// and the operator fixes config.yaml without touching this cmd.

	if *src == "" || *dst == "" {
		fmt.Fprintln(os.Stderr, "ERROR: --src and --dst are both required")
		fs.Usage()
		return 2
	}

	srcDB, err := openDB(*driver, *src)
	if err != nil {
		log.Printf("open src: %v", err)
		return 1
	}
	dstDB, err := openDB(*driver, *dst)
	if err != nil {
		log.Printf("open dst: %v", err)
		return 1
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	if err := guardDstEmpty(ctx, dstDB); err != nil {
		log.Printf("destination not safe to migrate into: %v", err)
		return 1
	}

	if !*dryRun {
		if err := mysql.EnsureSchema(dstDB); err != nil {
			log.Printf("create v3 schema on dst: %v", err)
			return 1
		}
	}

	plan, err := buildMigrationPlan(ctx, srcDB)
	if err != nil {
		log.Printf("scan src: %v", err)
		return 1
	}
	plan.print()

	if *dryRun {
		fmt.Println("\nDRY RUN — no rows written. Re-run without --dry-run to apply.")
		return 0
	}

	if err := runMigration(ctx, srcDB, dstDB, plan); err != nil {
		log.Printf("migration failed: %v", err)
		return 1
	}

	fmt.Println()
	fmt.Println("Migration complete.")
	fmt.Println()
	fmt.Println("Next steps:")
	fmt.Println("  1. Update config.yaml's `database` field to point at the dst DB.")
	fmt.Println("  2. Restart the panel and verify everything works.")
	fmt.Println("  3. Keep the old DB around for a week as backup, then drop it.")
	return 0
}

func openDB(driver, dsn string) (*gorm.DB, error) {
	cfg := &gorm.Config{
		Logger: gormlogger.New(log.New(os.Stderr, "[gorm] ", log.LstdFlags), gormlogger.Config{
			SlowThreshold:             5 * time.Second,
			LogLevel:                  gormlogger.Warn,
			IgnoreRecordNotFoundError: true,
		}),
	}
	switch driver {
	case "sqlite":
		if dir := filepath.Dir(dsn); dir != "" && dir != "." {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return nil, fmt.Errorf("mkdir for sqlite: %w", err)
			}
		}
		return gorm.Open(sqlitedriver.Open(dsn), cfg)
	case "mysql":
		return gorm.Open(mysqldriver.Open(dsn), cfg)
	default:
		return nil, fmt.Errorf("unknown driver %q", driver)
	}
}

// guardDstEmpty refuses to run if the destination already has any settings
// rows. The migration seeds a sentinel row (`type='_migration'`) BEFORE any
// other dst writes, so a freshly-created DB is the only state with empty
// settings — any other state (sentinel present, partial copy, full success,
// or hand-edited) means re-running would either double-insert or stomp on
// admin's work. Operator's recovery path is "DROP DATABASE; CREATE; re-run",
// matching the side-by-side design where the source DB is untouched.
func guardDstEmpty(ctx context.Context, dst *gorm.DB) error {
	// Settings table may not exist yet on a brand-new DB — that's fine.
	if !dst.Migrator().HasTable("settings") {
		return nil
	}
	var count int64
	if err := dst.WithContext(ctx).Table("settings").Count(&count).Error; err != nil {
		return fmt.Errorf("count settings on dst: %w", err)
	}
	if count > 0 {
		return fmt.Errorf("dst has %d rows in `settings` already — drop the destination DB and re-run", count)
	}
	return nil
}
