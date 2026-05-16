package handler

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/traffic"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/user"
	"github.com/KazuhaHub/passwall-sub-panel/internal/transport/http/middleware"
)

// UserMeHandler exposes the end-user self-service endpoints under
// /api/user/me — view expiry / traffic, change password, reset sub_token.
type UserMeHandler struct {
	user     *user.Service
	traffic  *traffic.Service
	settings ports.SettingsRepo
}

func NewUserMeHandler(userSvc *user.Service, trafficSvc *traffic.Service, settings ports.SettingsRepo) *UserMeHandler {
	return &UserMeHandler{user: userSvc, traffic: trafficSvc, settings: settings}
}

func (h *UserMeHandler) Profile(c *gin.Context) {
	claims := middleware.ClaimsFrom(c)
	if claims == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "No auth"})
		return
	}
	u, err := h.user.Get(c.Request.Context(), claims.UserID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "User not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	// Server-side derived: hide the "change password" affordance for SSO
	// users (no password to begin with) and for non-admin local users when
	// the admin has flipped DisallowUserPasswordChange on. Admins always
	// keep the option as a break-glass path.
	canChangePassword := u.HasLocalPassword()
	settings, settingsErr := h.settings.Load(c.Request.Context(), ports.UISettings{})
	if canChangePassword && u.Role != domain.RoleAdmin {
		if settingsErr == nil && settings.DisallowUserPasswordChange {
			canChangePassword = false
		}
	}
	var emergencyStatus user.EmergencyAccessStatus
	if settingsErr == nil {
		emergencyStatus = user.EmergencyAccessStatusForUserWithTrafficLimit(u, settings, time.Now(), h.trafficLimitExceeded(c.Request.Context(), u))
	}
	c.JSON(http.StatusOK, gin.H{
		"id":                      u.ID,
		"display_name":            u.DisplayName,
		"upn":                     u.UPN,
		"sub_url":                 h.subURL(c.Request, u.SubToken),
		"sub_import_clients":      enabledSubImportClients(settings.SubImportClients),
		"sub_import_tutorial_url": settings.SubImportTutorialURL,
		"quick_links":             enabledQuickLinks(settings.QuickLinks),
		"global_announcement":     visibleGlobalAnnouncement(settings.GlobalAnnouncement),
		"expire_at":               u.ExpireAt,
		"traffic_limit_bytes":     u.TrafficLimitBytes,
		"traffic_reset_period":    u.TrafficResetPeriod,
		"enabled":                 u.Enabled,
		"can_change_password":     canChangePassword,
		// can_edit_personal_rules drives the portal's rules dialog: when
		// false the textarea renders read-only and the Save button hides.
		// Admins always can; for non-admins it follows the global flag.
		"can_edit_personal_rules": u.Role == domain.RoleAdmin || (settingsErr == nil && settings.AllowUserPersonalRules),
		"emergency_access": gin.H{
			"enabled":         emergencyStatus.Enabled,
			"available":       emergencyStatus.Available,
			"status":          emergencyStatus.Status,
			"reason":          emergencyStatus.Reason,
			"duration_hours":  emergencyStatus.DurationHours,
			"max_count":       emergencyStatus.MaxCount,
			"used_count":      emergencyStatus.UsedCount,
			"remaining":       emergencyStatus.Remaining,
			"emergency_until": emergencyStatus.Until,
			"quota_bytes":     emergencyStatus.QuotaBytes,
			"used_bytes":      emergencyStatus.UsedBytes,
		},
	})
}

func enabledSubImportClients(clients []ports.SubImportClient) []ports.SubImportClient {
	out := make([]ports.SubImportClient, 0, len(clients))
	for _, c := range clients {
		if c.Enabled {
			out = append(out, c)
		}
	}
	return out
}

func enabledQuickLinks(links []ports.QuickLink) []ports.QuickLink {
	out := make([]ports.QuickLink, 0, len(links))
	for _, link := range links {
		if link.Enabled {
			out = append(out, link)
		}
	}
	return out
}

func visibleGlobalAnnouncement(a ports.GlobalAnnouncement) *ports.GlobalAnnouncement {
	if !a.Enabled || (strings.TrimSpace(a.Title) == "" && strings.TrimSpace(a.Content) == "") {
		return nil
	}
	return &a
}

