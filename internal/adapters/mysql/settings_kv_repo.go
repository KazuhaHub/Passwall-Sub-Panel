package mysql

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// settingRow is one (type, name, value) cell in the unified KV settings
// table. Replaces the previous wide ui_settings row. Grouping by `type` is
// purely organizational — it lets the admin scan the table by category in a
// SQL browser instead of one giant flat list.
//
// encrypted is provided for symmetry with the SAML/OIDC/mail single-row
// tables and is unused by the current UISettings field set (which is all
// non-secret strings, ints, bools, and JSON blobs). Future secret-type
// settings flip this flag and the repo will transparently AES-GCM encrypt
// the value at rest via the shared secrets.go helpers.
type settingRow struct {
	ID        int64  `gorm:"primaryKey;autoIncrement"`
	Type      string `gorm:"size:32;not null;uniqueIndex:uk_setting_kv,priority:1;index:idx_setting_type"`
	Name      string `gorm:"size:128;not null;uniqueIndex:uk_setting_kv,priority:2"`
	Value     string `gorm:"type:text"`
	Encrypted bool   `gorm:"not null;default:false"`
	UpdatedAt time.Time
}

func (settingRow) TableName() string { return "settings" }

// kvSettingsRepo implements ports.SettingsRepo against the KV settings table.
// External contract is unchanged from the previous wide-row repo: Load
// returns a fully populated ports.UISettings, Save persists one. Internals
// flatten that struct into ~40 KV rows and rebuild it on read.
type kvSettingsRepo struct{ db *gorm.DB }

func newKVSettingsRepo(db *gorm.DB) *kvSettingsRepo { return &kvSettingsRepo{db: db} }

func (r *kvSettingsRepo) Load(ctx context.Context, defaults ports.UISettings) (ports.UISettings, error) {
	var rows []settingRow
	if err := r.db.WithContext(ctx).Find(&rows).Error; err != nil {
		return defaults, err
	}

	// Build an addressable target so the descriptor unmarshalers can write
	// into it directly. Start from defaults so missing keys keep their
	// caller-provided default; applyDefaults at the bottom fills the rest.
	out := defaults
	byKey := map[string]settingRow{}
	for _, row := range rows {
		byKey[row.Type+"."+row.Name] = row
	}

	for _, d := range settingDescriptors(&out) {
		row, ok := byKey[d.Type+"."+d.Name]
		if !ok {
			// Setting never written — leave whatever the descriptor's pointer
			// already holds (caller's default).
			continue
		}
		raw := row.Value
		if d.Encrypted || row.Encrypted {
			plain, err := decryptSecret(raw)
			if err != nil {
				return defaults, fmt.Errorf("decrypt setting %s.%s: %w", d.Type, d.Name, err)
			}
			raw = plain
		}
		if err := d.Unmarshal(raw); err != nil {
			return defaults, fmt.Errorf("decode setting %s.%s: %w", d.Type, d.Name, err)
		}
	}

	return applyUISettingsDefaults(out, defaults), nil
}

func (r *kvSettingsRepo) Save(ctx context.Context, s ports.UISettings) error {
	now := time.Now()
	descriptors := settingDescriptors(&s)
	rows := make([]settingRow, 0, len(descriptors))
	for _, d := range descriptors {
		raw, err := d.Marshal()
		if err != nil {
			return fmt.Errorf("encode setting %s.%s: %w", d.Type, d.Name, err)
		}
		if d.Encrypted {
			enc, err := encryptSecret(raw)
			if err != nil {
				return fmt.Errorf("encrypt setting %s.%s: %w", d.Type, d.Name, err)
			}
			raw = enc
		}
		rows = append(rows, settingRow{
			Type:      d.Type,
			Name:      d.Name,
			Value:     raw,
			Encrypted: d.Encrypted,
			UpdatedAt: now,
		})
	}
	// One transaction, one batched upsert per (type, name) — every row's
	// presence is independent so failure of any single one rolls the whole
	// batch back, matching the old single-row upsert's atomicity guarantee.
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return tx.Clauses(clause.OnConflict{
			Columns: []clause.Column{
				{Name: "type"}, {Name: "name"},
			},
			DoUpdates: clause.AssignmentColumns([]string{"value", "encrypted", "updated_at"}),
		}).Create(&rows).Error
	})
}

