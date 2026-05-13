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
			return defaults, nil
		}
		if err != nil {
			return defaults, err
		}
	}
	if row.ID == 0 {
		return defaults, nil
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
		SubPath:                    row.SubPath,
		SubClientRules:             row.SubClientRules.toDomain(),
		SubImportClients:           row.SubImportClients.toDomain(),
		SubLogRetentionDays:        row.SubLogRetentionDays,
		SubBlockAutoDisable:        row.SubBlockAutoDisable,
		SubBlockAutoDisableCount:   row.SubBlockAutoDisableCount,
		QuickLinks:                 row.QuickLinks.toDomain(),
		GlobalAnnouncement:         row.GlobalAnnouncement.toDomain(),
	}
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
	if out.IconURL == "" {
		out.IconURL = defaults.IconURL
	}
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
		out.LoginPerIPPerMin = 5
	}
	if out.SubPath == "" {
		out.SubPath = "sub"
	}
	if out.SubLogRetentionDays <= 0 {
		out.SubLogRetentionDays = 7
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
	return out, nil
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
		},
		{
			Name:              "Clash Meta for Android",
			Platforms:         []string{"android"},
			RenderFormat:      "mihomo",
			ImportURLTemplate: "clash://install-config?url={{ sub_url_encoded }}",
			InstallURL:        "https://github.com/MetaCubeX/ClashMetaForAndroid/releases",
			Enabled:           true,
			Sort:              20,
		},
		{
			Name:              "Clash Mi",
			Platforms:         []string{"windows", "macos", "linux", "android", "ios"},
			RenderFormat:      "mihomo",
			ImportURLTemplate: "clash://install-config?url={{ sub_url_encoded }}",
			InstallURL:        "https://github.com/KaringX/clashmi/releases",
			Enabled:           true,
			Sort:              25,
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
		SubPath:                    s.SubPath,
		SubClientRules:             jsonSubRulesFromDomain(s.SubClientRules),
		SubImportClients:           jsonSubImportClientsFromDomain(s.SubImportClients),
		SubLogRetentionDays:        s.SubLogRetentionDays,
		SubBlockAutoDisable:        s.SubBlockAutoDisable,
		SubBlockAutoDisableCount:   s.SubBlockAutoDisableCount,
		QuickLinks:                 jsonQuickLinksFromDomain(s.QuickLinks),
		GlobalAnnouncement:         jsonGlobalAnnouncementFromDomain(s.GlobalAnnouncement),
	}
	return r.db.WithContext(ctx).Clauses(clause.OnConflict{UpdateAll: true}).Create(&row).Error
}
