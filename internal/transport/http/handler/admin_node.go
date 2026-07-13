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
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/realitykey"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/node"
	syncsvc "github.com/KazuhaHub/passwall-sub-panel/internal/service/sync"
	"github.com/KazuhaHub/passwall-sub-panel/internal/transport/http/middleware"
)

// AdminNodeHandler exposes node CRUD, the import-existing flow, and the
// claim-existing-client flow under /api/admin/nodes.
type AdminNodeHandler struct {
	node       *node.Service
	sync       *syncsvc.Service
	ownership  ports.OwnershipRepo
	users      ports.UserRepo
	panels     ports.XUIPanelRepo
	pspClients ports.PSPClientRepo
}

func NewAdminNodeHandler(nodeSvc *node.Service, syncSvc *syncsvc.Service, ownership ports.OwnershipRepo, users ports.UserRepo, panels ports.XUIPanelRepo, pspClients ports.PSPClientRepo) *AdminNodeHandler {
	return &AdminNodeHandler{node: nodeSvc, sync: syncSvc, ownership: ownership, users: users, panels: panels, pspClients: pspClients}
}

// ---- DTOs ----

type nodeDTO struct {
	ID            int64  `json:"id"`
	PanelID       int64  `json:"panel_id"`
	PanelName     string `json:"panel_name"`
	InboundID     int    `json:"inbound_id"`
	DisplayName   string `json:"display_name"`
	ServerAddress string `json:"server_address"`
	Flow          string `json:"flow,omitempty"`
	// Protocol caches the upstream inbound protocol so the UI can gate
	// protocol-specific fields (e.g. Flow is VLESS-only). Empty for rows
	// imported before this column existed.
	Protocol  string   `json:"protocol,omitempty"`
	Region    string   `json:"region"`
	Tags      []string `json:"tags"`
	SortOrder int      `json:"sort_order"`
	Enabled   bool     `json:"enabled"`
	// Kind is "real" for 3X-UI-backed nodes (default for legacy rows) and
	// "separator" for layout-only entries the admin uses to group the
	// subscription list. Frontend uses it to style separator rows and
	// hide irrelevant fields (server, panel, health).
	Kind string `json:"kind"`
	// Health surfaces the most recent probe outcome ("", "ok",
	// "panel_unreachable", "inbound_missing", "inbound_disabled"). Empty
	// before the first health check tick has run.
	HealthState     string     `json:"health_state,omitempty"`
	HealthCheckedAt *time.Time `json:"health_checked_at,omitempty"`
	HealthDetail    string     `json:"health_detail,omitempty"`
	// ConfigSyncState surfaces whether the local inbound-config snapshot (the
	// render truth source since v3.5) matches 3X-UI: "" (never captured — render
	// falls back to a live fetch for this node), "synced", "drift" (reconcile will
	// push the local config over the panel), or "pending" (last push/recapture
	// failed, retried next reconcile cycle). See docs/inbound-ownership.md.
	ConfigSyncState string     `json:"config_sync_state,omitempty"`
	ConfigSyncedAt  *time.Time `json:"config_synced_at,omitempty"`
	// CertSource/CertID surface the managed-certificate binding so the
	// node-edit form can pre-select the current source (psp_managed + which
	// cert). "" = unmanaged. Never carries any PEM/secret — just the binding.
	CertSource string `json:"cert_source,omitempty"`
	CertID     int64  `json:"cert_id,omitempty"`
	// Relays are the node's transit / 中转 lines; HideDirect drops the direct
	// entry when at least one line is enabled. Surfaced so the edit form can
	// round-trip them. Always present (possibly empty) for real nodes.
	Relays     []relayLineDTO `json:"relays"`
	HideDirect bool           `json:"hide_direct"`
}

// relayLineDTO is one transit front. Only the dialed endpoint differs from the
// landing; SNI/Host are optional CDN-fronting overrides. See domain.RelayLine.
type relayLineDTO struct {
	Name    string `json:"name"`
	Address string `json:"address"`
	Port    int    `json:"port"`
	SNI     string `json:"sni,omitempty"`
	Host    string `json:"host,omitempty"`
	Enabled bool   `json:"enabled"`
}

