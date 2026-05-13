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
	return out, nil
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
	}
	return r.db.WithContext(ctx).Clauses(clause.OnConflict{UpdateAll: true}).Create(&row).Error
}
