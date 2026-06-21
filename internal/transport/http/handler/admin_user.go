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
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/passkey"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/twofa"
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
		// Fail CLOSED on anything other than a genuine not-found. The old "let the
		// caller handle it" returned true on ANY error, so a transient DB fault
		// (pool exhaustion, deadlock retry, context deadline) against a real admin
		// target made this privilege gate pass and the destructive op run. A
		// missing target is safe to wave through — the caller's own service call
		// surfaces the 404.
		if errors.Is(err, domain.ErrNotFound) {
			return true
		}
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Could not verify target account; try again"})
		return false
	}
	if target.Role == domain.RoleAdmin || target.Role == domain.RoleOperator {
		c.JSON(http.StatusForbidden, gin.H{"error": "Operators cannot modify admin or operator accounts"})
		return false
	}
	return true
}

// shouldRedactPrivilegedSecrets reports whether the per-user UUID (the root all
// of a user's protocol secrets derive from) and the working sub URL must be
// hidden from the caller. Operators are semi-trusted staff who manage regular
// users but must not read an admin/operator account's credential material —
// mirroring redactInboundForRole on the node side.
func (h *AdminUserHandler) shouldRedactPrivilegedSecrets(c *gin.Context, target *domain.User) bool {
	claims := middleware.ClaimsFrom(c)
	if claims == nil || claims.Role != domain.RoleOperator {
		return false
	}
	return target.Role == domain.RoleAdmin || target.Role == domain.RoleOperator
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
	twofa    *twofa.Service
	passkey  *passkey.Service
}

func NewAdminUserHandler(userSvc *user.Service, settings ports.SettingsRepo, mailerSvc *mailer.Service, async AsyncDispatcher, twofaSvc *twofa.Service, passkeySvc *passkey.Service) *AdminUserHandler {
	return &AdminUserHandler{user: userSvc, settings: settings, mailer: mailerSvc, async: async, twofa: twofaSvc, passkey: passkeySvc}
}

// ---- DTOs ----

type userDTO struct {
	ID          int64  `json:"id"`
	DisplayName string `json:"display_name,omitempty"`
	UPN         string `json:"upn"`
	Email       string `json:"email,omitempty"`
	// SSOProvider + SSOSubject expose the SSO identity binding so the
	// admin UI can show "this account signs in via saml:default with
	// NameID=alice@example.com" and offer an Unlink action. Read-only —
	// /unlink-sso is the only path to mutate them, and successful SSO
	// logins overwrite them.
	SSOProvider string      `json:"sso_provider"`
	SSOSubject  string      `json:"sso_subject,omitempty"`
	Role        domain.Role `json:"role"`
	GroupID     int64       `json:"group_id"`
	UUID        string      `json:"uuid"`
	SubURL      string      `json:"sub_url"`
	ExpireAt    *time.Time  `json:"expire_at,omitempty"`
	// ExpireDate is ExpireAt rendered as the YYYY-MM-DD calendar day it
	// falls on in the *panel* timezone. The UI uses this for the date
	// picker and table so the displayed day matches what was set, free of
	// the browser's or 3X-UI server's timezone. Empty for permanent users.
	ExpireDate        string `json:"expire_date,omitempty"`
	TrafficLimitBytes int64  `json:"traffic_limit_bytes"`
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
	// LastOnlineAt is the most recent moment any owned 3X-UI client
	// reported activity, written by the traffic poll (v3.6.0-beta.4).
	// nil = never seen / panel still on 3X-UI < 3.1.0; UI renders that as
	// "—" rather than a literal "1970-01-01".
	LastOnlineAt *time.Time `json:"last_online_at,omitempty"`
	// TOTPEnabled lets the admin table show a 2FA badge and surface the
	// break-glass "reset 2FA" action only for accounts that actually have it on.
	TOTPEnabled bool `json:"totp_enabled"`
	// PasskeyCount is how many passkeys the account has enrolled. A passkey is a
	// second factor, so the admin "account security" drawer keys its recovery-code
	// actions on (TOTPEnabled || PasskeyCount>0), not TOTP alone. Bulk-filled.
	PasskeyCount int `json:"passkey_count"`
}

type createUserRequest struct {
	UPN         string     `json:"upn" binding:"required"`
	Email       string     `json:"email"`
	DisplayName string     `json:"display_name"`
	Password    string     `json:"password"`
	GroupID     int64      `json:"group_id" binding:"required"`
	ExpireAt    *time.Time `json:"expire_at"`
	// ExpireDate ("YYYY-MM-DD"), when set, is interpreted as end-of-day in
	// the panel timezone and wins over ExpireAt. Preferred over ExpireAt for
	// calendar-date expiry so the day can't drift with the caller's timezone.
	ExpireDate         string  `json:"expire_date"`
	TrafficLimitGB     float64 `json:"traffic_limit_gb"` // fractional GB allowed (e.g. 0.3)
	TrafficResetPeriod string  `json:"traffic_reset_period"`
	Remark             string  `json:"remark"`
}

