package handler

import (
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/jwtutil"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/paneltz"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/captcha"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/geo"
)

// AdminSettingsHandler exposes /api/admin/settings/ui — every runtime-editable
// preference (branding, login mode, email domains, cron cadence, JWT TTLs,
// rate limits, audit retention). All values are persisted in the DB; the
// YAML config file is intentionally not consulted here.
type AdminSettingsHandler struct {
	repo      ports.SettingsRepo
	jwtParams *jwtutil.ParamsCache
}

func NewAdminSettingsHandler(repo ports.SettingsRepo, jwtParams *jwtutil.ParamsCache) *AdminSettingsHandler {
	return &AdminSettingsHandler{repo: repo, jwtParams: jwtParams}
}

type settingsDTO struct {
	LoginMode                  string                   `json:"login_mode"`
	SiteTitle                  string                   `json:"site_title"`
	AppTitle                   string                   `json:"app_title"`
	IconURL                    string                   `json:"icon_url"`
	LogoURL                    string                   `json:"logo_url"`
	LogoURLDark                string                   `json:"logo_url_dark"`
	EmailDomain                string                   `json:"email_domain"`
	AuditRetentionDays         int                      `json:"audit_retention_days"`
	SubBaseURL                 string                   `json:"sub_base_url"`
	Timezone                   string                   `json:"timezone"`
	CronTrafficPullMinutes     int                      `json:"cron_traffic_pull_minutes"`
	CronReconcileMinutes       int                      `json:"cron_reconcile_minutes"`
	MaxPanelConcurrency        int                      `json:"max_panel_concurrency"`
	JWTAccessTTLMinutes        int                      `json:"jwt_access_ttl_minutes"`
	JWTRefreshTTLMinutes       int                      `json:"jwt_refresh_ttl_minutes"`
	JWTIssuer                  string                   `json:"jwt_issuer"`
	SubPerIPPerMin             int                      `json:"sub_per_ip_per_min"`
	LoginPerIPPerMin           int                      `json:"login_per_ip_per_min"`
	SyncTaskRetentionDays      int                      `json:"sync_task_retention_days"`
	TrafficHistoryDays         int                      `json:"traffic_history_days"`
	DisallowUserLocalLogin     bool                     `json:"disallow_user_local_login"`
	DisallowUserPasswordChange bool                     `json:"disallow_user_password_change"`
	AllowUserPersonalRules     bool                     `json:"allow_user_personal_rules"`
	EmergencyAccessEnabled     bool                     `json:"emergency_access_enabled"`
	EmergencyAccessHours       int                      `json:"emergency_access_hours"`
	EmergencyAccessMaxCount    int                      `json:"emergency_access_max_count"`
	EmergencyAccessQuotaGB     float64                  `json:"emergency_access_quota_gb"`
	SubPath                    string                   `json:"sub_path"`
	SubClients                 []ports.SubClientFamily  `json:"sub_clients"`
	SubClientFilterMode        string                   `json:"sub_client_filter_mode"`
	SubImportTutorialURL       string                   `json:"sub_import_tutorial_url"`
	SubLogRetentionDays        int                      `json:"sub_log_retention_days"`
	MailSentRetentionDays      int                      `json:"mail_sent_retention_days"`
	AuthEventRetentionDays     int                      `json:"auth_event_retention_days"`
	SubBlockAutoDisable        bool                     `json:"sub_block_auto_disable"`
	SubBlockAutoDisableCount   int                      `json:"sub_block_auto_disable_count"`
	SubBlockNotifyUser         bool                     `json:"sub_block_notify_user"`
	SubBlockNotifyMaxPerDay    int                      `json:"sub_block_notify_max_per_day"`
	SubUpdateIntervalHours     int                      `json:"sub_update_interval_hours"`
	SubProfileNameTemplate     string                   `json:"sub_profile_name_template"`
	SubRegionFlagPrefix        bool                     `json:"sub_region_flag_prefix"`
	QuickLinks                 []ports.QuickLink        `json:"quick_links"`
	GlobalAnnouncement         ports.GlobalAnnouncement `json:"global_announcement"`
	FooterText                 string                   `json:"footer_text"`
	ThemeColor                 string                   `json:"theme_color"`
	// Notify thresholds (moved from mail_settings to settings KV type='notify').
	ExpireBeforeDays     int `json:"expire_before_days"`
	TrafficRemainPercent int `json:"traffic_remain_percent"`
	// Geo IP (access-log region display, offline .mmdb).
	GeoIPEnabled             bool   `json:"geo_ip_enabled"`
	GeoIPDBFile              string `json:"geo_ip_db_file"`
	GeoIPAutoUpdate          bool   `json:"geo_ip_auto_update"`
	GeoIPUpdateSource        string `json:"geo_ip_update_source"`
	GeoIPUpdateURL           string `json:"geo_ip_update_url"`
	GeoIPUpdateEdition       string `json:"geo_ip_update_edition"`
	GeoIPUpdateIntervalHours int    `json:"geo_ip_update_interval_hours"`
	// GeoIPUpdateToken is write-only: accepted on PUT (empty = keep existing),
	// never echoed on GET. HasGeoIPUpdateToken reports whether one is set.
	GeoIPUpdateToken    string `json:"geo_ip_update_token,omitempty"`
	HasGeoIPUpdateToken bool   `json:"has_geo_ip_update_token"`
	// PSP-managed certificate automation (v3.6.4).
	CertRenewBeforeDays         int    `json:"cert_renew_before_days"`
	CertRenewCheckIntervalHours int    `json:"cert_renew_check_interval_hours"`
	ACMEEmail                   string `json:"acme_email"`
	ACMEDirectoryURL            string `json:"acme_directory_url"`
	// Login security: CAPTCHA + account lockout (v3.7.0).
	CaptchaEnabled       bool   `json:"captcha_enabled"`
	CaptchaProvider      string `json:"captcha_provider"`
	CaptchaTrigger       string `json:"captcha_trigger"`
	CaptchaFailThreshold int    `json:"captcha_fail_threshold"`
	CaptchaSiteKey       string `json:"captcha_site_key"`
	// CaptchaSecretKey is write-only: accepted on PUT (empty = keep existing),
	// never echoed on GET. HasCaptchaSecretKey reports whether one is set.
	CaptchaSecretKey       string `json:"captcha_secret_key,omitempty"`
	HasCaptchaSecretKey    bool   `json:"has_captcha_secret_key"`
	CaptchaRegisterEnabled bool   `json:"captcha_register_enabled"`
	CaptchaForgotEnabled   bool   `json:"captcha_forgot_enabled"`
	LockoutEnabled         bool   `json:"lockout_enabled"`
	LockoutThreshold       int    `json:"lockout_threshold"`
	LockoutWindowMinutes   int    `json:"lockout_window_minutes"`
	LockoutDurationMinutes int    `json:"lockout_duration_minutes"`
	LockoutScope           string `json:"lockout_scope"`
	// Self-service password recovery (v3.7.0).
	PasswordRecoveryEnabled  bool   `json:"password_recovery_enabled"`
	PasswordRecoveryDelivery string `json:"password_recovery_delivery"`
	// Self-service registration (v3.7.0). require_email_verification is the
	// positive form of the stored RegistrationAllowUnverified.
	RegistrationEnabled                  bool    `json:"registration_enabled"`
	RegistrationRequireEmailVerification bool    `json:"registration_require_email_verification"`
	RegistrationEmailDomains             string  `json:"registration_email_domains"`
	RegistrationDefaultGroupID           int64   `json:"registration_default_group_id"`
	RegistrationDelivery                 string  `json:"registration_delivery"`
	RegistrationDefaultTrafficGB         float64 `json:"registration_default_traffic_gb"`
	RegistrationDefaultExpireDays        int     `json:"registration_default_expire_days"`
	// Two-factor authentication (TOTP) master switch (v3.7.0). Off disables new
	// enrollment panel-wide; it does NOT silently strip 2FA from already-enrolled
	// accounts (their login challenge still applies until they disable it).
	TOTPEnabled bool `json:"totp_enabled"`
	// Passkeys / WebAuthn (v3.7.0). PasskeyEnabled lets local accounts register
	// passkeys; PasskeyPasswordless additionally allows usernameless passkey login.
	PasskeyEnabled      bool `json:"passkey_enabled"`
	PasskeyPasswordless bool `json:"passkey_passwordless"`
	// Email-OTP as an opt-in weaker 2FA fallback at the login challenge (v3.7.0),
	// on top of the factors the account already has (TOTP / passkey / recovery).
	// Default off.
	TwoFAAllowEmail    bool `json:"twofa_allow_email"`
	Require2FAForStaff bool `json:"require_2fa_for_staff"`
}

