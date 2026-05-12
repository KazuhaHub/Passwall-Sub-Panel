package handler

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/KazuhaHub/passwall-sub-panel/internal/config"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/user"
)

// AdminUserHandler exposes user CRUD under /api/admin/users.
type AdminUserHandler struct {
	user *user.Service
	cfg  *config.Config
}

func NewAdminUserHandler(userSvc *user.Service, cfg *config.Config) *AdminUserHandler {
	return &AdminUserHandler{user: userSvc, cfg: cfg}
}

// ---- DTOs ----

type userDTO struct {
	ID                 int64                      `json:"id"`
	Username           string                     `json:"username"`
	UPN                string                     `json:"upn,omitempty"`
	Source             domain.UserSource          `json:"source"`
	Role               domain.Role                `json:"role"`
	GroupID            int64                      `json:"group_id"`
	UUID               string                     `json:"uuid"`
	SubURL             string                     `json:"sub_url"`
	ExpireAt           *time.Time                 `json:"expire_at,omitempty"`
	TrafficLimitBytes  int64                      `json:"traffic_limit_bytes"`
	TrafficResetPeriod domain.ResetPeriod         `json:"traffic_reset_period"`
	Remark             string                     `json:"remark,omitempty"`
	Enabled            bool                       `json:"enabled"`
	AutoDisabledReason domain.AutoDisabledReason  `json:"auto_disabled_reason,omitempty"`
	CreatedAt          time.Time                  `json:"created_at"`
}

type createUserRequest struct {
	Username           string     `json:"username" binding:"required"`
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
		out[i] = h.toDTO(u)
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
	c.JSON(http.StatusOK, h.toDTO(u))
}

func (h *AdminUserHandler) Create(c *gin.Context) {
	var req createUserRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	in := user.CreateLocalInput{
		Username:           req.Username,
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
	c.JSON(http.StatusCreated, createUserResponse{
		User:            h.toDTO(res.User),
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
	c.Status(http.StatusNoContent)
}

func (h *AdminUserHandler) ResetSubToken(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	token, err := h.user.ResetSubToken(c.Request.Context(), id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"sub_token": token, "sub_url": h.subURLFor(token)})
}

func (h *AdminUserHandler) ResetUUID(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	uuid, err := h.user.ResetUUIDAndSync(c.Request.Context(), id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"uuid": uuid})
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
	c.Status(http.StatusNoContent)
}

func (h *AdminUserHandler) Update(c *gin.Context) {
	// TODO(Phase 2 follow-up): support group_id / expire / traffic_limit / remark changes.
	// Each of these triggers a different sync side effect that warrants its
	// own service method (e.g. ChangeGroupAndSync, SetTrafficLimit).
	c.JSON(http.StatusNotImplemented, gin.H{"error": "user update not implemented yet"})
}

// ---- helpers ----

func (h *AdminUserHandler) toDTO(u *domain.User) userDTO {
	return userDTO{
		ID:                 u.ID,
		Username:           u.Username,
		UPN:                u.UPN,
		Source:             u.Source,
		Role:               u.Role,
		GroupID:            u.GroupID,
		UUID:               u.UUID,
		SubURL:             h.subURLFor(u.SubToken),
		ExpireAt:           u.ExpireAt,
		TrafficLimitBytes:  u.TrafficLimitBytes,
		TrafficResetPeriod: u.TrafficResetPeriod,
		Remark:             u.Remark,
		Enabled:            u.Enabled,
		AutoDisabledReason: u.AutoDisabledReason,
		CreatedAt:          u.CreatedAt,
	}
}

func (h *AdminUserHandler) subURLFor(token string) string {
	base := strings.TrimRight(h.cfg.SubBaseURL, "/")
	if base == "" {
		return "/sub/" + token
	}
	return base + "/sub/" + token
}

func notImplemented(c *gin.Context) {
	c.JSON(http.StatusNotImplemented, gin.H{"error": "not implemented yet"})
}