func (h *UserMeHandler) EmergencyAccess(c *gin.Context) {
	claims := middleware.ClaimsFrom(c)
	if claims == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "No auth"})
		return
	}
	u, err := h.user.Get(c.Request.Context(), claims.UserID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "User not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	res, err := h.user.UseEmergencyAccess(c.Request.Context(), claims.UserID, h.trafficLimitExceeded(c.Request.Context(), u))
	if err != nil {
		switch {
		case errors.Is(err, domain.ErrNotFound):
			c.JSON(http.StatusNotFound, gin.H{"error": "User not found"})
		case errors.Is(err, domain.ErrForbidden):
			c.JSON(http.StatusForbidden, gin.H{"error": err.Error()})
		case errors.Is(err, domain.ErrValidation):
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		default:
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		}
		return
	}
	if h.user.HasPendingSync(c.Request.Context(), claims.UserID) {
		c.Header("X-Sync-Pending", "1")
	}
	settings, _ := h.settings.Load(c.Request.Context(), ports.UISettings{})
	c.JSON(http.StatusOK, gin.H{
		"expire_at":       res.User.ExpireAt,
		"emergency_until": res.User.EmergencyUntil,
		"extended_from":   res.ExtendedFrom,
		"extended_until":  res.ExtendedUntil,
		"used_count":      res.UsedCount,
		"max_count":       res.MaxCount,
		"remaining":       res.Remaining,
		"quota_bytes":     int64(settings.EmergencyAccessQuotaGB) * 1024 * 1024 * 1024,
		"used_bytes":      int64(0), // window just opened — UsedBytes is always 0 right after grant
		"sync_pending":    h.user.HasPendingSync(c.Request.Context(), claims.UserID),
	})
}

func (h *UserMeHandler) trafficLimitExceeded(ctx context.Context, u *domain.User) bool {
	if h == nil || h.traffic == nil || u == nil || u.TrafficLimitBytes <= 0 {
		return false
	}
	report, err := h.traffic.ReportFor(ctx, u.ID)
	return err == nil && report != nil && report.PeriodUsedBytes >= u.TrafficLimitBytes
}

func (h *UserMeHandler) Traffic(c *gin.Context) {
	claims := middleware.ClaimsFrom(c)
	if claims == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "No auth"})
		return
	}
	report, err := h.traffic.ReportFor(c.Request.Context(), claims.UserID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"user_id":               report.UserID,
		"permanent_total_bytes": report.PermanentTotalBytes,
		"period_used_bytes":     report.PeriodUsedBytes,
		"today_used_bytes":      report.TodayUsedBytes,
	})
}

func (h *UserMeHandler) TrafficHistory(c *gin.Context) {
	claims := middleware.ClaimsFrom(c)
	if claims == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "No auth"})
		return
	}
	period, since, until, err := parseTrafficHistoryQuery(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	report, err := h.traffic.HistoryFor(c.Request.Context(), claims.UserID, period, since, until)
	if err != nil {
		switch {
		case errors.Is(err, domain.ErrValidation):
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		default:
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		}
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"scope":   "user",
		"user_id": report.UserID,
		"period":  report.Period,
		"since":   report.Since,
		"until":   report.Until,
		"items":   historyItems(report.Items),
	})
}

func (h *UserMeHandler) GetRules(c *gin.Context) {
	claims := middleware.ClaimsFrom(c)
	if claims == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "No auth"})
		return
	}
	u, err := h.user.Get(c.Request.Context(), claims.UserID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "User not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"personal_rules": u.PersonalRules})
}

type updatePersonalRulesRequest struct {
	PersonalRules string `json:"personal_rules"`
}