func (h *AdminSettingsHandler) defaults() ports.UISettings {
	// Leave IconURL / Logo URLs blank intentionally — the frontend has a
	// built-in DEFAULT_ICON fallback (see web-react/src/stores/site.ts).
	// Filling them here would persist a panel-shipped path as if the admin
	// had picked it, making it impossible to fall back to the bundled icon.
	return ports.UISettings{
		LoginMode:   "dual",
		SiteTitle:   "Kazuha Hub Passwall",
		AppTitle:    "Passwall",
		EmailDomain: "psp.local",
	}
}

func (h *AdminSettingsHandler) Get(c *gin.Context) {
	s, err := h.repo.Load(c.Request.Context(), h.defaults())
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, settingsToDTO(s))
}

// settingsToDTO maps stored UISettings into the wire DTO. It is the SINGLE
// source of truth for BOTH the GET and PUT responses — keeping one builder means
// a newly-added field can't be echoed by one path and silently dropped by the
// other (the drift that briefly broke the 2FA totp_enabled round-trip).
func settingsToDTO(s ports.UISettings) settingsDTO {
	return settingsDTO{
		LoginMode:                  s.LoginMode,
		SiteTitle:                  s.SiteTitle,
		AppTitle:                   s.AppTitle,
		IconURL:                    s.IconURL,
		LogoURL:                    s.LogoURL,
		LogoURLDark:                s.LogoURLDark,
		EmailDomain:                s.EmailDomain,
		AuditRetentionDays:         s.AuditRetentionDays,
		SubBaseURL:                 s.SubBaseURL,
		Timezone:                   s.Timezone,
		CronTrafficPullMinutes:     s.CronTrafficPullMinutes,
		CronReconcileMinutes:       s.CronReconcileMinutes,
		MaxPanelConcurrency:        s.MaxPanelConcurrency,
		JWTAccessTTLMinutes:        s.JWTAccessTTLMinutes,
		JWTRefreshTTLMinutes:       s.JWTRefreshTTLMinutes,
		JWTIssuer:                  s.JWTIssuer,
		SubPerIPPerMin:             s.SubPerIPPerMin,
		LoginPerIPPerMin:           s.LoginPerIPPerMin,
		SyncTaskRetentionDays:      s.SyncTaskRetentionDays,
		TrafficHistoryDays:         s.TrafficHistoryDays,
		DisallowUserLocalLogin:     s.DisallowUserLocalLogin,
		DisallowUserPasswordChange: s.DisallowUserPasswordChange,
		AllowUserPersonalRules:     s.AllowUserPersonalRules,
		EmergencyAccessEnabled:     s.EmergencyAccessEnabled,
		EmergencyAccessHours:       s.EmergencyAccessHours,
		EmergencyAccessMaxCount:    s.EmergencyAccessMaxCount,
		EmergencyAccessQuotaGB:     s.EmergencyAccessQuotaGB,
		SubPath:                    s.SubPath,
		SubClients:                 s.SubClients,
		SubClientFilterMode:        s.SubClientFilterMode,
		SubImportTutorialURL:       s.SubImportTutorialURL,
		SubLogRetentionDays:        s.SubLogRetentionDays,
		MailSentRetentionDays:      s.MailSentRetentionDays,
		AuthEventRetentionDays:     s.AuthEventRetentionDays,
		SubBlockAutoDisable:        s.SubBlockAutoDisable,
		SubBlockAutoDisableCount:   s.SubBlockAutoDisableCount,
		SubBlockNotifyUser:         s.SubBlockNotifyUser,
		SubBlockNotifyMaxPerDay:    s.SubBlockNotifyMaxPerDay,
		SubUpdateIntervalHours:     s.SubUpdateIntervalHours,
		SubProfileNameTemplate:     s.SubProfileNameTemplate,
		SubRegionFlagPrefix:        s.SubRegionFlagPrefix,
		QuickLinks:                 s.QuickLinks,
		GlobalAnnouncement:         s.GlobalAnnouncement,
		FooterText:                 s.FooterText,
		ThemeColor:                 s.ThemeColor,
		ExpireBeforeDays:           s.ExpireBeforeDays,
		TrafficRemainPercent:       s.TrafficRemainPercent,
		GeoIPEnabled:               s.GeoIPEnabled,
		GeoIPDBFile:                s.GeoIPDBFile,
		GeoIPAutoUpdate:            s.GeoIPAutoUpdate,
		GeoIPUpdateSource:          s.GeoIPUpdateSource,
		GeoIPUpdateURL:             s.GeoIPUpdateURL,
		GeoIPUpdateEdition:         s.GeoIPUpdateEdition,
		GeoIPUpdateIntervalHours:   s.GeoIPUpdateIntervalHours,
		CertRenewBeforeDays:         s.CertRenewBeforeDays,
		CertRenewCheckIntervalHours: s.CertRenewCheckIntervalHours,
		ACMEEmail:                   s.ACMEEmail,
		ACMEDirectoryURL:            s.ACMEDirectoryURL,
		// Update token masked: never echoed, only presence reported.
		HasGeoIPUpdateToken: strings.TrimSpace(s.GeoIPUpdateToken) != "",
		CaptchaEnabled:       s.CaptchaEnabled,
		CaptchaProvider:      s.CaptchaProvider,
		CaptchaTrigger:       s.CaptchaTrigger,
		CaptchaFailThreshold: s.CaptchaFailThreshold,
		CaptchaSiteKey:       s.CaptchaSiteKey,
		// Secret key masked: never echoed, only presence reported.
		HasCaptchaSecretKey:    strings.TrimSpace(s.CaptchaSecretKey) != "",
		CaptchaRegisterEnabled: s.CaptchaRegisterEnabled,
		CaptchaForgotEnabled:   s.CaptchaForgotEnabled,
		LockoutEnabled:         s.LockoutEnabled,
		LockoutThreshold:       s.LockoutThreshold,
		LockoutWindowMinutes:   s.LockoutWindowMinutes,
		LockoutDurationMinutes: s.LockoutDurationMinutes,
		LockoutScope:           s.LockoutScope,
		PasswordRecoveryEnabled:  s.PasswordRecoveryEnabled,
		PasswordRecoveryDelivery: s.PasswordRecoveryDelivery,
		RegistrationEnabled:                  s.RegistrationEnabled,
		RegistrationRequireEmailVerification: !s.RegistrationAllowUnverified,
		RegistrationEmailDomains:             s.RegistrationEmailDomains,
		RegistrationDefaultGroupID:           s.RegistrationDefaultGroupID,
		RegistrationDelivery:                 s.RegistrationDelivery,
		RegistrationDefaultTrafficGB:         s.RegistrationDefaultTrafficGB,
		RegistrationDefaultExpireDays:        s.RegistrationDefaultExpireDays,
		TOTPEnabled:                          s.TOTPEnabled,
		PasskeyEnabled:                       s.PasskeyEnabled,
		PasskeyPasswordless:                  s.PasskeyPasswordless,
		TwoFAAllowEmail:                      s.TwoFAAllowEmail,
		Require2FAForStaff:                   s.Require2FAForStaff,
	}
}

