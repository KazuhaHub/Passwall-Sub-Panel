package handler

import (
	"context"
	"net/http"
	"slices"
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
	CheckAddable(ctx context.Context, n *domain.Node, lineID, ip string) error
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
	// Kind is "unregistered" (an address the line's class does not list) or
	// "wrong_line" (an identity used through the wrong line: the address is in a
	// class's set already).
	Kind string `json:"kind"`
	// Addable is whether adding the address to this line is the fix.
	Addable bool `json:"addable"`
}

type sourceGuardLineDTO struct {
	LineID  string `json:"line_id"`
	Guarded bool   `json:"guarded"`
	// Direct is whether the line costs what the direct line costs (it needs no
	// source addresses); Factor is its factor in thousandths.
	Direct    bool               `json:"direct"`
	Factor    int                `json:"factor_milli"`
	Effective []string           `json:"effective_cidrs"`
	Withheld  string             `json:"withheld,omitempty"`
	Refused   []refusedSourceDTO `json:"refused"`
}

type sourceGuardNodeDTO struct {
	CanGuard bool `json:"can_guard"`
	// CannotGuardReason is "server" (no agent that declared the capability) or
	// "listener" (the core would take the source from a client-sent header).
	CannotGuardReason string               `json:"cannot_guard_reason,omitempty"`
	Lines             []sourceGuardLineDTO `json:"lines"`
	Cloudflare        cloudflareRangesDTO  `json:"cloudflare"`
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
	out := sourceGuardNodeDTO{CanGuard: view.CanGuard, CannotGuardReason: view.Reason, Lines: make([]sourceGuardLineDTO, 0, len(view.Lines)), Cloudflare: h.cloudflare(c.Request.Context())}
	for _, line := range view.Lines {
		dto := sourceGuardLineDTO{LineID: line.LineID, Guarded: line.Guarded, Direct: line.Direct, Factor: line.Factor, Effective: append([]string{}, line.Effective...), Withheld: line.Withheld, Refused: []refusedSourceDTO{}}
		for _, r := range line.Refused {
			dto.Refused = append(dto.Refused, refusedSourceDTO{IP: r.IP, Count: r.Count, LastSeen: r.LastSeen, Kind: r.Kind, Addable: r.Addable})
		}
		out.Lines = append(out.Lines, dto)
	}
	c.JSON(http.StatusOK, out)
}

type addSourceRequest struct {
	IP string `json:"ip" binding:"required"`
}

// AddSource is POST /admin/nodes/:id/lines/:line_id/source-cidrs: the address a
// landing refused is added to the line's source addresses. Only an unregistered
// source of the line's class can be added, and only to a line with addresses of
// its own (a CDN line on the Cloudflare default has none); the write changes
// that line's addresses alone, under the locks of a node save, and the rules of
// that save apply.
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
	lineID := c.Param("line_id")
	index := slices.IndexFunc(n.Relays, func(r domain.RelayLine) bool { return r.ID == lineID })
	if index < 0 || !n.Relays[index].Guarded() {
		c.JSON(http.StatusNotFound, gin.H{"error": "Not found"})
		return
	}
	if err := h.view.CheckAddable(ctx, n, lineID, req.IP); err != nil {
		respondError(c, err)
		return
	}
	cidrs, err := h.node.AddLineSourceCIDR(ctx, id, lineID, cidr)
	if err != nil {
		mapNodeServiceError(c, err)
		return
	}
	if err := h.view.Forget(ctx, n, n.Relays[index], req.IP); err != nil {
		// The address is added; a warning that outlives it clears itself when the
		// landing stops refusing it.
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"source_cidrs": cidrs})
}
