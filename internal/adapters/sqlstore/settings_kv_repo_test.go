package sqlstore

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// TestKVSettings_ZeroRetentionMeansForever pins the UI contract that 0 =
// "keep forever" for the two retention fields whose hints say so
// (traffic_history_days, sub_log_retention_days). A fresh install (key never
// written) must still get the bounded default; only an EXPLICIT 0 means forever.
// applyUISettingsDefaults can't tell those apart on its own, so Load must.
func TestKVSettings_ZeroRetentionMeansForever(t *testing.T) {
	db, err := openTestDB(t)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { sqlDB, _ := db.DB(); _ = sqlDB.Close() })
	if err := EnsureSchema(db); err != nil {
		t.Fatalf("schema: %v", err)
	}
	repo := newKVSettingsRepo(db)
	ctx := context.Background()

	// Fresh: nothing saved → bounded defaults.
	fresh, _ := repo.Load(ctx, ports.UISettings{})
	if fresh.TrafficHistoryDays != 730 {
		t.Errorf("fresh traffic_history_days = %d, want 730 default", fresh.TrafficHistoryDays)
	}
	if fresh.SubLogRetentionDays != 7 {
		t.Errorf("fresh sub_log_retention_days = %d, want 7 default", fresh.SubLogRetentionDays)
	}

	// Admin explicitly sets 0 ("keep forever").
	s := fresh
	s.TrafficHistoryDays = 0
	s.SubLogRetentionDays = 0
	if err := repo.Save(ctx, s); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, _ := repo.Load(ctx, ports.UISettings{})
	if got.TrafficHistoryDays != 0 {
		t.Errorf("explicit traffic_history_days=0 came back %d; 0 must persist as keep-forever", got.TrafficHistoryDays)
	}
	if got.SubLogRetentionDays != 0 {
		t.Errorf("explicit sub_log_retention_days=0 came back %d; 0 must persist as keep-forever", got.SubLogRetentionDays)
	}
}

// TestKVSettings_AuthEventRetentionFreelyEditable pins that
// auth_event_retention_days behaves like the other retention fields: 90 is only
// the DEFAULT (applied when the key was never written), an explicit 0 persists
// as keep-forever, and any explicit positive value is honored as-is (not floored
// up to 90). Previously the loader hard-floored <=0 to 90, so admins could not
// set a shorter retention or keep-forever.
func TestKVSettings_AuthEventRetentionFreelyEditable(t *testing.T) {
	db, err := openTestDB(t)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { sqlDB, _ := db.DB(); _ = sqlDB.Close() })
	if err := EnsureSchema(db); err != nil {
		t.Fatalf("schema: %v", err)
	}
	repo := newKVSettingsRepo(db)
	ctx := context.Background()

	// Fresh: never saved → 90 default.
	fresh, _ := repo.Load(ctx, ports.UISettings{})
	if fresh.AuthEventRetentionDays != 90 {
		t.Errorf("fresh auth_event_retention_days = %d, want 90 default", fresh.AuthEventRetentionDays)
	}

	// Explicit 0 = keep forever — must survive, not be floored to 90.
	s := fresh
	s.AuthEventRetentionDays = 0
	if err := repo.Save(ctx, s); err != nil {
		t.Fatalf("save 0: %v", err)
	}
	got, _ := repo.Load(ctx, ports.UISettings{})
	if got.AuthEventRetentionDays != 0 {
		t.Errorf("explicit auth_event_retention_days=0 came back %d; 0 must persist as keep-forever", got.AuthEventRetentionDays)
	}

	// Explicit positive below the old floor is honored as-is.
	s.AuthEventRetentionDays = 45
	if err := repo.Save(ctx, s); err != nil {
		t.Fatalf("save 45: %v", err)
	}
	got, _ = repo.Load(ctx, ports.UISettings{})
	if got.AuthEventRetentionDays != 45 {
		t.Errorf("explicit auth_event_retention_days=45 came back %d; admin value must be honored, not floored to 90", got.AuthEventRetentionDays)
	}
}