func (h *AdminSettingsHandler) Put(c *gin.Context) {
	var req settingsDTO
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	switch req.LoginMode {
	case "sso_redirect", "sso_first", "dual", "local_only":
		// valid
	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": "Login_mode must be sso_redirect | sso_first | dual | local_only"})
		return
	}
	// Load the prior state so normalizeGlobalAnnouncement can decide
	// whether to bump UpdatedAt (only on meaningful change).
	prev, prevErr := h.repo.Load(c.Request.Context(), h.defaults())
	if prevErr != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": prevErr.Error()})
		return
	}
	s := ports.UISettings{
		LoginMode:                  req.LoginMode,
		SiteTitle:                  req.SiteTitle,
		AppTitle:                   req.AppTitle,
		IconURL:                    strings.TrimSpace(req.IconURL),
		LogoURL:                    req.LogoURL,
		LogoURLDark:                req.LogoURLDark,
		EmailDomain:                strings.TrimSpace(req.EmailDomain),
		AuditRetentionDays:         req.AuditRetentionDays,
		SubBaseURL:                 strings.TrimRight(strings.TrimSpace(req.SubBaseURL), "/"),
		Timezone:                   strings.TrimSpace(req.Timezone),
		CronTrafficPullMinutes:     req.CronTrafficPullMinutes,
		CronReconcileMinutes:       req.CronReconcileMinutes,
		MaxPanelConcurrency:        req.MaxPanelConcurrency,
		JWTAccessTTLMinutes:        req.JWTAccessTTLMinutes,
		JWTRefreshTTLMinutes:       req.JWTRefreshTTLMinutes,
		JWTIssuer:                  strings.TrimSpace(req.JWTIssuer),
		SubPerIPPerMin:             req.SubPerIPPerMin,
		LoginPerIPPerMin:           req.LoginPerIPPerMin,
		SyncTaskRetentionDays:      req.SyncTaskRetentionDays,
		TrafficHistoryDays:         req.TrafficHistoryDays,
		DisallowUserLocalLogin:     req.DisallowUserLocalLogin,
		DisallowUserPasswordChange: req.DisallowUserPasswordChange,
		AllowUserPersonalRules:     req.AllowUserPersonalRules,
		EmergencyAccessEnabled:     req.EmergencyAccessEnabled,
		EmergencyAccessHours:       req.EmergencyAccessHours,
		EmergencyAccessMaxCount:    req.EmergencyAccessMaxCount,
		EmergencyAccessQuotaGB:     req.EmergencyAccessQuotaGB,
		SubPath:                    strings.TrimSpace(req.SubPath),
		SubClients:                 normalizeSubClients(req.SubClients),
		SubClientFilterMode:        normalizeFilterMode(req.SubClientFilterMode),
		SubImportTutorialURL:       strings.TrimSpace(req.SubImportTutorialURL),
		SubLogRetentionDays:        req.SubLogRetentionDays,
		MailSentRetentionDays:      req.MailSentRetentionDays,
		AuthEventRetentionDays:     req.AuthEventRetentionDays,
		SubBlockAutoDisable:        req.SubBlockAutoDisable,
		SubBlockAutoDisableCount:   req.SubBlockAutoDisableCount,
		SubBlockNotifyUser:         req.SubBlockNotifyUser,
		SubBlockNotifyMaxPerDay:    req.SubBlockNotifyMaxPerDay,
		SubUpdateIntervalHours:     req.SubUpdateIntervalHours,
		SubProfileNameTemplate:     strings.TrimSpace(req.SubProfileNameTemplate),
		SubRegionFlagPrefix:        req.SubRegionFlagPrefix,
		QuickLinks:                 normalizeQuickLinks(req.QuickLinks),
		GlobalAnnouncement:         normalizeGlobalAnnouncement(req.GlobalAnnouncement, prev.GlobalAnnouncement),
		FooterText:                 strings.TrimSpace(req.FooterText),
		ThemeColor:                 strings.TrimSpace(req.ThemeColor),
		ExpireBeforeDays:           req.ExpireBeforeDays,
		TrafficRemainPercent:       req.TrafficRemainPercent,
		GeoIPEnabled:               req.GeoIPEnabled,
		GeoIPDBFile:                strings.TrimSpace(req.GeoIPDBFile),
		GeoIPAutoUpdate:            req.GeoIPAutoUpdate,
		GeoIPUpdateSource:          strings.TrimSpace(req.GeoIPUpdateSource),
		GeoIPUpdateURL:             strings.TrimSpace(req.GeoIPUpdateURL),
		GeoIPUpdateEdition:         strings.TrimSpace(req.GeoIPUpdateEdition),
		GeoIPUpdateIntervalHours:   req.GeoIPUpdateIntervalHours,
		CertRenewBeforeDays:         req.CertRenewBeforeDays,
		CertRenewCheckIntervalHours: req.CertRenewCheckIntervalHours,
		// ACMEEmail/ACMEDirectoryURL are legacy (multi-account moved them to the
		// acme_accounts table). No UI control writes them; carry the stored value
		// over so it survives as a prefill hint for the first ACME-account form.
		ACMEEmail:        prev.ACMEEmail,
		ACMEDirectoryURL: prev.ACMEDirectoryURL,
		CaptchaEnabled:              req.CaptchaEnabled,
		CaptchaProvider:             strings.ToLower(strings.TrimSpace(req.CaptchaProvider)),
		CaptchaTrigger:              strings.ToLower(strings.TrimSpace(req.CaptchaTrigger)),
		CaptchaFailThreshold:        req.CaptchaFailThreshold,
		CaptchaSiteKey:              strings.TrimSpace(req.CaptchaSiteKey),
		CaptchaRegisterEnabled:      req.CaptchaRegisterEnabled,
		CaptchaForgotEnabled:        req.CaptchaForgotEnabled,
		LockoutEnabled:              req.LockoutEnabled,
		LockoutThreshold:            req.LockoutThreshold,
		LockoutWindowMinutes:        req.LockoutWindowMinutes,
		LockoutDurationMinutes:      req.LockoutDurationMinutes,
		LockoutScope:                strings.ToLower(strings.TrimSpace(req.LockoutScope)),
		PasswordRecoveryEnabled:     req.PasswordRecoveryEnabled,
		PasswordRecoveryDelivery:    strings.ToLower(strings.TrimSpace(req.PasswordRecoveryDelivery)),
		RegistrationEnabled:           req.RegistrationEnabled,
		RegistrationAllowUnverified:   !req.RegistrationRequireEmailVerification,
		RegistrationEmailDomains:      strings.TrimSpace(req.RegistrationEmailDomains),
		RegistrationDefaultGroupID:    req.RegistrationDefaultGroupID,
		RegistrationDelivery:          strings.ToLower(strings.TrimSpace(req.RegistrationDelivery)),
		RegistrationDefaultTrafficGB:  req.RegistrationDefaultTrafficGB,
		RegistrationDefaultExpireDays: req.RegistrationDefaultExpireDays,
		TOTPEnabled:                   req.TOTPEnabled,
		PasskeyEnabled:                req.PasskeyEnabled,
		PasskeyPasswordless:           req.PasskeyPasswordless,
		TwoFAAllowEmail:               req.TwoFAAllowEmail,
		Require2FAForStaff:            req.Require2FAForStaff,
		// GeoIPUpdateToken / CaptchaSecretKey resolved below ("empty = keep existing").
	}
	// Update token is write-only: a blank field on save means the admin didn't
	// re-enter the masked token, so preserve the stored one (mirrors the
	// SMTP/SSO secret-handling pattern).
	if strings.TrimSpace(req.GeoIPUpdateToken) == "" {
		s.GeoIPUpdateToken = prev.GeoIPUpdateToken
	} else {
		s.GeoIPUpdateToken = strings.TrimSpace(req.GeoIPUpdateToken)
	}
	// Captcha secret key is write-only too: blank on save = keep the stored one
	// (mirrors the GeoIP/SMTP/SSO secret-handling pattern).
	if strings.TrimSpace(req.CaptchaSecretKey) == "" {
		s.CaptchaSecretKey = prev.CaptchaSecretKey
	} else {
		s.CaptchaSecretKey = strings.TrimSpace(req.CaptchaSecretKey)
	}
	// Validate login-security enums only when set (empty = the settings layer
	// fills the default on Load, so blanks are fine). captcha.IsValidProvider is
	// the SAME set captcha.Verify dispatches on, so the API can't store a
	// provider the verifier would reject.
	if s.CaptchaProvider != "" && !captcha.IsValidProvider(s.CaptchaProvider) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Captcha_provider must be image, turnstile, recaptcha, or hcaptcha"})
		return
	}
	if s.CaptchaTrigger != "" && s.CaptchaTrigger != "always" && s.CaptchaTrigger != "after_failures" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Captcha_trigger must be always or after_failures"})
		return
	}
	if s.LockoutScope != "" && s.LockoutScope != "ip" && s.LockoutScope != "ip_upn" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Lockout_scope must be ip or ip_upn"})
		return
	}
	if s.PasswordRecoveryDelivery != "" && s.PasswordRecoveryDelivery != "link" && s.PasswordRecoveryDelivery != "otp" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Password_recovery_delivery must be link or otp"})
		return
	}
	// Link-delivery recovery emails a reset URL, which we build only from the
	// configured canonical base URL (never the request Host header, to avoid
	// reset-link poisoning). So that combination requires sub_base_url to be set
	// — reject it up front instead of silently never sending. (OTP delivery
	// emails a code and needs no URL, so it's exempt.)
	if s.PasswordRecoveryEnabled && s.PasswordRecoveryDelivery != "otp" && strings.TrimSpace(s.SubBaseURL) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Password recovery with link delivery requires the subscription base URL (sub_base_url) to be set first"})
		return
	}
	if s.RegistrationDelivery != "" && s.RegistrationDelivery != "link" && s.RegistrationDelivery != "otp" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Registration_delivery must be link or otp"})
		return
	}
	if s.RegistrationDefaultTrafficGB < 0 || s.RegistrationDefaultExpireDays < 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Registration default traffic / expiry must be >= 0"})
		return
	}
	// Like recovery: the email-verify LINK is built only from the configured
	// canonical base URL, so verification-required + link delivery needs it set.
	if s.RegistrationEnabled && !s.RegistrationAllowUnverified && s.RegistrationDelivery != "otp" && strings.TrimSpace(s.SubBaseURL) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Registration email verification with link delivery requires the subscription base URL (sub_base_url) to be set first"})
		return
	}
	if s.CaptchaFailThreshold < 0 || s.LockoutThreshold < 0 ||
		s.LockoutWindowMinutes < 0 || s.LockoutDurationMinutes < 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Login-security thresholds must be >= 0"})
		return
	}
	// Upper-bound the lockout minute fields so a fat-fingered huge value can't
	// approach the time.Duration overflow boundary (the guard also saturates, but
	// reject early for clear admin feedback). 10 years of minutes is plenty.
	const maxLockoutMinutes = 10 * 365 * 24 * 60
	if s.LockoutWindowMinutes > maxLockoutMinutes || s.LockoutDurationMinutes > maxLockoutMinutes {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Lockout window/duration is unreasonably large"})
		return
	}
	// A token captcha provider can't verify anything without its secret key, so
	// reject "enabled but unverifiable": captcha must never be on yet silently
	// inert. s.CaptchaSecretKey is already resolved (kept-or-new), so a
	// previously-stored secret left blank on this save still passes.
	if s.CaptchaEnabled && s.CaptchaProvider != "" && s.CaptchaProvider != captcha.ProviderImage &&
		strings.TrimSpace(s.CaptchaSecretKey) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "A secret key is required for token captcha providers"})
		return
	}
	// Single source of truth: geo.IsValidUpdateSource is the SAME set
	// geo.Update (candidateURLs) can actually download, so the API can't accept
	// a source the downloader rejects (or vice-versa). dbip was previously
	// missing from this whitelist, so selecting "DB-IP City Lite" in the UI 400'd
	// even though the downloader supported it.
	if !geo.IsValidUpdateSource(s.GeoIPUpdateSource) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Geo_ip_update_source must be maxmind, dbip, ipinfo, or custom"})
		return
	}
	if s.AuditRetentionDays < 0 || s.SyncTaskRetentionDays < 0 || s.AuthEventRetentionDays < 0 {
		// 0 = keep forever (never prune) for AuthEventRetentionDays, matching the
		// repo's freely-editable semantics; only negative is rejected.
		c.JSON(http.StatusBadRequest, gin.H{"error": "Retention days must be >= 0"})
		return
	}
	if s.CronTrafficPullMinutes < 0 || s.CronReconcileMinutes < 0 ||
		s.JWTAccessTTLMinutes < 0 || s.JWTRefreshTTLMinutes < 0 ||
		s.SubPerIPPerMin < 0 || s.LoginPerIPPerMin < 0 ||
		s.EmergencyAccessHours < 0 || s.EmergencyAccessMaxCount < 0 || s.EmergencyAccessQuotaGB < 0 ||
		s.SubLogRetentionDays < 0 || s.SubBlockAutoDisableCount < 0 ||
		s.SubUpdateIntervalHours < 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Runtime tuning values must be >= 0"})
		return
	}
	if s.EmergencyAccessEnabled && (s.EmergencyAccessHours <= 0 || s.EmergencyAccessMaxCount <= 0) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Emergency access hours and max count must be > 0 when enabled"})
		return
	}
	// Validate timezone up front so admin gets immediate feedback if the
	// IANA name the browser offered isn't in the panel's Go tzdata. Without
	// this, save succeeds silently and paneltz.Location falls back to
	// time.Local at use time — admin can't tell their pick was rejected.
	if err := paneltz.Validate(s.Timezone); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Unknown timezone " + s.Timezone + " — panel can't resolve this IANA name"})
		return
	}
	if s.EmailDomain == "" {
		s.EmailDomain = "psp.local"
	}
	if s.SiteTitle == "" {
		s.SiteTitle = "Kazuha Hub Passwall"
	}
	if s.AppTitle == "" {
		s.AppTitle = "Passwall"
	}
	// IconURL intentionally left as the admin set it (possibly empty); the
	// frontend has a built-in fallback. Forcing a default here would prevent
	// admins from clearing a stale icon back to the bundled default.
	if s.SubPath == "" {
		s.SubPath = "sub"
	}
	if err := h.repo.Save(c.Request.Context(), s); err != nil {
		respondError(c, err)
		return
	}
	h.jwtParams.Store(jwtutil.Params{
		AccessTTL:  time.Duration(s.JWTAccessTTLMinutes) * time.Minute,
		RefreshTTL: time.Duration(s.JWTRefreshTTLMinutes) * time.Minute,
		Issuer:     s.JWTIssuer,
	})
	c.JSON(http.StatusOK, settingsToDTO(s))
}

