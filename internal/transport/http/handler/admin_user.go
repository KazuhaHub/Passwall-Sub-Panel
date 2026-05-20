package handler

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/log"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/paneltz"
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
	async    AsyncDispatcher
}

func NewAdminUserHandler(userSvc *user.Service, settings ports.SettingsRepo, mailerSvc *mailer.Service, async AsyncDispatcher) *AdminUserHandler {
	return &AdminUserHandler{user: userSvc, settings: settings, mailer: mailerSvc, async: async}
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
	// ExpireDate is ExpireAt rendered as the YYYY-MM-DD calendar day it
	// falls on in the *panel* timezone. The UI uses this for the date
	// picker and table so the displayed day matches what was set, free of
	// the browser's or 3X-UI server's timezone. Empty for permanent users.
	ExpireDate string `json:"expire_date,omitempty"`
	TrafficLimitBytes  int64                     `json:"traffic_limit_bytes"`
	// Lifetime counters (never reset by period rolls) — surfaced read-only in
	// the admin edit dialog's detail block.
	LifetimeUpBytes    int64                     `json:"lifetime_up_bytes"`
	LifetimeDownBytes  int64                     `json:"lifetime_down_bytes"`
	LifetimeTotalBytes int64                     `json:"lifetime_total_bytes"`
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
	// ExpireDate ("YYYY-MM-DD"), when set, is interpreted as end-of-day in
	// the panel timezone and wins over ExpireAt. Preferred over ExpireAt for
	// calendar-date expiry so the day can't drift with the caller's timezone.
	ExpireDate         string     `json:"expire_date"`
	TrafficLimitGB     float64    `json:"traffic_limit_gb"` // fractional GB allowed (e.g. 0.3)
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
	// Clamp to sane bounds: a caller-supplied negative/zero or absurdly large
	// page_size would otherwise drive a huge allocation / DB scan.
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 50
	} else if pageSize > 200 {
		pageSize = 200
	}
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
		respondError(c, err)
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
		respondError(c, err)
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
	// A calendar date wins over a raw timestamp: it's resolved against the
	// panel timezone so "2026-05-30" means end of that day for the panel,
	// not whatever instant the caller's browser computed.
	expireAt := req.ExpireAt
	if strings.TrimSpace(req.ExpireDate) != "" {
		inst, err := h.panelDateToInstant(c.Request.Context(), req.ExpireDate)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		expireAt = inst
	} else if expireAt != nil {
		// The create form sends a raw "now + N days" instant (browser clock),
		// while the edit dialog sends expire_date → panel-tz end-of-day. Anchor
		// the raw instant to the panel-tz end of its calendar day so both paths
		// store the same cutoff — otherwise a created user's expiry can land on
		// a different day (and at an arbitrary time) than the edit dialog shows.
		loc := paneltz.Location(c.Request.Context(), h.settings)
		if anchored, err := paneltz.EndOfDay(paneltz.DateString(*expireAt, loc), loc); err == nil {
			expireAt = &anchored
		}
	}
	in := user.CreateLocalInput{
		UPN:                req.UPN,
		Email:              strings.TrimSpace(req.Email),
		DisplayName:        req.DisplayName,
		InitialPassword:    req.Password,
		GroupID:            req.GroupID,
		ExpireAt:           expireAt,
		TrafficLimitBytes:  int64(req.TrafficLimitGB * 1024 * 1024 * 1024),
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
			respondError(c, err)
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
		respondError(c, err)
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
		respondError(c, err)
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
		respondError(c, err)
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
		respondError(c, err)
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
		respondError(c, err)
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
		respondError(c, err)
		return
	}

	// Send notification email (async). Dispatched through the panel-wide
	// AsyncDispatcher so the post-response goroutine is recover-shielded
	// and gets drained by App.Shutdown instead of running on an orphan
	// context.Background().
	if h.mailer != nil && h.async != nil {
		userID := id
		enabled := req.Enabled
		reasonText := detail
		if reasonText == "" {
			if enabled {
				reasonText = "管理员手动恢复"
			} else {
				reasonText = "管理员手动停用"
			}
		}
		detailText := detail
		h.async.Go("admin-user.toggle-email", func(ctx context.Context) {
			if !enabled {
				if err := h.mailer.SendAccountDisabledToUser(ctx, userID, reasonText, detailText); err != nil {
					log.Warn("failed to send disable notification", "user_id", userID, "err", err)
				}
				return
			}
			if err := h.mailer.SendAccountEnabledToUser(ctx, userID, reasonText, detailText); err != nil {
				log.Warn("failed to send enable notification", "user_id", userID, "err", err)
			}
		})
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
		respondError(c, err)
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
			respondError(c, err)
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
	// ExpireDate ("YYYY-MM-DD"), when non-nil, is interpreted as end-of-day
	// in the panel timezone and wins over ExpireAt. The date-mode UI sends
	// this; permanent-mode sends ClearExpire instead.
	ExpireDate         *string    `json:"expire_date,omitempty"`
	ClearExpire        bool       `json:"clear_expire,omitempty"`
	TrafficLimitGB     *float64   `json:"traffic_limit_gb,omitempty"` // fractional GB allowed
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
	// expire_date (panel-timezone calendar day) wins over a raw expire_at
	// timestamp when supplied, so the chosen day can't drift with the
	// caller's timezone.
	expireAt := req.ExpireAt
	if req.ExpireDate != nil {
		inst, err := h.panelDateToInstant(c.Request.Context(), *req.ExpireDate)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		expireAt = inst
	} else if expireAt != nil && !req.ClearExpire {
		// A raw expire_at (e.g. the renew action's "now + N days") is anchored
		// to the panel-tz end of its calendar day, matching the date-mode path
		// and Create — so renewals don't drift to an arbitrary time-of-day or a
		// different day than the edit dialog renders.
		loc := paneltz.Location(c.Request.Context(), h.settings)
		if anchored, err := paneltz.EndOfDay(paneltz.DateString(*expireAt, loc), loc); err == nil {
			expireAt = &anchored
		}
	}
	in := user.UpdateInput{
		GroupID:     req.GroupID,
		Email:       req.Email,
		ExpireAt:    expireAt,
		ClearExpire: req.ClearExpire,
		Remark:      req.Remark,
		DisplayName: req.DisplayName,
	}
	if req.Role != nil {
		role := domain.Role(*req.Role)
		in.Role = &role
	}
	if req.TrafficLimitGB != nil {
		bytes := int64(*req.TrafficLimitGB * 1024 * 1024 * 1024)
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
			respondError(c, err)
		}
		return
	}
	u, err := h.user.Get(c.Request.Context(), id)
	if err != nil {
		respondError(c, err)
		return
	}
	if h.user.HasPendingSync(c.Request.Context(), id) {
		c.Header("X-Sync-Pending", "1")
	}
	c.JSON(http.StatusOK, h.toDTO(c.Request, u))
}