// settingDescriptor is one (type, name) pair backed by a typed UISettings
// field. Marshal serializes the field to the KV string representation;
// Unmarshal parses it back. Encrypted=true routes the value through
// secrets.go enc/dec helpers transparently.
type settingDescriptor struct {
	Type      string
	Name      string
	Encrypted bool
	Marshal   func() (string, error)
	Unmarshal func(raw string) error
}

// KnownSettingNames returns the set of KV `name`s the live settings layer
// recognizes (one per settingDescriptor). Exported so out-of-package writers
// that seed the settings table — notably the v3 migrator's copySettingsKV — can
// be drift-tested against the canonical key set, catching a key that was
// renamed in settingDescriptors but not in the writer (which would silently
// strand the value on a dead key).
func KnownSettingNames() map[string]bool {
	var s ports.UISettings
	descs := settingDescriptors(&s)
	out := make(map[string]bool, len(descs))
	for _, d := range descs {
		out[d.Name] = true
	}
	return out
}

// settingDescriptors returns the full mapping between UISettings fields and
// (type, name) KV cells. The order is the documented type-grouping order
// from docs/db-refactor-plan.md §3.2 so SQL browsers display them by
// category. To add a new field: declare it on UISettings, then add one
// line in this list.
func settingDescriptors(s *ports.UISettings) []settingDescriptor {
	return []settingDescriptor{
		// site --- branding / domain
		strField("site", "site_title", &s.SiteTitle),
		strField("site", "app_title", &s.AppTitle),
		strField("site", "icon_url", &s.IconURL),
		strField("site", "logo_url", &s.LogoURL),
		strField("site", "logo_url_dark", &s.LogoURLDark),
		strField("site", "footer_text", &s.FooterText),
		strField("site", "theme_color", &s.ThemeColor),
		strField("site", "email_domain", &s.EmailDomain),
		strField("site", "sub_base_url", &s.SubBaseURL),

		// auth --- JWT + login policy
		strField("auth", "login_mode", &s.LoginMode),
		strField("auth", "jwt_issuer", &s.JWTIssuer),
		intField("auth", "jwt_access_ttl_minutes", &s.JWTAccessTTLMinutes),
		intField("auth", "jwt_refresh_ttl_minutes", &s.JWTRefreshTTLMinutes),
		boolField("auth", "disallow_user_local_login", &s.DisallowUserLocalLogin),
		boolField("auth", "disallow_user_password_change", &s.DisallowUserPasswordChange),

		// sub --- subscription rendering & access
		strField("sub", "sub_path", &s.SubPath),
		intField("sub", "sub_update_interval_hours", &s.SubUpdateIntervalHours),
		strField("sub", "sub_profile_name_template", &s.SubProfileNameTemplate),
		boolField("sub", "sub_region_flag_prefix", &s.SubRegionFlagPrefix),
		boolField("sub", "sub_block_auto_disable", &s.SubBlockAutoDisable),
		intField("sub", "sub_block_auto_disable_count", &s.SubBlockAutoDisableCount),
		boolField("sub", "sub_block_notify_user", &s.SubBlockNotifyUser),
		intField("sub", "sub_block_notify_max_per_day", &s.SubBlockNotifyMaxPerDay),
		intField("sub", "sub_log_retention_days", &s.SubLogRetentionDays),
		intField("notify", "mail_sent_retention_days", &s.MailSentRetentionDays),
		strField("sub", "sub_import_tutorial_url", &s.SubImportTutorialURL),
		jsonField("sub", "sub_client_rules", &s.SubClientRules),     // legacy, read-only for one-time migration (v3.3.0 → drop v4.0.0)
		jsonField("sub", "sub_import_clients", &s.SubImportClients), // legacy, read-only for one-time migration (v3.3.0 → drop v4.0.0)
		jsonField("sub", "sub_clients", &s.SubClients),
		strField("sub", "sub_client_filter_mode", &s.SubClientFilterMode),

		// security --- rate limits, retentions, emergency access
		intField("security", "sub_per_ip_per_min", &s.SubPerIPPerMin),
		intField("security", "login_per_ip_per_min", &s.LoginPerIPPerMin),
		intField("security", "audit_retention_days", &s.AuditRetentionDays),
		intField("security", "sync_task_retention_days", &s.SyncTaskRetentionDays),
		intField("security", "traffic_history_days", &s.TrafficHistoryDays),
		boolField("security", "emergency_access_enabled", &s.EmergencyAccessEnabled),
		intField("security", "emergency_access_hours", &s.EmergencyAccessHours),
		intField("security", "emergency_access_max_count", &s.EmergencyAccessMaxCount),
		floatField("security", "emergency_access_quota_gb", &s.EmergencyAccessQuotaGB),

		// runtime --- cron / performance / tz / global toggles
		strField("runtime", "timezone", &s.Timezone),
		intField("runtime", "cron_traffic_pull_minutes", &s.CronTrafficPullMinutes),
		intField("runtime", "cron_reconcile_minutes", &s.CronReconcileMinutes),
		intField("runtime", "max_panel_concurrency", &s.MaxPanelConcurrency),
		boolField("runtime", "allow_user_personal_rules", &s.AllowUserPersonalRules),

		// notice --- user-facing portal widgets
		jsonField("notice", "quick_links", &s.QuickLinks),
		jsonField("notice", "global_announcement", &s.GlobalAnnouncement),

		// notify --- mail trigger thresholds (moved out of mail_settings)
		intField("notify", "expire_before_days", &s.ExpireBeforeDays),
		intField("notify", "traffic_remain_percent", &s.TrafficRemainPercent),

		// geo --- IP geolocation for access-log region display (offline .mmdb)
		boolField("geo", "geo_ip_enabled", &s.GeoIPEnabled),
		strField("geo", "geo_ip_db_file", &s.GeoIPDBFile),
		boolField("geo", "geo_ip_auto_update", &s.GeoIPAutoUpdate),
		strField("geo", "geo_ip_update_source", &s.GeoIPUpdateSource),
		encStrField("geo", "geo_ip_update_token", &s.GeoIPUpdateToken), // encrypted at rest
		strField("geo", "geo_ip_update_url", &s.GeoIPUpdateURL),
		strField("geo", "geo_ip_update_edition", &s.GeoIPUpdateEdition),
		intField("geo", "geo_ip_update_interval_hours", &s.GeoIPUpdateIntervalHours),
	}
}

