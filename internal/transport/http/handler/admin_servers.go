package handler

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/log"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"github.com/KazuhaHub/passwall-sub-panel/internal/transport/http/middleware"
	"github.com/KazuhaHub/passwall-sub-panel/internal/version"
)

// AdminServersHandler exposes CRUD for 3X-UI server connections under
// /api/admin/servers. A "server" is a 3X-UI panel URL + credentials stored
// in the DB; nodes reference a server by ID when admin creates or imports
// inbounds.
//
// Mutations keep the DB and the in-memory XUIPool in lockstep so changes
// take effect immediately without restarting the panel binary.
//
// audit + async are used by the upgrade-panel / upgrade-xray handlers
// (v3.6.0-beta.3) to write audit-trail rows and to schedule the post-upgrade
// smoke probe; they're optional for the CRUD/Test flows.
type AdminServersHandler struct {
	repo      ports.XUIPanelRepo
	pool      ports.XUIPool
	nodes     ports.NodeRepo
	ownership ports.OwnershipRepo
	audit     ports.AuditRepo
	async     AsyncDispatcher
}

func NewAdminServersHandler(repo ports.XUIPanelRepo, pool ports.XUIPool, nodes ports.NodeRepo, ownership ports.OwnershipRepo, audit ports.AuditRepo, async AsyncDispatcher) *AdminServersHandler {
	return &AdminServersHandler{repo: repo, pool: pool, nodes: nodes, ownership: ownership, audit: audit, async: async}
}

// serverDTO is the API representation. Sensitive fields (api_token /
// password) are NEVER returned in plaintext — the response carries only
// "has_api_token" / "has_password" booleans. The edit dialog re-enters
// secrets when changing them.
//
// Version-identity fields (panel_version / xray_version / version_checked_at /
// compat_status / compat_message) reflect the last successful probe via the
// boot probe + traffic-poll-piggyback path (v3.6.0-beta.1) or the manual
// "test connection" trigger (Test handler, refreshes these on every click).
// Empty version strings + nil checked_at = "never probed" (UI shows ⋯).
type serverDTO struct {
	ID               int64      `json:"id"`
	Name             string     `json:"name"`
	URL              string     `json:"url"`
	Username         string     `json:"username,omitempty"`
	Remark           string     `json:"remark,omitempty"`
	HasAPIToken      bool       `json:"has_api_token"`
	HasPassword      bool       `json:"has_password"`
	PanelVersion     string     `json:"panel_version,omitempty"`
	XrayVersion      string     `json:"xray_version,omitempty"`
	VersionCheckedAt *time.Time `json:"version_checked_at,omitempty"`
	CompatStatus     string     `json:"compat_status,omitempty"`  // "supported" | "too_old" | "untested" | "unknown"
	CompatMessage    string     `json:"compat_message,omitempty"` // human-readable, for tooltip / banner
	// LatestXUIVersion / UpdateAvailable are derived per-request from the
	// PSP-wide version.LatestXUI() snapshot (one GitHub query feeds every
	// row) compared against this panel's PanelVersion. NOT persisted per
	// panel — the latest tag is panel-independent and storing it per row
	// would just be N copies of the same string going stale together.
	LatestXUIVersion string `json:"latest_xui_version,omitempty"`
	UpdateAvailable  bool   `json:"update_available,omitempty"`
}

type serverCreateRequest struct {
	Name     string `json:"name" binding:"required"`
	URL      string `json:"url" binding:"required"`
	APIToken string `json:"api_token"`
	Username string `json:"username"`
	Password string `json:"password"`
	Remark   string `json:"remark"`
}

// serverUpdateRequest uses pointers so omitted fields preserve existing
// values; admin only re-enters secrets when actually changing them.
type serverUpdateRequest struct {
	Name     *string `json:"name,omitempty"`
	URL      *string `json:"url,omitempty"`
	APIToken *string `json:"api_token,omitempty"`
	Username *string `json:"username,omitempty"`
	Password *string `json:"password,omitempty"`
	Remark   *string `json:"remark,omitempty"`
}

func (h *AdminServersHandler) List(c *gin.Context) {
	p := parsePagination(c)
	panels, total, err := h.repo.ListPaged(c.Request.Context(), p)
	if err != nil {
		respondError(c, err)
		return
	}
	out := make([]serverDTO, len(panels))
	for i, panel := range panels {
		out[i] = toServerDTO(panel)
	}
	c.JSON(http.StatusOK, pagedEnvelope(out, total, p))
}

