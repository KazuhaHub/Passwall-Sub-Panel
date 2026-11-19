package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/realitykey"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/xraycompat"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	certsvc "github.com/KazuhaHub/passwall-sub-panel/internal/service/cert"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/front"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/group"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/node"
	syncsvc "github.com/KazuhaHub/passwall-sub-panel/internal/service/sync"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/transit"
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
	certs      ports.CertificateRepo
	lines      NodeEditorPreviewer
	audit      ports.AuditRepo
}

// NodeEditorPreviewer is group.Service's answers for the node and separator
// editors (ADR 0041, ADR 0042), injected so the node handler does not own
// group semantics.
type NodeEditorPreviewer interface {
	PreviewLineVisibility(ctx context.Context, draft *domain.Node) (*group.LineVisibility, error)
	GroupsForNode(ctx context.Context, n *domain.Node) ([]int64, error)
	GroupsForSeparator(ctx context.Context, draft domain.SeparatorEntry) ([]int64, error)
	List(ctx context.Context) ([]*domain.Group, error)
}

// WithLineVisibility wires the node and separator editors' previews.
func (h *AdminNodeHandler) WithLineVisibility(previewer NodeEditorPreviewer) *AdminNodeHandler {
	h.lines = previewer
	return h
}

func NewAdminNodeHandler(nodeSvc *node.Service, syncSvc *syncsvc.Service, ownership ports.OwnershipRepo, users ports.UserRepo, panels ports.XUIPanelRepo, pspClients ports.PSPClientRepo, certs ports.CertificateRepo) *AdminNodeHandler {
	return &AdminNodeHandler{node: nodeSvc, sync: syncSvc, ownership: ownership, users: users, panels: panels, pspClients: pspClients, certs: certs}
}

// normalizeRealityFingerprint enforces the server-core compatibility rule at
// the write boundary. The UI mirrors this by locking the selector, but API
// callers and raw/advanced JSON must not be able to persist a contradictory
// desired configuration. Unknown core versions remain untouched.
func (h *AdminNodeHandler) normalizeRealityFingerprint(ctx context.Context, panelID int64, spec *ports.InboundSpec) error {
	if h.panels == nil || spec == nil {
		return nil
	}
	panel, err := h.panels.GetByID(ctx, panelID)
	if err != nil {
		return err
	}
	normalized, _, err := xraycompat.NormalizeRealityFingerprint(spec.StreamSettings, panel.XrayVersion)
	if err != nil {
		return fmt.Errorf("normalize REALITY fingerprint: %w", err)
	}
	spec.StreamSettings = normalized
	return nil
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
	// Protocol remains a compatibility alias for DesiredProtocol. New clients
	// should render desired and observed explicitly so rollout drift is visible.
	Protocol         string   `json:"protocol,omitempty"`
	DesiredProtocol  string   `json:"desired_protocol,omitempty"`
	DesiredPort      int      `json:"desired_port,omitempty"`
	ObservedProtocol string   `json:"observed_protocol,omitempty"`
	ObservedPort     int      `json:"observed_port,omitempty"`
	EndpointInSync   bool     `json:"endpoint_in_sync"`
	Tags             []string `json:"tags"`
	Region           string   `json:"region"`
	SortOrder        int      `json:"sort_order"`
	Enabled          bool     `json:"enabled"`
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
	// ConfigPendingSince is when this node FIRST stopped being converged, so
	// the admin can tell ten seconds apart from three days. Absent — not zero
	// — for a converged node and for rows written before the column existed:
	// a zero-length lag and an unknown one read identically, and only one of
	// them is reassuring.
	ConfigPendingSince *time.Time `json:"config_pending_since,omitempty"`
	// CertSource/CertID surface the managed-certificate binding so the
	// node-edit form can pre-select the current source (psp_managed + which
	// cert). "" = unmanaged. Never carries any PEM/secret — just the binding.
	CertSource string `json:"cert_source,omitempty"`
	CertID     int64  `json:"cert_id,omitempty"`
	// Relays are the node's transit / 中转 lines; HideDirect drops only the
	// standalone direct entry. Surfaced so the edit form can
	// round-trip them. Always present (possibly empty) for real nodes.
	Relays          []relayLineDTO `json:"relays"`
	HideDirect      bool           `json:"hide_direct"`
	ShowRelayStatus bool           `json:"show_relay_status"`
	// TrafficFactorMilli is the direct line's traffic multiplier in
	// thousandths (1000 = one-for-one, 0 = free); each relay carries its own.
	TrafficFactorMilli int `json:"traffic_factor_milli"`
	// EgressPanelID is the server the node's traffic leaves from (0: its
	// own); EgressReady reports that the tunnel runs on both servers, which
	// is when the node is published.
	EgressPanelID int64 `json:"egress_panel_id,omitempty"`
	EgressReady   bool  `json:"egress_ready,omitempty"`
	// EgressProbe is the tunnel's link quality (transit.probe.v1) and
	// EgressWithheld says its probes have taken the node out of subscriptions.
	EgressProbe    *probeQualityDTO `json:"egress_probe,omitempty"`
	EgressWithheld bool             `json:"egress_withheld,omitempty"`
	// ParentNodeID names the landing when this node is the hidden listener of
	// a tunnel line (ParentLineID the line): it is edited as a node but is not
	// listed, and its name, labels, exit and factor follow the line.
	ParentNodeID   int64  `json:"parent_node_id,omitempty"`
	ParentLineID   string `json:"parent_line_id,omitempty"`
	ParentNodeName string `json:"parent_node_name,omitempty"`
}

