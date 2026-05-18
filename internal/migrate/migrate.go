package migrate

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"gorm.io/gorm"

	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// migrationPlan summarises what we see on the source side so the operator
// gets a row count before any writes happen. Numbers in the report match
// what runMigration will actually copy.
type migrationPlan struct {
	uiSettings      bool
	mailSettings    bool
	samlConfig      bool
	oidcConfig      bool
	users           int64
	groups          int64
	nodes           int64
	xuiPanels       int64
	xuiClients      int64 // → user_xui_clients in v3
	clientSnapshots int64 // not copied; used only to backfill LastRaw on ownership
	trafficSnaps    int64
	nodeTrafficSnap int64
	auditLog        int64
	subLogs         int64
	syncTasks       int64
	mailTemplates   int64
	mailSent        int64
}

func buildMigrationPlan(ctx context.Context, src *gorm.DB) (*migrationPlan, error) {
	p := &migrationPlan{}

	// Existence checks first so a partial v2 DB (missing optional tables)
	// doesn't kill the migration; we just skip those copies.
	if src.Migrator().HasTable("ui_settings") {
		var n int64
		if err := src.WithContext(ctx).Table("ui_settings").Count(&n).Error; err == nil {
			p.uiSettings = n > 0
		}
	}
	if src.Migrator().HasTable("mail_settings") {
		var n int64
		if err := src.WithContext(ctx).Table("mail_settings").Count(&n).Error; err == nil {
			p.mailSettings = n > 0
		}
	}
	if src.Migrator().HasTable("saml_config") {
		var n int64
		if err := src.WithContext(ctx).Table("saml_config").Count(&n).Error; err == nil {
			p.samlConfig = n > 0
		}
	}
	if src.Migrator().HasTable("oidc_config") {
		var n int64
		if err := src.WithContext(ctx).Table("oidc_config").Count(&n).Error; err == nil {
			p.oidcConfig = n > 0
		}
	}

	counts := []struct {
		table string
		dst   *int64
	}{
		{"users", &p.users},
		{"groups_", &p.groups},
		{"nodes", &p.nodes},
		{"xui_panels", &p.xuiPanels},
		{"xui_clients", &p.xuiClients},
		{"client_traffic_snapshots", &p.clientSnapshots},
		{"traffic_snapshots", &p.trafficSnaps},
		{"node_traffic_snapshots", &p.nodeTrafficSnap},
		{"audit_log", &p.auditLog},
		{"sub_logs", &p.subLogs},
		{"sync_tasks", &p.syncTasks},
		{"mail_templates", &p.mailTemplates},
		{"mail_sent", &p.mailSent},
	}
	for _, c := range counts {
		if !src.Migrator().HasTable(c.table) {
			continue
		}
		var n int64
		if err := src.WithContext(ctx).Table(c.table).Count(&n).Error; err != nil {
			return nil, fmt.Errorf("count %s: %w", c.table, err)
		}
		*c.dst = n
	}
	return p, nil
}

func (p *migrationPlan) print() {
	fmt.Println("Migration plan (source-side row counts):")
	fmt.Println("  ─ configuration ─")
	fmt.Printf("    ui_settings:  %v\n", p.uiSettings)
	fmt.Printf("    mail_settings: %v\n", p.mailSettings)
	fmt.Printf("    saml_config:  %v → saml_settings\n", p.samlConfig)
	fmt.Printf("    oidc_config:  %v → oidc_settings\n", p.oidcConfig)
	fmt.Println("  ─ business entities ─")
	fmt.Printf("    users:           %d\n", p.users)
	fmt.Printf("    groups_:         %d\n", p.groups)
	fmt.Printf("    nodes:           %d (panel_name column dropped)\n", p.nodes)
	fmt.Printf("    xui_panels:      %d\n", p.xuiPanels)
	fmt.Printf("    xui_clients:     %d → user_xui_clients (panel_name dropped, lifetime/last_raw added)\n", p.xuiClients)
	fmt.Printf("    mail_templates:  %d\n", p.mailTemplates)
	fmt.Println("  ─ time series ─")
	fmt.Printf("    traffic_snapshots:      %d\n", p.trafficSnaps)
	fmt.Printf("    client_traffic_snapshots: %d (NOT copied — semantics changed; LastRaw seeded from latest row per client)\n", p.clientSnapshots)
	fmt.Printf("    node_traffic_snapshots: %d\n", p.nodeTrafficSnap)
	fmt.Println("  ─ logs / tasks ─")
	fmt.Printf("    audit_log:   %d\n", p.auditLog)
	fmt.Printf("    sub_logs:    %d\n", p.subLogs)
	fmt.Printf("    sync_tasks:  %d\n", p.syncTasks)
	fmt.Printf("    mail_sent:   %d\n", p.mailSent)
	fmt.Println("  ─ dropped ─")
	fmt.Println("    rule_sets:   skipped (dead code; rule sets live in config/rulesets/*.yaml)")
}

