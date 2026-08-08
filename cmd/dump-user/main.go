// dump-user reads a user row directly from the SQLite DB and prints
// fields that often disagree between the API and the actual storage
// (Enabled, AutoDisabledReason, DisableDetail, UpdatedAt). Use it to
// verify whether SetEnabled actually persisted, or whether a background
// task reverted the change.
//
// Usage (from repo root, panel may be running):
//
//	go run ./cmd/dump-user/ -id 3
//	go run ./cmd/dump-user/ -upn admin
package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

func main() {
	dbPath := flag.String("db", "data/panel.db", "path to panel.db (SQLite)")
	id := flag.Int64("id", 0, "user ID")
	upn := flag.String("upn", "", "user UPN (alternative to -id)")
	flag.Parse()

	if *id == 0 && *upn == "" {
		fmt.Fprintln(os.Stderr, "either -id or -upn is required")
		os.Exit(1)
	}
	if _, err := os.Stat(*dbPath); err != nil {
		fmt.Fprintf(os.Stderr, "db not found: %s\n", *dbPath)
		os.Exit(1)
	}
	g, err := gorm.Open(sqlite.Open(*dbPath), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		fmt.Fprintf(os.Stderr, "open db: %v\n", err)
		os.Exit(1)
	}

	var row struct {
		ID                 int64
		UPN                string
		Enabled            bool
		AutoDisabledReason string `gorm:"column:auto_disabled_reason"`
		DisableDetail      string `gorm:"column:disable_detail"`
		UpdatedAt          time.Time
	}
	q := g.Raw(
		`SELECT id, upn, enabled, auto_disabled_reason, disable_detail, updated_at FROM users WHERE `+
			ifThenElse(*id != 0, "id = ?", "upn IN (?)"),
		ifThenElseAny(*id != 0, *id, []any{strings.TrimSpace(*upn), domain.NormalizeUPN(*upn)}),
	)
	if err := q.Scan(&row).Error; err != nil {
		fmt.Fprintf(os.Stderr, "query: %v\n", err)
		os.Exit(1)
	}
	if row.ID == 0 {
		fmt.Fprintln(os.Stderr, "no user matched")
		os.Exit(1)
	}
	fmt.Printf("id=%d  upn=%s  enabled=%v  auto_disabled_reason=%q  disable_detail=%q  updated_at=%s\n",
		row.ID, row.UPN, row.Enabled, row.AutoDisabledReason, row.DisableDetail, row.UpdatedAt.Format(time.RFC3339))
}

func ifThenElse(cond bool, a, b string) string {
	if cond {
		return a
	}
	return b
}
func ifThenElseAny(cond bool, a, b any) any {
	if cond {
		return a
	}
	return b
}
