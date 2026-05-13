package handler

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/user"
)

// AdminUserHandler exposes user CRUD under /api/admin/users.
type AdminUserHandler struct {
	user     *user.Service
	settings ports.SettingsRepo
}

func NewAdminUserHandler(userSvc *user.Service, settings ports.SettingsRepo) *AdminUserHandler {
	return &AdminUserHandler{user: userSvc, settings: settings}
}

// ---- DTOs ----

type userDTO struct {
	ID                 int64                     `json:"id"`
	Username           string                    `json:"username"`
	DisplayName        string                    `json:"display_name,omitempty"`
	UPN                string                    `json:"upn,omitempty"`
	Role               domain.Role               `json:"role"`
	GroupID            int64                     `json:"group_id"`
	UUID               string                    `json:"uuid"`
	SubURL             string                    `json:"sub_url"`
	ExpireAt           *time.Time                `json:"expire_at,omitempty"`
	TrafficLimitBytes  int64                     `json:"traffic_limit_bytes"`
	TrafficResetPeriod domain.ResetPeriod        `json:"traffic_reset_period"`
	Remark             string                    `json:"remark,omitempty"`
	Enabled            bool                      `json:"enabled"`
	AutoDisabledReason domain.AutoDisabledReason `json:"auto_disabled_reason,omitempty"`
	EmergencyUsedCount int                       `json:"emergency_used_count"`
	CreatedAt          time.Time                 `json:"created_at"`
}

type createUserRequest struct {
	Username           string     `json:"username" binding:"required"`
	DisplayName        string     `json:"display_name"`
	Password           string     `json:"password"`
	GroupID            int64      `json:"group_id" binding:"required"`
	ExpireAt           *time.Time `json:"expire_at"`
	TrafficLimitGB     int64      `json:"traffic_limit_gb"`
	TrafficResetPeriod string     `json:"traffic_reset_period"`
	Remark             string     `json:"remark"`
}

type createUserResponse struct {
	User            userDTO `json:"user"`
	InitialPassword string  `json:"initial_password"`
	SyncedInbounds  int     `json:"synced_inbounds"`
}

// ---- Handlers ----

func (h *AdminUserHandler) List(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "50"))
	filter := ports.UserFilter{
		Pagination: ports.Pagination{Page: page, PageSize: pageSize},
		Search:     c.Query("search"),
	}
	if v := c.Query("enabled"); v != "" {
		enabled := v == "true" || v == "1"
		filter.Enabled = &enabled
	}
	if v := c.Query("group_id"); v != "" {
		if id, err := strconv.ParseInt(v, 10, 64); err == nil {
			filter.GroupID = &id
		}
	}
	items, total, err := h.user.List(c.Request.Context(), filter)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	out := make([]userDTO, len(items))
	for i, u := range items {
		out[i] = h.toDTO(c.Request.Context(), u)
	}
	c.JSON(http.StatusOK, gin.H{"items": out, "total": total})
}

func (h *AdminUserHandler) Get(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	u, err := h.user.Get(c.Request.Context(), id)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, h.toDTO(c.Request.Context(), u))
}

func (h *AdminUserHandler) Create(c *gin.Context) {
	var req createUserRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	in := user.CreateLocalInput{
		Username:           req.Username,
		DisplayName:        req.DisplayName,
		InitialPassword:    req.Password,
		GroupID:            req.GroupID,
		ExpireAt:           req.ExpireAt,
		TrafficLimitBytes:  req.TrafficLimitGB * 1024 * 1024 * 1024,
		TrafficResetPeriod: domain.ResetPeriod(req.TrafficResetPeriod),
		Remark:             req.Remark,
	}
	res, err := h.user.CreateLocalAndSync(c.Request.Context(), in)
	if err != nil {
		switch {
		case errors.Is(err, domain.ErrAlreadyExists):
			c.JSON(http.StatusConflict, gin.H{"error": "username already exists"})
		case errors.Is(err, domain.ErrValidation):
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		case errors.Is(err, domain.ErrNotFound):
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		default:
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		}
		return
	}
	if h.user.HasPendingSync(c.Request.Context(), res.User.ID) {
		c.Header("X-Sync-Pending", "1")
	}
	c.JSON(http.StatusCreated, createUserResponse{
		User:            h.toDTO(c.Request.Context(), res.User),
		InitialPassword: res.InitialPassword,
		SyncedInbounds:  res.SyncedInbounds,
	})
}