func (h *AdminServersHandler) Create(c *gin.Context) {
	var req serverCreateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if _, err := h.repo.GetByName(c.Request.Context(), req.Name); err == nil {
		c.JSON(http.StatusConflict, gin.H{"error": "Server name already exists"})
		return
	} else if !errors.Is(err, domain.ErrNotFound) {
		respondError(c, err)
		return
	}
	p := &domain.XUIPanel{
		Name:     req.Name,
		URL:      req.URL,
		APIToken: req.APIToken,
		Username: req.Username,
		Password: req.Password,
		Remark:   req.Remark,
	}
	if err := h.repo.Save(c.Request.Context(), p); err != nil {
		mapServerError(c, err)
		return
	}
	if err := h.pool.Add(p); err != nil {
		// DB succeeded but pool wiring failed; rollback so they stay in sync.
		_ = h.repo.Delete(c.Request.Context(), p.ID)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Register in pool: " + err.Error()})
		return
	}
	c.JSON(http.StatusCreated, toServerDTO(p))
}

func (h *AdminServersHandler) Update(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid id"})
		return
	}
	existing, err := h.repo.GetByID(c.Request.Context(), id)
	if err != nil {
		mapServerError(c, err)
		return
	}
	var req serverUpdateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.Name != nil {
		existing.Name = *req.Name
	}
	if req.URL != nil {
		existing.URL = *req.URL
	}
	if req.APIToken != nil {
		existing.APIToken = *req.APIToken
	}
	if req.Username != nil {
		existing.Username = *req.Username
	}
	if req.Password != nil {
		existing.Password = *req.Password
	}
	if req.Remark != nil {
		existing.Remark = *req.Remark
	}
	if err := h.repo.Save(c.Request.Context(), existing); err != nil {
		mapServerError(c, err)
		return
	}
	// Panel rename no longer needs to cascade to nodes / user_xui_clients —
	// the panel_name columns were dropped in v3. The pool refresh below
	// makes every subsequent name lookup return the new value automatically.
	//
	// Re-register in the pool. Prefer an atomic Replace (production *xui.Pool) so
	// a concurrent Get from traffic/reconcile/render/sync never hits the brief
	// "not registered" gap a Remove()+Add() pair exposes; fall back to the pair
	// for pools that don't implement it (test fakes).
	if rp, ok := h.pool.(interface {
		Replace(*domain.XUIPanel) error
	}); ok {
		if err := rp.Replace(existing); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Re-register in pool: " + err.Error()})
			return
		}
	} else {
		_ = h.pool.Remove(id)
		if err := h.pool.Add(existing); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Re-register in pool: " + err.Error()})
			return
		}
	}
	c.JSON(http.StatusOK, toServerDTO(existing))
}

// Test issues a lightweight ListInbounds against the named server. Returns
// {ok: bool, error: string, inbound_count: int} so the frontend can show a
// pass/fail badge next to the server row.
//
// Name is read from the JSON body (not the URL path) to dodge a Gin routing
// quirk where /servers/:name/test conflicts with the bare /servers/:name
// CRUD routes and falls through to the SPA NoRoute handler.
type testServerRequest struct {
	ID int64 `json:"id" binding:"required"`
}