// ---- Field helpers ----

func strField(typ, name string, dst *string) settingDescriptor {
	return settingDescriptor{
		Type: typ, Name: name,
		Marshal:   func() (string, error) { return *dst, nil },
		Unmarshal: func(raw string) error { *dst = raw; return nil },
	}
}

// encStrField is a string setting whose value is encrypted at rest (AES-GCM via
// secrets.go), the same as the SMTP/SAML/OIDC credential columns. Used for
// provider tokens stored in the KV table (e.g. geo_ip_token). The admin GET
// handler must still mask it (return has_* instead of the value).
func encStrField(typ, name string, dst *string) settingDescriptor {
	d := strField(typ, name, dst)
	d.Encrypted = true
	return d
}

func intField(typ, name string, dst *int) settingDescriptor {
	return settingDescriptor{
		Type: typ, Name: name,
		Marshal: func() (string, error) { return strconv.Itoa(*dst), nil },
		Unmarshal: func(raw string) error {
			if raw == "" {
				return nil
			}
			v, err := strconv.Atoi(raw)
			if err != nil {
				return err
			}
			*dst = v
			return nil
		},
	}
}

func floatField(typ, name string, dst *float64) settingDescriptor {
	return settingDescriptor{
		Type: typ, Name: name,
		Marshal: func() (string, error) { return strconv.FormatFloat(*dst, 'f', -1, 64), nil },
		Unmarshal: func(raw string) error {
			if raw == "" {
				return nil
			}
			v, err := strconv.ParseFloat(raw, 64)
			if err != nil {
				return err
			}
			*dst = v
			return nil
		},
	}
}