type inboundDTO struct {
	ID             int    `json:"id"`
	Remark         string `json:"remark"`
	Enable         bool   `json:"enable"`
	Listen         string `json:"listen"`
	Port           int    `json:"port"`
	Protocol       string `json:"protocol"`
	Settings       string `json:"settings"`
	StreamSettings string `json:"stream_settings"`
	Sniffing       string `json:"sniffing"`
	Allocate       string `json:"allocate"`
	ExpiryTime     int64  `json:"expiry_time"`
}

type importNodeRequest struct {
	PanelID       int64  `json:"panel_id" binding:"required"`
	InboundID     int    `json:"inbound_id" binding:"required"`
	DisplayName   string `json:"display_name" binding:"required"`
	ServerAddress string `json:"server_address" binding:"required"`
	Flow          string `json:"flow"`
	// Protocol of the source inbound (lowercased), cached on the node so the
	// UI can gate protocol-specific fields. Optional for backward compat.
	Protocol   string         `json:"protocol"`
	Region     string         `json:"region" binding:"required"`
	Tags       []string       `json:"tags"`
	SortOrder  int            `json:"sort_order"`
	Relays     []relayLineDTO `json:"relays"`
	HideDirect bool           `json:"hide_direct"`
}

type realityScanRequest struct {
	PanelID int64  `json:"panel_id" binding:"required"`
	Targets string `json:"targets"`
}

type createNodeRequest struct {
	PanelID       int64          `json:"panel_id" binding:"required"`
	DisplayName   string         `json:"display_name" binding:"required"`
	ServerAddress string         `json:"server_address" binding:"required"`
	Flow          string         `json:"flow"`
	Region        string         `json:"region" binding:"required"`
	Tags          []string       `json:"tags"`
	SortOrder     int            `json:"sort_order"`
	Relays        []relayLineDTO `json:"relays"`
	HideDirect    bool           `json:"hide_direct"`
	Inbound       inboundSpecDTO `json:"inbound" binding:"required"`
}

type inboundSpecDTO struct {
	Remark         string `json:"remark"`
	Enable         bool   `json:"enable"`
	Listen         string `json:"listen"`
	Port           int    `json:"port"`
	Protocol       string `json:"protocol"`
	Settings       string `json:"settings"`
	StreamSettings string `json:"stream_settings"`
	Sniffing       string `json:"sniffing"`
	Allocate       string `json:"allocate"`
	ExpiryTime     int64  `json:"expiry_time"`
}

type updateMetadataRequest struct {
	DisplayName   string   `json:"display_name"`
	ServerAddress string   `json:"server_address"`
	Flow          string   `json:"flow"`
	Region        string   `json:"region"`
	Tags          []string `json:"tags"`
	SortOrder     int      `json:"sort_order"`
	// Relays full-replaces the node's transit lines; nil (field absent) leaves
	// them untouched, an explicit [] clears them — same patch semantics as Tags.
	Relays     []relayLineDTO `json:"relays"`
	HideDirect bool           `json:"hide_direct"`
}

type setNodeEnabledRequest struct {
	Enabled bool `json:"enabled"`
}

type reorderRequestItem struct {
	ID        int64 `json:"id" binding:"required"`
	SortOrder int   `json:"sort_order"`
}

type reorderRequest struct {
	Items []reorderRequestItem `json:"items" binding:"required"`
}

type claimRequest struct {
	UserID      int64  `json:"user_id" binding:"required"`
	PanelID     int64  `json:"panel_id" binding:"required"`
	InboundID   int    `json:"inbound_id" binding:"required"`
	ClientEmail string `json:"client_email" binding:"required"`
	ClientUUID  string `json:"client_uuid"`
}

// ---- Handlers ----

func (h *AdminNodeHandler) List(c *gin.Context) {
	p := parsePagination(c)
	nodes, total, err := h.node.ListPaged(c.Request.Context(), p)
	if err != nil {
		respondError(c, err)
		return
	}
	// Batch load panel names to avoid N+1 queries.
	panelNames := h.loadPanelNames(c.Request.Context())
	out := make([]nodeDTO, len(nodes))
	for i, n := range nodes {
		out[i] = h.toNodeDTO(n, panelNames)
	}
	c.JSON(http.StatusOK, pagedEnvelope(out, total, p))
}