func (h *AdminServersHandler) Test(c *gin.Context) {
	var req testServerRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": err.Error()})
		return
	}
	// Piggyback the remote-compat refresh on every Test click — admin
	// opening the Servers page fires N parallel testServers, but the
	// single-flight + throttle inside RefreshRemoteCompat collapses
	// them to at most one actual GitHub fetch. Errors here are
	// non-blocking; an unreachable raw.githubusercontent.com just means
	// CheckXUI keeps returning Unknown until the next attempt.
	//
	// Uses background context (not c.Request.Context()) so an admin
	// navigating away mid-fetch doesn't cancel the in-flight GitHub
	// request — that cancel previously combined with v3.6.0-beta.5's
	// "advance lastAt on failure" bug to lock the throttle for 60s
	// after a single client disconnect. compat_remote.go enforces its
	// own 8s timeout internally so background ctx can't leak forever.
	if rerr := version.RefreshRemoteCompat(context.Background(), ""); rerr != nil {
		log.Warn("admin test: refresh remote compat", "panel_id", req.ID, "err", rerr)
	}
	// Same piggyback for the centralized latest-3X-UI tag fetch — one
	// PSP-wide GitHub query drives every panel's "update available"
	// badge. The 30-minute throttle inside RefreshLatestXUI means
	// page-refresh churn never reaches GitHub more than twice per hour
	// regardless of how many panels admin has. Background ctx for the
	// same reason as RefreshRemoteCompat (admin navigating away
	// shouldn't cancel the in-flight network call).
	if rerr := version.RefreshLatestXUI(context.Background()); rerr != nil {
		log.Debug("admin test: refresh latest 3X-UI failed", "err", rerr)
	}
	client, err := h.pool.Get(req.ID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"ok": false, "error": "Server not registered in pool: " + err.Error()})
		return
	}
	inbounds, err := client.ListInbounds(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"ok": false, "error": err.Error()})
		return
	}

	// Connection works — piggyback a version probe so the admin's "test
	// connection" click doubles as a manual version refresh. This is the
	// out-of-band manual trigger that complements the boot probe +
	// traffic-poll-piggyback re-probe (see app.go) — admin gets immediate
	// feedback after a 3X-UI upgrade instead of waiting for the next poll
	// tick. Best-effort: if the version probe fails for any reason we
	// still report the connection test result; the "ok: true" semantics
	// is about the panel being reachable + credentials valid.
	resp := gin.H{
		"ok":            true,
		"inbound_count": len(inbounds),
	}
	if status, perr := client.GetServerStatus(c.Request.Context()); perr == nil {
		now := time.Now()
		if uerr := h.repo.UpdateVersion(c.Request.Context(), req.ID, status.PanelVersion, status.XrayVersion, &now); uerr != nil {
			log.Warn("admin test: write version", "panel_id", req.ID, "err", uerr)
		}
		compatStatus := version.CheckXUI(status.PanelVersion)
		resp["panel_version"] = status.PanelVersion
		resp["xray_version"] = status.XrayVersion
		resp["xray_state"] = status.XrayState
		resp["compat_status"] = compatStatus.String()
		resp["compat_message"] = version.CompatMessage(status.PanelVersion, compatStatus)
		resp["version_checked_at"] = now
		// Carry the centralized "latest 3X-UI tag" snapshot back so the
		// UI refreshes its ⋮ kebab badge in lockstep with the probed
		// panel version. The latest tag itself is panel-independent
		// (one PSP-wide value), but UpdateAvailable becomes meaningful
		// only when paired with this specific panel's version.
		if latest := version.LatestXUI(); latest != "" {
			resp["latest_xui_version"] = latest
			resp["update_available"] = version.IsXUIUpdateAvailable(status.PanelVersion)
		}
	} else {
		log.Warn("admin test: version probe", "panel_id", req.ID, "err", perr)
		// Record the attempt time without touching the stored
		// versions (preserve last-known-good). Mirrors the boot/
		// piggyback probe's failure-path semantics so the UI's
		// "checked X minutes ago" indicator stays accurate.
		if uerr := h.repo.UpdateVersionCheckedAt(c.Request.Context(), req.ID, time.Now()); uerr != nil {
			log.Warn("admin test: write checked-at", "panel_id", req.ID, "err", uerr)
		}
	}
	c.JSON(http.StatusOK, resp)
}

// UpgradePanel triggers a remote 3X-UI panel self-upgrade after a
// pre-flight compat check: PSP refuses to fire the upgrade if the
// remote panel's reported "latest available" version falls outside the
// currently-loaded tested range (driven by docs/compat/xui-compat.json
// via RefreshRemoteCompat). The gate prevents admin from accidentally
// pulling a future schema-breaking release (e.g. the 2026-05-23 v3.1.0
// inbound serialization change that v3.5.1 had to special-case).
//
// Admin can bypass the gate with body {"force": true} — that path is
// the explicit "I know it's untested, do it anyway" escape hatch and is
// audited separately as panel_upgrade_forced so the trail is obvious.
// Force is also the only way to recover when remote-compat JSON has
// never been fetched (CheckXUI returns Unknown for everything, normal
// path always refuses).
//
// 3X-UI's /updatePanel has no version-selection knob — it always pulls
// latest from GitHub. So PSP can't "downgrade" or "pin" the target; it
// can only refuse the call when latest is out of range, or be forced.
//
// On success: a panel_upgrade_initiated (or _forced) audit row is
// written, the post-upgrade smoke probe is scheduled, and the handler
// returns 202 Accepted immediately (the panel restart drops the
// connection mid-call; the adapter swallows the resulting EOF). The
// smoke probe writes a follow-up panel_upgrade_succeeded / _failed
// audit row when it eventually concludes.
type upgradePanelRequest struct {
	Force bool `json:"force"`
}

