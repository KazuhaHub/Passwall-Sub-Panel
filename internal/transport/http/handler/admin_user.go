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
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/log"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/mailer"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/user"
	"github.com/KazuhaHub/passwall-sub-panel/internal/transport/http/middleware"
)

// ensureOperatorAllowed blocks an operator caller from mutating an
// admin/operator account. Returns true when the operation may proceed
// (caller is admin OR target is a regular user). On 403 it writes the
// response itself; the handler should return immediately.
//
// Centralised here so every mutating user-handler can call it without
// repeating the same fetch+compare four times.
func (h *AdminUserHandler) ensureOperatorAllowed(c *gin.Context, targetID int64) bool {
	claims := middleware.ClaimsFrom(c)
	if claims == nil || claims.Role != domain.RoleOperator {
		return true
	}
	target, err := h.user.Get(c.Request.Context(), targetID)
	if err != nil {
		// Let the caller handle 404/500 via its own service call.
		return true
	}
	if target.Role == domain.RoleAdmin || target.Role == domain.RoleOperator {
		c.JSON(http.StatusForbidden, gin.H{"error": "Operators cannot modify admin or operator accounts"})
		return false
	}
	return true
}

// guardOperatorRoleAssignment rejects operator callers that try to
// create or assign a non-user role. Operators can only manage regular
// users — letting them mint another operator/admin would be a privilege
// escalation path.
func guardOperatorRoleAssignment(c *gin.Context, targetRole domain.Role) bool {
	claims := middleware.ClaimsFrom(c)
	if claims == nil || claims.Role != domain.RoleOperator {
		return true
	}
	if targetRole != "" && targetRole != domain.RoleUser {
		c.JSON(http.StatusForbidden, gin.H{"error": "Operators can only assign role=user"})
		return false
	}
	return true
}

// AdminUserHandler exposes user CRUD under /api/admin/users.
type AdminUserHandler struct {
	user     *user.Service
	settings ports.SettingsRepo
	mailer   *mailer.Service
}

func NewAdminUserHandler(userSvc *user.Service, settings ports.SettingsRepo, mailerSvc *mailer.Service) *AdminUserHandler {
	return &AdminUserHandler{user: userSvc, settings: settings, mailer: mailerSvc}
}

// ---- DTOs ----

type userDTO struct {
	ID                 int64                     `json:"id"`
	DisplayName        string                    `json:"display_name,omitempty"`
	UPN                string                    `json:"upn"`
	Email              string                    `json:"email,omitempty"`
	// SSOProvider + SSOSubject expose the SSO identity binding so the
	// admin UI can show "this account signs in via saml:default with
	// NameID=alice@example.com" and offer an Unlink action. Read-only —
	// /unlink-sso is the only path to mutate them, and successful SSO
	// logins overwrite them.
	SSOProvider        string                    `json:"sso_provider"`
	SSOSubject         string                    `json:"sso_subject,omitempty"`
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
	EmergencyUntil     *time.Time                `json:"emergency_until,omitempty"`
	// EmergencyUsedBytes is how much traffic the user has consumed since the
	// active window opened (lifetime - baseline). Zero when no window is
	// active. Exposed so the admin's edit dialog can show "已用 X / Y GB"
	// without needing a separate API call.
	EmergencyUsedBytes int64 `json:"emergency_used_bytes"`
	// EmergencyQuotaBytes is the configured per-window cap (0 = unlimited).
	// Comes from UISettings; included per-user so the table doesn't need a
	// second round-trip to settings.
	EmergencyQuotaBytes int64     `json:"emergency_quota_bytes"`
	CreatedAt           time.Time `json:"created_at"`
}

type createUserRequest struct {
	UPN                string     `json:"upn" binding:"required"`
	Email              string     `json:"email"`
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
		out[i] = h.toDTO(c.Request, u)
	}
	c.JSON(http.StatusOK, gin.H{"items": out, "total": total})
}

func (h *AdminUserHandler) Get(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid id"})
		return
	}
	u, err := h.user.Get(c.Request.Context(), id)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "Not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, h.toDTO(c.Request, u))
}