func (h *AdminUserHandler) Delete(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	if err := h.user.DeleteAndSync(c.Request.Context(), id); err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if h.user.HasPendingSync(c.Request.Context(), id) {
		c.Header("X-Sync-Pending", "1")
	}
	c.Status(http.StatusNoContent)
}

func (h *AdminUserHandler) ResetCredentials(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	res, err := h.user.ResetCredentialsAndSync(c.Request.Context(), id)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if h.user.HasPendingSync(c.Request.Context(), id) {
		c.Header("X-Sync-Pending", "1")
	}
	c.JSON(http.StatusOK, gin.H{
		"sub_token": res.SubToken,
		"sub_url":   h.subURLFor(c.Request.Context(), res.SubToken),
		"uuid":      res.UUID,
	})
}

func (h *AdminUserHandler) ResetEmergencyUsage(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	if err := h.user.ResetEmergencyUsage(c.Request.Context(), id); err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.Status(http.StatusNoContent)
}

type setEnabledRequest struct {
	Enabled bool `json:"enabled"`
}

func (h *AdminUserHandler) SetEnabled(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	var req setEnabledRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	reason := domain.DisabledNone
	if !req.Enabled {
		reason = domain.DisabledManual
	}
	if err := h.user.SetEnabledAndSync(c.Request.Context(), id, req.Enabled, reason); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if h.user.HasPendingSync(c.Request.Context(), id) {
		c.Header("X-Sync-Pending", "1")
	}
	c.Status(http.StatusNoContent)
}

type updateUserRequest struct {
	GroupID            *int64     `json:"group_id,omitempty"`
	Role               *string    `json:"role,omitempty"`
	ExpireAt           *time.Time `json:"expire_at,omitempty"`
	ClearExpire        bool       `json:"clear_expire,omitempty"`
	TrafficLimitGB     *int64     `json:"traffic_limit_gb,omitempty"`
	TrafficResetPeriod *string    `json:"traffic_reset_period,omitempty"`
	Remark             *string    `json:"remark,omitempty"`
	DisplayName        *string    `json:"display_name,omitempty"`
}

func (h *AdminUserHandler) Update(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	var req updateUserRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	in := user.UpdateInput{
		GroupID:     req.GroupID,
		ExpireAt:    req.ExpireAt,
		ClearExpire: req.ClearExpire,
		Remark:      req.Remark,
		DisplayName: req.DisplayName,
	}
	if req.Role != nil {
		role := domain.Role(*req.Role)
		in.Role = &role
	}
	if req.TrafficLimitGB != nil {
		bytes := *req.TrafficLimitGB * 1024 * 1024 * 1024
		in.TrafficLimitBytes = &bytes
	}
	if req.TrafficResetPeriod != nil {
		p := domain.ResetPeriod(*req.TrafficResetPeriod)
		in.TrafficResetPeriod = &p
	}
	if err := h.user.UpdateProfile(c.Request.Context(), id, in); err != nil {
		switch {
		case errors.Is(err, domain.ErrNotFound):
			c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		case errors.Is(err, domain.ErrValidation):
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		default:
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		}
		return
	}
	u, err := h.user.Get(c.Request.Context(), id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if h.user.HasPendingSync(c.Request.Context(), id) {
		c.Header("X-Sync-Pending", "1")
	}
	c.JSON(http.StatusOK, h.toDTO(c.Request.Context(), u))
}

// ---- helpers ----

func (h *AdminUserHandler) toDTO(ctx context.Context, u *domain.User) userDTO {
	return userDTO{
		ID:                 u.ID,
		Username:           u.Username,
		DisplayName:        u.DisplayName,
		UPN:                u.UPN,
		Role:               u.Role,
		GroupID:            u.GroupID,
		UUID:               u.UUID,
		SubURL:             h.subURLFor(ctx, u.SubToken),
		ExpireAt:           u.ExpireAt,
		TrafficLimitBytes:  u.TrafficLimitBytes,
		TrafficResetPeriod: u.TrafficResetPeriod,
		Remark:             u.Remark,
		Enabled:            u.Enabled,
		AutoDisabledReason: u.AutoDisabledReason,
		EmergencyUsedCount: u.EmergencyUsedCount,
		CreatedAt:          u.CreatedAt,
	}
}

func (h *AdminUserHandler) subURLFor(ctx context.Context, token string) string {
	base := strings.TrimRight(resolveSubBase(ctx, h.settings), "/")
	if base == "" {
		return "/sub/" + token
	}
	return base + "/sub/" + token
}

func notImplemented(c *gin.Context) {
	c.JSON(http.StatusNotImplemented, gin.H{"error": "not implemented yet"})
}