type createUserResponse struct {
	User            userDTO `json:"user"`
	InitialPassword string  `json:"initial_password"`
	SyncedInbounds  int     `json:"synced_inbounds"`
}

// ---- Handlers ----

func (h *AdminUserHandler) List(c *gin.Context) {
	p := parsePagination(c)
	// Sensible default ordering: newest first. Frontend overrides via
	// sort_by/sort_dir on column-header click. Keep the historical
	// behavior for callers that don't pass either.
	if p.SortBy == "" {
		p.SortBy = "id"
		p.SortDir = "desc"
	}
	filter := ports.UserFilter{
		Pagination: p,
		// `search` retained as legacy alias so older callers / bookmarked
		// URLs keep working; new code sends `keyword` directly.
		Search: firstNonEmpty(p.Keyword, c.Query("search")),
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
	// Resolve the per-request shared state once instead of in toDTO per
	// row: settings (for emergency quota + panel tz), the *time.Location,
	// and the sub URL base+path. Pre-fix each toDTO call did 3 settings
	// loads + 1 tz parse + recomputed the sub base per user — at
	// page_size=25 that's 75 Settings.Load round-trips through the cache
	// + N path-rebuilds for one list response.
	settings := h.loadSettingsForRequest(c.Request)
	loc := paneltz.LocationOf(settings.Timezone)
	subBase := strings.TrimRight(resolveSubBaseForRequest(c.Request.Context(), h.settings, c.Request), "/")
	out := make([]userDTO, len(items))
	for i, u := range items {
		out[i] = h.toDTOWith(u, settings, loc, subBase, h.shouldRedactPrivilegedSecrets(c, u))
	}
	// Bulk-enrich the passkey count in ONE grouped query (not per row) so the
	// "account security" drawer knows which accounts have a passkey second factor.
	if h.passkey != nil && len(items) > 0 {
		ids := make([]int64, len(items))
		for i, u := range items {
			ids[i] = u.ID
		}
		if counts, err := h.passkey.CountByUsers(c.Request.Context(), ids); err == nil {
			for i := range out {
				out[i].PasskeyCount = counts[out[i].ID]
			}
		}
	}
	c.JSON(http.StatusOK, pagedEnvelope(out, total, p))
}

// loadSettingsForRequest is a thin wrapper around the (cached)
// SettingsRepo.Load that swallows errors back to zero-value defaults —
// per-row DTO code can't usefully propagate a settings load failure
// (the rest of the response is still serviceable).
func (h *AdminUserHandler) loadSettingsForRequest(r *http.Request) ports.UISettings {
	if h.settings == nil {
		return ports.UISettings{}
	}
	st, _ := h.settings.Load(r.Context(), ports.UISettings{})
	return st
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
	dto := h.toDTO(c.Request, u, h.shouldRedactPrivilegedSecrets(c, u))
	if h.passkey != nil {
		if creds, lerr := h.passkey.List(c.Request.Context(), u.ID); lerr == nil {
			dto.PasskeyCount = len(creds)
		}
	}
	c.JSON(http.StatusOK, dto)
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
		User:            h.toDTO(c.Request, res.User, h.shouldRedactPrivilegedSecrets(c, res.User)),
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

// Reset2FA is the admin break-glass for a user who lost both their authenticator
// and recovery codes: it clears their 2FA unconditionally so they can log in with
// just their password and re-enroll. Operators can't reset a privileged account.
func (h *AdminUserHandler) Reset2FA(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid id"})
		return
	}
	if !h.ensureOperatorAllowed(c, id) {
		return
	}
	if err := h.twofa.AdminReset(c.Request.Context(), id); err != nil {
		respondError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// RegenerateUser2FARecovery is the admin break-glass for a user who still has
// 2FA on but lost their recovery codes: it rotates the codes and returns the
// fresh plaintext set for the admin to relay over a secure channel. The user's
// 2FA stays enabled. Operators can't touch a privileged account.
func (h *AdminUserHandler) RegenerateUser2FARecovery(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid id"})
		return
	}
	if !h.ensureOperatorAllowed(c, id) {
		return
	}
	// Recovery codes are only meaningful for an account that actually has a second
	// factor — TOTP or a passkey (recovery is decoupled from TOTP now). Refuse
	// otherwise so the admin doesn't mint inert codes for a no-2FA account.
	u, err := h.user.Get(c.Request.Context(), id)
	if err != nil {
		respondError(c, err)
		return
	}
	hasPasskey := false
	if h.passkey != nil {
		if creds, lerr := h.passkey.List(c.Request.Context(), id); lerr == nil {
			hasPasskey = len(creds) > 0
		}
	}
	if !u.TOTPEnabled && !hasPasskey {
		c.JSON(http.StatusBadRequest, gin.H{"error": "This user has no second factor (TOTP or passkey) to attach recovery codes to"})
		return
	}
	codes, err := h.twofa.AdminRegenerateRecovery(c.Request.Context(), id)
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"recovery_codes": codes})
}