func (h *AdminNodeHandler) Get(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid id"})
		return
	}
	n, err := h.node.Get(c.Request.Context(), id)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "Not found"})
			return
		}
		respondError(c, err)
		return
	}

	inbound, inboundErr := h.node.GetInboundConfig(c.Request.Context(), id)
	// Operators may read node metadata but not the inbound's raw Settings /
	// StreamSettings (client creds + Reality privateKey) — strip them unless admin.
	isAdmin := callerIsAdmin(c)

	// Resolve only this node's panel name (one GetByID) instead of listing every
	// panel just to look up a single entry.
	panelNames := h.panelNameOf(c.Request.Context(), n.PanelID)

	// Bundle the inbound clients so the detail page only needs one round-trip.
	clients, err := h.node.ListClientsOfInbound(c.Request.Context(), id, h.ownership, h.pspClients)
	if err != nil {
		// Detail without clients is still useful; surface the error but don't 500.
		out := gin.H{
			"node":          h.toNodeDTO(n, panelNames),
			"clients":       []any{},
			"clients_error": err.Error(),
		}
		if inbound != nil {
			out["inbound"] = redactInboundForRole(toInboundDTO(inbound), isAdmin)
		}
		if inboundErr != nil {
			out["inbound_error"] = inboundErr.Error()
		}
		c.JSON(http.StatusOK, out)
		return
	}
	out := gin.H{
		"node":    h.toNodeDTO(n, panelNames),
		"clients": clients,
	}
	if inbound != nil {
		out["inbound"] = redactInboundForRole(toInboundDTO(inbound), isAdmin)
	}
	if inboundErr != nil {
		out["inbound_error"] = inboundErr.Error()
	}
	c.JSON(http.StatusOK, out)
}

// ---- Separator CRUD --------------------------------------------------------
//
// As of v3.0.0-beta.7, separators live in their own `nodes_separator`
// table and are bound to groups via an explicit ID list, not via
// tag_filter on a shared `nodes` row. Endpoints below are 1-to-1 with
// node.Service's separator methods.

type separatorRequest struct {
	DisplayName string `json:"display_name" binding:"required"`
	SortOrder   int    `json:"sort_order"`
	Enabled     *bool  `json:"enabled"` // nil → true (create default)
	// Mode is the visibility model: "global" (default) makes the row
	// visible in every group; "node_bound" gates it on NodeIDs ∩
	// (group's node set). Anything else falls back to "global" at
	// repo translation time.
	Mode    string  `json:"mode"`
	NodeIDs []int64 `json:"node_ids"`
}

type separatorDTO struct {
	ID          int64   `json:"id"`
	DisplayName string  `json:"display_name"`
	SortOrder   int     `json:"sort_order"`
	Enabled     bool    `json:"enabled"`
	Mode        string  `json:"mode"`
	NodeIDs     []int64 `json:"node_ids"`
	CreatedAt   string  `json:"created_at,omitempty"`
}

func toSeparatorDTO(e *domain.SeparatorEntry) separatorDTO {
	ids := e.NodeIDs
	if ids == nil {
		ids = []int64{}
	}
	return separatorDTO{
		ID:          e.ID,
		DisplayName: e.DisplayName,
		SortOrder:   e.SortOrder,
		Enabled:     e.Enabled,
		Mode:        string(e.Mode),
		NodeIDs:     ids,
		CreatedAt:   e.CreatedAt.Format(time.RFC3339),
	}
}

func separatorFromRequest(req *separatorRequest) *domain.SeparatorEntry {
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	// Default mode is "global" — same intent the old
	// ShowInAllGroups=true had. Anything other than the two valid
	// values is coerced to global by the domain/repo layer.
	mode := domain.SeparatorMode(req.Mode)
	if mode != domain.SeparatorModeGlobal && mode != domain.SeparatorModeNodeBound {
		mode = domain.SeparatorModeGlobal
	}
	ids := req.NodeIDs
	if ids == nil {
		ids = []int64{}
	}
	return &domain.SeparatorEntry{
		DisplayName: req.DisplayName,
		SortOrder:   req.SortOrder,
		Enabled:     enabled,
		Mode:        mode,
		NodeIDs:     ids,
	}
}

