package handler

import (
	"context"
	"errors"
	"net/http"
	"strconv"

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
	panels    ports.XUIPanelRepo
}

func NewAdminNodeHandler(nodeSvc *node.Service, syncSvc *syncsvc.Service, ownership ports.OwnershipRepo, panels ports.XUIPanelRepo) *AdminNodeHandler {
	return &AdminNodeHandler{node: nodeSvc, sync: syncSvc, ownership: ownership, panels: panels}
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

type claimRequest struct {
	UserID      int64  `json:"user_id" binding:"required"`
	PanelID     int64  `json:"panel_id" binding:"required"`
	InboundID   int    `json:"inbound_id" binding:"required"`
	ClientEmail string `json:"client_email" binding:"required"`
	ClientUUID  string `json:"client_uuid" binding:"required"`
}

// ---- Handlers ----

func (h *AdminNodeHandler) List(c *gin.Context) {
	nodes, err := h.node.List(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	out := make([]nodeDTO, len(nodes))
	for i, n := range nodes {
		out[i] = h.toNodeDTO(c.Request.Context(), n)
	}
	c.JSON(http.StatusOK, gin.H{"items": out})
}

func (h *AdminNodeHandler) Get(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	n, err := h.node.Get(c.Request.Context(), id)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	inbound, inboundErr := h.node.GetInboundConfig(c.Request.Context(), id)

	// Bundle the inbound clients so the detail page only needs one round-trip.
	clients, err := h.node.ListClientsOfInbound(c.Request.Context(), id, h.ownership)
	if err != nil {
		// Detail without clients is still useful; surface the error but don't 500.
		out := gin.H{
			"node":          h.toNodeDTO(c.Request.Context(), n),
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
		"node":    h.toNodeDTO(c.Request.Context(), n),
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
	c.JSON(http.StatusCreated, h.toNodeDTO(c.Request.Context(), n))
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
	c.JSON(http.StatusCreated, h.toNodeDTO(c.Request.Context(), n))
}

func (h *AdminNodeHandler) UpdateMetadata(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
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
	c.JSON(http.StatusOK, h.toNodeDTO(c.Request.Context(), n))
}

func (h *AdminNodeHandler) UpdateInboundConfig(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
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
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
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

func (h *AdminNodeHandler) Delete(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	if err := h.node.DeleteAndSync(c.Request.Context(), id); err != nil {
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
// touching 3X-UI. The frontend pre-fetches client_uuid via the unmanaged
// listing flow (TODO M2: surface uuid through a richer listing endpoint).
func (h *AdminNodeHandler) ClaimClient(c *gin.Context) {
	var req claimRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := h.sync.ClaimClient(c.Request.Context(), req.UserID, req.PanelID, req.InboundID, req.ClientEmail, req.ClientUUID); err != nil {
		if errors.Is(err, domain.ErrAlreadyExists) {
			c.JSON(http.StatusConflict, gin.H{"error": "client already managed"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.Status(http.StatusCreated)
}

// ---- helpers ----

func (h *AdminNodeHandler) toNodeDTO(ctx context.Context, n *domain.Node) nodeDTO {
	panelName := n.PanelName
	if h.panels != nil {
		if p, err := h.panels.GetByID(ctx, n.PanelID); err == nil && p != nil {
			panelName = p.Name
		}
	}
	return nodeDTO{
		ID:            n.ID,
		PanelID:       n.PanelID,
		PanelName:     panelName,
		InboundID:     n.InboundID,
		DisplayName:   n.DisplayName,
		ServerAddress: n.ServerAddress,
		Flow:          n.Flow,
		Region:        n.Region,
		Tags:          n.Tags,
		SortOrder:     n.SortOrder,
		Enabled:       n.Enabled,
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
