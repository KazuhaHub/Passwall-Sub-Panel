package domain

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"strings"

	"github.com/google/uuid"
)

// VlessRouteLine is a subscription entry, not a remote inbound/client.
type VlessRouteLine struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	OnlyLineName bool     `json:"only_line_name"`
	EntryRelayID string   `json:"entry_relay_id,omitempty"`
	OutboundTag  string   `json:"outbound_tag"`
	Network      string   `json:"network,omitempty"`
	Protocols    []string `json:"protocols,omitempty"`
	IP           []string `json:"ip,omitempty"`
	Domain       []string `json:"domain,omitempty"`
	Port         string   `json:"port,omitempty"`
	Route        uint16   `json:"route"`
	Enabled      bool     `json:"enabled"`
	Deleted      bool     `json:"deleted,omitempty"`
}

// Label follows the selected outbound tag when no custom name is configured.
func (line VlessRouteLine) Label() string {
	if line.Name != "" {
		return line.Name
	}
	return line.OutboundTag
}

// ResolveEntry returns nil,true for a visible direct endpoint, or the one
// enabled relay selected by stable ID. Unavailable entries never fall back.
func (line VlessRouteLine) ResolveEntry(n *Node) (*RelayLine, bool) {
	if line.EntryRelayID == "" {
		return nil, n != nil && !n.EffectiveHideDirect()
	}
	if n != nil {
		for i := range n.Relays {
			if n.Relays[i].ID == line.EntryRelayID && n.Relays[i].Enabled {
				return &n.Relays[i], true
			}
		}
	}
	return nil, false
}

// LocallyAvailable is the shared effective publication gate. Enabled records
// retain the administrator's intent while an unavailable entry suspends use.
func (line VlessRouteLine) LocallyAvailable(n *Node) bool {
	_, entryAvailable := line.ResolveEntry(n)
	return n != nil && n.Enabled && strings.EqualFold(n.DesiredProtocol, "vless") &&
		n.RouteState.Status == "applied" && line.Enabled && !line.Deleted && entryAvailable
}

// NodeRouteState records an idempotent target before touching the remote API.
// Retired lines remain here solely to identify owned rules and reserve IDs.
type NodeRouteState struct {
	Revision int64            `json:"revision"`
	Lines    []VlessRouteLine `json:"lines"`
	Status   string           `json:"status"`
	Error    string           `json:"error,omitempty"`
	Binding  string           `json:"binding,omitempty"`
}

func (state NodeRouteState) Visible() NodeRouteState {
	result := state
	result.Lines = []VlessRouteLine{}
	for _, line := range state.Lines {
		if !line.Deleted {
			result.Lines = append(result.Lines, line)
		}
	}
	return result
}

// RouteBinding prevents a confirmed route from following a node to another
// backend/listener before the new binding has been applied and verified.
func RouteBinding(panelURL string, n *Node) string {
	value := fmt.Sprintf("%s|%d|%d|%s", strings.TrimRight(panelURL, "/"), n.InboundID, n.DesiredPort, strings.ToLower(n.DesiredProtocol))
	return fmt.Sprintf("%x", sha256.Sum256([]byte(value)))
}

func UUIDRoute(id string) (uint16, error) {
	u, err := uuid.Parse(id)
	if err != nil {
		return 0, fmt.Errorf("%w: invalid VLESS UUID", ErrValidation)
	}
	return binary.BigEndian.Uint16(u[6:8]), nil
}

func WithUUIDRoute(id string, route uint16) (string, error) {
	u, err := uuid.Parse(id)
	if err != nil {
		return "", fmt.Errorf("%w: invalid VLESS UUID", ErrValidation)
	}
	binary.BigEndian.PutUint16(u[6:8], route)
	return u.String(), nil
}