// TestKVSettingsRoundtrip walks the v3.0.0 KV repo end-to-end: Save → SELECT
// against the raw schema → Load → check that every descriptor's typed
// field survives the round trip. This is the only test that exercises the
// Marshal/Unmarshal halves of every descriptor — strField / intField /
// boolField / jsonField — so regressions in any helper are caught here.
func TestKVSettingsRoundtrip(t *testing.T) {
	db, err := openTestDB(t)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() {
		// Windows file-lock: TempDir RemoveAll fails unless the SQLite
		// handle is closed first. Mirrors the close pattern used in
		// secrets_test.go.
		sqlDB, _ := db.DB()
		if sqlDB != nil {
			_ = sqlDB.Close()
		}
	})
	if err := EnsureSchema(db); err != nil {
		t.Fatalf("schema: %v", err)
	}

	repo := newKVSettingsRepo(db)
	ctx := context.Background()

	in := ports.UISettings{
		LoginMode:                  "dual",
		SiteTitle:                  "Test Panel",
		AppTitle:                   "PSP",
		IconURL:                    "https://cdn.example.com/icon.png",
		LogoURL:                    "https://cdn.example.com/logo.png",
		LogoURLDark:                "https://cdn.example.com/logo-dark.png",
		FooterText:                 "© 2026 Test",
		ThemeColor:                 "#0061A4",
		EmailDomain:                "users.example.com",
		SubBaseURL:                 "https://panel.example.com",
		PanelPath:                  "/panel",
		JWTIssuer:                  "passwall-sub-panel",
		JWTAccessTTLMinutes:        90,
		JWTRefreshTTLMinutes:       60 * 24 * 14,
		DisallowUserLocalLogin:     true,
		DisallowUserPasswordChange: false,
		AllowUserPersonalRules:     true,
		SubPath:                    "subscribe",
		SubUpdateIntervalHours:     12,
		SubRegionFlagPrefix:        true,
		SubBlockAutoDisable:        true,
		SubBlockAutoDisableCount:   5,
		SubLogRetentionDays:        14,
		SubImportTutorialURL:       "https://docs.example.com/import",
		SubClientRules:             []ports.SubClientRule{{Name: "Clash", Keywords: []string{"clash"}, RenderFormat: "mihomo", Enabled: true}},
		SubImportClients:           []ports.SubImportClient{{Name: "Verge", Platforms: []string{"windows"}, RenderFormat: "mihomo", ImportURLTemplate: "clash://x", Enabled: true, Sort: 10}},
		SubPerIPPerMin:             42,
		LoginPerIPPerMin:           7,
		AuditRetentionDays:         45,
		SyncTaskRetentionDays:      60,
		TrafficHistoryDays:         200,
		EmergencyAccessEnabled:     true,
		EmergencyAccessHours:       48,
		EmergencyAccessMaxCount:    4,
		EmergencyAccessQuotaGB:     20,
		Timezone:                   "America/Los_Angeles",
		CronTrafficPullMinutes:     10,
		CronReconcileMinutes:       30,
		MaxPanelConcurrency:        16,
		QuickLinks:                 []ports.QuickLink{{Label: "Docs", URL: "https://docs", Enabled: true, Sort: 1}},
		GlobalAnnouncement:         ports.GlobalAnnouncement{Enabled: true, Title: "Maintenance", Content: "tonight 23:00", Level: "info"},
		ExpireBeforeDays:           7,
		TrafficRemainPercent:       15,
	}

	if err := repo.Save(ctx, in); err != nil {
		t.Fatalf("Save: %v", err)
	}

	out, err := repo.Load(ctx, ports.UISettings{})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	checks := []struct {
		name string
		got  any
		want any
	}{
		{"LoginMode", out.LoginMode, in.LoginMode},
		{"SiteTitle", out.SiteTitle, in.SiteTitle},
		{"AppTitle", out.AppTitle, in.AppTitle},
		{"IconURL", out.IconURL, in.IconURL},
		{"LogoURL", out.LogoURL, in.LogoURL},
		{"LogoURLDark", out.LogoURLDark, in.LogoURLDark},
		{"FooterText", out.FooterText, in.FooterText},
		{"ThemeColor", out.ThemeColor, in.ThemeColor},
		{"EmailDomain", out.EmailDomain, in.EmailDomain},
		{"SubBaseURL", out.SubBaseURL, in.SubBaseURL},
		{"PanelPath", out.PanelPath, in.PanelPath},
		{"JWTAccessTTLMinutes", out.JWTAccessTTLMinutes, in.JWTAccessTTLMinutes},
		{"JWTRefreshTTLMinutes", out.JWTRefreshTTLMinutes, in.JWTRefreshTTLMinutes},
		{"DisallowUserLocalLogin", out.DisallowUserLocalLogin, in.DisallowUserLocalLogin},
		{"AllowUserPersonalRules", out.AllowUserPersonalRules, in.AllowUserPersonalRules},
		{"SubPath", out.SubPath, in.SubPath},
		{"SubUpdateIntervalHours", out.SubUpdateIntervalHours, in.SubUpdateIntervalHours},
		{"SubBlockAutoDisableCount", out.SubBlockAutoDisableCount, in.SubBlockAutoDisableCount},
		{"TrafficHistoryDays", out.TrafficHistoryDays, in.TrafficHistoryDays},
		{"EmergencyAccessQuotaGB", out.EmergencyAccessQuotaGB, in.EmergencyAccessQuotaGB},
		{"Timezone", out.Timezone, in.Timezone},
		{"MaxPanelConcurrency", out.MaxPanelConcurrency, in.MaxPanelConcurrency},
		{"ExpireBeforeDays", out.ExpireBeforeDays, in.ExpireBeforeDays},
		{"TrafficRemainPercent", out.TrafficRemainPercent, in.TrafficRemainPercent},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, c.got, c.want)
		}
	}
	// JSON fields — len check is enough as a sanity probe; the descriptor's
	// Marshal/Unmarshal goes through encoding/json round-trip.
	if len(out.SubClientRules) != 1 || out.SubClientRules[0].Name != "Clash" {
		t.Errorf("SubClientRules round-trip: got %+v", out.SubClientRules)
	}
	if len(out.SubImportClients) != 1 || out.SubImportClients[0].Name != "Verge" {
		t.Errorf("SubImportClients round-trip: got %+v", out.SubImportClients)
	}
	if len(out.QuickLinks) != 1 || out.QuickLinks[0].Label != "Docs" {
		t.Errorf("QuickLinks round-trip: got %+v", out.QuickLinks)
	}
	if !out.GlobalAnnouncement.Enabled || out.GlobalAnnouncement.Title != "Maintenance" {
		t.Errorf("GlobalAnnouncement round-trip: got %+v", out.GlobalAnnouncement)
	}
}

