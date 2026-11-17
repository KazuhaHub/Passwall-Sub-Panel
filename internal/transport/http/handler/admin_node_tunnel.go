package handler

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/realitykey"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/front"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/node"
)

// tunnelDTO is the hidden listener of a tunnel-mode line as the line's editor
// shows it: its own port and REALITY names and keys (every one admin-visible and
// editable), whether it is applied on the entry server, and the state of the
// tunnel to the landing's server.
type tunnelDTO struct {
	NodeID   int64  `json:"node_id"`
	Name     string `json:"name"`
	Enabled  bool   `json:"enabled"`
	Port     int    `json:"port"`
	Protocol string `json:"protocol"`
	Flow     string `json:"flow"`
	Listen   string `json:"listen,omitempty"`
	Security string `json:"security,omitempty"`
	// REALITY fields; empty when the listener uses another security.
	ServerNames []string `json:"server_names,omitempty"`
	Target      string   `json:"target,omitempty"`
	ShortIDs    []string `json:"short_ids,omitempty"`
	PublicKey   string   `json:"public_key,omitempty"`
	PrivateKey  string   `json:"private_key,omitempty"`
	Fingerprint string   `json:"fingerprint,omitempty"`
	// Applied: the entry server confirmed the listener's current settings.
	ConfigSyncState string `json:"config_sync_state"`
	Applied         bool   `json:"applied"`
	// The tunnel from the entry server to the landing's server, which is the
	// listener's default exit.
	EgressPanelID  int64            `json:"egress_panel_id"`
	EgressReady    bool             `json:"egress_ready"`
	EgressWithheld bool             `json:"egress_withheld,omitempty"`
	EgressProbe    *probeQualityDTO `json:"egress_probe,omitempty"`
	// Routes is how many routes by destination the listener has; they are set
	// per listener and are not inherited from the landing.
	Routes         int    `json:"routes"`
	RoutesWithheld string `json:"routes_withheld,omitempty"`
}

// tunnelDTOOf maps a loaded listener; nil when the listener is gone. secrets
// adds the REALITY private key.
func tunnelDTOOf(n *domain.Node, secrets bool) *tunnelDTO {
	if n == nil {
		return nil
	}
	dto := &tunnelDTO{
		NodeID: n.ID, Name: n.DisplayName, Enabled: n.Enabled, Port: n.DesiredPort, Protocol: n.DesiredProtocol,
		Flow: n.Flow, Listen: n.InboundListen, ConfigSyncState: n.ConfigSyncState,
		Applied:       n.ConfigSyncState == domain.ConfigSyncSynced,
		EgressPanelID: n.EgressPanelID, EgressReady: n.EgressPanelID != 0 && n.EgressReady,
		EgressWithheld: n.EgressPanelID != 0 && n.EgressWithheld, EgressProbe: probeQualityOf(n.EgressProbe),
		Routes: len(n.DestRoutes), RoutesWithheld: n.RoutesWithheld,
	}
	var stream struct {
		Security        string `json:"security"`
		RealitySettings struct {
			ServerNames []string `json:"serverNames"`
			Target      string   `json:"target"`
			Dest        string   `json:"dest"`
			ShortIDs    []string `json:"shortIds"`
			PrivateKey  string   `json:"privateKey"`
			Settings    struct {
				PublicKey   string `json:"publicKey"`
				Fingerprint string `json:"fingerprint"`
			} `json:"settings"`
		} `json:"realitySettings"`
	}
	if json.Unmarshal([]byte(n.StreamSettings), &stream) == nil {
		dto.Security = stream.Security
		if stream.Security == "reality" {
			r := stream.RealitySettings
			dto.ServerNames, dto.ShortIDs = r.ServerNames, r.ShortIDs
			dto.Target = r.Target
			if dto.Target == "" {
				dto.Target = r.Dest
			}
			dto.PublicKey, dto.Fingerprint = r.Settings.PublicKey, r.Settings.Fingerprint
			if secrets {
				dto.PrivateKey = r.PrivateKey
			}
		}
	}
	return dto
}

// tunnelContext carries a request's confirmation of what removes a tunnel
// listener. It is not a confirmation of anything a shared port does to the
// request's own node: a listener that leaves a shared port because its line is
// removed is listed in the same confirmation and is covered by it inside the
// service, and every other front change asks for its own.
//
// confirmFront is the separate go-ahead for a shared port's own change (a new
// listener joining one, a disabled one leaving): the 409 of such a change is
// the front's, and the editor resends the request with it (query or body).
func tunnelContext(c *gin.Context, confirmed bool, plan string, confirmFront ...bool) (context.Context, *front.Confirmation) {
	if plan == "" {
		plan = c.Query("confirm_plan")
	}
	ctx := node.WithTunnelConfirmation(c.Request.Context(), &node.TunnelConfirmation{Confirmed: confirmed, Plan: plan})
	front409 := c.Query("confirm_front") == "true"
	for _, v := range confirmFront {
		front409 = front409 || v
	}
	conf := &front.Confirmation{Confirmed: front409}
	return front.WithConfirmation(ctx, conf), conf
}