func boolField(typ, name string, dst *bool) settingDescriptor {
	return settingDescriptor{
		Type: typ, Name: name,
		Marshal: func() (string, error) {
			if *dst {
				return "1", nil
			}
			return "0", nil
		},
		Unmarshal: func(raw string) error {
			*dst = raw == "1" || raw == "true"
			return nil
		},
	}
}

func jsonField[T any](typ, name string, dst *T) settingDescriptor {
	return settingDescriptor{
		Type: typ, Name: name,
		Marshal: func() (string, error) {
			b, err := json.Marshal(*dst)
			if err != nil {
				return "", err
			}
			return string(b), nil
		},
		Unmarshal: func(raw string) error {
			if raw == "" || raw == "null" {
				return nil
			}
			return json.Unmarshal([]byte(raw), dst)
		},
	}
}

// ---- Defaults ----
//
// applyUISettingsDefaults fills in unset fields on the loaded settings.
// Runs on every Load (record found, record missing, table empty) so the
// panel always boots with sane runtime values — tickers and rate limiters
// panic on zero/negative intervals.
func applyUISettingsDefaults(out, defaults ports.UISettings) ports.UISettings {
	if out.LoginMode == "" {
		out.LoginMode = defaults.LoginMode
	}
	if out.SiteTitle == "" {
		out.SiteTitle = defaults.SiteTitle
	}
	if out.AppTitle == "" {
		out.AppTitle = defaults.AppTitle
		if out.AppTitle == "" {
			out.AppTitle = out.SiteTitle
		}
	}
	if out.EmailDomain == "" {
		out.EmailDomain = defaults.EmailDomain
	}
	if out.SubBaseURL == "" {
		out.SubBaseURL = defaults.SubBaseURL
	}
	if out.CronTrafficPullMinutes <= 0 {
		out.CronTrafficPullMinutes = 5
	}
	if out.CronReconcileMinutes <= 0 {
		out.CronReconcileMinutes = 15
	}
	if out.JWTAccessTTLMinutes <= 0 {
		// 60 min: halves the leak window of an access token vs the original 2h
		// default while still cheap on /refresh frequency (one extra HTTP round-
		// trip per hour per active session — negligible at self-use scale).
		out.JWTAccessTTLMinutes = 60
	}
	if out.JWTRefreshTTLMinutes <= 0 {
		// 1 day (was 7d): an idle session that didn't touch /refresh for 24h
		// must log in again. With sliding refresh still on (every /refresh
		// reissues a fresh refresh token alongside the access token), active
		// users still effectively never log out — only true abandonment hits
		// this ceiling. Tightens the absolute compromised-refresh-token
		// window from a week to a day at no UX cost for daily users.
		out.JWTRefreshTTLMinutes = 60 * 24
	}
	if out.JWTIssuer == "" {
		out.JWTIssuer = "passwall-sub-panel"
	}
	if out.SubPerIPPerMin <= 0 {
		out.SubPerIPPerMin = 60
	}
	if out.LoginPerIPPerMin <= 0 {
		out.LoginPerIPPerMin = 10
	}
	if out.SubPath == "" {
		out.SubPath = "sub"
	}
	if out.SubLogRetentionDays <= 0 {
		out.SubLogRetentionDays = 7
	}
	if out.MailSentRetentionDays <= 0 {
		out.MailSentRetentionDays = 30
	}
	if out.AuditRetentionDays <= 0 {
		out.AuditRetentionDays = 30
	}
	if out.SyncTaskRetentionDays <= 0 {
		out.SyncTaskRetentionDays = 30
	}
	if out.TrafficHistoryDays <= 0 {
		// 730 (2y) gives the chart's longest "last 1 year" range a full year of
		// breathing room — querying exactly N days back when retention is also N
		// days lets the prune trim a day off the front edge and produce a
		// blank-ish bucket. 2x the longest range avoids that. Cheap to keep: the
		// only charted tiers (user/node hourly) are small at this scale; the
		// large per-client tier is no longer rolled up at all. 0 is coerced here
		// (no "keep forever" mode), so the prune's `> 0` guard never sees a 0.
		out.TrafficHistoryDays = 730
	}
	if out.GeoIPUpdateSource == "" {
		// MaxMind GeoLite2-City is the recommended default: best free global
		// city-level detail. Inert until GeoIPEnabled + GeoIPAutoUpdate +
		// a license key are configured.
		out.GeoIPUpdateSource = "maxmind"
	}
	if out.GeoIPUpdateEdition == "" {
		out.GeoIPUpdateEdition = "GeoLite2-City"
	}
	if out.GeoIPUpdateIntervalHours < 1 {
		// Floor at 1h so a stray 0 (or a careless setting) can't turn the
		// updater into a tight loop against MaxMind / DB-IP. 12h default.
		out.GeoIPUpdateIntervalHours = 12
	}
	// Unified client registry (v3.3.0). When absent, either migrate the legacy
	// two-table config (sub_client_rules + sub_import_clients) in place — a
	// one-time best-effort fold, see sub_clients_legacy.go — or seed fresh
	// defaults. The legacy fields above stay readable solely for this.
	if out.SubClients == nil {
		if migrated := migrateLegacySubClients(out.SubClientRules, out.SubImportClients); migrated != nil {
			out.SubClients = migrated
		} else {
			out.SubClients = defaultSubClients()
		}
	}
	if out.SubClientFilterMode == "" {
		out.SubClientFilterMode = "blacklist"
	}
	if out.SubBlockAutoDisableCount <= 0 {
		out.SubBlockAutoDisableCount = 3
	}
	if out.SubBlockNotifyMaxPerDay <= 0 {
		out.SubBlockNotifyMaxPerDay = 1
	}
	if out.SubUpdateIntervalHours <= 0 {
		out.SubUpdateIntervalHours = 24
	}
	if out.FooterText == "" {
		out.FooterText = "© Kazuha Hub Passwall"
	}
	if out.ExpireBeforeDays <= 0 {
		out.ExpireBeforeDays = 3
	}
	if out.TrafficRemainPercent <= 0 {
		out.TrafficRemainPercent = 10
	}
	return out
}