const copyBatchSize = 500

// runMigration walks every table category in dependency order. Step 1 is a
// sentinel marker into the `settings` table so guardDstEmpty refuses any
// re-run after this point — without that, a mid-migration crash on (say)
// `nodes` would leave `users` committed, `settings` still empty (because
// the KV step ran last in pre-fix runs), and a naïve re-run would hit
// duplicate-PK on users with no warning. The marker is itself a regular
// settings row that the v3.0.0 panel can read; `_migration.completed_at`
// is updated at the very end to capture migration success time.
func runMigration(ctx context.Context, src, dst *gorm.DB, plan *migrationPlan) error {
	if err := seedMigrationMarker(ctx, dst); err != nil {
		return fmt.Errorf("seed migration marker: %w", err)
	}

	steps := []struct {
		name string
		fn   func(ctx context.Context, src, dst *gorm.DB) error
	}{
		{"users (with period_baseline_bytes backfill)", copyUsers},
		{"groups_", copyTableRaw("groups_")},
		{"xui_panels", copyTableRaw("xui_panels")},
		{"nodes", copyNodes},
		{"user_xui_clients (renamed from xui_clients)", copyOwnerships},
		{"mail_templates", copyTableRaw("mail_templates")},
		{"mail_sent", copyTableRaw("mail_sent")},
		{"traffic_snapshots", copyTableRaw("traffic_snapshots")},
		{"node_traffic_snapshots", copyTableRaw("node_traffic_snapshots")},
		{"audit_log", copyTableRaw("audit_log")},
		{"sub_logs", copyTableRaw("sub_logs")},
		{"sync_tasks", copyTableRaw("sync_tasks")},
		{"mail_settings (slimmed)", copyMailSettings},
		{"saml_settings (renamed from saml_config)", copySAMLConfig},
		{"oidc_settings (renamed from oidc_config)", copyOIDCConfig},
		{"settings (KV; from ui_settings + mail notify thresholds)", copySettingsKV},
	}

	for _, s := range steps {
		fmt.Printf("• %s ... ", s.name)
		if err := s.fn(ctx, src, dst); err != nil {
			fmt.Println("FAILED")
			return fmt.Errorf("%s: %w", s.name, err)
		}
		fmt.Println("ok")
	}

	if err := stampMigrationComplete(ctx, dst); err != nil {
		return fmt.Errorf("stamp migration completed: %w", err)
	}
	return nil
}

// seedMigrationMarker inserts a sentinel row into `settings` so a mid-
// migration crash followed by a re-run gets caught by guardDstEmpty. Done
// before any other dst writes so the marker is the FIRST row added — if
// any step fails the operator's only recovery is to `DROP DATABASE psp_v3`
// and re-run from scratch, which matches the "side-by-side new database"
// design (rerun cost is zero; partial state corruption cost is high).
func seedMigrationMarker(ctx context.Context, dst *gorm.DB) error {
	now := time.Now()
	return dst.WithContext(ctx).Table("settings").Create(map[string]any{
		"type":       "_migration",
		"name":       "started_at",
		"value":      now.UTC().Format(time.RFC3339),
		"encrypted":  false,
		"updated_at": now,
	}).Error
}