// ---- helpers ----

// panelDateToInstant turns an admin-picked "YYYY-MM-DD" into the absolute
// end-of-day instant in the panel timezone. Empty input returns (nil, nil)
// so callers can treat "no date supplied" distinctly from a parse error.
func (h *AdminUserHandler) panelDateToInstant(ctx context.Context, dateStr string) (*time.Time, error) {
	if strings.TrimSpace(dateStr) == "" {
		return nil, nil
	}
	t, err := paneltz.EndOfDay(dateStr, paneltz.Location(ctx, h.settings))
	if err != nil {
		return nil, fmt.Errorf("%w: invalid expire_date %q (want YYYY-MM-DD)", domain.ErrValidation, dateStr)
	}
	return &t, nil
}

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
	// repo layer. Falls back to 0 (= unlimited) if settings unreadable. The
	// same load resolves the panel timezone used to render expire_date, so
	// there's no extra round-trip.
	var quotaBytes int64
	loc := time.Local
	if h.settings != nil {
		if st, err := h.settings.Load(r.Context(), ports.UISettings{}); err == nil {
			quotaBytes = int64(st.EmergencyAccessQuotaGB * 1024 * 1024 * 1024)
			loc = paneltz.LocationOf(st.Timezone)
		}
	}
	var expireDate string
	if u.ExpireAt != nil {
		expireDate = paneltz.DateString(*u.ExpireAt, loc)
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
		ExpireDate:          expireDate,
		TrafficLimitBytes:   u.TrafficLimitBytes,
		LifetimeUpBytes:     u.LifetimeUpBytes,
		LifetimeDownBytes:   u.LifetimeDownBytes,
		LifetimeTotalBytes:  u.LifetimeTotalBytes,
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