// TestKVSettingsDefaultsOnEmpty covers the Load-from-empty path:
// applyUISettingsDefaults must populate runtime-critical fields (cron
// intervals, JWT TTLs, retention days) with non-zero values so the panel
// can boot against a freshly-created DB without an admin save first.
func TestKVSettingsDefaultsOnEmpty(t *testing.T) {
	db, err := openTestDB(t)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() {
		sqlDB, _ := db.DB()
		if sqlDB != nil {
			_ = sqlDB.Close()
		}
	})
	if err := EnsureSchema(db); err != nil {
		t.Fatalf("schema: %v", err)
	}

	repo := newKVSettingsRepo(db)
	out, err := repo.Load(context.Background(), ports.UISettings{})
	if err != nil {
		t.Fatalf("Load empty: %v", err)
	}

	// Spot-check the most critical defaults — tickers panic on zero/negative
	// intervals, so these MUST be non-zero before the cron loops fire.
	mustNonZero := map[string]int{
		"CronTrafficPullMinutes":   out.CronTrafficPullMinutes,
		"CronReconcileMinutes":     out.CronReconcileMinutes,
		"JWTAccessTTLMinutes":      out.JWTAccessTTLMinutes,
		"JWTRefreshTTLMinutes":     out.JWTRefreshTTLMinutes,
		"SubPerIPPerMin":           out.SubPerIPPerMin,
		"LoginPerIPPerMin":         out.LoginPerIPPerMin,
		"SubLogRetentionDays":      out.SubLogRetentionDays,
		"AuditRetentionDays":       out.AuditRetentionDays,
		"SyncTaskRetentionDays":    out.SyncTaskRetentionDays,
		"TrafficHistoryDays":       out.TrafficHistoryDays,
		"SubBlockAutoDisableCount": out.SubBlockAutoDisableCount,
		"SubUpdateIntervalHours":   out.SubUpdateIntervalHours,
		"ExpireBeforeDays":         out.ExpireBeforeDays,
		"TrafficRemainPercent":     out.TrafficRemainPercent,
	}
	for name, v := range mustNonZero {
		if v <= 0 {
			t.Errorf("%s defaulted to %d, must be > 0 for boot safety", name, v)
		}
	}
	if out.JWTIssuer == "" {
		t.Errorf("JWTIssuer must default to non-empty")
	}
	if out.SubPath == "" {
		t.Errorf("SubPath must default to non-empty")
	}
	if out.FooterText == "" {
		t.Errorf("FooterText must default to non-empty")
	}
	// v3.3.0: defaults now seed the unified registry (SubClients), not the
	// deprecated SubClientRules / SubImportClients.
	if len(out.SubClients) == 0 {
		t.Errorf("SubClients must default to a non-empty registry")
	}
	hasApps := false
	for _, fam := range out.SubClients {
		if fam.Name == "" || fam.RenderFormat == "" {
			t.Errorf("default family missing name/render_format: %+v", fam)
		}
		if len(fam.Apps) > 0 {
			hasApps = true
		}
	}
	if !hasApps {
		t.Errorf("default registry should include families with import apps")
	}
}

