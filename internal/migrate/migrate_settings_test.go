package migrate

import (
	"context"
	"path/filepath"
	"testing"

	"gorm.io/gorm"

	"github.com/KazuhaHub/passwall-sub-panel/internal/adapters/sqlstore"
)

func closeGorm(t *testing.T, db *gorm.DB) {
	t.Helper()
	// Windows file-lock: TempDir RemoveAll fails unless the SQLite handle is
	// closed first (same pattern as the mysql package tests).
	if sqlDB, err := db.DB(); err == nil && sqlDB != nil {
		_ = sqlDB.Close()
	}
}

// Regression (M9): copySettingsKV used to write the stale key
// `traffic_snapshot_retention_days`, which the v3 settings layer no longer
// reads (it was renamed to `traffic_history_days`) — so the migrated value was
// silently stranded on a dead key. This is the drift the maintainer wants
// caught by a test, not memory: every key the migrator writes MUST be one the
// live settingDescriptors() set recognizes. The test runs the real copy path
// against an actual SQLite legacy DB → v3 schema DB, so it also gives the
// previously-zero-coverage migrator its first end-to-end assertion.
func TestCopySettingsKV_OnlyWritesKnownKeys(t *testing.T) {
	ctx := context.Background()

	src, err := openDB("sqlite", filepath.Join(t.TempDir(), "src.db"))
	if err != nil {
		t.Fatalf("open src: %v", err)
	}
	defer closeGorm(t, src)
	if err := src.AutoMigrate(&legacyUISettingsRow{}, &legacyMailSettingsRow{}); err != nil {
		t.Fatalf("migrate legacy schema: %v", err)
	}
	if err := src.Create(&legacyUISettingsRow{
		ID:                    1,
		SiteTitle:             "My Panel",
		LoginMode:             "dual",
		EmailDomain:           "kazuha.org",
		SyncTaskRetentionDays: 30,
	}).Error; err != nil {
		t.Fatalf("seed ui_settings: %v", err)
	}
	if err := src.Create(&legacyMailSettingsRow{
		ID:                   1,
		ExpireBeforeDays:     7,
		TrafficRemainPercent: 10,
	}).Error; err != nil {
		t.Fatalf("seed mail_settings: %v", err)
	}

	dst, err := sqlstore.Open("sqlite", filepath.Join(t.TempDir(), "dst.db"))
	if err != nil {
		t.Fatalf("open dst: %v", err)
	}
	defer closeGorm(t, dst)
	if err := sqlstore.EnsureSchema(dst); err != nil {
		t.Fatalf("dst schema: %v", err)
	}

	if err := copySettingsKV(ctx, src, dst); err != nil {
		t.Fatalf("copySettingsKV: %v", err)
	}

	var rows []struct {
		Type  string
		Name  string
		Value string
	}
	if err := dst.Table("settings").Select("type, name, value").Find(&rows).Error; err != nil {
		t.Fatalf("read settings: %v", err)
	}
	if len(rows) == 0 {
		t.Fatal("copySettingsKV wrote nothing")
	}

	known := sqlstore.KnownSettingNames()
	got := make(map[string]string, len(rows))
	for _, r := range rows {
		if !known[r.Name] {
			t.Errorf("copySettingsKV wrote key %q which the live settingDescriptors() no longer reads — the migrated value is stranded on a dead key", r.Name)
		}
		got[r.Name] = r.Value
	}

	if _, dead := got["traffic_snapshot_retention_days"]; dead {
		t.Error("stale key traffic_snapshot_retention_days must no longer be written")
	}
	if got["traffic_history_days"] != "180" {
		t.Errorf("traffic_history_days = %q, want 180 (seeded default under the live key)", got["traffic_history_days"])
	}
	if got["site_title"] != "My Panel" {
		t.Errorf("site_title = %q, want My Panel (legacy value must carry over)", got["site_title"])
	}
	if got["expire_before_days"] != "7" {
		t.Errorf("expire_before_days = %q, want 7 (from legacy mail_settings)", got["expire_before_days"])
	}
}