type separatorReorderItem struct {
	ID        int64 `json:"id" binding:"required"`
	SortOrder int   `json:"sort_order"`
}

type separatorReorderRequest struct {
	Items []separatorReorderItem `json:"items" binding:"required"`
}

// ReorderSeparators bulk-rewrites sort_order on the separator table.
// Paired with the existing Reorder(): the admin's drag-to-reorder UI
// splits the mixed (node + separator) list and issues one PUT per
// kind so each backend handler operates on a homogeneous payload —
// avoids "is this ID a node or a separator?" guesswork.
func (h *AdminNodeHandler) ReorderSeparators(c *gin.Context) {
	var req separatorReorderRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	updates := make([]ports.SeparatorSortUpdate, len(req.Items))
	for i, it := range req.Items {
		updates[i] = ports.SeparatorSortUpdate{SeparatorID: it.ID, SortOrder: it.SortOrder}
	}
	if err := h.node.ReorderSeparators(c.Request.Context(), updates); err != nil {
		mapNodeServiceError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *AdminNodeHandler) ListSeparators(c *gin.Context) {
	p := parsePagination(c)
	items, total, err := h.node.ListSeparatorsPaged(c.Request.Context(), p)
	if err != nil {
		mapNodeServiceError(c, err)
		return
	}
	out := make([]separatorDTO, 0, len(items))
	for _, e := range items {
		out = append(out, toSeparatorDTO(e))
	}
	c.JSON(http.StatusOK, pagedEnvelope(out, total, p))
}

func (h *AdminNodeHandler) CreateSeparator(c *gin.Context) {
	var req separatorRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	e := separatorFromRequest(&req)
	if err := h.node.CreateSeparator(c.Request.Context(), e); err != nil {
		mapNodeServiceError(c, err)
		return
	}
	c.JSON(http.StatusCreated, toSeparatorDTO(e))
}

func (h *AdminNodeHandler) UpdateSeparator(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	var req separatorRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	e := separatorFromRequest(&req)
	e.ID = id
	if err := h.node.UpdateSeparator(c.Request.Context(), e); err != nil {
		mapNodeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, toSeparatorDTO(e))
}

func (h *AdminNodeHandler) DeleteSeparator(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	if err := h.node.DeleteSeparator(c.Request.Context(), id); err != nil {
		mapNodeServiceError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *AdminNodeHandler) ImportExisting(c *gin.Context) {
	var req importNodeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	relays, err := parseRelays(req.Relays)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	n := &domain.Node{
		PanelID:       req.PanelID,
		InboundID:     req.InboundID,
		DisplayName:   req.DisplayName,
		ServerAddress: req.ServerAddress,
		Flow:          req.Flow,
		Protocol:      strings.ToLower(req.Protocol),
		Region:        req.Region,
		Tags:          req.Tags,
		SortOrder:     req.SortOrder,
		Relays:        relays,
		HideDirect:    req.HideDirect,
	}
	if err := h.node.ImportExisting(c.Request.Context(), n); err != nil {
		mapNodeServiceError(c, err)
		return
	}
	panelNames := h.loadPanelNames(c.Request.Context())
	c.JSON(http.StatusCreated, h.toNodeDTO(n, panelNames))
}

func (h *AdminNodeHandler) CreateInbound(c *gin.Context) {
	var req createNodeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	relays, err := parseRelays(req.Relays)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	n := &domain.Node{
		PanelID:       req.PanelID,
		DisplayName:   req.DisplayName,
		ServerAddress: req.ServerAddress,
		Flow:          req.Flow,
		Protocol:      strings.ToLower(req.Inbound.Protocol),
		Region:        req.Region,
		Tags:          req.Tags,
		SortOrder:     req.SortOrder,
		Relays:        relays,
		HideDirect:    req.HideDirect,
	}
	spec := ports.InboundSpec{
		Remark:         req.Inbound.Remark,
		Enable:         req.Inbound.Enable,
		Listen:         req.Inbound.Listen,
		Port:           req.Inbound.Port,
		Protocol:       req.Inbound.Protocol,
		Settings:       req.Inbound.Settings,
		StreamSettings: req.Inbound.StreamSettings,
		Sniffing:       req.Inbound.Sniffing,
		Allocate:       req.Inbound.Allocate,
		ExpiryTime:     req.Inbound.ExpiryTime,
	}
	if err := h.node.CreateInbound(c.Request.Context(), n, spec); err != nil {
		mapNodeServiceError(c, err)
		return
	}
	if n.ID == 0 {
		c.JSON(http.StatusAccepted, gin.H{"queued": true})
		return
	}
	panelNames := h.loadPanelNames(c.Request.Context())
	c.JSON(http.StatusCreated, h.toNodeDTO(n, panelNames))
}

func (h *AdminNodeHandler) UpdateMetadata(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid id"})
		return
	}
	var req updateMetadataRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	relays, err := parseRelays(req.Relays)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	n, err := h.node.Get(c.Request.Context(), id)
	if err != nil {
		mapNodeServiceError(c, err)
		return
	}
	if req.DisplayName != "" {
		n.DisplayName = req.DisplayName
	}
	if req.ServerAddress != "" {
		n.ServerAddress = req.ServerAddress
	}
	n.Flow = req.Flow
	if req.Region != "" {
		n.Region = req.Region
	}
	if req.Tags != nil {
		n.Tags = req.Tags
	}
	n.SortOrder = req.SortOrder
	// Relays: nil (field absent) leaves them — and HideDirect — untouched, so a
	// relay-unaware client never disturbs them; an explicit [] clears the lines.
	if req.Relays != nil {
		n.Relays = relays
		n.HideDirect = req.HideDirect
	}
	if err := h.node.UpdateMetadata(c.Request.Context(), n); err != nil {
		mapNodeServiceError(c, err)
		return
	}
	panelNames := h.loadPanelNames(c.Request.Context())
	c.JSON(http.StatusOK, h.toNodeDTO(n, panelNames))
}

func (h *AdminNodeHandler) UpdateInboundConfig(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid id"})
		return
	}
	var req inboundSpecDTO
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	spec := ports.InboundSpec{
		Remark:         req.Remark,
		Enable:         req.Enable,
		Listen:         req.Listen,
		Port:           req.Port,
		Protocol:       req.Protocol,
		Settings:       req.Settings,
		StreamSettings: req.StreamSettings,
		Sniffing:       req.Sniffing,
		Allocate:       req.Allocate,
		ExpiryTime:     req.ExpiryTime,
	}
	if err := h.node.UpdateInboundConfig(c.Request.Context(), id, spec); err != nil {
		mapNodeServiceError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *AdminNodeHandler) SetEnabled(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid id"})
		return
	}
	var req setNodeEnabledRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := h.node.SetEnabled(c.Request.Context(), id, req.Enabled); err != nil {
		mapNodeServiceError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// RecreateInbound rebuilds a node's inbound on its (repointed/empty) server from
// PSP's captured config snapshot and relinks the node to the new inbound id. Used
// after moving a node's Server to a fresh 3X-UI that shows "Connected (0)".
func (h *AdminNodeHandler) RecreateInbound(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid id"})
		return
	}
	if err := h.node.RecreateInboundOnServer(c.Request.Context(), id); err != nil {
		mapNodeServiceError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// Reorder accepts a bulk (node_id, sort_order) list and rewrites sort_order
// for every listed node in one transaction. Powers drag-to-reorder in the
// admin nodes table.
func (h *AdminNodeHandler) Reorder(c *gin.Context) {
	var req reorderRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	updates := make([]ports.NodeSortUpdate, len(req.Items))
	for i, it := range req.Items {
		updates[i] = ports.NodeSortUpdate{NodeID: it.ID, SortOrder: it.SortOrder}
	}
	if err := h.node.Reorder(c.Request.Context(), updates); err != nil {
		mapNodeServiceError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *AdminNodeHandler) Delete(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid id"})
		return
	}
	if err := h.node.DeleteAndSync(c.Request.Context(), id); err != nil {
		mapNodeServiceError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// Detach drops the node record and the panel's ownership whitelist for the
// inbound without contacting 3X-UI. Use when the upstream server is offline
// or decommissioned and queueing a remote delete would just retry forever;
// any panel-created clients remain in 3X-UI for the admin to clean up there.
func (h *AdminNodeHandler) Detach(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid id"})
		return
	}
	if err := h.node.DetachAndSync(c.Request.Context(), id); err != nil {
		mapNodeServiceError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *AdminNodeHandler) ListUnmanaged(c *gin.Context) {
	// The unmanaged list is scoped to one server: the admin picks a panel and
	// only that panel is queried. Without an explicit panel_id we return an
	// empty list rather than scanning every panel (which would block on the
	// slowest / dead one).
	panelID, err := strconv.ParseInt(c.Query("panel_id"), 10, 64)
	if err != nil || panelID <= 0 {
		c.JSON(http.StatusOK, gin.H{"items": []any{}})
		return
	}
	items, err := h.node.ListUnmanagedInbounds(c.Request.Context(), panelID)
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}

// GenerateRealityKeypair returns a fresh X25519 keypair + shortID for use
// when admin creates a new Reality inbound. Frontend embeds these in the
// streamSettings JSON it composes for POST /api/admin/nodes.
func (h *AdminNodeHandler) GenerateRealityKeypair(c *gin.Context) {
	priv, pub, err := realitykey.GenerateKeypair()
	if err != nil {
		respondError(c, err)
		return
	}
	shortID, err := realitykey.GenerateShortID()
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"private_key": priv,
		"public_key":  pub,
		"short_id":    shortID,
	})
}

// ScanRealityTargets executes discovery on the selected 3X-UI host. PSP only
// authenticates, validates, and relays the request; it never dials the supplied
// domain/IP/CIDR itself, so reachability and latency reflect the Xray node.
func (h *AdminNodeHandler) ScanRealityTargets(c *gin.Context) {
	var req realityScanRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	// Upstream already caps tasks at 512, but cap the textual request too so a
	// valid admin session cannot make PSP parse/proxy an unbounded token list.
	if len(req.Targets) > 16*1024 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "targets is too long"})
		return
	}
	if h.panels == nil {
		respondError(c, errors.New("panel repository not configured"))
		return
	}
	panel, err := h.panels.GetByID(c.Request.Context(), req.PanelID)
	if err != nil {
		mapNodeServiceError(c, err)
		return
	}
	items, err := h.node.ScanRealityTargets(c.Request.Context(), req.PanelID, req.Targets)
	if err != nil {
		if errors.Is(err, ports.ErrXUIEndpointUnsupported) {
			c.JSON(http.StatusConflict, gin.H{
				"error": "selected 3X-UI server does not support REALITY target scanning; upgrade it to >= 3.4.2",
			})
			return
		}
		mapNodeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"source_panel_id":   panel.ID,
		"source_panel_name": panel.Name,
		"items":             items,
	})
}