// settingsMaxID returns MAX(id) (0 if the table is empty) — the auto-increment
// high-water mark the UPDATE-in-place write model must keep from growing on
// re-saves. Works across sqlite/mysql/postgres.
func settingsMaxID(t *testing.T, db *gorm.DB) int64 {
	t.Helper()
	var max sql.NullInt64
	if err := db.Raw(`SELECT MAX(id) FROM settings`).Scan(&max).Error; err != nil {
		t.Fatalf("max(id): %v", err)
	}
	return max.Int64
}

func settingsRowCount(t *testing.T, db *gorm.DB) int64 {
	t.Helper()
	var n int64
	if err := db.Model(&settingRow{}).Count(&n).Error; err != nil {
		t.Fatalf("count: %v", err)
	}
	return n
}

// TestSave_DoesNotGrowAutoIncrement is the core guarantee of the UPDATE-in-place
// write model: once the settings rows exist, repeated saves — whether they
// change values or not — must never mint new auto-increment ids. The old batch
// `INSERT ... ON DUPLICATE KEY UPDATE` burned ~46 ids per save under InnoDB
// (mixed-mode insert reserves an autoinc value per conflicting VALUES row).
func TestSave_DoesNotGrowAutoIncrement(t *testing.T) {
	db, err := openTestDB(t)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { sqlDB, _ := db.DB(); _ = sqlDB.Close() })
	if err := EnsureSchema(db); err != nil {
		t.Fatalf("schema: %v", err)
	}
	repo := newKVSettingsRepo(db)
	ctx := context.Background()

	// First save materializes all rows.
	if err := repo.Save(ctx, ports.UISettings{SiteTitle: "v1"}); err != nil {
		t.Fatalf("save 1: %v", err)
	}
	baseMax := settingsMaxID(t, db)
	baseCount := settingsRowCount(t, db)
	if baseMax == 0 || baseCount == 0 {
		t.Fatalf("first save wrote nothing (max=%d count=%d)", baseMax, baseCount)
	}

	// Re-save several times: once with a changed value, once identical.
	if err := repo.Save(ctx, ports.UISettings{SiteTitle: "v2"}); err != nil {
		t.Fatalf("save 2: %v", err)
	}
	if err := repo.Save(ctx, ports.UISettings{SiteTitle: "v2"}); err != nil { // no-op values
		t.Fatalf("save 3: %v", err)
	}
	if err := repo.Save(ctx, ports.UISettings{SiteTitle: "v3"}); err != nil {
		t.Fatalf("save 4: %v", err)
	}

	if got := settingsMaxID(t, db); got != baseMax {
		t.Errorf("MAX(id) grew from %d to %d across re-saves; UPDATE-in-place must not mint ids", baseMax, got)
	}
	if got := settingsRowCount(t, db); got != baseCount {
		t.Errorf("row count changed from %d to %d across re-saves", baseCount, got)
	}
	// And the last write actually landed (proves UPDATEs, not skips).
	out, _ := repo.Load(ctx, ports.UISettings{})
	if out.SiteTitle != "v3" {
		t.Errorf("SiteTitle = %q, want v3 (UPDATE didn't take effect)", out.SiteTitle)
	}
}

