package handler

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/node"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/sourceguard"
)

// SourceGuardView is the administrator's view of the source guard
// (service/sourceguard.Admin).
type SourceGuardView interface {
	Cloudflare(ctx context.Context) sourceguard.Ranges
	NodeView(ctx context.Context, n *domain.Node) (sourceguard.NodeView, error)
	Forget(ctx context.Context, n *domain.Node, line domain.RelayLine, ip string) error
}

// AdminSourceGuardHandler serves the line editor's source address panel and the
// one-click "add this refused address to the line".
type AdminSourceGuardHandler struct {
	view     SourceGuardView
	node     *node.Service
	settings ports.SettingsRepo
}

func NewAdminSourceGuardHandler(view SourceGuardView, nodeSvc *node.Service, settings ports.SettingsRepo) *AdminSourceGuardHandler {
	return &AdminSourceGuardHandler{view: view, node: nodeSvc, settings: settings}
}

type cloudflareRangesDTO struct {
	CIDRs []string `json:"cidrs"`
	// Source is "fetched" (the last good fetch from Cloudflare) or "builtin"
	// (the snapshot shipped with the panel).
	Source      string     `json:"source"`
	FetchedAt   *time.Time `json:"fetched_at,omitempty"`
	BuiltinDate string     `json:"builtin_date"`
	AutoFetch   bool       `json:"auto_fetch"`
}

type refusedSourceDTO struct {
	IP       string    `json:"ip"`
	Count    int64     `json:"count"`
	LastSeen time.Time `json:"last_seen"`
}

type sourceGuardLineDTO struct {
	LineID    string             `json:"line_id"`
	Guarded   bool               `json:"guarded"`
	Effective []string           `json:"effective_cidrs"`
	Withheld  string             `json:"withheld,omitempty"`
	Refused   []refusedSourceDTO `json:"refused"`
}

type sourceGuardNodeDTO struct {
	CanGuard   bool                 `json:"can_guard"`
	Lines      []sourceGuardLineDTO `json:"lines"`
	Cloudflare cloudflareRangesDTO  `json:"cloudflare"`
}

func (h *AdminSourceGuardHandler) cloudflare(ctx context.Context) cloudflareRangesDTO {
	r := h.view.Cloudflare(ctx)
	dto := cloudflareRangesDTO{CIDRs: r.CIDRs, Source: r.Source, BuiltinDate: r.BuiltinDate, AutoFetch: true}
	if r.Source == sourceguard.SourceFetched {
		at := r.FetchedAt
		dto.FetchedAt = &at
	}
	if h.settings != nil {
		if set, err := h.settings.Load(ctx, ports.UISettings{}); err == nil {
			dto.AutoFetch = !set.SourceGuardCloudflareFetchOff
		}
	}
	return dto
}

// GetCloudflare is GET /admin/source-guard/cloudflare: the ranges a CDN line
// defaults to, where they came from and when.
func (h *AdminSourceGuardHandler) GetCloudflare(c *gin.Context) {
	c.JSON(http.StatusOK, h.cloudflare(c.Request.Context()))
}

// Get is GET /admin/nodes/:id/source-guard: each line's effective source set,
// whether it is withheld, and the sources the landing refused for it.
func (h *AdminSourceGuardHandler) Get(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid id"})
		return
	}
	n, err := h.node.Get(c.Request.Context(), id)
	if err != nil {
		mapNodeServiceError(c, err)
		return
	}
	view, err := h.view.NodeView(c.Request.Context(), n)
	if err != nil {
		respondError(c, err)
		return
	}
	out := sourceGuardNodeDTO{CanGuard: view.CanGuard, Lines: make([]sourceGuardLineDTO, 0, len(view.Lines)), Cloudflare: h.cloudflare(c.Request.Context())}
	for _, line := range view.Lines {
		dto := sourceGuardLineDTO{LineID: line.LineID, Guarded: line.Guarded, Effective: append([]string{}, line.Effective...), Withheld: line.Withheld, Refused: []refusedSourceDTO{}}
		for _, r := range line.Refused {
			dto.Refused = append(dto.Refused, refusedSourceDTO{IP: r.IP, Count: r.Count, LastSeen: r.LastSeen})
		}
		out.Lines = append(out.Lines, dto)
	}
	c.JSON(http.StatusOK, out)
}

type addSourceRequest struct {
	IP string `json:"ip" binding:"required"`
}

// AddSource is POST /admin/nodes/:id/lines/:line_id/source-cidrs: the address a
// landing refused is added to the line's source addresses through the ordinary
// save of the node, so every rule of that save applies.
func (h *AdminSourceGuardHandler) AddSource(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid id"})
		return
	}
	var req addSourceRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	cidr, err := sourceguard.SourceToAdd(req.IP)
	if err != nil {
		respondError(c, err)
		return
	}
	ctx := c.Request.Context()
	n, err := h.node.Get(ctx, id)
	if err != nil {
		mapNodeServiceError(c, err)
		return
	}
	index := -1
	for i, line := range n.Relays {
		if line.ID == c.Param("line_id") {
			index = i
		}
	}
	if index < 0 || !n.Relays[index].Guarded() {
		c.JSON(http.StatusNotFound, gin.H{"error": "Not found"})
		return
	}
	line := n.Relays[index]
	n.Relays[index].SourceCIDRs = append(append([]string{}, line.SourceCIDRs...), cidr)
	if err := h.node.UpdateMetadata(ctx, n); err != nil {
		mapNodeServiceError(c, err)
		return
	}
	if err := h.view.Forget(ctx, n, line, req.IP); err != nil {
		// The address is added; a warning that outlives it clears itself when the
		// landing stops refusing it.
		respondError(c, err)
		return
	}
	saved, err := h.node.Get(ctx, id)
	if err != nil {
		mapNodeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"source_cidrs": sourceCIDRsOfLine(saved, line.ID)})
}

func sourceCIDRsOfLine(n *domain.Node, lineID string) []string {
	for _, line := range n.Relays {
		if line.ID == lineID {
			return append([]string{}, line.SourceCIDRs...)
		}
	}
	return []string{}
}