// ClaimClient adopts an existing 3X-UI client under a panel user without
// touching 3X-UI. Some protocols can have an empty client id, so email is the
// stable lookup key and client_uuid is optional.
func (h *AdminNodeHandler) ClaimClient(c *gin.Context) {
	var req claimRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	preExisting, err := h.ownership.ListByUser(c.Request.Context(), req.UserID)
	if err != nil {
		respondError(c, err)
		return
	}
	// MIGRATION(v3→v4): ownership+psp combined count. When the legacy path goes,
	// count psp_client only (drop the preExisting/ownership term).
	// v3.9.0: a migrated user owns SHARED clients, not ownership rows — so an
	// ownership-only count is 0 post-migration and the "already owns clients →
	// don't overwrite the UUID" guard in alignClaimedUserUUID would never fire,
	// silently clobbering the user's UUID and re-keying all their nodes. Count the
	// user's shared clients too.
	preExistingOwned := len(preExisting)
	if h.pspClients != nil {
		if shared, perr := h.pspClients.ListByUser(c.Request.Context(), req.UserID); perr == nil {
			preExistingOwned += len(shared)
		}
	}
	claimedUUID, err := h.sync.ClaimClient(c.Request.Context(), req.UserID, req.PanelID, req.InboundID, req.ClientEmail, req.ClientUUID)
	if err != nil {
		if errors.Is(err, domain.ErrAlreadyExists) {
			c.JSON(http.StatusConflict, gin.H{"error": "Client already managed"})
			return
		}
		respondError(c, err)
		return
	}
	if claimedUUID != "" {
		if err := h.alignClaimedUserUUID(c.Request.Context(), req.UserID, req.PanelID, req.InboundID, req.ClientEmail, claimedUUID, preExistingOwned); err != nil {
			if errors.Is(err, domain.ErrValidation) {
				c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
				return
			}
			respondError(c, err)
			return
		}
	}
	c.Status(http.StatusCreated)
}

