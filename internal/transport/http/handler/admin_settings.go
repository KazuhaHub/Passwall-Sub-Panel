package handler

import (
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/jwtutil"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/paneltz"
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
	TrafficHistoryDays           int                    `json:"traffic_history_days"`
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
	SubBlockAutoDisable        bool                     `json:"sub_block_auto_disable"`
	SubBlockAutoDisableCount   int                      `json:"sub_block_auto_disable_count"`
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
		SubBlockAutoDisable:        s.SubBlockAutoDisable,
		SubBlockAutoDisableCount:   s.SubBlockAutoDisableCount,
		SubUpdateIntervalHours:     s.SubUpdateIntervalHours,
		SubProfileNameTemplate:     s.SubProfileNameTemplate,
		SubRegionFlagPrefix:        s.SubRegionFlagPrefix,
		QuickLinks:                 s.QuickLinks,
		GlobalAnnouncement:         s.GlobalAnnouncement,
		FooterText:                 s.FooterText,
		ThemeColor:                 s.ThemeColor,
		ExpireBeforeDays:           s.ExpireBeforeDays,
		TrafficRemainPercent:       s.TrafficRemainPercent,
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
		SubBlockAutoDisable:        req.SubBlockAutoDisable,
		SubBlockAutoDisableCount:   req.SubBlockAutoDisableCount,
		SubUpdateIntervalHours:     req.SubUpdateIntervalHours,
		SubProfileNameTemplate:     strings.TrimSpace(req.SubProfileNameTemplate),
		SubRegionFlagPrefix:        req.SubRegionFlagPrefix,
		QuickLinks:                 normalizeQuickLinks(req.QuickLinks),
		GlobalAnnouncement:         normalizeGlobalAnnouncement(req.GlobalAnnouncement, prev.GlobalAnnouncement),
		FooterText:                 strings.TrimSpace(req.FooterText),
		ThemeColor:                 strings.TrimSpace(req.ThemeColor),
		ExpireBeforeDays:           req.ExpireBeforeDays,
		TrafficRemainPercent:       req.TrafficRemainPercent,
	}
	if s.AuditRetentionDays < 0 || s.SyncTaskRetentionDays < 0 {
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
		SubBlockAutoDisable:        s.SubBlockAutoDisable,
		SubBlockAutoDisableCount:   s.SubBlockAutoDisableCount,
		SubUpdateIntervalHours:     s.SubUpdateIntervalHours,
		SubProfileNameTemplate:     s.SubProfileNameTemplate,
		SubRegionFlagPrefix:        s.SubRegionFlagPrefix,
		QuickLinks:                 s.QuickLinks,
		GlobalAnnouncement:         s.GlobalAnnouncement,
		FooterText:                 s.FooterText,
		ThemeColor:                 s.ThemeColor,
		ExpireBeforeDays:           s.ExpireBeforeDays,
		TrafficRemainPercent:       s.TrafficRemainPercent,
	})
}

func normalizeQuickLinks(links []ports.QuickLink) []ports.QuickLink {
	out := make([]ports.QuickLink, 0, len(links))
	for _, link := range links {
		link.Label = strings.TrimSpace(link.Label)
		link.URL = strings.TrimSpace(link.URL)
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
