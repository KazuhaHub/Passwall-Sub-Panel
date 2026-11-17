package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/front"
)

// AdminFrontHandler serves the SNI-shared ports of a server
// (/api/admin/servers/:id/fronts) and its port-usage view
// (/api/admin/servers/:id/ports). adminGroup only. Every write is audited by
// name, on top of the request log, with the front before and after.
type AdminFrontHandler struct {
	fronts *front.Service
	audit  ports.AuditRepo
}

// NewAdminFrontHandler serves the fronts.
func NewAdminFrontHandler(fronts *front.Service, audit ports.AuditRepo) *AdminFrontHandler {
	return &AdminFrontHandler{fronts: fronts, audit: audit}
}

type frontListenerDTO struct {
	NodeID       int64 `json:"node_id"`
	LoopbackPort int   `json:"loopback_port"`
}

type frontRouteDTO struct {
	ServerNames []string           `json:"server_names"`
	Target      domain.FrontTarget `json:"target"`
}

type frontDTO struct {
	ID        int64              `json:"id"`
	Port      int                `json:"port"`
	Listeners []frontListenerDTO `json:"listeners"`
	Routes    []frontRouteDTO    `json:"routes"`
	Default   domain.FrontTarget `json:"default"`
}

func frontDTOOf(f domain.TransitFront) frontDTO {
	out := frontDTO{ID: f.ID, Port: f.Port, Default: f.Default, Listeners: []frontListenerDTO{}, Routes: []frontRouteDTO{}}
	for _, item := range f.Listeners {
		out.Listeners = append(out.Listeners, frontListenerDTO{NodeID: item.NodeID, LoopbackPort: item.LoopbackPort})
	}
	for _, route := range f.Routes {
		out.Routes = append(out.Routes, frontRouteDTO{ServerNames: route.ServerNames, Target: route.Target})
	}
	return out
}

type frontRequest struct {
	Port                 int                `json:"port"`
	Listeners            []frontListenerDTO `json:"listeners"`
	Routes               []frontRouteDTO    `json:"routes"`
	Default              domain.FrontTarget `json:"default"`
	ConfirmMoveListeners bool               `json:"confirm_move_listeners"`
}

func (r frontRequest) input() front.Input {
	in := front.Input{Port: r.Port, ConfirmMoveListeners: r.ConfirmMoveListeners, Default: r.Default}
	if in.Default.Kind == "" && in.Default.Host != "" {
		in.Default.Kind = domain.FrontTargetExternal
	}
	for _, item := range r.Listeners {
		in.Listeners = append(in.Listeners, domain.FrontListener{NodeID: item.NodeID, LoopbackPort: item.LoopbackPort})
	}
	for _, route := range r.Routes {
		in.Routes = append(in.Routes, domain.FrontRoute{ServerNames: route.ServerNames, Target: route.Target})
	}
	return in
}

func frontPanelID(c *gin.Context) (int64, bool) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid id"})
		return 0, false
	}
	return id, true
}

// respondFrontError answers a refusal with its stable code and parameters, so
// the editor writes its own wording; the English message is the fallback.
func respondFrontError(c *gin.Context, err error) {
	var refusal *front.Refusal
	if errors.As(err, &refusal) {
		status := http.StatusBadRequest
		if refusal.Code == front.CodeConfirmRequired {
			status = http.StatusConflict
		}
		c.JSON(status, gin.H{"error": refusal.Message, "code": refusal.Code, "params": refusal.Params})
		return
	}
	mapNodeServiceError(c, err)
}

type frontSupportDTO struct {
	Supported bool   `json:"supported"`
	Reason    string `json:"reason,omitempty"`
}

type frontWithheldDTO struct {
	FrontID int64  `json:"front_id"`
	Port    int    `json:"port"`
	Reason  string `json:"reason"`
	Detail  string `json:"detail,omitempty"`
}

