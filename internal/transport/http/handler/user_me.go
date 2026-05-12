package handler

import (
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/KazuhaHub/passwall-sub-panel/internal/config"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/traffic"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/user"
	"github.com/KazuhaHub/passwall-sub-panel/internal/transport/http/middleware"
)

// UserMeHandler exposes the end-user self-service endpoints under
// /api/user/me — view expiry / traffic, change password, reset sub_token.
type UserMeHandler struct {
	user    *user.Service
	traffic *traffic.Service
	cfg     *config.Config
}

func NewUserMeHandler(userSvc *user.Service, trafficSvc *traffic.Service, cfg *config.Config) *UserMeHandler {
	return &UserMeHandler{user: userSvc, traffic: trafficSvc, cfg: cfg}
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
		"id":                  u.ID,
		"username":            u.Username,
		"upn":                 u.UPN,
		"source":              u.Source,
		"sub_url":             h.subURL(u.SubToken),
		"expire_at":           u.ExpireAt,
		"traffic_limit_bytes": u.TrafficLimitBytes,
		"traffic_reset_period": u.TrafficResetPeriod,
		"enabled":             u.Enabled,
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

func (h *UserMeHandler) ResetSubToken(c *gin.Context) {
	claims := middleware.ClaimsFrom(c)
	if claims == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "no auth"})
		return
	}
	token, err := h.user.ResetSubToken(c.Request.Context(), claims.UserID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"sub_token": token, "sub_url": h.subURL(token)})
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

func (h *UserMeHandler) subURL(token string) string {
	base := strings.TrimRight(h.cfg.SubBaseURL, "/")
	if base == "" {
		return "/sub/" + token
	}
	return base + "/sub/" + token
}