// relayLineDTO is one transit front. Only the dialed endpoint differs from the
// landing; SNI/Host are optional CDN-fronting overrides. See domain.RelayLine.
type relayLineDTO struct {
	ID           string `json:"id,omitempty"`
	Name         string `json:"name"`
	OnlyLineName bool   `json:"only_line_name"`
	Address      string `json:"address"`
	Port         int    `json:"port"`
	SNI          string `json:"sni,omitempty"`
	Host         string `json:"host,omitempty"`
	// Domain is a CDN line's CDN domain (mode "cdn"). Address, port, SNI and
	// Host left empty in a request are filled from it and stored.
	Domain string `json:"domain,omitempty"`
	// ClientTLS is whether clients use TLS to the CDN (CDN lines only); omitted
	// in a request keeps the saved value, or defaults from the port.
	ClientTLS *bool `json:"client_tls,omitempty"`
	Enabled   bool  `json:"enabled"`
	// Region / Tags are line-only subscription labels (ADR 0040). Omitted in a
	// request keeps the saved value (a new line takes the node's value);
	// explicit "" / [] clears. Responses always carry both.
	Region *string   `json:"region,omitempty"`
	Tags   *[]string `json:"tags,omitempty"`
	// EntryPanelID selects a Passwall Node server that runs this line's
	// forwarding; 0 or absent is an external entry configured outside PSP.
	EntryPanelID int64 `json:"entry_panel_id,omitempty"`
	// ManagedReady is response-only: the entry server confirmed the forward
	// this managed line asks for, so the line is published.
	ManagedReady bool `json:"managed_ready,omitempty"`
	// Probe is the forward's quality (transit.probe.v1), response-only, and
	// ProbeWithheld says its probes have taken the line out of subscriptions.
	Probe         *probeQualityDTO `json:"probe,omitempty"`
	ProbeWithheld bool             `json:"probe_withheld,omitempty"`
	// TrafficFactorMilli is this line's traffic multiplier (0..100000). In a
	// request, omitted keeps the saved value (a new line is one-for-one);
	// responses always carry it.
	TrafficFactorMilli *int `json:"traffic_factor_milli,omitempty"`
	// SourceCIDRs are the source addresses the administrator adds to this line's
	// source set (ADR 0044): where the line's traffic reaches the landing from.
	// In a request, omitted keeps the saved list and [] clears it; responses
	// always carry it. Not for tunnel lines.
	SourceCIDRs *[]string `json:"source_cidrs,omitempty"`
	// SourceWithheld is response-only: why the source guard keeps the line out
	// of subscriptions (source_guard_factor, source_guard_unknown).
	SourceWithheld string `json:"source_withheld,omitempty"`
	// Mode is how the entry carries the line: absent is a raw port forward
	// (直接转发), "tunnel" a listener of its own on the entry that carries the
	// traffic through a tunnel to the landing (加密隧道). In a request a new line
	// may name it; an existing line's mode changes only through the confirmed
	// mode switch, and omitting it keeps the saved one. For a tunnel line port is
	// the listener's own port (response), or the one a new listener starts on
	// (request; 0 picks one).
	Mode *string `json:"mode,omitempty"`
	// Tunnel is response-only: the listener of a tunnel line and its state.
	Tunnel *tunnelDTO `json:"tunnel,omitempty"`
	// Running is response-only: the line is published, which for a tunnel line
	// needs its listener applied and its tunnel ready on both servers.
	Running bool `json:"running,omitempty"`
	// CDNIssues is response-only: what is likely to keep a CDN line from
	// working given its landing as it is now (never blocks saving).
	CDNIssues []cdnIssueDTO `json:"cdn_issues,omitempty"`
}

type cdnIssueDTO struct {
	Code   string            `json:"code"`
	Warn   bool              `json:"warn"`
	Params map[string]string `json:"params,omitempty"`
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
	FrontConfirm  *frontConfirmDTO `json:"front_confirm,omitempty"`
	PanelID       int64            `json:"panel_id" binding:"required"`
	InboundID     int              `json:"inbound_id" binding:"required"`
	DisplayName   string           `json:"display_name" binding:"required"`
	ServerAddress string           `json:"server_address" binding:"required"`
	Flow          string           `json:"flow"`
	// Protocol of the source inbound (lowercased), cached on the node so the
	// UI can gate protocol-specific fields. Optional for backward compat.
	Protocol   string         `json:"protocol"`
	Region     string         `json:"region" binding:"required"`
	Tags       []string       `json:"tags"`
	SortOrder  int            `json:"sort_order"`
	Relays     []relayLineDTO `json:"relays"`
	HideDirect bool           `json:"hide_direct"`
	// TrafficFactorMilli is the direct line's multiplier; omitted is 1000.
	TrafficFactorMilli *int `json:"traffic_factor_milli"`
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
	CertSource    string         `json:"cert_source"`
	CertID        int64          `json:"cert_id"`
	Inbound       inboundSpecDTO `json:"inbound" binding:"required"`
	// TrafficFactorMilli is the direct line's multiplier; omitted is 1000.
	TrafficFactorMilli *int               `json:"traffic_factor_milli"`
	EgressPanelID      int64              `json:"egress_panel_id"`
	Setup              *node.SetupOptions `json:"setup"`
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
	CertSource     string `json:"cert_source"`
	CertID         int64  `json:"cert_id"`
	// FrontConfirm confirms joining the shared port this inbound lands on.
	FrontConfirm *frontConfirmDTO `json:"front_confirm,omitempty"`
}

