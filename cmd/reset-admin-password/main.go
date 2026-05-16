// reset-admin-password rewrites a local-account password hash directly in
// the DB. Use it when the bootstrap admin password got lost — there is no
// reset-from-UI flow for the admin account itself.
//
// Stop the panel before running on SQLite (write locking will otherwise
// fight you). MySQL is fine to run live but you probably want the panel
// down for that minute anyway so a logged-in admin session doesn't act
// stale.
//
// Usage (from repo root):
//
//	# SQLite (default):
//	go run ./cmd/reset-admin-password/
//	go run ./cmd/reset-admin-password/ -upn alice -password s3cret
//
//	# MySQL:
//	go run ./cmd/reset-admin-password/ -driver mysql \
//	    -dsn 'user:pass@tcp(host:3306)/passwall?charset=utf8mb4&parseTime=true'
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/glebarez/sqlite"
	"golang.org/x/crypto/bcrypt"
	mysqldriver "gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func main() {
	driver := flag.String("driver", "sqlite", "db driver: sqlite | mysql")
	dbPath := flag.String("db", "data/panel.db", "SQLite db path (when -driver=sqlite); ignored for mysql")
	dsn := flag.String("dsn", "", "MySQL DSN (required when -driver=mysql), e.g. user:pass@tcp(host:3306)/db?charset=utf8mb4&parseTime=true")
	upn := flag.String("upn", "admin", "user UPN to reset / verify")
	password := flag.String("password", "admin", "new password (or password to verify in -verify mode)")
	verify := flag.Bool("verify", false, "do not modify; just compare password against the stored hash")
	flag.Parse()

	g, err := openDB(*driver, *dbPath, *dsn)
	if err != nil {
		fmt.Fprintf(os.Stderr, "open db: %v\n", err)
		os.Exit(1)
	}

	if *verify {
		var row struct {
			UPN          string
			PasswordHash string `gorm:"column:password_hash"`
			Enabled      bool
		}
		if err := g.Raw(`SELECT upn, password_hash, enabled FROM users WHERE upn = ?`, *upn).Scan(&row).Error; err != nil {
			fmt.Fprintf(os.Stderr, "select: %v\n", err)
			os.Exit(1)
		}
		if row.UPN == "" {
			fmt.Fprintf(os.Stderr, "no user found with upn=%s\n", *upn)
			os.Exit(1)
		}
		hashPrefix := row.PasswordHash
		if len(hashPrefix) > 12 {
			hashPrefix = hashPrefix[:12] + "..."
		}
		fmt.Printf("user found: upn=%s, enabled=%v, hash=%s (len=%d)\n",
			row.UPN, row.Enabled, hashPrefix, len(row.PasswordHash))
		if err := bcrypt.CompareHashAndPassword([]byte(row.PasswordHash), []byte(*password)); err != nil {
			fmt.Fprintf(os.Stderr, "VERIFY FAILED: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("VERIFY OK — password=%s matches stored hash\n", *password)
		return
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(*password), bcrypt.DefaultCost)
	if err != nil {
		fmt.Fprintf(os.Stderr, "hash: %v\n", err)
		os.Exit(1)
	}

	res := g.Exec(`UPDATE users SET password_hash = ? WHERE upn = ?`, string(hash), *upn)
	if res.Error != nil {
		fmt.Fprintf(os.Stderr, "update: %v\n", res.Error)
		os.Exit(1)
	}
	if res.RowsAffected == 0 {
		fmt.Fprintf(os.Stderr, "no user found with upn=%s\n", *upn)
		os.Exit(1)
	}
	hashPrefix := string(hash)[:12] + "..."
	fmt.Printf("password reset OK — upn=%s, rows=%d, password=%s, hash=%s\n", *upn, res.RowsAffected, *password, hashPrefix)
}

func openDB(driver, path, dsn string) (*gorm.DB, error) {
	cfg := &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)}
	switch driver {
	case "sqlite":
		if _, err := os.Stat(path); err != nil {
			return nil, fmt.Errorf("sqlite db not found: %s", path)
		}
		return gorm.Open(sqlite.Open(path), cfg)
	case "mysql":
		if dsn == "" {
			return nil, fmt.Errorf("-dsn is required when -driver=mysql")
		}
		return gorm.Open(mysqldriver.Open(dsn), cfg)
	default:
		return nil, fmt.Errorf("unknown -driver %q (expected sqlite or mysql)", driver)
	}
}