// stampMigrationComplete records the completion time alongside the start
// marker. Both rows live under settings.type='_migration' so an admin
// running `SELECT * FROM settings WHERE type='_migration'` can see exactly
// when the v3.0.0 migration ran. The v3.0.0 panel ignores this type entirely
// (settingDescriptors covers only site/auth/sub/security/runtime/notice/notify).
func stampMigrationComplete(ctx context.Context, dst *gorm.DB) error {
	now := time.Now()
	return dst.WithContext(ctx).Table("settings").Create(map[string]any{
		"type":       "_migration",
		"name":       "completed_at",
		"value":      now.UTC().Format(time.RFC3339),
		"encrypted":  false,
		"updated_at": now,
	}).Error
}

// copyTableRaw streams every row of `table` from src to dst with no
// transformation. Used for tables whose v2/v3 column lists are identical.
// Both panels-of-3X-UI panels and time-series tables go through here.
func copyTableRaw(table string) func(ctx context.Context, src, dst *gorm.DB) error {
	return func(ctx context.Context, src, dst *gorm.DB) error {
		if !src.Migrator().HasTable(table) {
			return nil
		}
		// Streaming via FindInBatches keeps memory flat on large tables
		// (audit_log / traffic_snapshots can be millions of rows).
		var batch []map[string]any
		rows, err := src.WithContext(ctx).Table(table).Rows()
		if err != nil {
			return err
		}
		defer rows.Close()

		for rows.Next() {
			row := map[string]any{}
			if err := src.WithContext(ctx).ScanRows(rows, &row); err != nil {
				return err
			}
			batch = append(batch, row)
			if len(batch) >= copyBatchSize {
				if err := dst.WithContext(ctx).Table(table).Create(&batch).Error; err != nil {
					return err
				}
				batch = batch[:0]
			}
		}
		if err := rows.Err(); err != nil {
			return err
		}
		if len(batch) > 0 {
			if err := dst.WithContext(ctx).Table(table).Create(&batch).Error; err != nil {
				return err
			}
		}
		return nil
	}
}