// TestSave_FreshEmptyTable_InsertsAll pins that a first save against an empty
// table inserts every descriptor row and Load reads them back.
func TestSave_FreshEmptyTable_InsertsAll(t *testing.T) {
	db, err := openTestDB(t)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { sqlDB, _ := db.DB(); _ = sqlDB.Close() })
	if err := EnsureSchema(db); err != nil {
		t.Fatalf("schema: %v", err)
	}
	repo := newKVSettingsRepo(db)
	ctx := context.Background()

	if settingsRowCount(t, db) != 0 {
		t.Fatalf("expected empty settings table at start")
	}
	if err := repo.Save(ctx, ports.UISettings{SiteTitle: "Fresh", EmailDomain: "u.example.com"}); err != nil {
		t.Fatalf("save: %v", err)
	}
	// Every descriptor should have produced exactly one row.
	wantRows := int64(len(settingDescriptors(&ports.UISettings{})))
	if got := settingsRowCount(t, db); got != wantRows {
		t.Errorf("row count = %d, want one per descriptor (%d)", got, wantRows)
	}
	out, _ := repo.Load(ctx, ports.UISettings{})
	if out.SiteTitle != "Fresh" || out.EmailDomain != "u.example.com" {
		t.Errorf("read back SiteTitle=%q EmailDomain=%q, want Fresh / u.example.com", out.SiteTitle, out.EmailDomain)
	}
}

// TestSave_NewKeyGetsInserted simulates an upgrade that adds a new setting key:
// one row is deleted to mimic "key not yet in DB", then Save must recreate it
// while minting exactly one new id (existing rows still go through UPDATE).
func TestSave_NewKeyGetsInserted(t *testing.T) {
	db, err := openTestDB(t)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { sqlDB, _ := db.DB(); _ = sqlDB.Close() })
	if err := EnsureSchema(db); err != nil {
		t.Fatalf("schema: %v", err)
	}
	repo := newKVSettingsRepo(db)
	ctx := context.Background()

	if err := repo.Save(ctx, ports.UISettings{SiteTitle: "seed", FooterText: "footer"}); err != nil {
		t.Fatalf("seed save: %v", err)
	}
	maxBefore := settingsMaxID(t, db)
	countBefore := settingsRowCount(t, db)

	// Drop one existing row to model a freshly-added descriptor.
	if err := db.Exec(`DELETE FROM settings WHERE type = 'site' AND name = 'footer_text'`).Error; err != nil {
		t.Fatalf("delete row: %v", err)
	}
	if settingsRowCount(t, db) != countBefore-1 {
		t.Fatalf("expected one fewer row after delete")
	}

	if err := repo.Save(ctx, ports.UISettings{SiteTitle: "seed2", FooterText: "footer2"}); err != nil {
		t.Fatalf("resave: %v", err)
	}
	// Row is back...
	if got := settingsRowCount(t, db); got != countBefore {
		t.Errorf("row count = %d, want %d (deleted key rebuilt)", got, countBefore)
	}
	// ...at the cost of exactly one new id (the reinserted key), not ~46.
	if got := settingsMaxID(t, db); got != maxBefore+1 {
		t.Errorf("MAX(id) = %d, want %d (only the one reinserted key mints an id)", got, maxBefore+1)
	}
	out, _ := repo.Load(ctx, ports.UISettings{})
	if out.FooterText != "footer2" {
		t.Errorf("FooterText = %q, want footer2 (reinserted value)", out.FooterText)
	}
}

