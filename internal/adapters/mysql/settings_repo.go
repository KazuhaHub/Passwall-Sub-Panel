package mysql

import (
	"context"
	"errors"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

type settingsRepo struct{ db *gorm.DB }

func (r *settingsRepo) Load(ctx context.Context, defaults ports.UISettings) (ports.UISettings, error) {
	var row uiSettingsRow
	if err := r.db.WithContext(ctx).First(&row, 1).Error; err != nil {
		err = wrapNotFound(err)
		if errors.Is(err, domain.ErrNotFound) {
			return applyDefaults(defaults, defaults), nil
		}
		return defaults, err
	}
	if row.ID == 0 {
		return applyDefaults(defaults, defaults), nil
	}
	out := ports.UISettings{
		LoginMode:                  row.LoginMode,
		SiteTitle:                  row.SiteTitle,
		AppTitle:                   row.AppTitle,
		IconURL:                    row.IconURL,
		LogoURL:                    row.LogoURL,
		LogoURLDark:                row.LogoURLDark,
		EmailDomain:                row.EmailDomain,
		AuditRetentionDays:         row.AuditRetentionDays,
		SubBaseURL:                 row.SubBaseURL,
		CronTrafficPullMinutes:     row.CronTrafficPullMinutes,
		CronReconcileMinutes:       row.CronReconcileMinutes,
		JWTAccessTTLMinutes:        row.JWTAccessTTLMinutes,
		JWTRefreshTTLMinutes:       row.JWTRefreshTTLMinutes,
		JWTIssuer:                  row.JWTIssuer,
		SubPerIPPerMin:             row.SubPerIPPerMin,
		LoginPerIPPerMin:           row.LoginPerIPPerMin,
		SyncTaskRetentionDays:      row.SyncTaskRetentionDays,
		DisallowUserLocalLogin:     row.DisallowUserLocalLogin,
		DisallowUserPasswordChange: row.DisallowUserPasswordChange,
		EmergencyAccessEnabled:     row.EmergencyAccessEnabled,
		EmergencyAccessHours:       row.EmergencyAccessHours,
		EmergencyAccessMaxCount:    row.EmergencyAccessMaxCount,
		EmergencyAccessQuotaGB:     row.EmergencyAccessQuotaGB,
		SubPath:                    row.SubPath,
		SubClientRules:             row.SubClientRules.toDomain(),
		SubImportClients:           row.SubImportClients.toDomain(),
		SubImportTutorialURL:       row.SubImportTutorialURL,
		SubLogRetentionDays:        row.SubLogRetentionDays,
		SubBlockAutoDisable:        row.SubBlockAutoDisable,
		SubBlockAutoDisableCount:   row.SubBlockAutoDisableCount,
		SubUpdateIntervalHours:     row.SubUpdateIntervalHours,
		QuickLinks:                 row.QuickLinks.toDomain(),
		GlobalAnnouncement:         row.GlobalAnnouncement.toDomain(),
		FooterText:                 row.FooterText,
		ThemeColor:                 row.ThemeColor,
	}
	return applyDefaults(out, defaults), nil
}

// applyDefaults fills in unset fields on the loaded settings. Runs on every
// Load path (record found, record missing, table empty) so the panel always
// boots with sane runtime values — required because tickers and rate limiters
// panic on zero/negative intervals.
func applyDefaults(out, defaults ports.UISettings) ports.UISettings {
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
	// IconURL / LogoURL / LogoURLDark intentionally left blank when the admin
	// hasn't customized them — the frontend site store renders a built-in
	// fallback. Filling defaults here would surface the placeholder URL in the
	// settings form and confuse admins.
	if out.EmailDomain == "" {
		out.EmailDomain = defaults.EmailDomain
	}
	if out.SubBaseURL == "" {
		out.SubBaseURL = defaults.SubBaseURL
	}
	// Hardcoded fallbacks for runtime tuning fields. These preserve the
	// original defaults so an empty DB still produces a working panel.
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
	if out.AuditRetentionDays <= 0 {
		out.AuditRetentionDays = 30
	}
	if out.SyncTaskRetentionDays <= 0 {
		out.SyncTaskRetentionDays = 30
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
		out.FooterText = "© Passwall Sub Panel"
	}
	return out
}

// defaultSubClientRules returns the default subscription client detection rules.
func defaultSubClientRules() []ports.SubClientRule {
	return []ports.SubClientRule{
		{Name: "Clash / mihomo", Keywords: []string{"clash", "mihomo", "meta"}, RenderFormat: "mihomo", Enabled: true},
		{Name: "sing-box", Keywords: []string{"sing-box"}, RenderFormat: "sing-box", Enabled: true},
		{Name: "Surge", Keywords: []string{"surge"}, RenderFormat: "mihomo", Enabled: true},
		{Name: "Shadowrocket", Keywords: []string{"shadowrocket"}, RenderFormat: "mihomo", Enabled: true},
		{Name: "Loon", Keywords: []string{"loon"}, RenderFormat: "mihomo", Enabled: true},
		{Name: "Quantumult X", Keywords: []string{"quantumult x", "quantumultx"}, RenderFormat: "mihomo", Enabled: true},
		{Name: "V2RayN", Keywords: []string{"v2rayn", "v2ray"}, RenderFormat: "mihomo", Enabled: true},
		{Name: "Stash", Keywords: []string{"stash"}, RenderFormat: "mihomo", Enabled: true},
		{Name: "Surfboard", Keywords: []string{"surfboard"}, RenderFormat: "mihomo", Enabled: true},
	}
}

// defaultSubImportClients returns user-facing one-click import targets.
func defaultSubImportClients() []ports.SubImportClient {
	return []ports.SubImportClient{
		{
			Name:              "Clash Verge Rev",
			Platforms:         []string{"windows", "macos", "linux"},
			RenderFormat:      "mihomo",
			ImportURLTemplate: "clash://install-config?url={{ sub_url_encoded }}",
			InstallURL:        "https://github.com/clash-verge-rev/clash-verge-rev/releases",
			Enabled:           true,
			Sort:              10,
			RecommendedFor:    []string{"windows", "macos", "linux"},
		},
		{
			Name:              "Clash Meta for Android",
			Platforms:         []string{"android"},
			RenderFormat:      "mihomo",
			ImportURLTemplate: "clash://install-config?url={{ sub_url_encoded }}",
			InstallURL:        "https://github.com/MetaCubeX/ClashMetaForAndroid/releases",
			Enabled:           true,
			Sort:              20,
			RecommendedFor:    []string{"android"},
		},
		{
			Name:              "Clash Mi",
			Platforms:         []string{"windows", "macos", "linux", "android", "ios"},
			RenderFormat:      "mihomo",
			ImportURLTemplate: "clash://install-config?url={{ sub_url_encoded }}",
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
	}
}

func (r *settingsRepo) Save(ctx context.Context, s ports.UISettings) error {
	row := uiSettingsRow{
		ID:                         1,
		LoginMode:                  s.LoginMode,
		SiteTitle:                  s.SiteTitle,
		AppTitle:                   s.AppTitle,
		IconURL:                    s.IconURL,
		LogoURL:                    s.LogoURL,
		LogoURLDark:                s.LogoURLDark,
		EmailDomain:                s.EmailDomain,
		AuditRetentionDays:         s.AuditRetentionDays,
		SubBaseURL:                 s.SubBaseURL,
		CronTrafficPullMinutes:     s.CronTrafficPullMinutes,
		CronReconcileMinutes:       s.CronReconcileMinutes,
		JWTAccessTTLMinutes:        s.JWTAccessTTLMinutes,
		JWTRefreshTTLMinutes:       s.JWTRefreshTTLMinutes,
		JWTIssuer:                  s.JWTIssuer,
		SubPerIPPerMin:             s.SubPerIPPerMin,
		LoginPerIPPerMin:           s.LoginPerIPPerMin,
		SyncTaskRetentionDays:      s.SyncTaskRetentionDays,
		DisallowUserLocalLogin:     s.DisallowUserLocalLogin,
		DisallowUserPasswordChange: s.DisallowUserPasswordChange,
		EmergencyAccessEnabled:     s.EmergencyAccessEnabled,
		EmergencyAccessHours:       s.EmergencyAccessHours,
		EmergencyAccessMaxCount:    s.EmergencyAccessMaxCount,
		EmergencyAccessQuotaGB:     s.EmergencyAccessQuotaGB,
		SubPath:                    s.SubPath,
		SubClientRules:             jsonSubRulesFromDomain(s.SubClientRules),
		SubImportClients:           jsonSubImportClientsFromDomain(s.SubImportClients),
		SubImportTutorialURL:       s.SubImportTutorialURL,
		SubLogRetentionDays:        s.SubLogRetentionDays,
		SubBlockAutoDisable:        s.SubBlockAutoDisable,
		SubBlockAutoDisableCount:   s.SubBlockAutoDisableCount,
		SubUpdateIntervalHours:     s.SubUpdateIntervalHours,
		QuickLinks:                 jsonQuickLinksFromDomain(s.QuickLinks),
		GlobalAnnouncement:         jsonGlobalAnnouncementFromDomain(s.GlobalAnnouncement),
		FooterText:                 s.FooterText,
		ThemeColor:                 s.ThemeColor,
	}
	return r.db.WithContext(ctx).Clauses(clause.OnConflict{UpdateAll: true}).Create(&row).Error
}