func (h *AdminNodeHandler) alignClaimedUserUUID(ctx context.Context, userID, panelID int64, inboundID int, email, claimedUUID string, preExistingOwned int) error {
	if h.users == nil {
		return nil
	}
	u, err := h.users.GetByID(ctx, userID)
	if err != nil {
		_ = h.ownership.RemoveByMatch(ctx, panelID, inboundID, email)
		return err
	}
	if u.UUID == claimedUUID {
		return nil
	}
	if preExistingOwned > 0 {
		_ = h.ownership.RemoveByMatch(ctx, panelID, inboundID, email)
		return fmt.Errorf("%w: claimed client UUID differs from a user that already owns other clients; claim it to a fresh user or reset credentials first", domain.ErrValidation)
	}
	u.UUID = claimedUUID
	if err := h.users.Update(ctx, u); err != nil {
		_ = h.ownership.RemoveByMatch(ctx, panelID, inboundID, email)
		return err
	}
	return nil
}

// ---- helpers ----

// loadPanelNames fetches all panels and returns a map of panelID -> panelName.
// panelNameOf resolves only the named node's panel, for the single-node detail
// view — avoiding loadPanelNames' full List() when toNodeDTO needs just one
// name. Returns a one-entry map so it drops straight into toNodeDTO.
func (h *AdminNodeHandler) panelNameOf(ctx context.Context, panelID int64) map[int64]string {
	names := make(map[int64]string, 1)
	if h.panels == nil {
		return names
	}
	if p, err := h.panels.GetByID(ctx, panelID); err == nil && p != nil {
		names[panelID] = p.Name
	}
	return names
}

