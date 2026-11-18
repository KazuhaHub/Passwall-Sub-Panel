package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/route"
)

// AdminRouteHandler serves the routes by destination of a node
// (/api/admin/nodes/:id/routes), transit.route.v1. adminGroup only. Every save
// is audited by name, with the routes before and after.
type AdminRouteHandler struct {
	routes *route.Service
	audit  ports.AuditRepo
}

// NewAdminRouteHandler serves the routes.
func NewAdminRouteHandler(routes *route.Service, audit ports.AuditRepo) *AdminRouteHandler {
	return &AdminRouteHandler{routes: routes, audit: audit}
}

type routeTargetDTO struct {
	Kind    string `json:"kind"`
	PanelID int64  `json:"panel_id,omitempty"`
}

type routeDTO struct {
	ID      string         `json:"id,omitempty"`
	Name    string         `json:"name,omitempty"`
	Target  routeTargetDTO `json:"target"`
	ListIDs []int64        `json:"list_ids"`
	Domains []string       `json:"domains"`
	CIDRs   []string       `json:"cidrs"`
	Ports   string         `json:"ports"`
	Network string         `json:"network"`
}

func routeOf(r domain.DestRoute) routeDTO {
	out := routeDTO{ID: r.ID, Name: r.Name, Target: routeTargetDTO{Kind: string(r.Target.Kind), PanelID: r.Target.PanelID},
		ListIDs: r.ListIDs, Domains: r.Domains, CIDRs: r.CIDRs, Ports: r.Ports, Network: r.Network}
	if out.ListIDs == nil {
		out.ListIDs = []int64{}
	}
	if out.Domains == nil {
		out.Domains = []string{}
	}
	if out.CIDRs == nil {
		out.CIDRs = []string{}
	}
	return out
}

func (r routeDTO) domain() domain.DestRoute {
	return domain.DestRoute{ID: r.ID, Name: r.Name,
		Target:  domain.DestRouteTarget{Kind: domain.DestRouteTargetKind(r.Target.Kind), PanelID: r.Target.PanelID},
		ListIDs: r.ListIDs, Domains: r.Domains, CIDRs: r.CIDRs, Ports: r.Ports, Network: r.Network}
}

type routeNoteDTO struct {
	Code   string `json:"code"`
	ListID int64  `json:"list_id,omitempty"`
	List   string `json:"list,omitempty"`
	Count  int    `json:"count,omitempty"`
}

type routeLinkDTO struct {
	PanelID    int64  `json:"panel_id"`
	ServerName string `json:"server_name,omitempty"`
	Gone       bool   `json:"gone,omitempty"`
	Exists     bool   `json:"exists"`
	Ready      bool   `json:"ready"`
	InApplied  bool   `json:"in_applied"`
	OutApplied bool   `json:"out_applied"`
	// PortsDiffer says the exit and the entry were last sent different ports.
	PortsDiffer bool             `json:"ports_differ,omitempty"`
	InPort      int              `json:"in_port,omitempty"`
	OutPort     int              `json:"out_port,omitempty"`
	Quality     *probeQualityDTO `json:"quality,omitempty"`
}

func routeLinkOf(l *route.LinkState) *routeLinkDTO {
	if l == nil {
		return nil
	}
	return &routeLinkDTO{PanelID: l.PanelID, ServerName: l.ServerName, Gone: l.Gone, Exists: l.Exists, Ready: l.Ready,
		InApplied: l.InApplied, OutApplied: l.OutApplied,
		PortsDiffer: l.PortsDiffer, InPort: l.InPort, OutPort: l.OutPort, Quality: probeQualityOf(l.Quality)}
}

type routeItemDTO struct {
	routeDTO
	Entries int            `json:"entries"`
	Sent    bool           `json:"sent"`
	Notes   []routeNoteDTO `json:"notes"`
	Link    *routeLinkDTO  `json:"link,omitempty"`
}

type routeWarningDTO struct {
	Code     string         `json:"code"`
	Message  string         `json:"message"`
	Params   map[string]any `json:"params,omitempty"`
	Listener string         `json:"listener,omitempty"`
	RouteID  string         `json:"route_id,omitempty"`
}

func routeWarningsOf(in []route.Warning) []routeWarningDTO {
	out := make([]routeWarningDTO, 0, len(in))
	for _, w := range in {
		out = append(out, routeWarningDTO{Code: w.Code, Message: w.Message, Params: w.Params, Listener: w.Listener, RouteID: w.RouteID})
	}
	return out
}

type routesResponse struct {
	Supported   bool              `json:"supported"`
	Reason      string            `json:"reason,omitempty"`
	Routes      []routeItemDTO    `json:"routes"`
	DefaultExit routeTargetDTO    `json:"default_exit"`
	DefaultLink *routeLinkDTO     `json:"default_link,omitempty"`
	Sent        bool              `json:"sent"`
	Running     bool              `json:"running"`
	SentAt      *time.Time        `json:"sent_at,omitempty"`
	AppliedAt   *time.Time        `json:"applied_at,omitempty"`
	Warnings    []routeWarningDTO `json:"warnings"`
	Budget      *route.BudgetView `json:"budget,omitempty"`
}

