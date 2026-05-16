package handler

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/realitykey"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/node"
	syncsvc "github.com/KazuhaHub/passwall-sub-panel/internal/service/sync"
)

// AdminNodeHandler exposes node CRUD, the import-existing flow, and the
// claim-existing-client flow under /api/admin/nodes.
type AdminNodeHandler struct {
	node      *node.Service
	sync      *syncsvc.Service
	ownership ports.OwnershipRepo
	users     ports.UserRepo
	panels    ports.XUIPanelRepo
}

func NewAdminNodeHandler(nodeSvc *node.Service, syncSvc *syncsvc.Service, ownership ports.OwnershipRepo, users ports.UserRepo, panels ports.XUIPanelRepo) *AdminNodeHandler {
	return &AdminNodeHandler{node: nodeSvc, sync: syncSvc, ownership: ownership, users: users, panels: panels}
}

// ---- DTOs ----

type nodeDTO struct {
	ID            int64    `json:"id"`
	PanelID       int64    `json:"panel_id"`
	PanelName     string   `json:"panel_name"`
	InboundID     int      `json:"inbound_id"`
	DisplayName   string   `json:"display_name"`
	ServerAddress string   `json:"server_address"`
	Flow          string   `json:"flow,omitempty"`
	Region        string   `json:"region"`
	Tags          []string `json:"tags"`
	SortOrder     int      `json:"sort_order"`
	Enabled       bool     `json:"enabled"`
	// Health surfaces the most recent probe outcome ("", "ok",
	// "panel_unreachable", "inbound_missing", "inbound_disabled"). Empty
	// before the first health check tick has run.
	HealthState     string     `json:"health_state,omitempty"`
	HealthCheckedAt *time.Time `json:"health_checked_at,omitempty"`
	HealthDetail    string     `json:"health_detail,omitempty"`
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
	PanelID       int64    `json:"panel_id" binding:"required"`
	InboundID     int      `json:"inbound_id" binding:"required"`
	DisplayName   string   `json:"display_name" binding:"required"`
	ServerAddress string   `json:"server_address" binding:"required"`
	Flow          string   `json:"flow"`
	Region        string   `json:"region" binding:"required"`
	Tags          []string `json:"tags"`
	SortOrder     int      `json:"sort_order"`
}

type createNodeRequest struct {
	PanelID       int64          `json:"panel_id" binding:"required"`
	DisplayName   string         `json:"display_name" binding:"required"`
	ServerAddress string         `json:"server_address" binding:"required"`
	Flow          string         `json:"flow"`
	Region        string         `json:"region" binding:"required"`
	Tags          []string       `json:"tags"`
	SortOrder     int            `json:"sort_order"`
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
	nodes, err := h.node.List(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	// Batch load panel names to avoid N+1 queries.
	panelNames := h.loadPanelNames(c.Request.Context())
	out := make([]nodeDTO, len(nodes))
	for i, n := range nodes {
		out[i] = h.toNodeDTO(n, panelNames)
	}
	c.JSON(http.StatusOK, gin.H{"items": out})
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
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	inbound, inboundErr := h.node.GetInboundConfig(c.Request.Context(), id)

	// Load panel name for this node.
	panelNames := h.loadPanelNames(c.Request.Context())

	// Bundle the inbound clients so the detail page only needs one round-trip.
	clients, err := h.node.ListClientsOfInbound(c.Request.Context(), id, h.ownership)
	if err != nil {
		// Detail without clients is still useful; surface the error but don't 500.
		out := gin.H{
			"node":          h.toNodeDTO(n, panelNames),
			"clients":       []any{},
			"clients_error": err.Error(),
		}
		if inbound != nil {
			out["inbound"] = toInboundDTO(inbound)
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
		out["inbound"] = toInboundDTO(inbound)
	}
	if inboundErr != nil {
		out["inbound_error"] = inboundErr.Error()
	}
	c.JSON(http.StatusOK, out)
}

func (h *AdminNodeHandler) ImportExisting(c *gin.Context) {
	var req importNodeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	n := &domain.Node{
		PanelID:       req.PanelID,
		InboundID:     req.InboundID,
		DisplayName:   req.DisplayName,
		ServerAddress: req.ServerAddress,
		Flow:          req.Flow,
		Region:        req.Region,
		Tags:          req.Tags,
		SortOrder:     req.SortOrder,
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
	n := &domain.Node{
		PanelID:       req.PanelID,
		DisplayName:   req.DisplayName,
		ServerAddress: req.ServerAddress,
		Flow:          req.Flow,
		Region:        req.Region,
		Tags:          req.Tags,
		SortOrder:     req.SortOrder,
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

// Detach drops the node record and removes only the panel-managed clients
// from 3X-UI; the inbound itself and any unmanaged clients are preserved.
// Use this when an admin wants to stop managing an inbound without losing
// the upstream resource — for example, an inbound that's also used by
// non-panel users.
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
	items, err := h.node.ListUnmanagedInbounds(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
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
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	shortID, err := realitykey.GenerateShortID()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"private_key": priv,
		"public_key":  pub,
		"short_id":    shortID,
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
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	claimedUUID, err := h.sync.ClaimClient(c.Request.Context(), req.UserID, req.PanelID, req.InboundID, req.ClientEmail, req.ClientUUID)
	if err != nil {
		if errors.Is(err, domain.ErrAlreadyExists) {
			c.JSON(http.StatusConflict, gin.H{"error": "Client already managed"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if claimedUUID != "" {
		if err := h.alignClaimedUserUUID(c.Request.Context(), req.UserID, req.PanelID, req.InboundID, req.ClientEmail, claimedUUID, len(preExisting)); err != nil {
			if errors.Is(err, domain.ErrValidation) {
				c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
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

func (h *AdminNodeHandler) toNodeDTO(n *domain.Node, panelNames map[int64]string) nodeDTO {
	panelName := n.PanelName
	if name, ok := panelNames[n.PanelID]; ok {
		panelName = name
	}
	return nodeDTO{
		ID:              n.ID,
		PanelID:         n.PanelID,
		PanelName:       panelName,
		InboundID:       n.InboundID,
		DisplayName:     n.DisplayName,
		ServerAddress:   n.ServerAddress,
		Flow:            n.Flow,
		Region:          n.Region,
		Tags:            n.Tags,
		SortOrder:       n.SortOrder,
		Enabled:         n.Enabled,
		HealthState:     string(n.HealthState),
		HealthCheckedAt: n.HealthCheckedAt,
		HealthDetail:    n.HealthDetail,
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
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
	}
}