func (h *AdminNodeHandler) loadPanelNames(ctx context.Context) map[int64]string {
	names := make(map[int64]string)
	if h.panels == nil {
		return names
	}
	panels, err := h.panels.List(ctx)
	if err != nil {
		return names
	}
	for _, p := range panels {
		names[p.ID] = p.Name
	}
	return names
}

// maxRelayLines caps the transit lines per node — a sane ceiling so a typo or
// malicious payload can't bloat the row / the rendered subscription.
const maxRelayLines = 64

// parseRelays validates + normalises the transit-line payload into the domain
// shape. Whitespace is trimmed; a line must carry an address; the port is
// either 0 (reuse the inbound port) or a valid 1-65535. Returns a friendly
// error suitable for a 400.
func parseRelays(dtos []relayLineDTO) ([]domain.RelayLine, error) {
	if len(dtos) == 0 {
		return nil, nil
	}
	if len(dtos) > maxRelayLines {
		return nil, fmt.Errorf("too many relay lines (%d, max %d)", len(dtos), maxRelayLines)
	}
	out := make([]domain.RelayLine, 0, len(dtos))
	for i, d := range dtos {
		addr := strings.TrimSpace(d.Address)
		if addr == "" {
			return nil, fmt.Errorf("relay line %d: address is required", i+1)
		}
		if d.Port < 0 || d.Port > 65535 {
			return nil, fmt.Errorf("relay line %d: port %d out of range (0-65535; 0 reuses the inbound port)", i+1, d.Port)
		}
		out = append(out, domain.RelayLine{
			Name:    strings.TrimSpace(d.Name),
			Address: addr,
			Port:    d.Port,
			SNI:     strings.TrimSpace(d.SNI),
			Host:    strings.TrimSpace(d.Host),
			Enabled: d.Enabled,
		})
	}
	return out, nil
}