// List answers the server's fronts with whether it can run them.
func (h *AdminFrontHandler) List(c *gin.Context) {
	panelID, ok := frontPanelID(c)
	if !ok {
		return
	}
	support, err := h.fronts.SupportOf(c.Request.Context(), panelID)
	if err != nil {
		mapNodeServiceError(c, err)
		return
	}
	fronts, err := h.fronts.List(c.Request.Context(), panelID)
	if err != nil {
		mapNodeServiceError(c, err)
		return
	}
	out := make([]frontDTO, 0, len(fronts))
	for _, f := range fronts {
		out = append(out, frontDTOOf(f))
	}
	diagnosis, err := h.fronts.Diagnose(c.Request.Context(), panelID)
	if err != nil {
		mapNodeServiceError(c, err)
		return
	}
	withheld := []frontWithheldDTO{}
	for _, w := range diagnosis.Withheld {
		withheld = append(withheld, frontWithheldDTO{FrontID: w.FrontID, Port: w.Port, Reason: w.Reason, Detail: w.Detail})
	}
	c.JSON(http.StatusOK, gin.H{
		"support": frontSupportDTO{Supported: support.Supported, Reason: support.Reason},
		// Saved fronts that are not being sent right now, and why: the whole
		// server (support.reason) or one front.
		"withheld": withheld,
		"fronts":   out,
		"max":      front.MaxFronts,
		"defaults": gin.H{
			"port": domain.DefaultFrontPort, "loopback_low": domain.FrontLoopbackSuggestLow, "loopback_high": domain.FrontLoopbackSuggestHigh,
			"probe_low": domain.NodeProbeLoopbackLow, "probe_high": domain.NodeProbeLoopbackHigh,
		},
	})
}

type frontListenerOptionDTO struct {
	NodeID      int64    `json:"node_id"`
	Name        string   `json:"name"`
	Protocol    string   `json:"protocol"`
	Port        int      `json:"port"`
	Enabled     bool     `json:"enabled"`
	Frontable   bool     `json:"frontable"`
	Reason      string   `json:"reason,omitempty"`
	Security    string   `json:"security,omitempty"`
	ServerNames []string `json:"server_names"`
	FrontID     int64    `json:"front_id,omitempty"`
	// ParentNodeID is set for the hidden listener of a tunnel-mode line: it is
	// the landing the line belongs to, and the editor writes "<landing>'s tunnel
	// line <line>" instead of a node name.
	ParentNodeID int64  `json:"parent_node_id,omitempty"`
	ParentLineID string `json:"parent_line_id,omitempty"`
	ParentName   string `json:"parent_name,omitempty"`
	LineName     string `json:"line_name,omitempty"`
}

type frontLineOptionDTO struct {
	NodeID      int64    `json:"node_id"`
	NodeName    string   `json:"node_name"`
	LineID      string   `json:"line_id"`
	LineName    string   `json:"line_name"`
	Port        int      `json:"port"`
	Enabled     bool     `json:"enabled"`
	ServerNames []string `json:"server_names"`
	FrontID     int64    `json:"front_id,omitempty"`
}

// Options answers the candidates the editor offers: every listener of the
// server with whether and why it can sit behind a front, and the managed relay
// lines entering through it.
func (h *AdminFrontHandler) Options(c *gin.Context) {
	panelID, ok := frontPanelID(c)
	if !ok {
		return
	}
	options, err := h.fronts.Options(c.Request.Context(), panelID)
	if err != nil {
		mapNodeServiceError(c, err)
		return
	}
	listeners := make([]frontListenerOptionDTO, 0, len(options.Listeners))
	for _, item := range options.Listeners {
		names := item.Frontable.ServerNames
		if names == nil {
			names = []string{}
		}
		listeners = append(listeners, frontListenerOptionDTO{
			NodeID: item.NodeID, Name: item.Name, Protocol: item.Protocol, Port: item.Port, Enabled: item.Enabled,
			Frontable: item.Frontable.OK, Reason: item.Frontable.Reason, Security: item.Frontable.Security,
			ServerNames: names, FrontID: item.FrontID, ParentNodeID: item.ParentNodeID, ParentLineID: item.ParentLineID,
			ParentName: item.ParentName, LineName: item.LineName,
		})
	}
	lines := make([]frontLineOptionDTO, 0, len(options.Lines))
	for _, item := range options.Lines {
		names := item.ServerNames
		if names == nil {
			names = []string{}
		}
		lines = append(lines, frontLineOptionDTO{
			NodeID: item.NodeID, NodeName: item.NodeName, LineID: item.LineID, LineName: item.LineName,
			Port: item.Port, Enabled: item.Enabled, ServerNames: names, FrontID: item.FrontID,
		})
	}
	c.JSON(http.StatusOK, gin.H{"listeners": listeners, "lines": lines})
}