func (h *AdminServersHandler) UpgradePanel(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid id"})
		return
	}
	panel, err := h.repo.GetByID(c.Request.Context(), id)
	if err != nil {
		mapServerError(c, err)
		return
	}
	client, err := h.pool.Get(id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Server not registered in pool: " + err.Error()})
		return
	}
	var req upgradePanelRequest
	_ = c.ShouldBindJSON(&req) // body optional; missing → force=false
	// Pre-flight: ask the panel what version it would upgrade TO.
	info, err := client.GetPanelUpdateInfo(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "Failed to query 3X-UI update info: " + err.Error()})
		return
	}
	if !info.UpdateAvailable {
		// Already on the latest release — fire nothing. /server/updatePanel on
		// an up-to-date panel is a no-op that 3X-UI reports as an error, which
		// otherwise surfaces as a spurious "upgrade failed" (the common
		// select-all-and-upgrade case for a panel already at the target). No
		// audit row: nothing happened.
		c.JSON(http.StatusOK, gin.H{
			"ok":              true,
			"already_latest":  true,
			"current_version": info.CurrentVersion,
			"latest_version":  info.LatestVersion,
			"message":         "Panel is already on the latest version (" + info.CurrentVersion + "); nothing to upgrade.",
		})
		return
	}
	compat := version.CheckXUI(info.LatestVersion)
	if compat != version.CompatSupported && !req.Force {
		// Refuse: latest is outside the currently-loaded tested range
		// (or compat data isn't loaded yet → everything is Unknown).
		// Audit the rejection so the trail shows admin tried + got
		// blocked + can retry with force.
		h.writeUpgradeAudit(c, "panel_upgrade_blocked", panel, info.LatestVersion,
			"target latest version "+info.LatestVersion+" outside PSP active tested range ["+version.ActiveMinXUI()+", "+version.ActiveMaxTestedXUI()+"] (compat="+compat.String()+")")
		c.JSON(http.StatusConflict, gin.H{
			"ok":             false,
			"reason":         "untested_target",
			"latest_version": info.LatestVersion,
			"compat_status":  compat.String(),
			"psp_min_xui":    version.ActiveMinXUI(),
			"psp_max_xui":    version.ActiveMaxTestedXUI(),
			"message":        version.CompatMessage(info.LatestVersion, compat) + " — upgrade PSP first, or resend with {force: true} to override at your own risk",
			"can_force":      true,
		})
		return
	}
	// Fire the upgrade. The adapter swallows the EOF/reset that the
	// panel restart produces, so a nil err here means "upgrade signal
	// accepted; panel is restarting".
	if err := client.UpdatePanel(c.Request.Context()); err != nil {
		h.writeUpgradeAudit(c, "panel_upgrade_failed", panel, info.LatestVersion, err.Error())
		c.JSON(http.StatusBadGateway, gin.H{"ok": false, "error": "updatePanel call failed: " + err.Error()})
		return
	}
	// Distinguish "normal initiate" vs "forced through gate" in the
	// audit trail — both are admin-initiated but forced carries an
	// implicit "operator accepted the untested-target risk" context.
	action := "panel_upgrade_initiated"
	detail := ""
	if req.Force && compat != version.CompatSupported {
		action = "panel_upgrade_forced"
		detail = "compat=" + compat.String() + " (out of active tested range), admin overrode the gate"
	}
	h.writeUpgradeAudit(c, action, panel, info.LatestVersion, detail)
	// Schedule the smoke probe — runs in the panel-wide background
	// context so it survives this request returning. async + audit
	// must be present for the smoke to run; if either is nil this
	// gracefully degrades to "fire and forget, no follow-up audit".
	if h.async != nil {
		panelID := id
		panelName := panel.Name
		targetVersion := info.LatestVersion
		h.async.Go("upgrade-panel.smoke", func(bg context.Context) {
			h.runPostUpgradeSmoke(bg, panelID, panelName, targetVersion)
		})
	}
	c.JSON(http.StatusAccepted, gin.H{
		"ok":             true,
		"started":        true,
		"target_version": info.LatestVersion,
		"message":        "3X-UI upgrade initiated; the panel is restarting. PSP will run a smoke probe in ~60s and log success or failure to the audit trail.",
	})
}