// relayDTOs maps the domain shape back for the API response.
func relayDTOs(relays []domain.RelayLine) []relayLineDTO {
	out := make([]relayLineDTO, 0, len(relays))
	for _, r := range relays {
		out = append(out, relayLineDTO{
			Name:    r.Name,
			Address: r.Address,
			Port:    r.Port,
			SNI:     r.SNI,
			Host:    r.Host,
			Enabled: r.Enabled,
		})
	}
	return out
}

func (h *AdminNodeHandler) toNodeDTO(n *domain.Node, panelNames map[int64]string) nodeDTO {
	// Panel name is resolved from the in-memory pool snapshot (panelNames map)
	// rather than from the DB row — the v3 schema dropped the redundant
	// panel_name column on nodes since renaming a panel would otherwise leave
	// every historical row stale.
	panelName := panelNames[n.PanelID]
	return nodeDTO{
		ID:              n.ID,
		PanelID:         n.PanelID,
		PanelName:       panelName,
		InboundID:       n.InboundID,
		DisplayName:     n.DisplayName,
		ServerAddress:   n.ServerAddress,
		Flow:            n.Flow,
		Protocol:        n.Protocol,
		Region:          n.Region,
		Tags:            n.Tags,
		SortOrder:       n.SortOrder,
		Enabled:         n.Enabled,
		HealthState:     string(n.HealthState),
		HealthCheckedAt: n.HealthCheckedAt,
		HealthDetail:    n.HealthDetail,
		ConfigSyncState: n.ConfigSyncState,
		ConfigSyncedAt:  n.ConfigSyncedAt,
		Kind:            string(n.Kind),
		CertSource:      string(n.CertSource),
		CertID:          n.CertID,
		Relays:          relayDTOs(n.Relays),
		HideDirect:      n.HideDirect,
	}
}

func toInboundDTO(inb *ports.Inbound) inboundDTO {
	return inboundDTO{
		ID:             inb.ID,
		Remark:         inb.Remark,
		Enable:         inb.Enable,
		Listen:         inb.Listen,
		Port:           inb.Port,
		Protocol:       inb.Protocol,
		Settings:       inb.Settings,
		StreamSettings: inb.StreamSettings,
		Sniffing:       inb.Sniffing,
		Allocate:       inb.Allocate,
		ExpiryTime:     inb.ExpiryTime,
	}
}

// redactInboundForRole blanks the two secret-bearing raw config blobs for
// non-admin callers: Settings holds every client's UUID (which derives all
// protocol credentials) and SS/Trojan passwords; StreamSettings holds the
// Reality privateKey / inline TLS keys. The node-detail GET is on staffGroup
// (admin+operator), but operators are low-trust and every node WRITE path is
// already admin-only — so an operator reading these would be a credential-
// disclosure hole. Non-secret fields (protocol/port/listen/remark/…) stay so
// the operator's read-only detail view still works.
func redactInboundForRole(dto inboundDTO, isAdmin bool) inboundDTO {
	if isAdmin {
		return dto
	}
	dto.Settings = ""
	dto.StreamSettings = ""
	return dto
}

// callerIsAdmin reports whether the request's JWT claims carry the admin role.
func callerIsAdmin(c *gin.Context) bool {
	claims := middleware.ClaimsFrom(c)
	return claims != nil && claims.Role == domain.RoleAdmin
}

func mapNodeServiceError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, domain.ErrNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
	case errors.Is(err, domain.ErrAlreadyExists):
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
	case errors.Is(err, domain.ErrValidation):
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
	case errors.Is(err, domain.ErrInboundHasUnmanagedClients):
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
	default:
		respondError(c, err)
	}
}
