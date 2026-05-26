package handler

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/log"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"github.com/KazuhaHub/passwall-sub-panel/internal/version"
)

// AdminServersHandler exposes CRUD for 3X-UI server connections under
// /api/admin/servers. A "server" is a 3X-UI panel URL + credentials stored
// in the DB; nodes reference a server by ID when admin creates or imports
// inbounds.
//
// Mutations keep the DB and the in-memory XUIPool in lockstep so changes
// take effect immediately without restarting the panel binary.
type AdminServersHandler struct {
	repo      ports.XUIPanelRepo
	pool      ports.XUIPool
	nodes     ports.NodeRepo
	ownership ports.OwnershipRepo
}

func NewAdminServersHandler(repo ports.XUIPanelRepo, pool ports.XUIPool, nodes ports.NodeRepo, ownership ports.OwnershipRepo) *AdminServersHandler {
	return &AdminServersHandler{repo: repo, pool: pool, nodes: nodes, ownership: ownership}
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
	panels, err := h.repo.List(c.Request.Context())
	if err != nil {
		respondError(c, err)
		return
	}
	out := make([]serverDTO, len(panels))
	for i, p := range panels {
		out[i] = toServerDTO(p)
	}
	c.JSON(http.StatusOK, gin.H{"items": out})
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
	// Re-register in the pool: remove old client, add fresh one with updated creds.
	_ = h.pool.Remove(id)
	if err := h.pool.Add(existing); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Re-register in pool: " + err.Error()})
		return
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
	} else {
		log.Warn("admin test: version probe", "panel_id", req.ID, "err", perr)
	}
	c.JSON(http.StatusOK, resp)
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