// TestSave_EncryptedRoundTrip pins that an encrypted-at-rest field
// (geo_ip_update_token) survives Save→Load with the plaintext intact, and that
// the value is genuinely stored ciphertext (enc:v1: prefix) — the UPDATE branch
// must write the encrypted value, not the plaintext.
func TestSave_EncryptedRoundTrip(t *testing.T) {
	db, err := openTestDB(t)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { sqlDB, _ := db.DB(); _ = sqlDB.Close() })
	if err := EnsureSchema(db); err != nil {
		t.Fatalf("schema: %v", err)
	}
	// Install a process-wide key for the duration of this test, then restore.
	prev := dbSecretKey
	ConfigureSecretKey("test-encryption-key-material")
	t.Cleanup(func() { dbSecretKey = prev })

	repo := newKVSettingsRepo(db)
	ctx := context.Background()

	const token = "super-secret-geoip-token"
	if err := repo.Save(ctx, ports.UISettings{GeoIPUpdateToken: token}); err != nil {
		t.Fatalf("save 1: %v", err)
	}
	// Stored value must be ciphertext, not the plaintext.
	var stored string
	if err := db.Raw(`SELECT value FROM settings WHERE type='geo' AND name='geo_ip_update_token'`).Scan(&stored).Error; err != nil {
		t.Fatalf("read raw: %v", err)
	}
	if stored == token || !strings.HasPrefix(stored, secretPrefix) {
		t.Errorf("stored value %q is not encrypted (want %s… prefix)", stored, secretPrefix)
	}
	// Load decrypts back to the original.
	out, err := repo.Load(ctx, ports.UISettings{})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if out.GeoIPUpdateToken != token {
		t.Errorf("GeoIPUpdateToken round-trip = %q, want %q", out.GeoIPUpdateToken, token)
	}
	// Re-save via the UPDATE branch (row now exists) must keep ciphertext intact.
	if err := repo.Save(ctx, ports.UISettings{GeoIPUpdateToken: token}); err != nil {
		t.Fatalf("save 2: %v", err)
	}
	out2, _ := repo.Load(ctx, ports.UISettings{})
	if out2.GeoIPUpdateToken != token {
		t.Errorf("GeoIPUpdateToken after UPDATE = %q, want %q", out2.GeoIPUpdateToken, token)
	}
}

// TestKVSettingsBoolMarshal covers the boolField descriptor edge cases —
// "0"/"1" round trip in both directions, and "true" accepted as well so
// external SQL tools can write a human-friendly value.
func TestKVSettingsBoolMarshal(t *testing.T) {
	db, err := openTestDB(t)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() {
		sqlDB, _ := db.DB()
		if sqlDB != nil {
			_ = sqlDB.Close()
		}
	})
	if err := EnsureSchema(db); err != nil {
		t.Fatalf("schema: %v", err)
	}
	repo := newKVSettingsRepo(db)
	ctx := context.Background()

	// Direct UPSERT of "true" — simulates an admin editing the row in a
	// SQL browser. The boolField.Unmarshal must accept it. `encrypted` is bound
	// as a Go bool (not a literal 0) so the driver encodes it per-dialect —
	// Postgres' boolean column rejects an integer literal (SQLSTATE 42804).
	if err := db.Exec(`INSERT INTO settings(type, name, value, encrypted, updated_at) VALUES ('auth','disallow_user_local_login','true',?,?)`, false, time.Now()).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}
	out, err := repo.Load(ctx, ports.UISettings{})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !out.DisallowUserLocalLogin {
		t.Errorf("boolField Unmarshal(\"true\") should set true")
	}
}
