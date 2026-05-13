package handler

import (
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/jwtutil"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
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
	LoginMode                  string `json:"login_mode"`
	SiteTitle                  string `json:"site_title"`
	AppTitle                   string `json:"app_title"`
	IconURL                    string `json:"icon_url"`
	LogoURL                    string `json:"logo_url"`
	LogoURLDark                string `json:"logo_url_dark"`
	EmailDomain                string `json:"email_domain"`
	AuditRetentionDays         int    `json:"audit_retention_days"`
	SubBaseURL                 string `json:"sub_base_url"`
	CronTrafficPullMinutes     int    `json:"cron_traffic_pull_minutes"`
	CronReconcileMinutes       int    `json:"cron_reconcile_minutes"`
	JWTAccessTTLMinutes        int    `json:"jwt_access_ttl_minutes"`
	JWTRefreshTTLMinutes       int    `json:"jwt_refresh_ttl_minutes"`
	JWTIssuer                  string `json:"jwt_issuer"`
	SubPerIPPerMin             int    `json:"sub_per_ip_per_min"`
	LoginPerIPPerMin           int    `json:"login_per_ip_per_min"`
	SyncTaskRetentionDays      int    `json:"sync_task_retention_days"`
	DisallowUserLocalLogin     bool   `json:"disallow_user_local_login"`
	DisallowUserPasswordChange bool   `json:"disallow_user_password_change"`
	EmergencyAccessEnabled     bool   `json:"emergency_access_enabled"`
	EmergencyAccessHours       int    `json:"emergency_access_hours"`
	EmergencyAccessMaxCount    int    `json:"emergency_access_max_count"`
}

func (h *AdminSettingsHandler) defaults() ports.UISettings {
	return ports.UISettings{
		LoginMode:   "dual",
		SiteTitle:   "Passwall",
		AppTitle:    "Passwall",
		IconURL:     "/images/HeadPicture.png",
		EmailDomain: "psp.local",
	}
}

func (h *AdminSettingsHandler) Get(c *gin.Context) {
	s, err := h.repo.Load(c.Request.Context(), h.defaults())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	mode := s.LoginMode
	c.JSON(http.StatusOK, settingsDTO{
		LoginMode:                  mode,
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
	})
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
		c.JSON(http.StatusBadRequest, gin.H{"error": "login_mode must be sso_redirect | sso_first | dual | local_only"})
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
		CronTrafficPullMinutes:     req.CronTrafficPullMinutes,
		CronReconcileMinutes:       req.CronReconcileMinutes,
		JWTAccessTTLMinutes:        req.JWTAccessTTLMinutes,
		JWTRefreshTTLMinutes:       req.JWTRefreshTTLMinutes,
		JWTIssuer:                  strings.TrimSpace(req.JWTIssuer),
		SubPerIPPerMin:             req.SubPerIPPerMin,
		LoginPerIPPerMin:           req.LoginPerIPPerMin,
		SyncTaskRetentionDays:      req.SyncTaskRetentionDays,
		DisallowUserLocalLogin:     req.DisallowUserLocalLogin,
		DisallowUserPasswordChange: req.DisallowUserPasswordChange,
		EmergencyAccessEnabled:     req.EmergencyAccessEnabled,
		EmergencyAccessHours:       req.EmergencyAccessHours,
		EmergencyAccessMaxCount:    req.EmergencyAccessMaxCount,
	}
	if s.AuditRetentionDays < 0 || s.SyncTaskRetentionDays < 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "retention days must be >= 0"})
		return
	}
	if s.CronTrafficPullMinutes < 0 || s.CronReconcileMinutes < 0 ||
		s.JWTAccessTTLMinutes < 0 || s.JWTRefreshTTLMinutes < 0 ||
		s.SubPerIPPerMin < 0 || s.LoginPerIPPerMin < 0 ||
		s.EmergencyAccessHours < 0 || s.EmergencyAccessMaxCount < 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "runtime tuning values must be >= 0"})
		return
	}
	if s.EmergencyAccessEnabled && (s.EmergencyAccessHours <= 0 || s.EmergencyAccessMaxCount <= 0) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "emergency access hours and max count must be > 0 when enabled"})
		return
	}
	if s.EmailDomain == "" {
		s.EmailDomain = "psp.local"
	}
	if s.SiteTitle == "" {
		s.SiteTitle = "Passwall"
	}
	if s.AppTitle == "" {
		s.AppTitle = "Passwall"
	}
	if s.IconURL == "" {
		s.IconURL = "/images/HeadPicture.png"
	}
	if err := h.repo.Save(c.Request.Context(), s); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	h.jwtParams.Store(jwtutil.Params{
		AccessTTL:  time.Duration(s.JWTAccessTTLMinutes) * time.Minute,
		RefreshTTL: time.Duration(s.JWTRefreshTTLMinutes) * time.Minute,
		Issuer:     s.JWTIssuer,
	})
	c.JSON(http.StatusOK, settingsDTO{
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
	})
}