// ListXrayVersions returns the xray-core tags the 3X-UI panel knows it
// can install. Drives the Upgrade-Xray dialog's version dropdown so admin
// can pin a specific tag instead of always taking "latest". GET so it's
// cacheable / browser-prefetchable and clearly read-only.
func (h *AdminServersHandler) ListXrayVersions(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid id"})
		return
	}
	client, err := h.pool.Get(id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Server not registered in pool: " + err.Error()})
		return
	}
	versions, err := client.GetXrayVersionList(c.Request.Context())
	if err != nil {
		// Frontend falls back to a single "latest" option when this 502s,
		// so an upstream failure is recoverable without admin intervention.
		c.JSON(http.StatusBadGateway, gin.H{"error": "GetXrayVersion failed: " + err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"versions": versions})
}

// WebCert proxies GET /panel/api/server/getWebCertFiles on the panel and
// returns that panel's own web TLS cert/key file PATHS (not the PEM bytes).
// Panel-scoped: the cert is identical for every inbound on the panel, and this
// serves both the create- and edit-node forms (each carries a panel_id).
// Backs cert_source=from_panel. A pre-3.2.7 panel has no such route, which the
// xui adapter surfaces as ports.ErrXUIEndpointUnsupported → reported here as
// 200 {"supported":false} so the node form greys out the "fetch from panel"
// button WITHOUT firing the global error toast a 4xx/5xx would trigger. A
// genuine upstream failure still 502s so the normal error path applies.
func (h *AdminServersHandler) WebCert(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid id"})
		return
	}
	client, err := h.pool.Get(id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Server not registered in pool: " + err.Error()})
		return
	}
	wc, err := client.GetWebCertFiles(c.Request.Context())
	if err != nil {
		if errors.Is(err, ports.ErrXUIEndpointUnsupported) {
			c.JSON(http.StatusOK, gin.H{"supported": false})
			return
		}
		c.JSON(http.StatusBadGateway, gin.H{"error": "getWebCertFiles failed: " + err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"supported": true, "cert_file": wc.CertFile, "key_file": wc.KeyFile})
}

// UpgradeXray triggers a remote xray-core install. xray-core compatibility
// with PSP is low-coupling (PSP only talks to the 3X-UI panel, never
// directly to xray), and 3X-UI's installXray accepts an explicit version
// tag, so we DON'T pre-check against any PSP-side range — admin can pull
// "latest" or pin a specific tag freely. Unlike UpdatePanel, the 3X-UI
// panel itself keeps running across this call (only xray-core restarts),
// so there's no smoke probe — the handler returns the underlying API
// result synchronously.
type upgradeXrayRequest struct {
	Version string `json:"version"`
}

func (h *AdminServersHandler) UpgradeXray(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid id"})
		return
	}
	panel, err := h.repo.GetByID(c.Request.Context(), id)
	if err != nil {
		mapServerError(c, err)
		return
	}
	client, err := h.pool.Get(id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Server not registered in pool: " + err.Error()})
		return
	}
	var req upgradeXrayRequest
	// Body is optional — empty/missing means "latest".
	_ = c.ShouldBindJSON(&req)
	if req.Version == "" {
		req.Version = "latest"
	}
	if err := client.InstallXray(c.Request.Context(), req.Version); err != nil {
		h.writeUpgradeAudit(c, "xray_upgrade_failed", panel, req.Version, err.Error())
		c.JSON(http.StatusBadGateway, gin.H{"ok": false, "error": "installXray failed: " + err.Error()})
		return
	}
	h.writeUpgradeAudit(c, "xray_upgrade_completed", panel, req.Version, "")
	// Refresh version snapshot — installXray triggers an xray restart
	// so the panel's reported xray.version field updates immediately.
	if status, perr := client.GetServerStatus(c.Request.Context()); perr == nil {
		now := time.Now()
		_ = h.repo.UpdateVersion(c.Request.Context(), id, status.PanelVersion, status.XrayVersion, &now)
	}
	c.JSON(http.StatusOK, gin.H{
		"ok":      true,
		"version": req.Version,
		"message": "Xray upgrade completed.",
	})
}

