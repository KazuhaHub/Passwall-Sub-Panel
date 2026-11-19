package handler

import (
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/node"
)

// Per-line subscription ordering: GET and PUT /api/admin/nodes/order.
//
// The list is the global, unfiltered order one subscriber's output is a subset
// of (see node.SubscriptionOrder). The PUT takes the complete list back, with
// the version it was built from, and writes it atomically.

type orderRowDTO struct {
	Kind string `json:"kind"`
	// Separator rows.
	ID      int64  `json:"id,omitempty"`
	Enabled bool   `json:"enabled"`
	Mode    string `json:"mode,omitempty"`
	// Line rows.
	NodeID int64  `json:"node_id,omitempty"`
	LineID string `json:"line_id,omitempty"`
	Name   string `json:"name"`
	// Position is the effective place on the shared sort scale; Positioned is
	// false for a line that was never positioned (it sits under its landing and
	// Position is the landing's).
	Position   int  `json:"position"`
	Positioned bool `json:"positioned"`

	Landing      *orderLandingDTO `json:"landing,omitempty"`
	Entry        *orderEntryDTO   `json:"entry,omitempty"`
	LineType     string           `json:"line_type,omitempty"`
	HiddenDirect *bool            `json:"hidden_direct,omitempty"`
	Published    *bool            `json:"published,omitempty"`
	// Withheld is why a line is not published ("" when it is): hidden_direct,
	// landing_disabled, disabled, egress, probe, source_guard, waiting_entry,
	// cdn_unfit.
	Withheld           string           `json:"withheld,omitempty"`
	Address            string           `json:"address,omitempty"`
	Port               int              `json:"port,omitempty"`
	TrafficFactorMilli *int             `json:"traffic_factor_milli,omitempty"`
	Running            bool             `json:"running,omitempty"`
	Probe              *probeQualityDTO `json:"probe,omitempty"`
	SourceWithheld     string           `json:"source_withheld,omitempty"`
	CDNIssues          []cdnIssueDTO    `json:"cdn_issues,omitempty"`
	Health             *orderHealthDTO  `json:"health,omitempty"`
}

type orderLandingDTO struct {
	NodeID    int64  `json:"node_id"`
	Name      string `json:"name"`
	Region    string `json:"region"`
	PanelID   int64  `json:"panel_id"`
	PanelName string `json:"panel_name"`
	Enabled   bool   `json:"enabled"`
}

// orderEntryDTO is the server a managed line enters through. Missing is true
// when that server no longer exists.
type orderEntryDTO struct {
	PanelID   int64  `json:"panel_id"`
	PanelName string `json:"panel_name"`
	Missing   bool   `json:"missing"`
}

type orderHealthDTO struct {
	State     string     `json:"state"`
	CheckedAt *time.Time `json:"checked_at"`
	Detail    string     `json:"detail,omitempty"`
}

type orderListDTO struct {
	OrderVersion string        `json:"order_version"`
	Rows         []orderRowDTO `json:"rows"`
}

type orderItemRequest struct {
	Kind   string `json:"kind"`
	ID     int64  `json:"id"`
	NodeID int64  `json:"node_id"`
	LineID string `json:"line_id"`
}

type orderPutRequest struct {
	BaseVersion string             `json:"base_version"`
	Items       []orderItemRequest `json:"items"`
}

func (h *AdminNodeHandler) orderListDTO(o *node.SubscriptionOrder, panelNames map[int64]string) orderListDTO {
	out := orderListDTO{OrderVersion: o.Version, Rows: make([]orderRowDTO, 0, len(o.Rows))}
	for _, row := range o.Rows {
		out.Rows = append(out.Rows, orderRowToDTO(row, panelNames))
	}
	return out
}