func normalizeQuickLinks(links []ports.QuickLink) []ports.QuickLink {
	out := make([]ports.QuickLink, 0, len(links))
	for _, link := range links {
		link.Label = strings.TrimSpace(link.Label)
		link.URL = strings.TrimSpace(link.URL)
		link.Icon = strings.TrimSpace(link.Icon)
		link.Description = strings.TrimSpace(link.Description)
		link.Group = strings.TrimSpace(link.Group)
		if link.Label == "" || link.URL == "" {
			continue
		}
		out = append(out, link)
	}
	return out
}

// normalizeGlobalAnnouncement cleans up the incoming payload and decides
// whether UpdatedAt needs to be bumped to "now". The bump matters because
// the user portal uses UpdatedAt as the localStorage key for the "don't
// remind again" dismissal — keeping the same stamp would mean a freshly
// edited notice never re-appears for visitors who muted the previous one.
//
// Rule: bump UpdatedAt when any visible field (title/content/level/popup)
// or the enabled flag changes vs the previously stored announcement. A
// pure no-op save keeps the old timestamp so quiet "save" clicks don't
// surprise users with a re-popup.
func normalizeGlobalAnnouncement(a, prev ports.GlobalAnnouncement) ports.GlobalAnnouncement {
	a.Title = strings.TrimSpace(a.Title)
	a.Content = strings.TrimSpace(a.Content)
	a.Level = strings.ToLower(strings.TrimSpace(a.Level))
	switch a.Level {
	case "warning", "danger":
	default:
		a.Level = "info"
	}
	if a.Title == "" && a.Content == "" {
		a.Enabled = false
	}
	changed := a.Enabled != prev.Enabled ||
		a.Title != prev.Title ||
		a.Content != prev.Content ||
		a.Level != prev.Level ||
		a.Popup != prev.Popup
	if changed && a.Enabled {
		a.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	} else if a.UpdatedAt == "" {
		a.UpdatedAt = prev.UpdatedAt
	}
	return a
}