// runPostUpgradeSmoke waits for the panel to come back after a
// /updatePanel restart, then verifies both that /server/status returns
// 200 (panel is alive) and that /inbounds/list decodes (schema didn't
// silently break, à la the v3.1.0 incident). Records the outcome via
// audit. Runs in the panel-wide background context so it survives the
// admin's HTTP request returning.
//
// Timing: 60s initial grace (3X-UI usually takes ~10-15s to come back
// up; 60s gives broad headroom), then poll every 10s for up to 2 minutes
// of additional retries (12 attempts). If status comes back but
// /inbounds/list errors with a JSON decode failure, that's the v3.1.0
// pattern — flagged explicitly so admin grep on "schema_break" finds it.
func (h *AdminServersHandler) runPostUpgradeSmoke(ctx context.Context, panelID int64, panelName, targetVersion string) {
	const initialGrace = 60 * time.Second
	const probeInterval = 10 * time.Second
	const maxAttempts = 12

	select {
	case <-time.After(initialGrace):
	case <-ctx.Done():
		// PSP shutting down before the grace period elapsed. Record
		// the abort with a background ctx (the caller's ctx is already
		// cancelled, so audit.Insert on it would fail) so the audit
		// trail doesn't dead-end at panel_upgrade_initiated and admin
		// can see the upgrade never got its smoke probe.
		h.writeSmokeAudit(context.Background(), "panel_upgrade_aborted", panelID, panelName, targetVersion,
			"smoke probe cancelled (PSP shutdown?) during initial grace window — admin should manually verify the panel via test/Servers page")
		return
	}
	client, err := h.pool.Get(panelID)
	if err != nil {
		h.writeSmokeAudit(ctx, "panel_upgrade_failed", panelID, panelName, targetVersion,
			"pool lost panel client: "+err.Error())
		return
	}
	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if ctx.Err() != nil {
			h.writeSmokeAudit(context.Background(), "panel_upgrade_aborted", panelID, panelName, targetVersion,
				"smoke probe cancelled (PSP shutdown?) during retry loop on attempt "+strconv.Itoa(attempt)+" — admin should manually verify")
			return
		}
		probeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		status, serr := client.GetServerStatus(probeCtx)
		cancel()
		if serr != nil {
			lastErr = serr
			log.Debug("upgrade smoke: panel still down", "panel_id", panelID, "attempt", attempt, "err", serr)
			select {
			case <-time.After(probeInterval):
			case <-ctx.Done():
				h.writeSmokeAudit(context.Background(), "panel_upgrade_aborted", panelID, panelName, targetVersion,
					"smoke probe cancelled during retry sleep on attempt "+strconv.Itoa(attempt)+" — admin should manually verify")
				return
			}
			continue
		}
		// /status came back — second probe: schema-decode of
		// /inbounds/list. If decoding errors, that's the schema-break
		// signature (the v3.1.0 incident shape).
		listCtx, lcancel := context.WithTimeout(ctx, 15*time.Second)
		_, lerr := client.ListInbounds(listCtx)
		lcancel()
		if lerr != nil {
			h.writeSmokeAudit(ctx, "panel_upgrade_schema_break", panelID, panelName, targetVersion,
				"panel reachable but /inbounds/list decode failed: "+lerr.Error())
			return
		}
		// All good — refresh the cached version snapshot too so the
		// Servers UI immediately reflects the post-upgrade version.
		now := time.Now()
		_ = h.repo.UpdateVersion(ctx, panelID, status.PanelVersion, status.XrayVersion, &now)
		h.writeSmokeAudit(ctx, "panel_upgrade_succeeded", panelID, panelName, targetVersion,
			"panel back online at "+status.PanelVersion+" (xray "+status.XrayVersion+"), inbounds decode ok")
		return
	}
	if lastErr == nil {
		lastErr = errors.New("unknown")
	}
	h.writeSmokeAudit(ctx, "panel_upgrade_failed", panelID, panelName, targetVersion,
		"panel still unreachable after "+initialGrace.String()+" grace + "+(probeInterval*maxAttempts).String()+" of retries: "+lastErr.Error())
}

