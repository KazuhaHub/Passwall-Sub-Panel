package handler

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/paneltz"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/authpolicy"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/group"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/passkey"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/render"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/traffic"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/twofa"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/user"
	"github.com/KazuhaHub/passwall-sub-panel/internal/transport/http/middleware"
)

// UserMeHandler exposes the end-user self-service endpoints under
// /api/user/me — view expiry / traffic, change password, reset sub_token.
type UserMeHandler struct {
	user     *user.Service
	traffic  *traffic.Service
	settings ports.ScopedSettings
	group    *group.Service
	twofa    *twofa.Service
	passkey  *passkey.Service
	enroll   *authpolicy.Service
}

func NewUserMeHandler(userSvc *user.Service, trafficSvc *traffic.Service, settings ports.ScopedSettings, groupSvc *group.Service, twofaSvc *twofa.Service, passkeySvc *passkey.Service, enroll *authpolicy.Service) *UserMeHandler {
	return &UserMeHandler{user: userSvc, traffic: trafficSvc, settings: settings, group: groupSvc, twofa: twofaSvc, passkey: passkeySvc, enroll: enroll}
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
		respondError(c, err)
		return
	}
	// Server-side derived: hide the "change password" affordance for SSO
	// users (no password to begin with) and for non-admin local users when
	// the admin has flipped DisallowUserPasswordChange on. Admins always
	// keep the option as a break-glass path.
	// `settings` = global (sub / branding / traffic, read below); `suEff` =
	// per-user effective (global ⊕ this user's group overrides) for the
	// group-scoped categories: emergency-access caps, 2FA method availability,
	// and login policy (password-change / personal-rules locks).
	settings, _ := h.settings.Load(c.Request.Context(), ports.UISettings{})
	suEff, suErr := h.settings.LoadForUser(c.Request.Context(), u, ports.UISettings{})
	canChangePassword := u.HasLocalPassword()
	if canChangePassword && u.Role != domain.RoleAdmin {
		if suErr == nil && suEff.DisallowUserPasswordChange {
			canChangePassword = false
		}
	}
	var emergencyStatus user.EmergencyAccessStatus
	if suErr == nil {
		emergencyStatus = user.EmergencyAccessStatusForUserWithTrafficLimit(u, suEff, time.Now(), h.trafficLimitExceeded(c.Request.Context(), u))
	}
	// Passkeys (best-effort): the sanitized credential list for the management
	// dialog. Never expose the raw credential record — only id/name/timestamps.
	passkeyAvailable := u.HasLocalPassword() && suErr == nil && suEff.PasskeyEnabled
	passkeyList := h.passkeyList(c.Request.Context(), u.ID)
	c.JSON(http.StatusOK, gin.H{
		"id":           u.ID,
		"display_name": u.DisplayName,
		"upn":          u.UPN,
		"sub_url":      h.subURL(c.Request, u.SubToken),
		// profile_name is the server-resolved SubProfileNameTemplate.
		// Exposing it pre-rendered means the frontend's buildImportURL
		// can drop {{ profile_name_encoded }} into deep links exactly
		// matching the Content-Disposition / Profile-Title strings the
		// subscription response itself carries — no client-side template
		// engine, no risk of the two surfaces drifting. MUST use the per-user
		// effective settings (suEff): sub_profile_name_template is group-
		// overridable, and the /sub render resolves it via LoadForUser, so a
		// global read here would drift the deep-link name from the served
		// Profile-Title for any group that overrides the template.
		"profile_name": render.RenderProfileName(suEff, u),
		// sub_update_interval_hours surfaces the admin-configured value so
		// import-URL templates can embed it (CMfA reads `update-interval`
		// from the intent URI in minutes; the frontend converts on render).
		"sub_update_interval_hours": suEff.SubUpdateIntervalHours, // group-scoped (matches the rendered sub)
		"sub_import_clients":        enabledImportApps(settings.SubClients),
		"sub_import_tutorial_url":   settings.SubImportTutorialURL,
		"quick_links":               enabledQuickLinks(settings.QuickLinks),
		"global_announcement":       visibleGlobalAnnouncement(settings.GlobalAnnouncement),
		"expire_at":                 u.ExpireAt,
		"traffic_limit_bytes":       u.TrafficLimitBytes,
		"traffic_reset_period":      u.TrafficResetPeriod,
		// Exposed so the user portal's traffic chart can hide range options
		// that exceed retention (e.g. don't offer "last 1 year" when the
		// admin has set TrafficHistoryDays to 90). Mirrors the admin chart's
		// rangeOptions filter; 0 means "no retention cap" (chart treats it
		// as unlimited).
		"traffic_history_days":    settings.TrafficHistoryDays,
		"enabled":                 u.Enabled,
		"account_status":          string(u.AccountStatus()),
		"service_status":          string(u.ServiceStatus(time.Now())),
		"service_disabled_reason": string(u.ServiceDisabledReason),
		"service_disable_detail":  u.ServiceDisableDetail,
		"service_disabled_at":     u.ServiceDisabledAt,
		"can_change_password":     canChangePassword,
		// 2FA self-service: totp_available means the admin has turned on
		// panel-wide enrollment AND the account has a local password (SSO-only
		// accounts can't enroll). totp_enabled is this user's current state.
		"totp_available": u.HasLocalPassword() && suErr == nil && suEff.TOTPEnabled,
		"totp_enabled":   u.TOTPEnabled,
		// recovery_codes_remaining drives the user portal's "recovery codes" surface:
		// it's shown whenever the account has any second factor (TOTP or passkey),
		// since recovery codes are now decoupled from TOTP. 0 = none left / not set up.
		"recovery_codes_remaining": h.recoveryRemaining(c.Request.Context(), u.ID),
		// must_enroll_2fa drives the frontend enrollment gate: the account is
		// required to set up a second factor (per-user / group / staff-wide) but
		// hasn't (no TOTP, no passkey). The backend hard-gate enforces the same.
		"must_enroll_2fa": h.mustEnroll2FA(c.Request.Context(), u),
		// Passkeys: passkey_available gates the "add passkey" affordance;
		// passkey_credentials is the sanitized list; passkey_enabled is whether
		// any are registered.
		"passkey_available":   passkeyAvailable,
		"passkey_enabled":     len(passkeyList) > 0,
		"passkey_credentials": passkeyList,
		// can_edit_personal_rules drives the portal's rules dialog: when
		// false the textarea renders read-only and the Save button hides.
		// Admins always can; for non-admins it follows the per-user effective
		// flag (group override ⊕ global).
		"can_edit_personal_rules": u.Role == domain.RoleAdmin || (suErr == nil && suEff.AllowUserPersonalRules),
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

// mustEnroll2FA reports whether the account is required to set up a second factor
// but hasn't. Best-effort: a checker/DB blip returns false so the profile still
// loads (the backend hard-gate is the real enforcement).
func (h *UserMeHandler) mustEnroll2FA(ctx context.Context, u *domain.User) bool {
	if h.enroll == nil {
		return false
	}
	must, err := h.enroll.MustEnroll(ctx, u)
	return err == nil && must
}

type userRelayStatus struct {
	Name      string     `json:"name"`
	Status    string     `json:"status"` // "ok" | "down" | "unknown"
	CheckedAt *time.Time `json:"checked_at,omitempty"`
}

type userNodeStatus struct {
	Name          string            `json:"name"`
	Region        string            `json:"region"`
	Status        string            `json:"status"` // landing status; retained for API compatibility
	CheckedAt     *time.Time        `json:"checked_at,omitempty"`
	ShowDirect    bool              `json:"show_direct"`
	RelayStatuses []userRelayStatus `json:"relay_statuses,omitempty"`
}

// userStatusForNode produces the sanitized presentation model. Relay endpoint
// addresses/ports and failure details intentionally never cross this boundary.
func userStatusForNode(n *domain.Node) userNodeStatus {
	out := userNodeStatus{
		Name: n.DisplayName, Region: n.Region,
		Status: coarseNodeStatus(n.HealthState), CheckedAt: n.HealthCheckedAt,
		ShowDirect: !n.EffectiveHideDirect(),
	}
	if !n.EffectiveShowRelayStatus() {
		return out
	}
	for relayIndex, relay := range n.Relays {
		if !relay.Enabled {
			continue
		}
		port := relay.Port
		if port <= 0 {
			port = n.Port
		}
		status := userRelayStatus{Name: strings.TrimSpace(relay.Name), Status: "unknown"}
		for _, health := range n.RelayHealth {
			if health.Index == relayIndex && health.Address == relay.Address && health.Port == port {
				status.Status = coarseNodeStatus(health.State)
				status.CheckedAt = health.CheckedAt
				break
			}
		}
		out.RelayStatuses = append(out.RelayStatuses, status)
	}
	return out
}

// ServerStatus returns a sanitized, per-node availability list for the nodes
// the caller actually has in their subscription (resolved from their ownership
// rows). Deliberately user-safe: only display name + region + a coarse status
// + last-checked time. It never leaks HealthDetail (internal panel/inbound
// error strings), the panel host's CPU/mem (that's the admin /server/status),
// panel URLs, inbound IDs, or any other group's nodes.
func (h *UserMeHandler) ServerStatus(c *gin.Context) {
	claims := middleware.ClaimsFrom(c)
	if claims == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "No auth"})
		return
	}
	// Resolve the user's visible nodes the SAME way RenderForUser does — via the
	// group selector — NOT via the ownership table. Post-migration the ownership
	// table is empty/dropped, so the old ownership-driven lookup returned zero nodes
	// and this panel rendered blank for every migrated user. NodesFor already returns
	// the enabled, tag-matched, de-duplicated node set, so no extra enabled/seen
	// filtering or per-inbound index is needed.
	u, err := h.user.Get(c.Request.Context(), claims.UserID)
	if err != nil {
		respondError(c, err)
		return
	}
	g, err := h.group.Get(c.Request.Context(), u.GroupID)
	if err != nil {
		respondError(c, err)
		return
	}
	nodes, err := h.group.NodesFor(c.Request.Context(), g)
	if err != nil {
		respondError(c, err)
		return
	}
	out := make([]userNodeStatus, 0, len(nodes))
	for _, n := range nodes {
		if n == nil || n.Kind == domain.NodeKindSeparator || !n.Enabled {
			continue
		}
		out = append(out, userStatusForNode(n))
	}
	// Stable order: region, then name — matches how the subscription groups them.
	sort.Slice(out, func(i, j int) bool {
		if out[i].Region != out[j].Region {
			return out[i].Region < out[j].Region
		}
		return out[i].Name < out[j].Name
	})
	c.JSON(http.StatusOK, gin.H{"nodes": out})
}