func orderRowToDTO(row node.OrderRow, panelNames map[int64]string) orderRowDTO {
	if s := row.Separator; s != nil {
		return orderRowDTO{Kind: node.OrderKindSeparator, ID: s.ID, Name: s.DisplayName, Enabled: s.Enabled,
			Mode: string(s.Mode), Position: row.Position, Positioned: true}
	}
	n := row.Node
	published := row.Published
	dto := orderRowDTO{
		Kind: node.OrderKindLine, NodeID: n.ID, LineID: row.Ref.LineID, Position: row.Position, Positioned: row.Positioned,
		Published: &published, Withheld: row.Withheld,
		Landing: &orderLandingDTO{NodeID: n.ID, Name: n.DisplayName, Region: n.Region, PanelID: n.PanelID,
			PanelName: panelNames[n.PanelID], Enabled: n.Enabled},
	}
	if row.Line == nil {
		hidden := n.EffectiveHideDirect()
		factor := n.EffectiveTrafficFactor()
		dto.LineType, dto.Name, dto.Enabled, dto.HiddenDirect = "direct", n.DisplayName, n.Enabled, &hidden
		dto.Address, dto.Port, dto.TrafficFactorMilli = n.ServerAddress, n.DesiredPort, &factor
		dto.Health = &orderHealthDTO{State: healthStateOrUnknown(string(n.HealthState)), CheckedAt: n.HealthCheckedAt, Detail: n.HealthDetail}
		return dto
	}
	line := *row.Line
	// The line's own fields come from the same mapping the node editor uses, so
	// the list and the editor cannot disagree about a line.
	rd := relayDTOsOn(n, []domain.RelayLine{line}, false)[0]
	dto.Name, dto.Enabled = line.Label(), line.Enabled
	dto.Address, dto.Port, dto.TrafficFactorMilli = rd.Address, rd.Port, rd.TrafficFactorMilli
	dto.Running, dto.Probe, dto.SourceWithheld, dto.CDNIssues = rd.Running, rd.Probe, rd.SourceWithheld, rd.CDNIssues
	switch {
	case line.Tunnel():
		dto.LineType = "tunnel"
	case line.CDN():
		dto.LineType = "cdn"
	case line.EntryPanelID != 0:
		dto.LineType = "forward"
	default:
		dto.LineType = "external"
	}
	if line.EntryPanelID != 0 {
		name, ok := panelNames[line.EntryPanelID]
		dto.Entry = &orderEntryDTO{PanelID: line.EntryPanelID, PanelName: name, Missing: !ok}
	}
	dto.Health = relayHealthOf(n, line)
	return dto
}

// relayHealthOf matches the health worker's snapshot to the line by index,
// address and port, exactly as the user status page does: a snapshot taken
// before the line was edited or reordered must not be shown for it.
func relayHealthOf(n *domain.Node, line domain.RelayLine) *orderHealthDTO {
	index := -1
	for i := range n.Relays {
		if n.Relays[i].ID == line.ID {
			index = i
			break
		}
	}
	port := line.Port
	if port <= 0 {
		port = n.DesiredPort
	}
	for _, h := range n.RelayHealth {
		if h.Index == index && h.Address == line.Address && h.Port == port {
			return &orderHealthDTO{State: healthStateOrUnknown(string(h.State)), CheckedAt: h.CheckedAt}
		}
	}
	return &orderHealthDTO{State: "unknown"}
}

func healthStateOrUnknown(s string) string {
	if s == "" {
		return "unknown"
	}
	return s
}

// ListOrder serves the global order, one row per separator and per line.
func (h *AdminNodeHandler) ListOrder(c *gin.Context) {
	order, err := h.node.SubscriptionOrder(c.Request.Context())
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, h.orderListDTO(order, h.loadPanelNames(c.Request.Context())))
}

// PutOrder applies a complete arrangement atomically. 200 {"order_version"};
// 400 for a malformed list; 409 {"code":"order_stale","order":{...}} when the
// order changed since the client read it, with the current order attached.
func (h *AdminNodeHandler) PutOrder(c *gin.Context) {
	var req orderPutRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	items := make([]node.OrderRef, len(req.Items))
	for i, it := range req.Items {
		items[i] = node.OrderRef{Kind: it.Kind, ID: it.ID, NodeID: it.NodeID, LineID: it.LineID}
	}
	version, err := h.node.ApplySubscriptionOrder(c.Request.Context(), req.BaseVersion, items)
	if err != nil {
		var stale *node.OrderStaleError
		if errors.As(err, &stale) {
			c.JSON(http.StatusConflict, gin.H{
				"error": err.Error(), "code": "order_stale",
				"order": h.orderListDTO(stale.Current, h.loadPanelNames(c.Request.Context())),
			})
			return
		}
		mapNodeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"order_version": version})
}