// defaultSubClients returns the unified client registry seeded on first run:
// detection families (UA keywords + render format) each owning the import apps
// shown in the portal. Apps don't repeat the render format — it's the
// family's, served by UA. The deep-link / install strings are the audited
// values carried over from the previous import-client defaults. (v3.3.0)
func defaultSubClients() []ports.SubClientFamily {
	return []ports.SubClientFamily{
		{
			Name: "Clash / mihomo", Keywords: []string{"clash", "mihomo", "meta"}, RenderFormat: "mihomo", Enabled: true,
			Apps: []ports.SubClientApp{
				{
					Name:              "Clash Verge Rev",
					Platforms:         []string{"windows", "macos", "linux"},
					ImportURLTemplate: "clash://install-config?url={{ sub_url_encoded }}&name={{ profile_name_encoded }}",
					InstallURL:        "https://github.com/clash-verge-rev/clash-verge-rev/releases",
					Enabled:           true, Sort: 10, RecommendedFor: []string{"windows", "macos", "linux"},
				},
				{
					Name:              "Clash Meta for Android",
					Platforms:         []string{"android"},
					ImportURLTemplate: "clash://install-config?url={{ sub_url_encoded }}&name={{ profile_name_encoded }}&update-interval={{ sub_update_interval_minutes }}",
					InstallURL:        "https://github.com/MetaCubeX/ClashMetaForAndroid/releases",
					Enabled:           true, Sort: 20, RecommendedFor: []string{"android"},
				},
				{
					Name:              "Clash Mi",
					Platforms:         []string{"windows", "macos", "linux", "android", "ios"},
					ImportURLTemplate: "clashmi://install-config?url={{ sub_url_encoded }}&name={{ profile_name_encoded }}",
					InstallURL:        "https://github.com/KaringX/clashmi/releases",
					Enabled:           true, Sort: 25, RecommendedFor: []string{"ios"},
				},
			},
		},
		{
			// "karing" keyword so the Karing app (sing-box engine) is detected
			// as sing-box rather than falling through to "other".
			Name: "sing-box", Keywords: []string{"sing-box", "karing"}, RenderFormat: "sing-box", Enabled: true,
			Apps: []ports.SubClientApp{
				{
					Name:              "sing-box",
					Platforms:         []string{"ios", "macos", "android"},
					ImportURLTemplate: "sing-box://import-remote-profile?url={{ sub_url_encoded }}#{{ profile_name_encoded }}",
					InstallURL:        "https://sing-box.sagernet.org/clients/",
					Enabled:           true, Sort: 40,
				},
				{
					Name:              "Karing",
					Platforms:         []string{"windows", "macos", "linux", "android", "ios"},
					ImportURLTemplate: "karing://install-config?url={{ sub_url_encoded }}&name={{ profile_name_encoded }}",
					InstallURL:        "https://github.com/KaringX/karing/releases",
					Enabled:           true, Sort: 65,
				},
			},
		},
		// NOTE: Surge / Loon / Surfboard are intentionally NOT seeded. They only
		// consume Surge's proprietary .conf, which the panel doesn't emit, so
		// they can't use our subscription at all (verified against official
		// docs). An unmatched UA falls back to mihomo in Detect and is never
		// blocked, so leaving them out costs those clients nothing.
		{
			Name: "Shadowrocket", Keywords: []string{"shadowrocket"}, RenderFormat: "uri-list", Enabled: true,
			Apps: []ports.SubClientApp{
				{
					Name:              "Shadowrocket",
					Platforms:         []string{"ios"},
					ImportURLTemplate: "shadowrocket://add/sub://{{ sub_url_b64_url_safe }}?remark={{ profile_name_encoded }}",
					InstallURL:        "https://apps.apple.com/app/shadowrocket/id932747118",
					Enabled:           true, Sort: 60,
				},
			},
		},
		// Quantumult X accepts Clash YAML in its [server_remote] block, so
		// mihomo imports cleanly (rules/policies dropped). No bundled importer.
		// Its UA is "Quantumult%20X/..." (the space is %20), so the keyword has
		// to be the bare "quantumult" — "quantumult x" / "quantumultx" never
		// match the encoded UA (verified against subconverter's UA table).
		{Name: "Quantumult X", Keywords: []string{"quantumult"}, RenderFormat: "mihomo", Enabled: true},
		{
			// V2rayNG before V2RayN: the more specific "v2rayng" must win
			// substring matching, else "v2rayn" would eat it.
			Name: "V2rayNG", Keywords: []string{"v2rayng"}, RenderFormat: "uri-list", Enabled: true,
			Apps: []ports.SubClientApp{
				{
					Name:              "V2rayNG",
					Platforms:         []string{"android"},
					ImportURLTemplate: "v2rayng://install-sub?url={{ sub_url_encoded }}#{{ profile_name_encoded }}",
					InstallURL:        "https://github.com/2dust/v2rayNG/releases",
					Enabled:           true, Sort: 55,
				},
			},
		},
		{
			Name: "V2RayN", Keywords: []string{"v2rayn", "v2ray"}, RenderFormat: "uri-list", Enabled: true,
			Apps: []ports.SubClientApp{
				{
					Name:              "V2rayN",
					Platforms:         []string{"windows"},
					ImportURLTemplate: "{{ sub_url }}?remarks={{ profile_name_encoded }}",
					InstallURL:        "https://github.com/2dust/v2rayN/releases",
					Enabled:           true, Sort: 50,
				},
			},
		},
		{Name: "Passwall (OpenWrt)", Keywords: []string{"passwall"}, RenderFormat: "uri-list", Enabled: true},
		{
			Name: "Stash", Keywords: []string{"stash"}, RenderFormat: "mihomo", Enabled: true,
			Apps: []ports.SubClientApp{
				{
					Name:              "Stash",
					Platforms:         []string{"ios"},
					ImportURLTemplate: "stash://install-config?url={{ sub_url_encoded }}",
					InstallURL:        "https://apps.apple.com/app/stash-rule-based-proxy/id1596063349",
					Enabled:           true, Sort: 30,
				},
			},
		},
	}
}