// normalizeFilterMode clamps the client filter mode to the two valid values;
// anything unrecognized falls back to blacklist (the safe, non-breaking default).
func normalizeFilterMode(m string) string {
	if m == "whitelist" {
		return "whitelist"
	}
	return "blacklist"
}

// normalizeSubClients cleans the unified client registry from the admin form:
// each family gets a trimmed/deduped keyword set and a valid render format;
// each app gets normalized platforms / recommended-for and incomplete apps are
// dropped. Families with an empty name are dropped; a family with no apps is
// kept (detection-only, e.g. Surge).
func normalizeSubClients(families []ports.SubClientFamily) []ports.SubClientFamily {
	out := make([]ports.SubClientFamily, 0, len(families))
	for _, fam := range families {
		fam.Name = strings.TrimSpace(fam.Name)
		if fam.Name == "" {
			continue
		}
		fam.RenderFormat = normalizeRenderFormat(fam.RenderFormat)
		kws := make([]string, 0, len(fam.Keywords))
		seenKw := map[string]bool{}
		for _, kw := range fam.Keywords {
			kw = strings.TrimSpace(kw)
			low := strings.ToLower(kw)
			if kw == "" || seenKw[low] {
				continue
			}
			seenKw[low] = true
			kws = append(kws, kw)
		}
		fam.Keywords = kws
		fam.Apps = normalizeSubClientApps(fam.Apps)
		out = append(out, fam)
	}
	return out
}

