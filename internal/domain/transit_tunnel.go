package domain

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Tunnel-mode relay lines (加密隧道).
//
// A relay line on entry server E for landing node L is normally a raw port
// forward (直接转发): E relays the user's connection, bytes unchanged, to L's own
// listener, so L's encryption is end to end. A tunnel-mode line is the other
// way to carry it: E terminates the user's connection with a listener of its
// own, and that listener's default exit is L's server, through the transit link
// between the two servers. The user never speaks to L's listener, and L's
// fingerprint is not visible to E's network.
//
// The listener is an ordinary Node row marked hidden (ParentNodeID = L,
// ParentLineID = the line). Everything that works for a listener therefore
// works for it unchanged: sync, roster, fronts, ports, the egress link and its
// readiness, probes, routes by destination. It is left out of node lists and
// group membership, and the subscription renders it as a line of L.

// RelayModeTunnel marks a tunnel-mode relay line. The empty mode is a forward.
const RelayModeTunnel = "tunnel"

// Tunnel reports whether the line is carried by a listener of its own.
func (r RelayLine) Tunnel() bool { return r.Mode == RelayModeTunnel }

// IsTunnelChild reports whether the node is the hidden listener of a tunnel
// line.
func (n *Node) IsTunnelChild() bool { return n != nil && n.ParentNodeID != 0 }

// TunnelRunning reports whether the tunnel line is carried end to end: its
// listener is on the entry server, enabled and applied there, and the link from
// the entry server to the landing's server is ready on both sides and not
// withheld by its probes or by routes that cannot be sent. Nothing falls back
// to a direct line: a line that is not running is not published.
func TunnelRunning(landing *Node, line RelayLine, child *Node) bool {
	return landing != nil && child != nil && line.Tunnel() && line.Enabled && landing.Enabled && child.Enabled &&
		child.ParentNodeID == landing.ID && child.ParentLineID == line.ID &&
		child.PanelID == line.EntryPanelID && child.EgressPanelID == landing.PanelID &&
		child.ConfigSyncState == ConfigSyncSynced && child.EgressPublishable()
}

// TunnelProtocolCopyable reports whether the landing's listener can be copied
// as the entry's own: only a REALITY listener has nothing bound to its server
// (a TLS listener carries a certificate that belongs to the landing's host, and
// SS-2022 and the QUIC protocols carry server keys that would then live on two
// servers). Any other landing gets the default VLESS + REALITY + Vision
// listener, which the administrator can then change.
func TunnelProtocolCopyable(landing *Node) bool {
	if landing == nil {
		return false
	}
	switch Protocol(strings.ToLower(landing.DesiredProtocol)) {
	case ProtoVLESS, ProtoVMess, ProtoTrojan:
	default:
		return false
	}
	var stream struct {
		Security string `json:"security"`
	}
	if json.Unmarshal([]byte(landing.StreamSettings), &stream) != nil {
		return false
	}
	return strings.EqualFold(stream.Security, "reality")
}

// TunnelLineLabel names a tunnel line's listener for an administrator: where it
// is shown (port usage, issues) it reads as the landing's tunnel line.
func TunnelLineLabel(landingName, lineName string) string {
	if strings.TrimSpace(lineName) == "" {
		return landingName
	}
	return landingName + " · " + lineName
}

// ValidateTunnelRelays checks the tunnel lines of one landing node: an entry on
// another server and the address users connect to. Entry-server kind, ports and
// the listener itself are checked by the node service, which can see the fleet.
func ValidateTunnelRelays(landing *Node, lines []RelayLine) error {
	for i, line := range lines {
		if !line.Tunnel() {
			if line.Mode != "" && !line.CDN() {
				return fmt.Errorf("%w: relay line %d: unknown mode %q", ErrValidation, i+1, line.Mode)
			}
			continue
		}
		if line.EntryPanelID <= 0 {
			return fmt.Errorf("%w: relay line %d: a tunnel line needs an entry server", ErrValidation, i+1)
		}
		if landing != nil && landing.PanelID == line.EntryPanelID {
			return fmt.Errorf("%w: relay line %d: a tunnel cannot enter through the landing's own server", ErrValidation, i+1)
		}
		if strings.TrimSpace(line.Address) == "" {
			return fmt.Errorf("%w: relay line %d: a tunnel line needs the address users connect to", ErrValidation, i+1)
		}
		if line.SNI != "" || line.Host != "" {
			return fmt.Errorf("%w: relay line %d: a tunnel line is served by its own listener, whose names are set on the listener", ErrValidation, i+1)
		}
	}
	return nil
}