// writeUpgradeAudit is the in-request audit writer. detail is opaque text
// stored in the after_json column (audit table reuses it as a free-form
// field for non-CRUD events).
func (h *AdminServersHandler) writeUpgradeAudit(c *gin.Context, action string, panel *domain.XUIPanel, targetVersion, detail string) {
	if h.audit == nil {
		return
	}
	target := "panel=" + strconv.FormatInt(panel.ID, 10) + " name=" + panel.Name + " target=" + targetVersion
	_ = h.audit.Insert(c.Request.Context(), &domain.AuditEntry{
		Actor:     actorFromGin(c),
		Action:    action,
		Target:    target,
		AfterJSON: detail,
		IP:        c.ClientIP(),
		At:        time.Now(),
	})
}

// writeSmokeAudit is the smoke-probe audit writer. Runs in the background
// context (no gin.Context), so actor is hardcoded to "upgrade-smoke".
func (h *AdminServersHandler) writeSmokeAudit(ctx context.Context, action string, panelID int64, panelName, targetVersion, detail string) {
	if h.audit == nil {
		return
	}
	target := "panel=" + strconv.FormatInt(panelID, 10) + " name=" + panelName + " target=" + targetVersion
	_ = h.audit.Insert(ctx, &domain.AuditEntry{
		Actor:     "upgrade-smoke",
		Action:    action,
		Target:    target,
		AfterJSON: detail,
		At:        time.Now(),
	})
}

// actorFromGin extracts the acting admin's UPN from the request's JWT claims
// for the audit trail. The auth middleware sets the parsed Claims (not a bare
// "upn" context key — reading that always missed and logged everyone as
// "admin"), so go through ClaimsFrom like the audit middleware does.
func actorFromGin(c *gin.Context) string {
	if claims := middleware.ClaimsFrom(c); claims != nil && claims.UPN != "" {
		return claims.UPN
	}
	return "admin"
}

func (h *AdminServersHandler) Delete(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid id"})
		return
	}
	// Refuse if any node still references this server.
	all, err := h.nodes.List(c.Request.Context())
	if err != nil {
		respondError(c, err)
		return
	}
	for _, n := range all {
		if n.PanelID == id {
			c.JSON(http.StatusConflict, gin.H{
				"error": "Server still has nodes attached; delete or reassign them first",
			})
			return
		}
	}
	if err := h.repo.Delete(c.Request.Context(), id); err != nil {
		mapServerError(c, err)
		return
	}
	_ = h.pool.Remove(id)
	c.Status(http.StatusNoContent)
}

// ---- helpers ----

func toServerDTO(p *domain.XUIPanel) serverDTO {
	dto := serverDTO{
		ID:               p.ID,
		Name:             p.Name,
		URL:              p.URL,
		Username:         p.Username,
		Remark:           p.Remark,
		HasAPIToken:      p.APIToken != "",
		HasPassword:      p.Password != "",
		PanelVersion:     p.PanelVersion,
		XrayVersion:      p.XrayVersion,
		VersionCheckedAt: p.VersionCheckedAt,
	}
	// Only compute compat fields when there's actually a probed version —
	// "never probed" panels stay blank rather than displaying a meaningless
	// "unknown" badge that admins would have to dismiss.
	if p.PanelVersion != "" {
		status := version.CheckXUI(p.PanelVersion)
		dto.CompatStatus = status.String()
		dto.CompatMessage = version.CompatMessage(p.PanelVersion, status)
	}
	// Derive the "update available" indicator from the PSP-wide latest
	// tag rather than a per-panel column. Same snapshot drives every
	// panel's badge, so the kebab dots flip in lockstep with one fetch.
	if latest := version.LatestXUI(); latest != "" && p.PanelVersion != "" {
		dto.LatestXUIVersion = latest
		dto.UpdateAvailable = version.IsXUIUpdateAvailable(p.PanelVersion)
	}
	return dto
}

func mapServerError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, domain.ErrNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": "Server not found"})
	case errors.Is(err, domain.ErrValidation):
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
	default:
		respondError(c, err)
	}
}