// Suggest answers a pre-fill for the editor's "random" buttons: a free
// loopback port, or a camouflage site. It stores nothing and changes nothing;
// a GET, so it is not audited as a write.
func (h *AdminFrontHandler) Suggest(c *gin.Context) {
	panelID, ok := frontPanelID(c)
	if !ok {
		return
	}
	switch c.Query("what") {
	case "loopback":
		port, err := h.fronts.SuggestLoopbackPort(c.Request.Context(), panelID)
		if err != nil {
			mapNodeServiceError(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"port": port})
	case "camouflage":
		site := h.fronts.SuggestCamouflage()
		c.JSON(http.StatusOK, gin.H{"host": site.Host, "port": site.Port})
	case "port":
		// A free public port for a new listener, such as the one of a tunnel line.
		port, err := h.fronts.SuggestPublicPort(c.Request.Context(), panelID)
		if err != nil {
			mapNodeServiceError(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"port": port})
	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": "what must be loopback, camouflage or port"})
	}
}

// Create stores a new front.
func (h *AdminFrontHandler) Create(c *gin.Context) {
	panelID, ok := frontPanelID(c)
	if !ok {
		return
	}
	var req frontRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	saved, err := h.fronts.Create(c.Request.Context(), panelID, req.input())
	if err != nil {
		respondFrontError(c, err)
		return
	}
	h.record(c, "transit_front_created", panelID, nil, &saved.Front)
	c.JSON(http.StatusCreated, gin.H{"front": frontDTOOf(saved.Front), "warnings": warningsOf(saved.Warnings)})
}

// Update replaces a front.
func (h *AdminFrontHandler) Update(c *gin.Context) {
	panelID, ok := frontPanelID(c)
	if !ok {
		return
	}
	frontID, err := strconv.ParseInt(c.Param("fid"), 10, 64)
	if err != nil || frontID <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid front id"})
		return
	}
	var req frontRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	before := h.existing(c.Request.Context(), panelID, frontID)
	saved, err := h.fronts.Update(c.Request.Context(), panelID, frontID, req.input())
	if err != nil {
		respondFrontError(c, err)
		return
	}
	h.record(c, "transit_front_updated", panelID, before, &saved.Front)
	c.JSON(http.StatusOK, gin.H{"front": frontDTOOf(saved.Front), "warnings": warningsOf(saved.Warnings)})
}

// Delete removes a front; its listeners return to their public ports.
func (h *AdminFrontHandler) Delete(c *gin.Context) {
	panelID, ok := frontPanelID(c)
	if !ok {
		return
	}
	frontID, err := strconv.ParseInt(c.Param("fid"), 10, 64)
	if err != nil || frontID <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid front id"})
		return
	}
	removed, err := h.fronts.Delete(c.Request.Context(), panelID, frontID, c.Query("confirm") == "true")
	if err != nil {
		respondFrontError(c, err)
		return
	}
	h.record(c, "transit_front_deleted", panelID, &removed, nil)
	c.JSON(http.StatusOK, gin.H{"deleted": true})
}

func warningsOf(w []string) []string {
	if w == nil {
		return []string{}
	}
	return w
}

func (h *AdminFrontHandler) existing(ctx context.Context, panelID, frontID int64) *domain.TransitFront {
	fronts, err := h.fronts.List(ctx, panelID)
	if err != nil {
		return nil
	}
	for i := range fronts {
		if fronts[i].ID == frontID {
			return &fronts[i]
		}
	}
	return nil
}