func (h *UserMeHandler) PutRules(c *gin.Context) {
	claims := middleware.ClaimsFrom(c)
	if claims == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "No auth"})
		return
	}
	// Global lock: when the admin has turned off the personal-rules editor
	// the user can still GET (read) but PUT is rejected. Mirrors the
	// DisallowUserPasswordChange gate one method below.
	settings, sErr := h.settings.Load(c.Request.Context(), ports.UISettings{})
	if sErr == nil && !settings.AllowUserPersonalRules {
		c.JSON(http.StatusForbidden, gin.H{"error": "Personal rules editing is disabled by admin"})
		return
	}
	var req updatePersonalRulesRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := h.user.SetPersonalRules(c.Request.Context(), claims.UserID, req.PersonalRules); err != nil {
		switch {
		case errors.Is(err, domain.ErrNotFound):
			c.JSON(http.StatusNotFound, gin.H{"error": "User not found"})
		case errors.Is(err, domain.ErrValidation):
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		default:
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		}
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *UserMeHandler) ResetCredentials(c *gin.Context) {
	claims := middleware.ClaimsFrom(c)
	if claims == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "No auth"})
		return
	}
	res, err := h.user.ResetCredentialsAndSync(c.Request.Context(), claims.UserID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "User not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"sub_token": res.SubToken,
		"sub_url":   h.subURL(c.Request, res.SubToken),
		"uuid":      res.UUID,
	})
}

type changePasswordRequest struct {
	OldPassword string `json:"old_password" binding:"required"`
	NewPassword string `json:"new_password" binding:"required"`
}

func (h *UserMeHandler) ChangePassword(c *gin.Context) {
	claims := middleware.ClaimsFrom(c)
	if claims == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "No auth"})
		return
	}
	var req changePasswordRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	u, err := h.user.Get(c.Request.Context(), claims.UserID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if !u.HasLocalPassword() {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Account has no local password"})
		return
	}
	// Optional admin-controlled lock that prevents non-admin local users
	// from rotating their password through the panel UI. Admins always retain
	// the ability (used by the break-glass account when SSO is broken).
	if u.Role != domain.RoleAdmin {
		s, sErr := h.settings.Load(c.Request.Context(), ports.UISettings{})
		if sErr == nil && s.DisallowUserPasswordChange {
			c.JSON(http.StatusForbidden, gin.H{"error": "Password change is disabled for non-administrators"})
			return
		}
	}
	if _, err := h.user.VerifyLocalPassword(c.Request.Context(), u.UPN, req.OldPassword); err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Old password incorrect"})
		return
	}
	if err := h.user.SetPassword(c.Request.Context(), claims.UserID, req.NewPassword); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *UserMeHandler) subURL(r *http.Request, token string) string {
	base := strings.TrimRight(resolveSubBaseForRequest(r.Context(), h.settings, r), "/")
	path := resolveSubPath(r.Context(), h.settings, token)
	if base == "" {
		return path
	}
	return base + path
}

// resolveSubBase returns the panel's public base URL from the DB settings.
// Empty means the caller may fall back to the current request's origin.
func resolveSubBase(ctx context.Context, s ports.SettingsRepo) string {
	if s == nil {
		return ""
	}
	st, err := s.Load(ctx, ports.UISettings{})
	if err != nil {
		return ""
	}
	return st.SubBaseURL
}

func resolveSubBaseForRequest(ctx context.Context, s ports.SettingsRepo, r *http.Request) string {
	if base := strings.TrimSpace(resolveSubBase(ctx, s)); base != "" {
		return base
	}
	return inferRequestBaseURL(r)
}

func inferRequestBaseURL(r *http.Request) string {
	if r == nil {
		return ""
	}
	scheme := firstForwardedValue(r.Header.Get("X-Forwarded-Proto"))
	if scheme != "http" && scheme != "https" {
		if r.TLS != nil {
			scheme = "https"
		} else {
			scheme = "http"
		}
	}
	host := firstForwardedValue(r.Header.Get("X-Forwarded-Host"))
	if host == "" {
		host = strings.TrimSpace(r.Host)
	}
	if host == "" {
		return ""
	}
	return scheme + "://" + host
}

func firstForwardedValue(v string) string {
	if i := strings.IndexByte(v, ','); i >= 0 {
		v = v[:i]
	}
	return strings.TrimSpace(v)
}

func resolveSubPath(ctx context.Context, s ports.SettingsRepo, token string) string {
	subPath := "sub"
	if s != nil {
		st, err := s.Load(ctx, ports.UISettings{SubPath: "sub"})
		if err == nil && strings.TrimSpace(st.SubPath) != "" {
			subPath = strings.Trim(strings.TrimSpace(st.SubPath), "/")
		}
	}
	if subPath == "" {
		subPath = "sub"
	}
	return "/" + subPath + "/" + token
}