// normalizeRenderFormat clamps a free-form render-format string to one of the
// three the renderer understands; anything unrecognized defaults to mihomo.
func normalizeRenderFormat(f string) string {
	switch strings.TrimSpace(strings.ToLower(f)) {
	case "sing-box":
		return "sing-box"
	case "uri-list":
		return "uri-list"
	default:
		return "mihomo"
	}
}

func normalizeSubClientApps(apps []ports.SubClientApp) []ports.SubClientApp {
	out := make([]ports.SubClientApp, 0, len(apps))
	for _, a := range apps {
		a.Name = strings.TrimSpace(a.Name)
		a.ImportURLTemplate = strings.TrimSpace(a.ImportURLTemplate)
		a.InstallURL = strings.TrimSpace(a.InstallURL)
		platforms := make([]string, 0, len(a.Platforms))
		seen := map[string]bool{}
		for _, p := range a.Platforms {
			p = strings.ToLower(strings.TrimSpace(p))
			switch p {
			case "windows", "macos", "linux", "ios", "android", "other":
				if !seen[p] {
					seen[p] = true
					platforms = append(platforms, p)
				}
			}
		}
		a.Platforms = platforms
		// RecommendedFor must be a subset of the app's own Platforms — it makes
		// no sense to hero an Android-only app to desktop visitors. Drop any
		// platform not in the support list; dedupe.
		recFor := make([]string, 0, len(a.RecommendedFor))
		recSeen := map[string]bool{}
		for _, p := range a.RecommendedFor {
			p = strings.ToLower(strings.TrimSpace(p))
			if !seen[p] || recSeen[p] {
				continue
			}
			recSeen[p] = true
			recFor = append(recFor, p)
		}
		a.RecommendedFor = recFor
		if a.Name == "" || a.ImportURLTemplate == "" || len(a.Platforms) == 0 {
			continue
		}
		out = append(out, a)
	}
	return out
}