func (h *AdminUserHandler) Create(c *gin.Context) {
	var req createUserRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	in := user.CreateLocalInput{
		UPN:                req.UPN,
		Email:              strings.TrimSpace(req.Email),
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
			c.JSON(http.StatusConflict, gin.H{"error": "Upn already exists"})
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
		User:            h.toDTO(c.Request, res.User),
		InitialPassword: res.InitialPassword,
		SyncedInbounds:  res.SyncedInbounds,
	})
}

func (h *AdminUserHandler) Delete(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid id"})
		return
	}
	if !h.ensureOperatorAllowed(c, id) {
		return
	}
	if err := h.user.DeleteAndSync(c.Request.Context(), id); err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "Not found"})
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
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid id"})
		return
	}
	if !h.ensureOperatorAllowed(c, id) {
		return
	}
	res, err := h.user.ResetCredentialsAndSync(c.Request.Context(), id)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "Not found"})
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
		"sub_url":   h.subURLFor(c.Request, res.SubToken),
		"uuid":      res.UUID,
	})
}

// ResetPassword sets a new password for the target user and returns the
// plaintext once. An empty body / empty password field means "server,
// generate a random one"; otherwise the supplied value is validated and
// stored. Separate from ResetCredentials (which rotates sub_token + UUID)
// because the two operations serve different recovery scenarios.
func (h *AdminUserHandler) ResetPassword(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid id"})
		return
	}
	if !h.ensureOperatorAllowed(c, id) {
		return
	}
	var req struct {
		Password string `json:"password"`
	}
	// Empty / missing body is fine — falls through to random generation.
	_ = c.ShouldBindJSON(&req)
	pwd, err := h.user.AdminResetPassword(c.Request.Context(), id, req.Password)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "Not found"})
			return
		}
		if errors.Is(err, domain.ErrValidation) {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"password": pwd})
}

// UnlinkSSO clears the SSO binding on a user, dropping them back to a
// local row. The next SSO login by the same person will re-bind via
// EnsureSSO's first-time-linking path. Admin uses this to force a
// rebind after rotating IdP tenants or recycling a UPN.
func (h *AdminUserHandler) UnlinkSSO(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid id"})
		return
	}
	if !h.ensureOperatorAllowed(c, id) {
		return
	}
	if err := h.user.UnlinkSSO(c.Request.Context(), id); err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "Not found"})
			return
		}
		if errors.Is(err, domain.ErrValidation) {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *AdminUserHandler) ResetEmergencyUsage(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid id"})
		return
	}
	if !h.ensureOperatorAllowed(c, id) {
		return
	}
	if err := h.user.ResetEmergencyUsage(c.Request.Context(), id); err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "Not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.Status(http.StatusNoContent)
}

type setEnabledRequest struct {
	Enabled bool   `json:"enabled"`
	Reason  string `json:"reason,omitempty"`
}

func (h *AdminUserHandler) SetEnabled(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid id"})
		return
	}
	if !h.ensureOperatorAllowed(c, id) {
		return
	}
	var req setEnabledRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	reason := domain.DisabledNone
	detail := ""
	if !req.Enabled {
		reason = domain.DisabledManual
		detail = req.Reason
	}
	if err := h.user.SetEnabledAndSync(c.Request.Context(), id, req.Enabled, reason, detail); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// Send notification email (async).
	if h.mailer != nil {
		// Use background context since request context will be cancelled after response.
		go func(userID int64, enabled bool, reasonText, detailText string) {
			ctx := context.Background()
			if !enabled {
				if err := h.mailer.SendAccountDisabledToUser(ctx, userID, reasonText, detailText); err != nil {
					log.Warn("failed to send disable notification", "user_id", userID, "err", err)
				}
			} else {
				if err := h.mailer.SendAccountEnabledToUser(ctx, userID, reasonText, detailText); err != nil {
					log.Warn("failed to send enable notification", "user_id", userID, "err", err)
				}
			}
		}(id, req.Enabled, func() string {
			if !req.Enabled {
				if detail != "" {
					return detail
				}
				return "管理员手动停用"
			}
			if detail != "" {
				return detail
			}
			return "管理员手动恢复"
		}(), detail)
	}

	if h.user.HasPendingSync(c.Request.Context(), id) {
		c.Header("X-Sync-Pending", "1")
	}
	c.Status(http.StatusNoContent)
}