type updateMetadataRequest struct {
	DisplayName   string   `json:"display_name"`
	ServerAddress string   `json:"server_address"`
	Flow          string   `json:"flow"`
	Region        string   `json:"region"`
	Tags          []string `json:"tags"`
	// SortOrder nil (field absent) keeps the stored position. The edit dialog
	// used to send it with every save, so a dialog opened before a drag and
	// saved after it put the node back where it was, and a client that left it
	// out wrote 0. Reordering belongs to PUT /nodes/order; an explicit value is
	// still written for the legacy UI.
	SortOrder *int `json:"sort_order"`
	// Relays full-replaces the node's transit lines; nil (field absent) leaves
	// them untouched, an explicit [] clears them — same patch semantics as Tags.
	Relays     []relayLineDTO `json:"relays"`
	HideDirect bool           `json:"hide_direct"`
	// EgressPanelID is the Passwall Node server the node's traffic leaves
	// from, through a tunnel; 0 leaves from its own server. nil (field
	// absent) leaves it unchanged.
	EgressPanelID *int64 `json:"egress_panel_id"`
	// TrafficFactorMilli is the direct line's multiplier; nil (absent) keeps
	// the saved value. Changes are audited per line.
	TrafficFactorMilli *int `json:"traffic_factor_milli"`
}

type setNodeEnabledRequest struct {
	Enabled bool `json:"enabled"`
	// ConfirmFront is the go-ahead for disabling a listener that is behind a
	// shared port: the names routed to it fall to the default site.
	ConfirmFront bool `json:"confirm_front"`
}

// frontConfirmDTO is the administrator's go-ahead for a listener joining a
// shared port. Its presence is the confirmation; LoopbackPort is the one the
// 409 offered (0 takes the suggestion).
type frontConfirmDTO struct {
	LoopbackPort int `json:"loopback_port"`
}

// frontContext puts a request's confirmation, if it carries one, in the
// context the node service reads, and returns what the service reports back.
func frontContext(c *gin.Context, confirm *frontConfirmDTO, extra bool) (context.Context, *front.Confirmation) {
	conf := &front.Confirmation{Confirmed: extra}
	if confirm != nil {
		conf.Confirmed, conf.LoopbackPort = true, confirm.LoopbackPort
	}
	return front.WithConfirmation(c.Request.Context(), conf), conf
}

// auditFrontJoin records that a save took a listener off a shared port, added it
// to one, or both (a move), with the front and the SNI it is routed by.
func (h *AdminNodeHandler) auditFrontJoin(c *gin.Context, conf *front.Confirmation) {
	if h.audit == nil || conf == nil {
		return
	}
	// A move leaves one front, then joins another: record them in that order.
	for _, step := range []struct {
		action string
		change *front.Joined
	}{{"transit_front_listener_left", conf.Left}, {"transit_front_listener_joined", conf.Joined}} {
		action, change := step.action, step.change
		if change == nil {
			continue
		}
		after, _ := json.Marshal(change)
		_ = h.audit.Insert(c.Request.Context(), &domain.AuditEntry{
			Actor: actorFromGin(c), Action: action,
			Target:    "server=" + strconv.FormatInt(change.PanelID, 10) + " port=" + strconv.Itoa(change.Port) + " node=" + strconv.FormatInt(change.NodeID, 10),
			AfterJSON: string(after), IP: c.ClientIP(), At: time.Now(),
		})
	}
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
	nodeView := func() nodeDTO {
		dto := h.toNodeDTO(n, panelNames)
		// An administrator also reads the REALITY private key of each tunnel
		// listener; an operator never does.
		dto.Relays = relayDTOsOn(n, n.Relays, isAdmin)
		if n.IsTunnelChild() {
			if parent, perr := h.node.Get(c.Request.Context(), n.ParentNodeID); perr == nil {
				dto.ParentNodeName = parent.DisplayName
			}
		}
		return dto
	}

	// Bundle the inbound clients so the detail page only needs one round-trip.
	clients, err := h.node.ListClientsOfInbound(c.Request.Context(), id, h.ownership, h.pspClients)
	if err != nil {
		// Detail without clients is still useful; surface the error but don't 500.
		out := gin.H{
			"node":          nodeView(),
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
		"node":    nodeView(),
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
		PanelID:            req.PanelID,
		InboundID:          req.InboundID,
		DisplayName:        req.DisplayName,
		ServerAddress:      req.ServerAddress,
		Flow:               req.Flow,
		DesiredProtocol:    strings.ToLower(req.Protocol),
		Region:             req.Region,
		Tags:               req.Tags,
		SortOrder:          req.SortOrder,
		Relays:             relays,
		HideDirect:         req.HideDirect,
		TrafficFactorMilli: req.TrafficFactorMilli,
	}
	if req.TrafficFactorMilli != nil && domain.ValidateTrafficFactor(*req.TrafficFactorMilli) != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": trafficFactorError})
		return
	}
	ctx, conf := frontContext(c, req.FrontConfirm, false)
	// Audited whatever happens next: a join or leave that was done stays done.
	err = h.node.ImportExisting(ctx, n)
	h.auditFrontJoin(c, conf)
	if err != nil {
		mapNodeServiceError(c, err)
		return
	}
	panelNames := h.loadPanelNames(c.Request.Context())
	c.JSON(http.StatusCreated, h.toNodeDTO(n, panelNames))
}