// record writes the named audit entry of a front change.
func (h *AdminFrontHandler) record(c *gin.Context, action string, panelID int64, before, after *domain.TransitFront) {
	if h.audit == nil {
		return
	}
	encode := func(f *domain.TransitFront) string {
		if f == nil {
			return ""
		}
		raw, _ := json.Marshal(frontDTOOf(*f))
		return string(raw)
	}
	port := 0
	switch {
	case after != nil:
		port = after.Port
	case before != nil:
		port = before.Port
	}
	_ = h.audit.Insert(c.Request.Context(), &domain.AuditEntry{
		Actor:      actorFromGin(c),
		Action:     action,
		Target:     "server=" + strconv.FormatInt(panelID, 10) + " port=" + strconv.Itoa(port),
		BeforeJSON: encode(before),
		AfterJSON:  encode(after),
		IP:         c.ClientIP(),
		At:         time.Now(),
	})
}

type portBehindDTO struct {
	ServerNames  []string           `json:"server_names"`
	Target       domain.FrontTarget `json:"target"`
	Label        string             `json:"label"`
	LoopbackPort int                `json:"loopback_port,omitempty"`
	IsDefault    bool               `json:"is_default,omitempty"`
	ParentNodeID int64              `json:"parent_node_id,omitempty"`
	ParentName   string             `json:"parent_name,omitempty"`
	LineName     string             `json:"line_name,omitempty"`
}

type portUsageDTO struct {
	Port        int             `json:"port"`
	RangeHigh   int             `json:"range_high,omitempty"`
	Bind        string          `json:"bind"`
	Kind        string          `json:"kind"`
	NodeID      int64           `json:"node_id,omitempty"`
	NodeName    string          `json:"node_name,omitempty"`
	LineID      string          `json:"line_id,omitempty"`
	PeerPanelID int64           `json:"peer_panel_id,omitempty"`
	FrontID     int64           `json:"front_id,omitempty"`
	Protocol    string          `json:"protocol,omitempty"`
	Label       string          `json:"label"`
	Enabled     bool            `json:"enabled"`
	Unrouted    bool            `json:"unrouted,omitempty"`
	Behind      []portBehindDTO `json:"behind,omitempty"`
	Inactive    string          `json:"inactive,omitempty"`
	// ParentNodeID, ParentName and LineName are set for the listener of a
	// tunnel-mode line: it reads as the tunnel line of its landing.
	ParentNodeID int64  `json:"parent_node_id,omitempty"`
	ParentName   string `json:"parent_name,omitempty"`
	LineName     string `json:"line_name,omitempty"`
	// Orphan: a tunnel listener whose line is gone; deletable as a node.
	Orphan bool `json:"orphan,omitempty"`
}

// Ports answers every port the server uses, read only: listeners, relay lines,
// tunnel ports, each shared port with what is behind it, the loopback ports of
// fronted listeners, and Node's internal probe range.
func (h *AdminFrontHandler) Ports(c *gin.Context) {
	panelID, ok := frontPanelID(c)
	if !ok {
		return
	}
	usage, err := h.fronts.Usage(c.Request.Context(), panelID)
	if err != nil {
		mapNodeServiceError(c, err)
		return
	}
	out := make([]portUsageDTO, 0, len(usage))
	for _, item := range usage {
		dto := portUsageDTO{
			Port: item.Port, RangeHigh: item.RangeHigh, Bind: item.Bind, Kind: item.Kind, NodeID: item.NodeID,
			NodeName: item.NodeName, LineID: item.LineID, PeerPanelID: item.PeerPanelID, FrontID: item.FrontID,
			Protocol: item.Protocol, Label: item.Label, Enabled: item.Enabled, Unrouted: item.Unrouted, Inactive: item.Inactive,
			ParentNodeID: item.ParentNodeID, ParentName: item.ParentName, LineName: item.LineName, Orphan: item.Orphan,
		}
		for _, behind := range item.Behind {
			names := behind.ServerNames
			if names == nil {
				names = []string{}
			}
			dto.Behind = append(dto.Behind, portBehindDTO{
				ServerNames: names, Target: behind.Target, Label: behind.Label,
				LoopbackPort: behind.LoopbackPort, IsDefault: behind.IsDefault,
				ParentNodeID: behind.ParentNodeID, ParentName: behind.ParentName, LineName: behind.LineName,
			})
		}
		out = append(out, dto)
	}
	c.JSON(http.StatusOK, gin.H{"ports": out})
}