func (h *AdminUserHandler) GetRules(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid id"})
		return
	}
	u, err := h.user.Get(c.Request.Context(), id)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "Not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"personal_rules": u.PersonalRules})
}

func (h *AdminUserHandler) PutRules(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid id"})
		return
	}
	if !h.ensureOperatorAllowed(c, id) {
		return
	}
	var req updatePersonalRulesRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := h.user.SetPersonalRules(c.Request.Context(), id, req.PersonalRules); err != nil {
		switch {
		case errors.Is(err, domain.ErrNotFound):
			c.JSON(http.StatusNotFound, gin.H{"error": "Not found"})
		case errors.Is(err, domain.ErrValidation):
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		default:
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		}
		return
	}
	c.Status(http.StatusNoContent)
}

type updateUserRequest struct {
	GroupID            *int64     `json:"group_id,omitempty"`
	Role               *string    `json:"role,omitempty"`
	Email              *string    `json:"email,omitempty"`
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
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid id"})
		return
	}
	if !h.ensureOperatorAllowed(c, id) {
		return
	}
	var req updateUserRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	// Belt-and-suspenders: even if the frontend sends a role field by
	// accident, operators can never escalate via this endpoint.
	if req.Role != nil && !guardOperatorRoleAssignment(c, domain.Role(*req.Role)) {
		return
	}
	if req.Email != nil {
		email := strings.TrimSpace(*req.Email)
		req.Email = &email
	}
	in := user.UpdateInput{
		GroupID:     req.GroupID,
		Email:       req.Email,
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
			c.JSON(http.StatusNotFound, gin.H{"error": "Not found"})
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
	c.JSON(http.StatusOK, h.toDTO(c.Request, u))
}

// ---- helpers ----

func (h *AdminUserHandler) toDTO(r *http.Request, u *domain.User) userDTO {
	// Only fill EmergencyUsedBytes when a window is actually active — a stale
	// baseline from a closed window is meaningless and would mislead the UI.
	var usedBytes int64
	if u.EmergencyUntil != nil && u.EmergencyUntil.After(time.Now()) {
		usedBytes = u.LifetimeTotalBytes - u.EmergencyBaselineBytes
		if usedBytes < 0 {
			usedBytes = 0
		}
	}
	// Read quota from current settings; cheap because Load is cached in the
	// repo layer. Falls back to 0 (= unlimited) if settings unreadable.
	var quotaBytes int64
	if h.settings != nil {
		if st, err := h.settings.Load(r.Context(), ports.UISettings{}); err == nil {
			quotaBytes = int64(st.EmergencyAccessQuotaGB) * 1024 * 1024 * 1024
		}
	}
	return userDTO{
		ID:                  u.ID,
		DisplayName:         u.DisplayName,
		UPN:                 u.UPN,
		Email:               u.Email,
		SSOProvider:         u.SSOProvider,
		SSOSubject:          u.SSOSubject,
		Role:                u.Role,
		GroupID:             u.GroupID,
		UUID:                u.UUID,
		SubURL:              h.subURLFor(r, u.SubToken),
		ExpireAt:            u.ExpireAt,
		TrafficLimitBytes:   u.TrafficLimitBytes,
		TrafficResetPeriod:  u.TrafficResetPeriod,
		Remark:              u.Remark,
		Enabled:             u.Enabled,
		AutoDisabledReason:  u.AutoDisabledReason,
		EmergencyUsedCount:  u.EmergencyUsedCount,
		EmergencyUntil:      u.EmergencyUntil,
		EmergencyUsedBytes:  usedBytes,
		EmergencyQuotaBytes: quotaBytes,
		CreatedAt:           u.CreatedAt,
	}
}

func (h *AdminUserHandler) subURLFor(r *http.Request, token string) string {
	base := strings.TrimRight(resolveSubBaseForRequest(r.Context(), h.settings, r), "/")
	path := resolveSubPath(r.Context(), h.settings, token)
	if base == "" {
		return path
	}
	return base + path
}