// auditTunnel records a change to tunnel listeners.
func (h *AdminNodeHandler) auditTunnel(c *gin.Context, action, target string, before, after any) {
	if h.audit == nil {
		return
	}
	b, _ := json.Marshal(before)
	a, _ := json.Marshal(after)
	_ = h.audit.Insert(c.Request.Context(), &domain.AuditEntry{
		Actor: actorFromGin(c), Action: action, Target: target,
		BeforeJSON: string(b), AfterJSON: string(a), IP: c.ClientIP(), At: time.Now(),
	})
}

// auditTunnelLines records the tunnel lines a save made or removed.
func (h *AdminNodeHandler) auditTunnelLines(c *gin.Context, landing int64, before, after []domain.RelayLine) {
	had := map[string]domain.RelayLine{}
	for _, line := range before {
		if line.Tunnel() {
			had[line.ID] = line
		}
	}
	for _, line := range after {
		if !line.Tunnel() {
			continue
		}
		if _, existed := had[line.ID]; !existed {
			h.auditTunnel(c, "transit_tunnel_line_created", "node="+strconv.FormatInt(landing, 10)+" line="+line.ID,
				nil, map[string]any{"line_id": line.ID, "entry_panel_id": line.EntryPanelID, "listener_node_id": line.TunnelNodeID})
		}
		delete(had, line.ID)
	}
	for _, line := range before {
		if _, gone := had[line.ID]; gone {
			h.auditTunnel(c, "transit_tunnel_line_removed", "node="+strconv.FormatInt(landing, 10)+" line="+line.ID,
				map[string]any{"line_id": line.ID, "entry_panel_id": line.EntryPanelID, "listener_node_id": line.TunnelNodeID}, nil)
		}
	}
}

func tunnelParams(c *gin.Context) (int64, string, bool) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 || c.Param("line_id") == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid id"})
		return 0, "", false
	}
	return id, c.Param("line_id"), true
}

type switchLineModeRequest struct {
	// Mode is "tunnel" or "forward".
	Mode string `json:"mode"`
	// Port is the entry port of the forward, or the listener's port, 0 to pick.
	Port    int  `json:"port"`
	Confirm bool `json:"confirm"`
	// ConfirmFront is the go-ahead for what a shared port does (see tunnelContext).
	ConfirmFront bool `json:"confirm_front"`
	// ConfirmPlan is params.plan of the 409 being confirmed, echoed back.
	ConfirmPlan string `json:"confirm_plan"`
}

// SwitchLineMode answers POST /admin/nodes/:id/lines/:line_id/mode: the
// explicit, confirmed switch of one line between 直接转发 and 加密隧道. Without
// confirm it answers 409 listing what is created and removed.
func (h *AdminNodeHandler) SwitchLineMode(c *gin.Context) {
	id, lineID, ok := tunnelParams(c)
	if !ok {
		return
	}
	var req switchLineModeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	mode := ""
	switch req.Mode {
	case "tunnel":
		mode = domain.RelayModeTunnel
	case "forward":
	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": "mode must be tunnel or forward"})
		return
	}
	before, err := h.node.Get(c.Request.Context(), id)
	if err != nil {
		mapNodeServiceError(c, err)
		return
	}
	ctx, conf := tunnelContext(c, req.Confirm, req.ConfirmPlan, req.ConfirmFront)
	after, err := h.node.SwitchLineMode(ctx, id, lineID, mode, req.Port)
	h.auditFrontJoin(c, conf)
	if err != nil {
		mapNodeServiceError(c, err)
		return
	}
	h.auditTunnel(c, "transit_line_mode_switched", "node="+strconv.FormatInt(id, 10)+" line="+lineID,
		map[string]any{"line_id": lineID, "mode": modeName(lineModeOf(before.Relays, lineID))},
		map[string]any{"line_id": lineID, "mode": modeName(lineModeOf(after.Relays, lineID))})
	panelNames := h.loadPanelNames(c.Request.Context())
	dto := h.toNodeDTO(after, panelNames)
	dto.Relays = relayDTOsFor(after.Relays, callerIsAdmin(c))
	c.JSON(http.StatusOK, dto)
}

