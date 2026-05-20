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
		intField("sub", "sub_log_retention_days", &s.SubLogRetentionDays),
		intField("notify", "mail_sent_retention_days", &s.MailSentRetentionDays),
		strField("sub", "sub_import_tutorial_url", &s.SubImportTutorialURL),
		jsonField("sub", "sub_client_rules", &s.SubClientRules),
		jsonField("sub", "sub_import_clients", &s.SubImportClients),

		// security --- rate limits, retentions, emergency access
		intField("security", "sub_per_ip_per_min", &s.SubPerIPPerMin),
		intField("security", "login_per_ip_per_min", &s.LoginPerIPPerMin),
		intField("security", "audit_retention_days", &s.AuditRetentionDays),
		intField("security", "sync_task_retention_days", &s.SyncTaskRetentionDays),
		intField("security", "traffic_history_days", &s.TrafficHistoryDays),
		boolField("security", "emergency_access_enabled", &s.EmergencyAccessEnabled),
		intField("security", "emergency_access_hours", &s.EmergencyAccessHours),
		intField("security", "emergency_access_max_count", &s.EmergencyAccessMaxCount),
		intField("security", "emergency_access_quota_gb", &s.EmergencyAccessQuotaGB),

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
		out.JWTAccessTTLMinutes = 120
	}
	if out.JWTRefreshTTLMinutes <= 0 {
		out.JWTRefreshTTLMinutes = 60 * 24 * 7
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
		// 730 (2y) gives the chart's "last 1 year" range a full year of
		// breathing room — querying exactly N days back when retention is
		// also N days lets the rollup prune trim a day off the front edge
		// and produce a blank-ish bucket. 2x the longest range avoids that.
		out.TrafficHistoryDays = 730
	}
	if out.SubClientRules == nil {
		out.SubClientRules = defaultSubClientRules()
	}
	if out.SubImportClients == nil {
		out.SubImportClients = defaultSubImportClients()
	}
	if out.SubBlockAutoDisableCount <= 0 {
		out.SubBlockAutoDisableCount = 3
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

// defaultSubClientRules returns the default subscription client detection rules.
func defaultSubClientRules() []ports.SubClientRule {
	return []ports.SubClientRule{
		{Name: "Clash / mihomo", Keywords: []string{"clash", "mihomo", "meta"}, RenderFormat: "mihomo", Enabled: true},
		{Name: "sing-box", Keywords: []string{"sing-box"}, RenderFormat: "sing-box", Enabled: true},
		// Surge uses its own .conf format and does NOT parse Clash YAML
		// as a subscription (the panel previously mapped it to mihomo,
		// which produced unimportable output). URI list is the
		// least-bad fallback — Surge can at least consume individual
		// proxy URIs even though it can't apply rules/groups from it.
		{Name: "Surge", Keywords: []string{"surge"}, RenderFormat: "uri-list", Enabled: true},
		// Shadowrocket's native subscription format is the V2RayN
		// base64-encoded URI list. It does NOT parse Clash YAML.
		// The panel's own one-click import uses shadowrocket://add/sub/<b64>
		// which is precisely the URI list scheme.
		{Name: "Shadowrocket", Keywords: []string{"shadowrocket"}, RenderFormat: "uri-list", Enabled: true},
		// Loon is Surge-derived (.conf format, no Clash YAML parser).
		// Same uri-list fallback rationale as Surge.
		{Name: "Loon", Keywords: []string{"loon"}, RenderFormat: "uri-list", Enabled: true},
		// Quantumult X DOES accept Clash YAML in its [server_remote]
		// block (per the official sample.conf and Sub-Store docs),
		// so mihomo is acceptable here — rules/policies are dropped
		// but nodes import cleanly.
		{Name: "Quantumult X", Keywords: []string{"quantumult x", "quantumultx"}, RenderFormat: "mihomo", Enabled: true},
		// V2rayNG (Android) — UA "v2rayNG/<ver>". Placed BEFORE V2RayN so
		// the more specific "v2rayng" keyword wins substring matching;
		// otherwise V2rayN's "v2rayn" keyword would eat V2rayNG too and
		// the two apps couldn't be enabled/disabled independently.
		{Name: "V2rayNG", Keywords: []string{"v2rayng"}, RenderFormat: "uri-list", Enabled: true},
		// V2rayN (Windows desktop) — UA "v2rayN/<ver>". Both consume the
		// base64 URI list (vless:// / vmess:// / trojan:// / ss:// lines).
		{Name: "V2RayN", Keywords: []string{"v2rayn", "v2ray"}, RenderFormat: "uri-list", Enabled: true},
		// OpenWrt Passwall plugin subscriber. Same base64 URI list format —
		// Passwall's UA contains "Passwall" verbatim.
		{Name: "Passwall (OpenWrt)", Keywords: []string{"passwall"}, RenderFormat: "uri-list", Enabled: true},
		{Name: "Stash", Keywords: []string{"stash"}, RenderFormat: "mihomo", Enabled: true},
		{Name: "Surfboard", Keywords: []string{"surfboard"}, RenderFormat: "mihomo", Enabled: true},
	}
}

// defaultSubImportClients returns user-facing one-click import targets.
func defaultSubImportClients() []ports.SubImportClient {
	return []ports.SubImportClient{
		{
			Name:         "Clash Verge Rev",
			Platforms:    []string{"windows", "macos", "linux"},
			RenderFormat: "mihomo",
			// scheme.rs reads `name=` first, then falls back to
			// Content-Disposition.filename* (which we already send).
			// Setting both means the name is known before the response
			// is fetched and there's no round-trip dependency.
			ImportURLTemplate: "clash://install-config?url={{ sub_url_encoded }}&name={{ profile_name_encoded }}",
			InstallURL:        "https://github.com/clash-verge-rev/clash-verge-rev/releases",
			Enabled:           true,
			Sort:              10,
			RecommendedFor:    []string{"windows", "macos", "linux"},
		},
		{
			Name:         "Clash Meta for Android",
			Platforms:    []string{"android"},
			RenderFormat: "mihomo",
			// update-interval (MINUTES) comes from CMfA PR #732, distinct from
			// the Profile-Update-Interval HTTP header (HOURS). Both units are
			// correct per CMfA's source. &name= is read by ExternalControlActivity
			// and used as the profile remark — CMfA doesn't parse Profile-Title
			// or Content-Disposition, so this is the ONLY way the panel can
			// influence the imported name.
			ImportURLTemplate: "clash://install-config?url={{ sub_url_encoded }}&name={{ profile_name_encoded }}&update-interval={{ sub_update_interval_minutes }}",
			InstallURL:        "https://github.com/MetaCubeX/ClashMetaForAndroid/releases",
			Enabled:           true,
			Sort:              20,
			RecommendedFor:    []string{"android"},
		},
		{
			Name:         "Clash Mi",
			Platforms:    []string{"windows", "macos", "linux", "android", "ios"},
			RenderFormat: "mihomo",
			// clashmi:// — ClashMi's iOS Info.plist registers `clash`, `clashmi`,
			// `clashmeta` and `flclash`. Using `clashmi://` keeps iOS from
			// offering Stash (which owns clash://) when both apps are installed.
			//
			// &name=... is read by SchemeHandler in lib/screens/scheme_handler.dart
			// and passed as the `remark` into ProfileManager.addRemote — without
			// it ClashMi falls back to scraping the panel root's HTML <title>,
			// which is the same string for every user and visually collides with
			// the URL-hashCode-based on-disk filename ClashMi shows below it.
			ImportURLTemplate: "clashmi://install-config?url={{ sub_url_encoded }}&name={{ profile_name_encoded }}",
			InstallURL:        "https://github.com/KaringX/clashmi/releases",
			Enabled:           true,
			Sort:              25,
			RecommendedFor:    []string{"ios"},
		},
		{
			Name:              "Stash",
			Platforms:         []string{"ios"},
			RenderFormat:      "mihomo",
			ImportURLTemplate: "stash://install-config?url={{ sub_url_encoded }}",
			InstallURL:        "https://apps.apple.com/app/stash-rule-based-proxy/id1596063349",
			Enabled:           true,
			Sort:              30,
		},
		{
			Name:              "sing-box",
			Platforms:         []string{"ios", "macos", "android"},
			RenderFormat:      "sing-box",
			ImportURLTemplate: "sing-box://import-remote-profile?url={{ sub_url_encoded }}#{{ profile_name_encoded }}",
			InstallURL:        "https://sing-box.sagernet.org/clients/",
			Enabled:           true,
			Sort:              40,
		},
		{
			// V2rayN (Windows) has no URL scheme — the panel UI copies
			// the template to clipboard and the user pastes into the
			// "Add subscription from clipboard" dialog. AddSubItem
			// (ConfigHandler.cs:1920) reads `?remarks=` from the URL's
			// query string and uses it as the subscription Remarks;
			// fragment / JSON / `name|url` forms are all ignored. Sub
			// URLs don't carry other query params, so a bare
			// `?remarks=` is safe — no `&` ambiguity.
			Name:              "V2rayN",
			Platforms:         []string{"windows"},
			RenderFormat:      "uri-list",
			ImportURLTemplate: "{{ sub_url }}?remarks={{ profile_name_encoded }}",
			InstallURL:        "https://github.com/2dust/v2rayN/releases",
			Enabled:           true,
			Sort:              50,
		},
		{
			// V2rayNG (Android) install-sub intent: `url` param is URL-encoded
			// (NOT base64 — base64 forms are silently rejected). The remark
			// comes from the OUTER URL's #fragment, not a `name=` query param
			// — UrlSchemeActivity.kt reads uri.fragment, not
			// getQueryParameter("name"). Setting `&name=` is a no-op (every
			// pre-2026 panel that ships that form has silently been losing
			// the remark for years). See 2dust/v2rayNG@master and
			// EZ_THEME/MiSub/ppanel-web for the convergent fragment form.
			Name:              "V2rayNG",
			Platforms:         []string{"android"},
			RenderFormat:      "uri-list",
			ImportURLTemplate: "v2rayng://install-sub?url={{ sub_url_encoded }}#{{ profile_name_encoded }}",
			InstallURL:        "https://github.com/2dust/v2rayNG/releases",
			Enabled:           true,
			Sort:              55,
		},
		{
			// Shadowrocket (iOS). The dominant production form (Xboard /
			// v2board themes / EZ_THEME / MiSub) is
			//   shadowrocket://add/sub://<b64>?remark=<name>
			// where the inner sub:// is a NESTED URI scheme, not a path
			// segment, and <b64> is URL-safe base64 with padding
			// stripped. Path-form `add/sub/<b64>` (rare; 3x-ui-only)
			// silently no-ops on real Shadowrocket builds — confirmed
			// by user testing.
			Name:              "Shadowrocket",
			Platforms:         []string{"ios"},
			RenderFormat:      "uri-list",
			ImportURLTemplate: "shadowrocket://add/sub://{{ sub_url_b64_url_safe }}?remark={{ profile_name_encoded }}",
			InstallURL:        "https://apps.apple.com/app/shadowrocket/id932747118",
			Enabled:           true,
			Sort:              60,
		},
		{
			// Karing is the sister sing-box app to Clash Mi from the same
			// author (KaringX). karing:// is unique so it won't collide with
			// other iOS proxy apps.
			Name:              "Karing",
			Platforms:         []string{"windows", "macos", "linux", "android", "ios"},
			RenderFormat:      "sing-box",
			ImportURLTemplate: "karing://install-config?url={{ sub_url_encoded }}&name={{ profile_name_encoded }}",
			InstallURL:        "https://github.com/KaringX/karing/releases",
			Enabled:           true,
			Sort:              65,
		},
	}
}