func (h *AdminNodeHandler) PreviewSetup(c *gin.Context) {
	h.createInbound(c, true)
}

func (h *AdminNodeHandler) CreateInbound(c *gin.Context) {
	h.createInbound(c, false)
}

func (h *AdminNodeHandler) createInbound(c *gin.Context, preview bool) {
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
		PanelID:            req.PanelID,
		DisplayName:        req.DisplayName,
		ServerAddress:      req.ServerAddress,
		Flow:               req.Flow,
		DesiredProtocol:    strings.ToLower(req.Inbound.Protocol),
		Region:             req.Region,
		Tags:               req.Tags,
		SortOrder:          req.SortOrder,
		Relays:             relays,
		HideDirect:         req.HideDirect,
		TrafficFactorMilli: req.TrafficFactorMilli,
	}
	if req.TrafficFactorMilli != nil && domain.ValidateTrafficFactor(*req.TrafficFactorMilli) != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": trafficFactorError})
		return
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
	if domain.CertSource(req.CertSource) == domain.CertSourceManaged {
		if req.CertID <= 0 || h.certs == nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "an active managed certificate is required"})
			return
		}
		managed, err := h.certs.GetByID(c.Request.Context(), req.CertID)
		if err != nil || managed == nil || managed.Status != domain.CertStatusActive ||
			strings.TrimSpace(managed.CertPEM) == "" || strings.TrimSpace(managed.KeyPEM) == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "managed certificate is not active or has no issued key pair"})
			return
		}
		spec.StreamSettings, err = certsvc.InjectInlineCert(spec.StreamSettings, managed.CertPEM, managed.KeyPEM)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		n.CertSource = domain.CertSourceManaged
		n.CertID = managed.ID
	}
	if err := h.normalizeRealityFingerprint(c.Request.Context(), n.PanelID, &spec); err != nil {
		mapNodeServiceError(c, err)
		return
	}
	if preview || req.Setup != nil {
		if req.Setup == nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "node setup settings are required"})
			return
		}
		n.EgressPanelID = req.EgressPanelID
		plan, err := h.node.SetupInbound(c.Request.Context(), n, spec, *req.Setup, preview)
		if err != nil {
			mapNodeServiceError(c, err)
		} else if preview {
			c.JSON(http.StatusOK, plan)
		} else {
			h.auditTunnel(c, "create_node_setup", fmt.Sprintf("node:%d", n.ID), nil, plan.Bindings)
			dto := h.toNodeDTO(n, h.loadPanelNames(c.Request.Context()))
			dto.Relays = relayDTOsOn(n, n.Relays, callerIsAdmin(c))
			c.JSON(http.StatusCreated, dto)
		}
		return
	}
	ctx, conf := frontContext(c, req.Inbound.FrontConfirm, false)
	// Audited whatever happens next: a join or leave that was done stays done.
	err = h.node.CreateInbound(ctx, n, spec)
	h.auditFrontJoin(c, conf)
	if err != nil {
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
	before := *n
	before.Relays = append([]domain.RelayLine(nil), n.Relays...)
	if req.TrafficFactorMilli != nil {
		n.TrafficFactorMilli = req.TrafficFactorMilli
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
	if req.SortOrder != nil {
		n.SortOrder = *req.SortOrder
	}
	// Relays: nil (field absent) leaves them — and HideDirect — untouched, so a
	// relay-unaware client never disturbs them; an explicit [] clears the lines.
	if req.Relays != nil {
		n.Relays = relays
		n.HideDirect = req.HideDirect
	}
	if req.EgressPanelID != nil {
		n.EgressPanelID = *req.EgressPanelID
	}
	// Removing a tunnel line removes its listener, which asks first.
	ctx, conf := tunnelContext(c, c.Query("confirm_tunnels") == "true", "")
	err = h.node.UpdateMetadata(ctx, n)
	h.auditFrontJoin(c, conf)
	if err != nil {
		mapNodeServiceError(c, err)
		return
	}
	h.auditLineFactors(c, node.ChangedLineFactors(&before, n))
	h.auditTunnelLines(c, n.ID, before.Relays, n.Relays)
	// An explicit sort_order (the legacy edit dialog) is a deliberate move and goes
	// through the reorder writer: the metadata save itself never writes the column.
	if req.SortOrder != nil {
		if err := h.node.Reorder(c.Request.Context(), []ports.NodeSortUpdate{{NodeID: n.ID, SortOrder: *req.SortOrder}}); err != nil {
			mapNodeServiceError(c, err)
			return
		}
	}
	// Reload: the service settled the tunnel lines (listener ids, ports).
	saved, err := h.node.Get(c.Request.Context(), id)
	if err != nil {
		mapNodeServiceError(c, err)
		return
	}
	panelNames := h.loadPanelNames(c.Request.Context())
	dto := h.toNodeDTO(saved, panelNames)
	dto.Relays = relayDTOsOn(saved, saved.Relays, callerIsAdmin(c))
	c.JSON(http.StatusOK, dto)
}

const trafficFactorError = "traffic_factor_milli must be between 0 and 100000 (1000 = one-for-one, 0 = free)"

// WithAudit wires the audit log for line traffic factor changes.
func (h *AdminNodeHandler) WithAudit(audit ports.AuditRepo) *AdminNodeHandler {
	h.audit = audit
	return h
}

// auditLineFactors records each line whose traffic factor changed. A factor
// is a price: it changes what every user of the line is charged from now on.
func (h *AdminNodeHandler) auditLineFactors(c *gin.Context, changes []node.AppliedLineFactor) {
	if h.audit == nil {
		return
	}
	for _, ch := range changes {
		before, _ := json.Marshal(map[string]any{"node_id": ch.Line.NodeID, "line_id": ch.Line.LineID, "traffic_factor_milli": ch.From})
		after, _ := json.Marshal(map[string]any{"node_id": ch.Line.NodeID, "line_id": ch.Line.LineID, "traffic_factor_milli": ch.To})
		_ = h.audit.Insert(c.Request.Context(), &domain.AuditEntry{
			Actor:      actorFromGin(c),
			Action:     "line_traffic_factor_changed",
			Target:     "node=" + strconv.FormatInt(ch.Line.NodeID, 10) + " line=" + ch.Line.LineID + " name=" + ch.NodeName,
			BeforeJSON: string(before),
			AfterJSON:  string(after),
			IP:         c.ClientIP(),
			At:         time.Now(),
		})
	}
}

type lineFactorRequest struct {
	Lines []struct {
		NodeID             int64  `json:"node_id"`
		LineID             string `json:"line_id"`
		TrafficFactorMilli *int   `json:"traffic_factor_milli"`
	} `json:"lines"`
}

// SetServerLineFactors is the server page's bulk edit: it sets the factors of
// the selected lines on one server, all or nothing, and audits each change.
func (h *AdminNodeHandler) SetServerLineFactors(c *gin.Context) {
	panelID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid id"})
		return
	}
	var req lineFactorRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if len(req.Lines) == 0 || len(req.Lines) > 1000 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "lines must hold between 1 and 1000 entries"})
		return
	}
	changes := make([]node.LineFactorChange, 0, len(req.Lines))
	for _, l := range req.Lines {
		if l.TrafficFactorMilli == nil || domain.ValidateTrafficFactor(*l.TrafficFactorMilli) != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": trafficFactorError})
			return
		}
		if l.NodeID <= 0 || l.LineID == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "every line needs node_id and line_id (\"direct\" for the node's own line)"})
			return
		}
		changes = append(changes, node.LineFactorChange{Line: domain.LineKey{NodeID: l.NodeID, LineID: l.LineID}, FactorMilli: *l.TrafficFactorMilli})
	}
	applied, err := h.node.SetLineFactors(c.Request.Context(), panelID, changes)
	if err != nil {
		mapNodeServiceError(c, err)
		return
	}
	h.auditLineFactors(c, applied)
	c.JSON(http.StatusOK, gin.H{"changed": len(applied)})
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
	if domain.CertSource(req.CertSource) == domain.CertSourceManaged {
		if req.CertID <= 0 || h.certs == nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "an active managed certificate is required"})
			return
		}
		managed, err := h.certs.GetByID(c.Request.Context(), req.CertID)
		if err != nil || managed == nil || managed.Status != domain.CertStatusActive ||
			strings.TrimSpace(managed.CertPEM) == "" || strings.TrimSpace(managed.KeyPEM) == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "managed certificate is not active or has no issued key pair"})
			return
		}
		spec.StreamSettings, err = certsvc.InjectInlineCert(spec.StreamSettings, managed.CertPEM, managed.KeyPEM)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
	}
	n, err := h.node.Get(c.Request.Context(), id)
	if err != nil {
		mapNodeServiceError(c, err)
		return
	}
	if err := h.normalizeRealityFingerprint(c.Request.Context(), n.PanelID, &spec); err != nil {
		mapNodeServiceError(c, err)
		return
	}
	ctx, conf := frontContext(c, req.FrontConfirm, false)
	// Audited whatever happens next: a join or leave that was done stays done.
	err = h.node.UpdateInboundConfig(ctx, id, spec)
	h.auditFrontJoin(c, conf)
	if err != nil {
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
	ctx, _ := frontContext(c, nil, req.ConfirmFront)
	if err := h.node.SetEnabled(ctx, id, req.Enabled); err != nil {
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
	ctx, conf := tunnelContext(c, c.Query("confirm_tunnels") == "true", "")
	if c.Query("confirm_front") == "true" {
		conf.Confirmed = true
	}
	// Audited whatever happens next: a join or leave that was done stays done.
	err = h.node.DeleteAndSync(ctx, id)
	h.auditFrontJoin(c, conf)
	if err != nil {
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
	ctx, conf := tunnelContext(c, c.Query("confirm_tunnels") == "true", "")
	if c.Query("confirm_front") == "true" {
		conf.Confirmed = true
	}
	// Audited whatever happens next: a join or leave that was done stays done.
	err = h.node.DetachAndSync(ctx, id)
	h.auditFrontJoin(c, conf)
	if err != nil {
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
	var err error
	for i, d := range dtos {
		addr := strings.TrimSpace(d.Address)
		cdn := d.Mode != nil && *d.Mode == domain.RelayModeCDN
		// A CDN line's address is the optimised IP and defaults to its domain.
		if addr == "" && !cdn && !(d.Mode == nil && d.Domain != "") {
			return nil, fmt.Errorf("relay line %d: address is required", i+1)
		}
		if d.Port < 0 || d.Port > 65535 {
			return nil, fmt.Errorf("relay line %d: port %d out of range (0-65535; 0 reuses the inbound port)", i+1, d.Port)
		}
		line := domain.RelayLine{
			ID:           d.ID,
			Name:         strings.TrimSpace(d.Name),
			OnlyLineName: d.OnlyLineName,
			Address:      addr,
			Port:         d.Port,
			SNI:          strings.TrimSpace(d.SNI),
			Host:         strings.TrimSpace(d.Host),
			Domain:       strings.TrimSpace(d.Domain),
			ClientTLS:    d.ClientTLS,
			Enabled:      d.Enabled,
			EntryPanelID: d.EntryPanelID,
			// nil keeps the saved factor (resolved by the node service).
			TrafficFactorMilli: d.TrafficFactorMilli,
		}
		if d.TrafficFactorMilli != nil && domain.ValidateTrafficFactor(*d.TrafficFactorMilli) != nil {
			return nil, fmt.Errorf("relay line %d: %s", i+1, trafficFactorError)
		}
		if d.EntryPanelID < 0 {
			return nil, fmt.Errorf("relay line %d: invalid entry server", i+1)
		}
		// nil keeps the saved addresses (resolved by the node service, which also
		// validates and normalises them).
		if d.SourceCIDRs == nil {
			line.SourceCIDRsUnresolved = true
		} else {
			line.SourceCIDRs = *d.SourceCIDRs
		}
		if d.Mode == nil {
			line.ModeUnresolved = true
		} else if *d.Mode == domain.RelayModeTunnel {
			line.Mode = domain.RelayModeTunnel
		} else if *d.Mode == domain.RelayModeCDN {
			line.Mode = domain.RelayModeCDN
		} else if *d.Mode != "" && *d.Mode != "forward" {
			return nil, fmt.Errorf("relay line %d: unknown mode %q", i+1, *d.Mode)
		}
		// A managed forward needs its entry port (a request that omits the mode is
		// judged by the service, which knows the saved line); a tunnel line's
		// listener takes the port given, or one the service picks, when it is made.
		if d.EntryPanelID > 0 && d.Port == 0 && d.Mode != nil && !line.Tunnel() && !line.CDN() {
			return nil, fmt.Errorf("relay line %d: a managed relay needs an entry port between 1 and 65535", i+1)
		}
		if d.Region == nil {
			line.RegionUnresolved = true
		} else if line.Region, err = domain.NormalizeLineRegion(*d.Region); err != nil {
			return nil, fmt.Errorf("relay line %d: region must be at most %d bytes", i+1, domain.MaxLineRegionBytes)
		}
		if d.Tags == nil {
			line.TagsUnresolved = true
		} else if line.Tags, err = domain.NormalizeLineTags(*d.Tags); err != nil {
			return nil, fmt.Errorf("relay line %d: at most %d tags of at most %d bytes each", i+1, domain.MaxLineTags, domain.MaxLineTagBytes)
		}
		out = append(out, line)
	}
	return out, nil
}

// relayDTOs maps the domain shape back for the API response.
func relayDTOs(relays []domain.RelayLine) []relayLineDTO { return relayDTOsFor(relays, false) }

// relayDTOsFor is relayDTOs; secrets adds the private keys of tunnel listeners,
// which only an administrator reads.
func relayDTOsFor(relays []domain.RelayLine, secrets bool) []relayLineDTO {
	return relayDTOsOn(nil, relays, secrets)
}

// relayDTOsOn is relayDTOsFor for the lines of a landing node: with the
// landing it also says what is likely to keep each CDN line from working.
func relayDTOsOn(landing *domain.Node, relays []domain.RelayLine, secrets bool) []relayLineDTO {
	out := make([]relayLineDTO, 0, len(relays))
	for _, r := range relays {
		region, tags := r.Region, domain.CopyLineTags(r.Tags)
		mode, port := (*string)(nil), r.Port
		var tunnel *tunnelDTO
		if r.Tunnel() {
			m := domain.RelayModeTunnel
			mode, tunnel = &m, tunnelDTOOf(r.TunnelNode, secrets)
			if r.TunnelNode != nil {
				port = r.TunnelNode.DesiredPort
			}
		}
		var issues []cdnIssueDTO
		if r.CDN() {
			m := domain.RelayModeCDN
			mode = &m
			for _, issue := range domain.CDNWarnings(landing, r) {
				issues = append(issues, cdnIssueDTO{Code: issue.Code, Warn: issue.Warn, Params: issue.Params})
			}
		}
		out = append(out, relayLineDTO{
			CDNIssues:          issues,
			Domain:             r.Domain,
			ClientTLS:          r.ClientTLS,
			Mode:               mode,
			Tunnel:             tunnel,
			Running:            r.Gated() && r.ManagedReady,
			ID:                 r.ID,
			Name:               r.Name,
			OnlyLineName:       r.OnlyLineName,
			Address:            r.Address,
			Port:               port,
			SNI:                r.SNI,
			Host:               r.Host,
			Enabled:            r.Enabled,
			Region:             &region,
			Tags:               &tags,
			EntryPanelID:       r.EntryPanelID,
			ManagedReady:       r.Gated() && r.ManagedReady,
			Probe:              probeQualityOf(r.Probe),
			ProbeWithheld:      r.ProbeWithheld,
			TrafficFactorMilli: ptrInt(r.EffectiveTrafficFactor()),
			SourceCIDRs:        sourceCIDRsOf(r),
			SourceWithheld:     r.SourceWithheld,
		})
	}
	return out
}

// sourceCIDRsOf is a line's own source addresses for a response; a tunnel line
// has none and carries nothing.
func sourceCIDRsOf(r domain.RelayLine) *[]string {
	if r.Tunnel() {
		return nil
	}
	cidrs := append([]string{}, r.SourceCIDRs...)
	return &cidrs
}

func (h *AdminNodeHandler) toNodeDTO(n *domain.Node, panelNames map[int64]string) nodeDTO {
	// Panel name is resolved from the in-memory pool snapshot (panelNames map)
	// rather than from the DB row — the v3 schema dropped the redundant
	// panel_name column on nodes since renaming a panel would otherwise leave
	// every historical row stale.
	panelName := panelNames[n.PanelID]
	return nodeDTO{
		ID:                 n.ID,
		PanelID:            n.PanelID,
		PanelName:          panelName,
		InboundID:          n.InboundID,
		DisplayName:        n.DisplayName,
		ServerAddress:      n.ServerAddress,
		Flow:               n.Flow,
		Protocol:           n.DesiredProtocol,
		DesiredProtocol:    n.DesiredProtocol,
		DesiredPort:        n.DesiredPort,
		ObservedProtocol:   n.ObservedProtocol,
		ObservedPort:       n.ObservedPort,
		EndpointInSync:     n.EndpointInSync(),
		Region:             n.Region,
		Tags:               n.Tags,
		SortOrder:          n.SortOrder,
		Enabled:            n.Enabled,
		HealthState:        string(n.HealthState),
		HealthCheckedAt:    n.HealthCheckedAt,
		HealthDetail:       n.HealthDetail,
		ConfigSyncState:    n.ConfigSyncState,
		ConfigSyncedAt:     n.ConfigSyncedAt,
		ConfigPendingSince: n.ConfigPendingSince,
		Kind:               string(n.Kind),
		CertSource:         string(n.CertSource),
		CertID:             n.CertID,
		Relays:             relayDTOsOn(n, n.Relays, false),
		HideDirect:         n.HideDirect,
		TrafficFactorMilli: n.EffectiveTrafficFactor(),
		ShowRelayStatus:    n.EffectiveShowRelayStatus(),
		EgressPanelID:      n.EgressPanelID,
		EgressReady:        n.EgressPanelID != 0 && n.EgressReady,
		EgressProbe:        probeQualityOf(n.EgressProbe),
		EgressWithheld:     n.EgressPanelID != 0 && n.EgressWithheld,
		ParentNodeID:       n.ParentNodeID,
		ParentLineID:       n.ParentLineID,
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
	var refusal *front.Refusal
	var coded *domain.CodedValidationError
	switch {
	case errors.As(err, &refusal):
		respondFrontError(c, err)
	case errors.As(err, &coded):
		respondError(c, err)
	case errors.Is(err, transit.ErrReservedPortTaken):
		// The port promised to a new tunnel was taken before it was made: a
		// conflict the admin can retry (the retry plans a fresh port).
		c.JSON(http.StatusConflict, gin.H{"error": err.Error(), "code": "link_port_taken"})
	case errors.Is(err, domain.ErrNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
	case errors.Is(err, domain.ErrAlreadyExists), errors.Is(err, domain.ErrConflict):
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
	case errors.Is(err, domain.ErrValidation):
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
	case errors.Is(err, domain.ErrInboundHasUnmanagedClients):
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
	default:
		respondError(c, err)
	}
}

// lineVisibilityRequest is the node editor's unsaved state: the node's own
// labels and each line's labels in editor order. Only labels matter for
// visibility; nothing here is stored.
type lineVisibilityRequest struct {
	ServerAddress string               `json:"server_address"`
	Region        string               `json:"region"`
	Tags          []string             `json:"tags"`
	Relays        []lineVisibilityLine `json:"relays"`
}

type lineVisibilityLine struct {
	ID     string   `json:"id,omitempty"`
	Region string   `json:"region"`
	Tags   []string `json:"tags"`
}

type lineVisibilityGroup struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

// lineVisibilityResponse lists every group once; node_groups are the groups
// that pass the node and relays[i] the groups that would see the
// i-th draft line. An empty list means "no group", never "unknown": a failed
// read is an error response instead.
type lineVisibilityResponse struct {
	Groups     []lineVisibilityGroup `json:"groups"`
	NodeGroups []int64               `json:"node_groups"`
	// SavedNodeGroups are the groups the stored node passes, so the editor can
	// name the groups this save would remove the node from (ADR 0042 §2).
	SavedNodeGroups []int64   `json:"saved_node_groups"`
	Relays          [][]int64 `json:"relays"`
}

// PreviewLineVisibility answers POST /admin/nodes/:id/line-visibility. It
// reads only and is exempt from audit by exact path (middleware/audit.go).
func (h *AdminNodeHandler) PreviewLineVisibility(c *gin.Context) {
	if h.lines == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "line visibility is unavailable", "code": "line_visibility_unavailable"})
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid id"})
		return
	}
	var req lineVisibilityRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if len(req.Relays) > maxRelayLines {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("at most %d relay lines", maxRelayLines)})
		return
	}
	stored, err := h.node.Get(c.Request.Context(), id)
	if err != nil {
		mapNodeServiceError(c, err)
		return
	}
	draft := *stored
	if req.ServerAddress != "" {
		draft.ServerAddress = req.ServerAddress
	}
	draft.Region = strings.TrimSpace(req.Region)
	draft.Tags = []string{}
	for _, tag := range req.Tags {
		if tag = strings.TrimSpace(tag); tag != "" {
			draft.Tags = append(draft.Tags, tag)
		}
	}
	draft.Relays = make([]domain.RelayLine, len(req.Relays))
	for i, line := range req.Relays {
		region, tags, err := normalizeLineLabels(line)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("relay line %d: %v", i+1, err)})
			return
		}
		draft.Relays[i] = domain.RelayLine{ID: strings.TrimSpace(line.ID), Region: region, Tags: tags}
	}
	result, err := h.lines.PreviewLineVisibility(c.Request.Context(), &draft)
	if err != nil {
		respondError(c, err)
		return
	}
	saved, err := h.lines.GroupsForNode(c.Request.Context(), stored)
	if err != nil {
		respondError(c, err)
		return
	}
	out := lineVisibilityResponse{Groups: make([]lineVisibilityGroup, 0, len(result.Groups)), NodeGroups: result.NodeGroups, SavedNodeGroups: saved, Relays: result.Relays}
	for _, g := range result.Groups {
		out.Groups = append(out.Groups, lineVisibilityGroup{ID: g.ID, Name: g.Name})
	}
	c.JSON(http.StatusOK, out)
}