func routesResponseOf(v route.View) routesResponse {
	out := routesResponse{
		Supported: v.Support.Supported, Reason: v.Support.Reason, Routes: []routeItemDTO{},
		DefaultExit: routeTargetDTO{Kind: string(v.Default.Kind), PanelID: v.Default.PanelID}, DefaultLink: routeLinkOf(v.DefaultLink),
		Sent: v.Sent, Running: v.Running, SentAt: v.SentAt, AppliedAt: v.AppliedAt,
		Warnings: routeWarningsOf(v.Warnings), Budget: v.Budget,
	}
	state := map[string]route.RouteState{}
	for _, item := range v.Items {
		state[item.ID] = item
	}
	for _, r := range v.Routes {
		item := routeItemDTO{routeDTO: routeOf(r), Notes: []routeNoteDTO{}}
		if st, ok := state[r.ID]; ok {
			item.Entries, item.Sent, item.Link = st.Entries, st.Sent, routeLinkOf(st.Link)
			for _, note := range st.Notes {
				item.Notes = append(item.Notes, routeNoteDTO{Code: note.Code, ListID: note.ListID, List: note.List, Count: note.Count})
			}
		}
		out.Routes = append(out.Routes, item)
	}
	return out
}

func routeNodeID(c *gin.Context) (int64, bool) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid id"})
		return 0, false
	}
	return id, true
}

// respondRouteError answers a refusal with its stable code and parameters, so
// the editor writes its own wording; the English message is the fallback.
func respondRouteError(c *gin.Context, err error) {
	var refusal *route.Refusal
	if errors.As(err, &refusal) {
		c.JSON(http.StatusBadRequest, gin.H{"error": refusal.Message, "code": refusal.Code, "params": refusal.Params})
		return
	}
	mapNodeServiceError(c, err)
}

// Get answers a node's routes, with whether its server can run them, how each
// route is delivered and the warnings they carry.
func (h *AdminRouteHandler) Get(c *gin.Context) {
	id, ok := routeNodeID(c)
	if !ok {
		return
	}
	view, err := h.routes.View(c.Request.Context(), id)
	if err != nil {
		respondRouteError(c, err)
		return
	}
	c.JSON(http.StatusOK, routesResponseOf(view))
}

type routesRequest struct {
	Routes []routeDTO `json:"routes"`
}

func (r routesRequest) input() []domain.DestRoute {
	out := make([]domain.DestRoute, 0, len(r.Routes))
	for _, item := range r.Routes {
		out = append(out, item.domain())
	}
	return out
}

// Put replaces a node's routes and answers the stored routes with the warnings
// of the save.
func (h *AdminRouteHandler) Put(c *gin.Context) {
	h.save(c, true)
}

// Check answers what a save would, and writes nothing.
func (h *AdminRouteHandler) Check(c *gin.Context) {
	h.save(c, false)
}

func (h *AdminRouteHandler) save(c *gin.Context, write bool) {
	id, ok := routeNodeID(c)
	if !ok {
		return
	}
	var req routesRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	ctx := c.Request.Context()
	var saved route.Saved
	var before []domain.DestRoute
	var err error
	if write {
		// The audit row carries the routes as they were: the stored ones, read
		// plainly (the full view would compute budgets nobody asked for).
		node, gerr := h.routes.Stored(ctx, id)
		if gerr != nil {
			respondRouteError(c, gerr)
			return
		}
		before = node
		saved, err = h.routes.Save(ctx, id, req.input())
	} else {
		saved, err = h.routes.Check(ctx, id, req.input())
	}
	if err != nil {
		respondRouteError(c, err)
		return
	}
	if !write {
		out := routesResponse{Supported: true, Routes: []routeItemDTO{}, Warnings: routeWarningsOf(saved.Warnings), Budget: saved.Budget}
		for _, r := range saved.Routes {
			out.Routes = append(out.Routes, routeItemDTO{routeDTO: routeOf(r), Notes: []routeNoteDTO{}})
		}
		c.JSON(http.StatusOK, out)
		return
	}
	h.auditSave(c, id, before, saved.Routes)
	view, err := h.routes.View(ctx, id)
	if err != nil {
		respondRouteError(c, err)
		return
	}
	out := routesResponseOf(view)
	out.Warnings = routeWarningsOf(saved.Warnings)
	c.JSON(http.StatusOK, out)
}

// auditSave records that a node's routes changed, by name, with the routes
// before and after.
func (h *AdminRouteHandler) auditSave(c *gin.Context, nodeID int64, before, after []domain.DestRoute) {
	if h.audit == nil {
		return
	}
	beforeJSON, _ := json.Marshal(before)
	afterJSON, _ := json.Marshal(after)
	_ = h.audit.Insert(c.Request.Context(), &domain.AuditEntry{
		Actor: actorFromGin(c), Action: "transit_routes_updated",
		Target:     "node=" + strconv.FormatInt(nodeID, 10),
		BeforeJSON: string(beforeJSON), AfterJSON: string(afterJSON), IP: c.ClientIP(), At: time.Now(),
	})
}