// copyUsers copies the users table and backfills users.period_baseline_bytes,
// which is new in v3.0.0. The naive copy (every user gets baseline = 0) would
// make the new PeriodUsed() return the full lifetime on first read — users
// who'd been on the panel for months would be immediately re-disabled even
// though their current period barely started.
//
// Faithful backfill: for each user with a traffic_period_start, look up the
// last v2 traffic_snapshot captured BEFORE that timestamp. Its total_bytes
// is the lifetime as it stood at period start — exactly the value the legacy
// periodUsage was deriving on every read. We set period_baseline_bytes to
// that, so PeriodUsed() = lifetime_total - baseline matches the legacy semantics
// for the migrated user's very first post-upgrade poll. No baseline snapshot found
// (fresh user, or snapshots were pruned) → leave at 0, which mirrors the legacy
// fallback that treated full lifetime as period_used.
func copyUsers(ctx context.Context, src, dst *gorm.DB) error {
	if !src.Migrator().HasTable("users") {
		return nil
	}
	rows, err := src.WithContext(ctx).Table("users").Rows()
	if err != nil {
		return err
	}
	defer rows.Close()

	var batch []map[string]any
	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		if err := dst.WithContext(ctx).Table("users").Create(&batch).Error; err != nil {
			return err
		}
		batch = batch[:0]
		return nil
	}
	for rows.Next() {
		row := map[string]any{}
		if err := src.WithContext(ctx).ScanRows(rows, &row); err != nil {
			return err
		}
		row["period_baseline_bytes"] = backfillPeriodBaseline(ctx, src, row)
		batch = append(batch, row)
		if len(batch) >= copyBatchSize {
			if err := flush(); err != nil {
				return err
			}
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	return flush()
}

// backfillPeriodBaseline derives users.period_baseline_bytes for one row by
// reproducing the legacy periodUsage logic: LastBefore(period_start) on the v2
// traffic_snapshots, returning its total_bytes if present. Returns 0 (the
// permissive fallback) when there's no period_start, no snapshot, or any
// query error — the v3.0.0 panel will then treat the full lifetime as
// period_used on the first read, which is the legacy fallback behavior.
func backfillPeriodBaseline(ctx context.Context, src *gorm.DB, row map[string]any) int64 {
	periodStart, ok := row["traffic_period_start"]
	if !ok || periodStart == nil {
		return 0
	}
	uid, ok := row["id"]
	if !ok {
		return 0
	}
	if !src.Migrator().HasTable("traffic_snapshots") {
		return 0
	}
	var snap struct {
		TotalBytes int64 `gorm:"column:total_bytes"`
	}
	tx := src.WithContext(ctx).
		Table("traffic_snapshots").
		Select("total_bytes").
		Where("user_id = ? AND captured_at < ?", uid, periodStart).
		Order("captured_at DESC").
		Limit(1).
		Scan(&snap)
	if tx.Error != nil || tx.RowsAffected == 0 {
		return 0
	}
	return snap.TotalBytes
}

// copyNodes copies rows from the src `nodes` table to dst, dropping the
// legacy `panel_name` column (the v3 schema resolves panel name from the
// in-memory pool at render time).
func copyNodes(ctx context.Context, src, dst *gorm.DB) error {
	if !src.Migrator().HasTable("nodes") {
		return nil
	}
	rows, err := src.WithContext(ctx).Table("nodes").Rows()
	if err != nil {
		return err
	}
	defer rows.Close()

	var batch []map[string]any
	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		if err := dst.WithContext(ctx).Table("nodes").Create(&batch).Error; err != nil {
			return err
		}
		batch = batch[:0]
		return nil
	}
	for rows.Next() {
		row := map[string]any{}
		if err := src.WithContext(ctx).ScanRows(rows, &row); err != nil {
			return err
		}
		delete(row, "panel_name")
		batch = append(batch, row)
		if len(batch) >= copyBatchSize {
			if err := flush(); err != nil {
				return err
			}
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	return flush()
}

// copyOwnerships migrates xui_clients → user_xui_clients. Two transforms:
//  1. Drop the legacy panel_name column (v3 resolves from the panel pool).
//  2. Seed LastRawXxx from the latest pre-v3 client_traffic_snapshot for
//     this (panel_id, inbound_id, client_email) triple. This makes the
//     first post-migration traffic poll compute delta = current_3xui_raw -
//     last_observed_raw and accumulate just that delta into the new
//     LifetimeXxx counters — preventing the entire historical cumulative
//     from being double-counted into v3 lifetime on the first poll.
//
// Without this seeding step, a freshly-imported v3 ownership row would
// start at LastRaw=0 and treat the current 3X-UI cumulative as the full
// initial delta — adding the user's entire ever-used traffic to v3 lifetime
// in one shot.
func copyOwnerships(ctx context.Context, src, dst *gorm.DB) error {
	if !src.Migrator().HasTable("xui_clients") {
		return nil
	}
	// Read via ScanRows-into-map so we don't trip over driver-specific
	// time.Time parsing quirks. The columns we transform / read explicitly
	// (panel_id, inbound_id, client_email) are int / string and parse
	// uniformly across drivers.
	rows, err := src.WithContext(ctx).Table("xui_clients").Rows()
	if err != nil {
		return err
	}
	defer rows.Close()

	var batch []map[string]any
	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		if err := dst.WithContext(ctx).Table("user_xui_clients").Create(&batch).Error; err != nil {
			return err
		}
		batch = batch[:0]
		return nil
	}
	hasClientSnaps := src.Migrator().HasTable("client_traffic_snapshots")

	for rows.Next() {
		row := map[string]any{}
		if err := src.WithContext(ctx).ScanRows(rows, &row); err != nil {
			return err
		}
		delete(row, "panel_name") // dropped in v3.0.0

		// Seed last_raw_*_bytes on the new table from the most-recent
		// legacy client snapshot for this triple. Without this seeding the
		// first post-migration poll would treat the entire current 3X-UI
		// cumulative as the delta and double-count every byte ever sent
		// through this client into the new v3.0.0 lifetime counter.
		row["lifetime_up_bytes"] = int64(0)
		row["lifetime_down_bytes"] = int64(0)
		row["lifetime_total_bytes"] = int64(0)
		row["last_raw_up_bytes"] = int64(0)
		row["last_raw_down_bytes"] = int64(0)
		row["last_raw_total_bytes"] = int64(0)
		if hasClientSnaps {
			var snap struct {
				UpBytes    int64 `gorm:"column:up_bytes"`
				DownBytes  int64 `gorm:"column:down_bytes"`
				TotalBytes int64 `gorm:"column:total_bytes"`
			}
			tx := src.WithContext(ctx).
				Table("client_traffic_snapshots").
				Select("up_bytes, down_bytes, total_bytes").
				Where("panel_id = ? AND inbound_id = ? AND client_email = ?",
					row["panel_id"], row["inbound_id"], row["client_email"]).
				Order("id DESC").
				Limit(1).
				Scan(&snap)
			if tx.Error == nil && tx.RowsAffected > 0 {
				row["last_raw_up_bytes"] = snap.UpBytes
				row["last_raw_down_bytes"] = snap.DownBytes
				row["last_raw_total_bytes"] = snap.TotalBytes
			}
		}

		batch = append(batch, row)
		if len(batch) >= copyBatchSize {
			if err := flush(); err != nil {
				return err
			}
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	return flush()
}

// copyMailSettings copies the SMTP-connection subset of legacy mail_settings.
// The two notify-threshold fields (expire_before_days / traffic_remain_percent)
// are NOT copied here — copySettingsKV writes them into settings.type=notify.
func copyMailSettings(ctx context.Context, src, dst *gorm.DB) error {
	if !src.Migrator().HasTable("mail_settings") {
		return nil
	}
	var legacy legacyMailSettingsRow
	tx := src.WithContext(ctx).First(&legacy)
	if tx.Error != nil || tx.RowsAffected == 0 {
		return nil
	}
	row := map[string]any{
		"id":            legacy.ID,
		"enabled":       legacy.Enabled,
		"smtp_host":     legacy.SMTPHost,
		"smtp_port":     legacy.SMTPPort,
		"smtp_username": legacy.SMTPUsername,
		"smtp_password": legacy.SMTPPassword, // ciphertext stays ciphertext
		"from_email":    legacy.FromEmail,
		"from_name":     legacy.FromName,
		"encryption":    legacy.Encryption,
		"updated_at":    parseLegacyTimestamp(legacy.UpdatedAt),
	}
	return dst.WithContext(ctx).Table("mail_settings").Create(row).Error
}

func copySAMLConfig(ctx context.Context, src, dst *gorm.DB) error {
	if !src.Migrator().HasTable("saml_config") {
		return nil
	}
	var legacy legacySAMLConfigRow
	tx := src.WithContext(ctx).First(&legacy)
	if tx.Error != nil || tx.RowsAffected == 0 {
		return nil
	}
	row := map[string]any{
		"id":                            legacy.ID,
		"enabled":                       legacy.Enabled,
		"mode":                          legacy.Mode,
		"sp_entity_id":                  legacy.SPEntityID,
		"sp_acs_url":                    legacy.SPACSURL,
		"sp_cert_pem":                   legacy.SPCertPEM,
		"sp_key_pem":                    legacy.SPKeyPEM,
		"idp_metadata_url":              legacy.IDPMetadataURL,
		"idp_metadata_refresh_sec":      legacy.IDPMetadataRefreshSec,
		"attr_upn":                      legacy.AttrUPN,
		"attr_email":                    legacy.AttrEmail,
		"attr_display_name":             legacy.AttrDisplayName,
		"attr_groups":                   legacy.AttrGroups,
		"role_rules":                    legacy.RoleRules,
		"default_group_slug":            legacy.DefaultGroupSlug,
		"allow_auto_create":             legacy.AllowAutoCreate,
		"new_user_expire_days":          legacy.NewUserExpireDays,
		"new_user_traffic_limit_bytes":  legacy.NewUserTrafficLimitBytes,
		"new_user_traffic_reset_period": legacy.NewUserTrafficResetPeriod,
		"updated_at":                    parseLegacyTimestamp(legacy.UpdatedAt),
	}
	return dst.WithContext(ctx).Table("saml_settings").Create(row).Error
}

func copyOIDCConfig(ctx context.Context, src, dst *gorm.DB) error {
	if !src.Migrator().HasTable("oidc_config") {
		return nil
	}
	var legacy legacyOIDCConfigRow
	tx := src.WithContext(ctx).First(&legacy)
	if tx.Error != nil || tx.RowsAffected == 0 {
		return nil
	}
	row := map[string]any{
		"id":                            legacy.ID,
		"enabled":                       legacy.Enabled,
		"issuer_url":                    legacy.IssuerURL,
		"client_id":                     legacy.ClientID,
		"client_secret":                 legacy.ClientSecret,
		"redirect_url":                  legacy.RedirectURL,
		"scopes":                        legacy.Scopes,
		"attr_username":                 legacy.AttrUsername,
		"attr_email":                    legacy.AttrEmail,
		"attr_display_name":             legacy.AttrDisplayName,
		"attr_groups":                   legacy.AttrGroups,
		"role_rules":                    legacy.RoleRules,
		"default_group_slug":            legacy.DefaultGroupSlug,
		"allow_auto_create":             legacy.AllowAutoCreate,
		"new_user_expire_days":          legacy.NewUserExpireDays,
		"new_user_traffic_limit_bytes":  legacy.NewUserTrafficLimitBytes,
		"new_user_traffic_reset_period": legacy.NewUserTrafficResetPeriod,
		"updated_at":                    parseLegacyTimestamp(legacy.UpdatedAt),
	}
	return dst.WithContext(ctx).Table("oidc_settings").Create(row).Error
}

// copySettingsKV flattens the wide legacy ui_settings row into the v3
// `settings` KV table, then appends the two notify thresholds pulled out
// of legacy mail_settings. Field grouping (`type`) matches the layout
// documented in docs/db-refactor-plan.md §3.2.
//
// The row order here mirrors settingDescriptors() in
// internal/adapters/mysql/settings_kv_repo.go — keep them in sync if a
// new UISettings field is added before this migration cmd is deleted.
func copySettingsKV(ctx context.Context, src, dst *gorm.DB) error {
	type kv struct {
		t, n, v string
	}
	var rows []kv

	if src.Migrator().HasTable("ui_settings") {
		var ui legacyUISettingsRow
		if tx := src.WithContext(ctx).First(&ui); tx.Error == nil && tx.RowsAffected > 0 {
			rows = append(rows,
				// site
				kv{"site", "site_title", ui.SiteTitle},
				kv{"site", "app_title", ui.AppTitle},
				kv{"site", "icon_url", ui.IconURL},
				kv{"site", "logo_url", ui.LogoURL},
				kv{"site", "logo_url_dark", ui.LogoURLDark},
				kv{"site", "footer_text", ui.FooterText},
				kv{"site", "theme_color", ui.ThemeColor},
				kv{"site", "email_domain", ui.EmailDomain},
				kv{"site", "sub_base_url", ui.SubBaseURL},
				// auth
				kv{"auth", "login_mode", ui.LoginMode},
				kv{"auth", "jwt_issuer", ui.JWTIssuer},
				kv{"auth", "jwt_access_ttl_minutes", strconv.Itoa(ui.JWTAccessTTLMinutes)},
				kv{"auth", "jwt_refresh_ttl_minutes", strconv.Itoa(ui.JWTRefreshTTLMinutes)},
				kv{"auth", "disallow_user_local_login", boolToStr(ui.DisallowUserLocalLogin)},
				kv{"auth", "disallow_user_password_change", boolToStr(ui.DisallowUserPasswordChange)},
				// sub
				kv{"sub", "sub_path", ui.SubPath},
				kv{"sub", "sub_update_interval_hours", strconv.Itoa(ui.SubUpdateIntervalHours)},
				kv{"sub", "sub_region_flag_prefix", boolToStr(ui.SubRegionFlagPrefix)},
				kv{"sub", "sub_block_auto_disable", boolToStr(ui.SubBlockAutoDisable)},
				kv{"sub", "sub_block_auto_disable_count", strconv.Itoa(ui.SubBlockAutoDisableCount)},
				kv{"sub", "sub_log_retention_days", strconv.Itoa(ui.SubLogRetentionDays)},
				kv{"sub", "sub_import_tutorial_url", ui.SubImportTutorialURL},
				kv{"sub", "sub_client_rules", coalesceJSON(ui.SubClientRules)},
				kv{"sub", "sub_import_clients", coalesceJSON(ui.SubImportClients)},
				// security
				kv{"security", "sub_per_ip_per_min", strconv.Itoa(ui.SubPerIPPerMin)},
				kv{"security", "login_per_ip_per_min", strconv.Itoa(ui.LoginPerIPPerMin)},
				kv{"security", "audit_retention_days", strconv.Itoa(ui.AuditRetentionDays)},
				kv{"security", "sync_task_retention_days", strconv.Itoa(ui.SyncTaskRetentionDays)},
				// New in v3 — seed the documented default since v2 had no equivalent.
				kv{"security", "traffic_snapshot_retention_days", "180"},
				kv{"security", "emergency_access_enabled", boolToStr(ui.EmergencyAccessEnabled)},
				kv{"security", "emergency_access_hours", strconv.Itoa(ui.EmergencyAccessHours)},
				kv{"security", "emergency_access_max_count", strconv.Itoa(ui.EmergencyAccessMaxCount)},
				kv{"security", "emergency_access_quota_gb", strconv.Itoa(ui.EmergencyAccessQuotaGB)},
				// runtime
				kv{"runtime", "timezone", ui.Timezone},
				kv{"runtime", "cron_traffic_pull_minutes", strconv.Itoa(ui.CronTrafficPullMinutes)},
				kv{"runtime", "cron_reconcile_minutes", strconv.Itoa(ui.CronReconcileMinutes)},
				kv{"runtime", "max_panel_concurrency", strconv.Itoa(ui.MaxPanelConcurrency)},
				kv{"runtime", "allow_user_personal_rules", boolToStr(ui.AllowUserPersonalRules)},
				// notice
				kv{"notice", "quick_links", coalesceJSON(ui.QuickLinks)},
				kv{"notice", "global_announcement", coalesceJSON(ui.GlobalAnnouncement)},
			)
		}
	}

	// notify thresholds: pulled out of legacy mail_settings.
	if src.Migrator().HasTable("mail_settings") {
		var ms legacyMailSettingsRow
		if tx := src.WithContext(ctx).First(&ms); tx.Error == nil && tx.RowsAffected > 0 {
			rows = append(rows,
				kv{"notify", "expire_before_days", strconv.Itoa(ms.ExpireBeforeDays)},
				kv{"notify", "traffic_remain_percent", strconv.Itoa(ms.TrafficRemainPercent)},
			)
		}
	}

	if len(rows) == 0 {
		return nil
	}
	now := time.Now()
	dstRows := make([]map[string]any, 0, len(rows))
	for _, r := range rows {
		dstRows = append(dstRows, map[string]any{
			"type":       r.t,
			"name":       r.n,
			"value":      r.v,
			"encrypted":  false,
			"updated_at": now,
		})
	}
	return dst.WithContext(ctx).Table("settings").CreateInBatches(dstRows, copyBatchSize).Error
}

// ports/repos.go provides the canonical UISettings struct definition; the
// reference here keeps the import non-empty so a stale build of this cmd
// against a renamed ports package will fail at compile rather than at run.
var _ ports.UISettings

func boolToStr(b bool) string {
	if b {
		return "1"
	}
	return "0"
}

func coalesceJSON(s string) string {
	if s == "" {
		return "null"
	}
	return s
}

// parseLegacyTimestamp converts a legacy `updated_at` string back into a
// time.Time the destination driver will accept. Legacy struct fields are
// declared as `string` (see legacy_schema.go) to dodge the SQLite TEXT-
// vs-DATETIME parse mismatch on read; but MySQL strict mode rejects
// ISO-8601 strings like "2026-05-16T10:44:22.662Z" when inserting into a
// DATETIME column. Parse it back to a real time.Time here so GORM's
// driver layer can format it correctly per destination dialect.
//
// Falls back to time.Now() on any parse failure — for these single-row
// config tables (mail_settings / saml_settings / oidc_settings) the
// updated_at is informational only ("when was this saved"), so the
// migration timestamp is a defensible substitute.
func parseLegacyTimestamp(s string) time.Time {
	if s == "" {
		return time.Now()
	}
	// Try formats in rough order of likelihood for a panel that has been
	// running across multiple Go versions and driver configs.
	formats := []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02T15:04:05.999999999Z",
		"2006-01-02 15:04:05.999999999",
		"2006-01-02 15:04:05",
		"2006-01-02T15:04:05",
	}
	for _, layout := range formats {
		if t, err := time.Parse(layout, s); err == nil {
			return t
		}
	}
	return time.Now()
}