func normalizeLineLabels(line lineVisibilityLine) (string, []string, error) {
	region, err := domain.NormalizeLineRegion(line.Region)
	if err != nil {
		return "", nil, err
	}
	tags, err := domain.NormalizeLineTags(line.Tags)
	if err != nil {
		return "", nil, err
	}
	return region, tags, nil
}

// separatorGroupsRequest is the separator editor's unsaved visibility rule.
type separatorGroupsRequest struct {
	Mode    string  `json:"mode"`
	NodeIDs []int64 `json:"node_ids"`
}

// separatorGroupsResponse names every group once and lists those whose
// subscriptions would show the separator.
type separatorGroupsResponse struct {
	Groups  []lineVisibilityGroup `json:"groups"`
	Visible []int64               `json:"visible"`
}

// PreviewSeparatorGroups answers POST /admin/nodes/separator/group-preview
// (ADR 0042 §4). Read-only; registered through previewPOST.
func (h *AdminNodeHandler) PreviewSeparatorGroups(c *gin.Context) {
	if h.lines == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "separator preview is unavailable", "code": "separator_preview_unavailable"})
		return
	}
	var req separatorGroupsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	mode := domain.SeparatorMode(req.Mode)
	if mode != domain.SeparatorModeGlobal && mode != domain.SeparatorModeNodeBound {
		c.JSON(http.StatusBadRequest, gin.H{"error": "mode must be global or node_bound"})
		return
	}
	groups, err := h.lines.List(c.Request.Context())
	if err != nil {
		respondError(c, err)
		return
	}
	visible, err := h.lines.GroupsForSeparator(c.Request.Context(), domain.SeparatorEntry{Mode: mode, NodeIDs: req.NodeIDs})
	if err != nil {
		respondError(c, err)
		return
	}
	out := separatorGroupsResponse{Groups: make([]lineVisibilityGroup, 0, len(groups)), Visible: visible}
	for _, g := range groups {
		out.Groups = append(out.Groups, lineVisibilityGroup{ID: g.ID, Name: g.Name})
	}
	c.JSON(http.StatusOK, out)
}

func ptrInt(v int) *int { return &v }
