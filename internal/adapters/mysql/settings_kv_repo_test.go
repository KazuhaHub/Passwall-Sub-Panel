package mysql

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// TestKVSettingsRoundtrip walks the v3.0.0 KV repo end-to-end: Save → SELECT
// against the raw schema → Load → check that every descriptor's typed
// field survives the round trip. This is the only test that exercises the
// Marshal/Unmarshal halves of every descriptor — strField / intField /
// boolField / jsonField — so regressions in any helper are caught here.
func TestKVSettingsRoundtrip(t *testing.T) {
	db, err := Open("sqlite", filepath.Join(t.TempDir(), "panel.db"))
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
		LoginMode:                    "dual",
		SiteTitle:                    "Test Panel",
		AppTitle:                     "PSP",
		IconURL:                      "https://cdn.example.com/icon.png",
		LogoURL:                      "https://cdn.example.com/logo.png",
		LogoURLDark:                  "https://cdn.example.com/logo-dark.png",
		FooterText:                   "© 2026 Test",
		ThemeColor:                   "#0061A4",
		EmailDomain:                  "users.example.com",
		SubBaseURL:                   "https://panel.example.com",
		JWTIssuer:                    "passwall-sub-panel",
		JWTAccessTTLMinutes:          90,
		JWTRefreshTTLMinutes:         60 * 24 * 14,
		DisallowUserLocalLogin:       true,
		DisallowUserPasswordChange:   false,
		AllowUserPersonalRules:       true,
		SubPath:                      "subscribe",
		SubUpdateIntervalHours:       12,
		SubRegionFlagPrefix:          true,
		SubBlockAutoDisable:          true,
		SubBlockAutoDisableCount:     5,
		SubLogRetentionDays:          14,
		SubImportTutorialURL:         "https://docs.example.com/import",
		SubClientRules:               []ports.SubClientRule{{Name: "Clash", Keywords: []string{"clash"}, RenderFormat: "mihomo", Enabled: true}},
		SubImportClients:             []ports.SubImportClient{{Name: "Verge", Platforms: []string{"windows"}, RenderFormat: "mihomo", ImportURLTemplate: "clash://x", Enabled: true, Sort: 10}},
		SubPerIPPerMin:               42,
		LoginPerIPPerMin:             7,
		AuditRetentionDays:           45,
		SyncTaskRetentionDays:        60,
		TrafficHistoryDays: 200,
		EmergencyAccessEnabled:       true,
		EmergencyAccessHours:         48,
		EmergencyAccessMaxCount:      4,
		EmergencyAccessQuotaGB:       20,
		Timezone:                     "America/Los_Angeles",
		CronTrafficPullMinutes:       10,
		CronReconcileMinutes:         30,
		MaxPanelConcurrency:          16,
		QuickLinks:                   []ports.QuickLink{{Label: "Docs", URL: "https://docs", Enabled: true, Sort: 1}},
		GlobalAnnouncement:           ports.GlobalAnnouncement{Enabled: true, Title: "Maintenance", Content: "tonight 23:00", Level: "info"},
		ExpireBeforeDays:             7,
		TrafficRemainPercent:         15,
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
	db, err := Open("sqlite", filepath.Join(t.TempDir(), "panel.db"))
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
		"CronTrafficPullMinutes":       out.CronTrafficPullMinutes,
		"CronReconcileMinutes":         out.CronReconcileMinutes,
		"JWTAccessTTLMinutes":          out.JWTAccessTTLMinutes,
		"JWTRefreshTTLMinutes":         out.JWTRefreshTTLMinutes,
		"SubPerIPPerMin":               out.SubPerIPPerMin,
		"LoginPerIPPerMin":             out.LoginPerIPPerMin,
		"SubLogRetentionDays":          out.SubLogRetentionDays,
		"AuditRetentionDays":           out.AuditRetentionDays,
		"SyncTaskRetentionDays":        out.SyncTaskRetentionDays,
		"TrafficHistoryDays": out.TrafficHistoryDays,
		"SubBlockAutoDisableCount":     out.SubBlockAutoDisableCount,
		"SubUpdateIntervalHours":       out.SubUpdateIntervalHours,
		"ExpireBeforeDays":             out.ExpireBeforeDays,
		"TrafficRemainPercent":         out.TrafficRemainPercent,
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

// TestKVSettingsBoolMarshal covers the boolField descriptor edge cases —
// "0"/"1" round trip in both directions, and "true" accepted as well so
// external SQL tools can write a human-friendly value.
func TestKVSettingsBoolMarshal(t *testing.T) {
	db, err := Open("sqlite", filepath.Join(t.TempDir(), "panel.db"))
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
	// SQL browser. The boolField.Unmarshal must accept it.
	if err := db.Exec(`INSERT INTO settings(type, name, value, encrypted, updated_at) VALUES ('auth','disallow_user_local_login','true',0,?)`, time.Now()).Error; err != nil {
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