type updateUserRequest struct {
	GroupID  *int64     `json:"group_id,omitempty"`
	Role     *string    `json:"role,omitempty"`
	Email    *string    `json:"email,omitempty"`
	ExpireAt *time.Time `json:"expire_at,omitempty"`
	// ExpireDate ("YYYY-MM-DD"), when non-nil, is interpreted as end-of-day
	// in the panel timezone and wins over ExpireAt. The date-mode UI sends
	// this; permanent-mode sends ClearExpire instead.
	ExpireDate         *string  `json:"expire_date,omitempty"`
	ClearExpire        bool     `json:"clear_expire,omitempty"`
	TrafficLimitGB     *float64 `json:"traffic_limit_gb,omitempty"` // fractional GB allowed
	TrafficResetPeriod *string  `json:"traffic_reset_period,omitempty"`
	Remark             *string  `json:"remark,omitempty"`
	DisplayName        *string  `json:"display_name,omitempty"`
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
	c.JSON(http.StatusOK, h.toDTO(c.Request, u, h.shouldRedactPrivilegedSecrets(c, u)))
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

// toDTO is the single-row path (Get / Create / Update). It loads
// settings + resolves the sub base once per call, then delegates the
// pure mapping to toDTOWith. List-style callers should bypass this and
// call toDTOWith directly with shared state to avoid per-row Load /
// per-row sub-base recompute (see List).
func (h *AdminUserHandler) toDTO(r *http.Request, u *domain.User, redactSecrets bool) userDTO {
	st := h.loadSettingsForRequest(r)
	loc := paneltz.LocationOf(st.Timezone)
	subBase := strings.TrimRight(resolveSubBaseForRequest(r.Context(), h.settings, r), "/")
	return h.toDTOWith(u, st, loc, subBase, redactSecrets)
}

// toDTOWith is the pure mapping — no I/O, no Load — so list endpoints
// can call it inside a tight loop with caller-supplied shared state.
func (h *AdminUserHandler) toDTOWith(u *domain.User, st ports.UISettings, loc *time.Location, subBase string, redactSecrets bool) userDTO {
	// Only fill EmergencyUsedBytes when a window is actually active — a stale
	// baseline from a closed window is meaningless and would mislead the UI.
	var usedBytes int64
	if u.EmergencyUntil != nil && u.EmergencyUntil.After(time.Now()) {
		usedBytes = u.LifetimeTotalBytes - u.EmergencyBaselineBytes
		if usedBytes < 0 {
			usedBytes = 0
		}
	}
	if loc == nil {
		loc = time.Local
	}
	// Cosmetic admin-view field, deliberately the GLOBAL quota: toDTOWith is the
	// pure no-I/O mapping the List loop reuses with one shared settings load, so
	// resolving a per-user (group-scoped) quota here would mean a per-row Load.
	// Enforcement (user.emergencyFloor / /sub / the traffic-poll teardown) already
	// honors the per-group quota; this display can lag without functional impact.
	quotaBytes := int64(st.EmergencyAccessQuotaGB * 1024 * 1024 * 1024)
	var expireDate string
	if u.ExpireAt != nil {
		expireDate = paneltz.DateString(*u.ExpireAt, loc)
	}
	uuid := u.UUID
	subURL := joinSubURL(subBase, resolveSubPathFromSettings(st, u.SubToken))
	if redactSecrets {
		// Hide the credential root + working sub URL from an operator viewing a
		// privileged account (see shouldRedactPrivilegedSecrets).
		uuid = ""
		subURL = ""
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
		UUID:                uuid,
		SubURL:              subURL,
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
		LastOnlineAt:        u.LastOnlineAt,
		TOTPEnabled:         u.TOTPEnabled,
	}
}

func joinSubURL(base, path string) string {
	if base == "" {
		return path
	}
	return base + path
}

func (h *AdminUserHandler) subURLFor(r *http.Request, token string) string {
	base := strings.TrimRight(resolveSubBaseForRequest(r.Context(), h.settings, r), "/")
	path := resolveSubPath(r.Context(), h.settings, token)
	return joinSubURL(base, path)
}