func lineModeOf(lines []domain.RelayLine, id string) string {
	for _, line := range lines {
		if line.ID == id {
			return line.Mode
		}
	}
	return ""
}

func modeName(mode string) string {
	if mode == domain.RelayModeTunnel {
		return "tunnel"
	}
	return "forward"
}

type tunnelListenerRequest struct {
	Port        int    `json:"port"`
	ServerName  string `json:"server_name"`
	Target      string `json:"target"`
	ShortID     string `json:"short_id"`
	PrivateKey  string `json:"private_key"`
	PublicKey   string `json:"public_key"`
	Fingerprint string `json:"fingerprint"`
}

// UpdateTunnelListener answers PUT /admin/nodes/:id/lines/:line_id/tunnel: the
// port and REALITY names and keys of the line's listener. Empty fields keep the
// stored value; what is sent is stored as sent.
func (h *AdminNodeHandler) UpdateTunnelListener(c *gin.Context) {
	id, lineID, ok := tunnelParams(c)
	if !ok {
		return
	}
	var req tunnelListenerRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	ctx, conf := frontContext(c, nil, c.Query("confirm_front") == "true")
	err := h.node.UpdateTunnelListener(ctx, id, lineID, node.TunnelListenerEdit{
		Port: req.Port, ServerName: req.ServerName, Target: req.Target, ShortID: req.ShortID,
		PrivateKey: req.PrivateKey, PublicKey: req.PublicKey, Fingerprint: req.Fingerprint,
	})
	h.auditFrontJoin(c, conf)
	if err != nil {
		mapNodeServiceError(c, err)
		return
	}
	// The private key is a secret: the audit records which fields changed.
	h.auditTunnel(c, "transit_tunnel_listener_updated", "node="+strconv.FormatInt(id, 10)+" line="+lineID, nil, map[string]any{
		"port": req.Port, "server_name": req.ServerName, "target": req.Target, "short_id": req.ShortID,
		"public_key": req.PublicKey, "private_key_changed": req.PrivateKey != "", "fingerprint": req.Fingerprint,
	})
	landing, err := h.node.Get(c.Request.Context(), id)
	if err != nil {
		mapNodeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"relays": relayDTOsFor(landing.Relays, callerIsAdmin(c))})
}

// PreviewTunnelSync answers GET /admin/nodes/:id/lines/:line_id/tunnel/sync:
// what re-copying the landing's protocol settings would change. It reads only.
func (h *AdminNodeHandler) PreviewTunnelSync(c *gin.Context) {
	id, lineID, ok := tunnelParams(c)
	if !ok {
		return
	}
	diff, err := h.node.PreviewTunnelSync(c.Request.Context(), id, lineID)
	if err != nil {
		mapNodeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"diff": diff})
}

type tunnelSyncRequest struct {
	Confirm      bool   `json:"confirm"`
	ConfirmFront bool   `json:"confirm_front"`
	ConfirmPlan  string `json:"confirm_plan"`
}

// SyncTunnelFromLanding answers POST /admin/nodes/:id/lines/:line_id/tunnel/sync
// (从落地同步): the explicit, confirmed re-copy of the landing's protocol
// settings. Without confirm it answers 409 with the differences.
func (h *AdminNodeHandler) SyncTunnelFromLanding(c *gin.Context) {
	id, lineID, ok := tunnelParams(c)
	if !ok {
		return
	}
	var req tunnelSyncRequest
	if err := c.ShouldBindJSON(&req); err != nil && !errors.Is(err, io.EOF) {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	ctx, conf := tunnelContext(c, req.Confirm, req.ConfirmPlan, req.ConfirmFront)
	diff, err := h.node.SyncTunnelFromLanding(ctx, id, lineID)
	h.auditFrontJoin(c, conf)
	if err != nil {
		mapNodeServiceError(c, err)
		return
	}
	h.auditTunnel(c, "transit_tunnel_listener_synced", "node="+strconv.FormatInt(id, 10)+" line="+lineID, nil, map[string]any{"changed": diff})
	landing, err := h.node.Get(c.Request.Context(), id)
	if err != nil {
		mapNodeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"diff": diff, "relays": relayDTOsFor(landing.Relays, callerIsAdmin(c))})
}

type realityPublicKeyRequest struct {
	PrivateKey string `json:"private_key"`
}

// DeriveRealityPublicKey answers POST /admin/nodes/reality-public-key: the public
// key of a REALITY private key, so the editor fills it in when the private key
// is edited. It stores nothing.
func (h *AdminNodeHandler) DeriveRealityPublicKey(c *gin.Context) {
	var req realityPublicKeyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	pub, err := realitykey.PublicFromPrivate(strings.TrimSpace(req.PrivateKey))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"public_key": pub})
}