// coarseNodeStatus collapses the internal NodeHealthState into the three
// user-facing buckets, so the response doesn't reveal WHERE a failure is
// (panel vs inbound) — only that the node is up, down, or not yet probed.
func coarseNodeStatus(s domain.NodeHealthState) string {
	switch s {
	case domain.NodeHealthOK:
		return "ok"
	case domain.NodeHealthPanelUnreachable, domain.NodeHealthInboundMissing, domain.NodeHealthInboundDisabled, domain.NodeHealthUnreachable:
		return "down"
	default:
		return "unknown"
	}
}

// enabledImportApps flattens the unified client registry into the flat list
// the portal expects: every app whose family AND itself are enabled, ordered
// by Sort. Deriving visibility from the family's Enabled flag is what
// guarantees a blocked client is never advertised for one-click import.
func enabledImportApps(families []ports.SubClientFamily) []ports.SubClientApp {
	out := make([]ports.SubClientApp, 0)
	for _, fam := range families {
		if !fam.Enabled {
			continue
		}
		for _, app := range fam.Apps {
			if app.Enabled {
				out = append(out, app)
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Sort < out[j].Sort })
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
		respondError(c, err)
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
			respondError(c, err)
		}
		return
	}
	// Single HasPendingSync query reused for both the header and the JSON field —
	// nothing writes sync_tasks between them.
	pendingSync := h.user.HasPendingSync(c.Request.Context(), claims.UserID)
	if pendingSync {
		c.Header("X-Sync-Pending", "1")
	}
	// Per-user effective: the emergency quota is group-scoped, so the grant
	// result must report this user's effective cap, not the global one.
	settings, _ := h.settings.LoadForUser(c.Request.Context(), res.User, ports.UISettings{})
	c.JSON(http.StatusOK, gin.H{
		"expire_at":       res.User.ExpireAt,
		"emergency_until": res.User.EmergencyUntil,
		"extended_from":   res.ExtendedFrom,
		"extended_until":  res.ExtendedUntil,
		"used_count":      res.UsedCount,
		"max_count":       res.MaxCount,
		"remaining":       res.Remaining,
		"quota_bytes":     int64(settings.EmergencyAccessQuotaGB * 1024 * 1024 * 1024),
		"used_bytes":      int64(0), // window just opened — UsedBytes is always 0 right after grant
		"sync_pending":    pendingSync,
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
		respondError(c, err)
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
	period, since, until, err := parseTrafficHistoryQuery(c, paneltz.Location(c.Request.Context(), h.settings))
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
			respondError(c, err)
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
		respondError(c, err)
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
	// Per-group lock: when the personal-rules editor is off (globally or for
	// this user's group) the user can still GET (read) but PUT is rejected.
	// Mirrors the DisallowUserPasswordChange gate one method below.
	u, err := h.user.Get(c.Request.Context(), claims.UserID)
	if err != nil {
		respondError(c, err)
		return
	}
	settings, sErr := h.settings.LoadForUser(c.Request.Context(), u, ports.UISettings{})
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
			respondError(c, err)
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
		respondError(c, err)
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
		respondError(c, err)
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
		s, sErr := h.settings.LoadForUser(c.Request.Context(), u, ports.UISettings{})
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
		respondError(c, err)
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
func resolveSubBase(ctx context.Context, s ports.SettingsReader) string {
	if s == nil {
		return ""
	}
	st, err := s.Load(ctx, ports.UISettings{})
	if err != nil {
		return ""
	}
	return st.SubBaseURL
}

func resolveSubBaseForRequest(ctx context.Context, s ports.SettingsReader, r *http.Request) string {
	if base := strings.TrimSpace(resolveSubBase(ctx, s)); base != "" {
		return base
	}
	return inferRequestBaseURL(r)
}

func inferRequestBaseURL(r *http.Request) string {
	if r == nil {
		return ""
	}
	// Only honour X-Forwarded-* when a trusted proxy set them (ProxyTrust
	// middleware). Otherwise an attacker could inject X-Forwarded-Host to poison
	// the sub URL handed to any user, and the SAML SP EntityID/ACSURL written on
	// an admin save in auto mode — fall back to the actual request host/scheme.
	trusted := middleware.ProxyHeadersTrusted(r.Context())
	var scheme string
	if trusted {
		scheme = firstForwardedValue(r.Header.Get("X-Forwarded-Proto"))
	}
	if scheme != "http" && scheme != "https" {
		if r.TLS != nil {
			scheme = "https"
		} else {
			scheme = "http"
		}
	}
	var host string
	if trusted {
		host = firstForwardedValue(r.Header.Get("X-Forwarded-Host"))
	}
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

func resolveSubPath(ctx context.Context, s ports.SettingsReader, token string) string {
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

// resolveSubPathFromSettings is the pre-loaded variant of resolveSubPath
// for callers that already hold a UISettings value — list handlers loop
// over rows and shouldn't re-load per row just to build the same /sub
// path prefix.
func resolveSubPathFromSettings(st ports.UISettings, token string) string {
	subPath := strings.Trim(strings.TrimSpace(st.SubPath), "/")
	if subPath == "" {
		subPath = "sub"
	}
	return "/" + subPath + "/" + token
}
