package handler

import (
	"context"
	"errors"
	"net/http"
	"strings"

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
		c.JSON(http.StatusUnauthorized, gin.H{"error": "no auth"})
		return
	}
	u, err := h.user.Get(c.Request.Context(), claims.UserID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "user not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"id":                   u.ID,
		"username":             u.Username,
		"display_name":         u.DisplayName,
		"upn":                  u.UPN,
		"sub_url":              h.subURL(c.Request.Context(), u.SubToken),
		"expire_at":            u.ExpireAt,
		"traffic_limit_bytes":  u.TrafficLimitBytes,
		"traffic_reset_period": u.TrafficResetPeriod,
		"enabled":              u.Enabled,
	})
}

func (h *UserMeHandler) Traffic(c *gin.Context) {
	claims := middleware.ClaimsFrom(c)
	if claims == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "no auth"})
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

func (h *UserMeHandler) ResetCredentials(c *gin.Context) {
	claims := middleware.ClaimsFrom(c)
	if claims == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "no auth"})
		return
	}
	res, err := h.user.ResetCredentialsAndSync(c.Request.Context(), claims.UserID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "user not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"sub_token": res.SubToken,
		"sub_url":   h.subURL(c.Request.Context(), res.SubToken),
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
		c.JSON(http.StatusUnauthorized, gin.H{"error": "no auth"})
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
	if u.Source != domain.UserSourceLocal {
		c.JSON(http.StatusBadRequest, gin.H{"error": "only local accounts may change password here"})
		return
	}
	// Optional admin-controlled lock that prevents non-admin local users
	// from rotating their password through the panel UI. Admins always retain
	// the ability (used by the break-glass account when SSO is broken).
	if u.Role != domain.RoleAdmin {
		s, sErr := h.settings.Load(c.Request.Context(), ports.UISettings{})
		if sErr == nil && s.DisallowUserPasswordChange {
			c.JSON(http.StatusForbidden, gin.H{"error": "password change is disabled for non-administrators"})
			return
		}
	}
	if _, err := h.user.VerifyLocalPassword(c.Request.Context(), u.Username, req.OldPassword); err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "old password incorrect"})
		return
	}
	if err := h.user.SetPassword(c.Request.Context(), claims.UserID, req.NewPassword); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *UserMeHandler) subURL(ctx context.Context, token string) string {
	base := strings.TrimRight(resolveSubBase(ctx, h.settings), "/")
	if base == "" {
		return "/sub/" + token
	}
	return base + "/sub/" + token
}

// resolveSubBase returns the panel's public base URL from the DB settings.
// Empty means "use relative /sub/<token>" — the caller handles that.
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
