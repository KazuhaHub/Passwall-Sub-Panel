package front

import (
	"context"
	"fmt"
	"math/rand/v2"
	"sort"
	"time"

	nodeprotocol "github.com/KazuhaHub/passwall-protocol/protocol"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// Kinds of port use the usage view lists.
const (
	KindListener      = "listener"       // a node's inbound, on its own port
	KindForward       = "forward"        // a managed relay line entering on this server
	KindLinkReserved  = "link_reserved"  // standalone port of a fronted tunnel
	KindLink          = "link"           // the exit half of a tunnel into this server
	KindFront         = "front"          // a shared port
	KindFrontLoopback = "front_loopback" // 127.0.0.1 port of a listener behind a front
	KindProbeLoopback = "probe_loopback"
	KindProbeRange    = "probe_range"  // Node's internal probe sockets
	KindReserved      = "reserved"     // a port Node keeps for itself (its core API)
	KindLinkPlanned   = "link_planned" // a port promised to a tunnel about to be made into this server
)

// PortBehind is one thing behind a front: the names that reach it, or, for the
// default, none.
type PortBehind struct {
	ServerNames []string
	Target      domain.FrontTarget
	// Label names the target in English; the editor builds its own wording
	// from Target.
	Label string
	// LoopbackPort is the loopback port a listener target is moved to.
	LoopbackPort int
	IsDefault    bool
	// ParentNodeID, ParentName and LineName are set when the target is the
	// listener of a tunnel-mode line: it reads as the line of its landing.
	ParentNodeID int64
	ParentName   string
	LineName     string
}

// PortUsage is one port a server uses, and what holds it.
type PortUsage struct {
	Port int
	// Bind is "public" (every address) or "loopback" (127.0.0.1 only).
	Bind string
	// Listen is the explicitly configured bind address, when present.
	Listen string
	Kind   string
	// NodeID and NodeName identify the node whose listener or line it is;
	// LineID the relay line of a forward.
	NodeID   int64
	NodeName string
	LineID   string
	// ParentNodeID is, for the listener of a tunnel-mode line, the landing node
	// the line belongs to (LineID is the line): the view reads "tunnel line of
	// <landing>" rather than a node of its own.
	ParentNodeID int64
	// ParentName and LineName name the landing and the line, for the wording.
	ParentName string
	LineName   string
	// Orphan marks a tunnel listener whose line is gone: it can be removed from
	// here (DELETE the node), and is also an issue of its server.
	Orphan bool
	// PeerPanelID is the entry server of a tunnel into this one.
	PeerPanelID int64
	FrontID     int64
	Protocol    string
	Label       string
	// Enabled is false for a disabled inbound or line, which still holds the
	// port: enabling it later must not collide.
	Enabled bool
	// Behind lists what a front hands connections to.
	Behind []PortBehind
	// Unrouted marks a relay line on a shared port that no route targets: it is
	// not sent until a route names it.
	Unrouted bool
	// RangeHigh is the last port of a range entry (the probe sockets).
	RangeHigh int
	// Inactive is why a saved front is not being sent (a Code* value or a
	// Withheld* reason), "" while it is in effect.
	Inactive string
}

type useTable struct{ entries []PortUsage }

func (t useTable) owners(port int) []PortUsage {
	var out []PortUsage
	for _, e := range t.entries {
		if e.RangeHigh == 0 && e.Port == port {
			out = append(out, e)
		}
	}
	return out
}

func (t useTable) has(port int) bool {
	for _, e := range t.entries {
		if e.Port == port || (e.RangeHigh != 0 && port >= e.Port && port <= e.RangeHigh) {
			return true
		}
	}
	return false
}

// collectUse lists every port the server uses, from the fronts given (so a
// caller can leave out the one it is editing).
func (s *Service) collectUse(ctx context.Context, panelID int64, onPanel []*domain.Node, all []*domain.Node, fronts []domain.TransitFront) (useTable, error) {
	var table useTable
	byID := make(map[int64]*domain.Node, len(all))
	for _, n := range all {
		if n != nil {
			byID[n.ID] = n
		}
	}
	behind := map[int64]bool{}
	sharedLinks := map[int64]bool{}
	routed := map[string]bool{}
	for _, f := range fronts {
		for _, item := range f.Listeners {
			behind[item.NodeID] = true
		}
		for _, target := range f.Targets() {
			if target.Kind == domain.FrontTargetLink {
				sharedLinks[target.LinkID] = true
			}
			if target.Kind == domain.FrontTargetForward {
				routed[fmt.Sprintf("%d/%s", target.NodeID, target.LineID)] = true
			}
		}
	}
	frontPorts := map[int]bool{}
	for _, f := range fronts {
		frontPorts[f.Port] = true
	}
	for _, n := range onPanel {
		if n.DesiredPort <= 0 || behind[n.ID] {
			continue
		}
		entry := PortUsage{
			Port: n.DesiredPort, Bind: "public", Listen: n.InboundListen, Kind: KindListener, NodeID: n.ID, NodeName: n.DisplayName,
			Protocol: n.DesiredProtocol, Enabled: n.Enabled, Label: "inbound of node " + nodeLabel(n),
		}
		if n.IsTunnelChild() {
			entry.ParentNodeID, entry.LineID = n.ParentNodeID, n.ParentLineID
			entry.ParentName, entry.LineName = tunnelNames(n, byID[n.ParentNodeID])
			entry.Label = tunnelListenerLabel(n, byID[n.ParentNodeID])
			entry.Orphan = !lineServedBy(byID[n.ParentNodeID], n)
			if entry.Orphan {
				entry.Label = fmt.Sprintf("orphaned tunnel listener %q (its line is gone: remove it)", n.DisplayName)
			}
		}
		table.entries = append(table.entries, entry)
	}
	for _, n := range all {
		if n == nil || n.IsSeparator() {
			continue
		}
		for _, line := range n.Relays {
			if !line.Managed() || line.EntryPanelID != panelID || line.Port <= 0 {
				continue
			}
			table.entries = append(table.entries, PortUsage{
				Port: line.Port, Bind: "public", Kind: KindForward, NodeID: n.ID, NodeName: n.DisplayName, LineID: line.ID,
				Enabled: line.Enabled, Label: fmt.Sprintf("relay line %q of node %s", firstNonEmpty(line.Name, line.Address, line.ID), nodeLabel(n)),
				Unrouted: frontPorts[line.Port] && !routed[fmt.Sprintf("%d/%s", n.ID, line.ID)],
			})
		}
	}
	if s.links != nil {
		links, err := s.links.ListLinks(ctx)
		if err != nil {
			return useTable{}, fmt.Errorf("front: list tunnels: %w", err)
		}
		for _, l := range links {
			if l.ExitPanelID != panelID || l.Port <= 0 {
				continue
			}
			kind := KindLink
			if sharedLinks[l.ID] {
				kind = KindLinkReserved
			}
			table.entries = append(table.entries, PortUsage{
				Port: l.Port, Bind: "public", Kind: kind, PeerPanelID: l.EntryPanelID, Enabled: true,
				Label: fmt.Sprintf("tunnel from server %d", l.EntryPanelID),
			})
		}
	}
	for _, f := range fronts {
		entry := PortUsage{Port: f.Port, Bind: "public", Kind: KindFront, FrontID: f.ID, Enabled: true, Label: fmt.Sprintf("shared port %d", f.Port)}
		table.entries = append(table.entries, entry)
		for _, item := range f.Listeners {
			n := byID[item.NodeID]
			entry := PortUsage{
				Port: item.LoopbackPort, Bind: "loopback", Kind: KindFrontLoopback, FrontID: f.ID, NodeID: item.NodeID,
				NodeName: nameOf(n), Protocol: protocolOf(n), Enabled: n != nil && n.Enabled,
				Label: fmt.Sprintf("loopback port of node %s behind the shared port %d", nodeLabel(n), f.Port),
			}
			if n.IsTunnelChild() {
				entry.ParentNodeID, entry.LineID = n.ParentNodeID, n.ParentLineID
				entry.ParentName, entry.LineName = tunnelNames(n, byID[n.ParentNodeID])
				entry.Label = fmt.Sprintf("loopback port of the %s behind the shared port %d", tunnelListenerLabel(n, byID[n.ParentNodeID]), f.Port)
			}
			table.entries = append(table.entries, entry)
		}
	}
	if s.reserved != nil {
		for _, port := range s.reserved(panelID) {
			table.entries = append(table.entries, PortUsage{
				Port: port, Bind: "public", Kind: KindLinkPlanned, Enabled: true,
				Label: "port promised to a tunnel that is being set up into this server",
			})
		}
	}
	for _, port := range domain.NodeReservedPorts {
		what, _ := domain.NodeReservedPort(port)
		table.entries = append(table.entries, PortUsage{
			Port: port, Bind: "loopback", Kind: KindReserved, Enabled: true, Label: "Node's " + what,
		})
	}
	table.entries = append(table.entries, PortUsage{
		Port: domain.NodeProbeLoopbackLow, RangeHigh: domain.NodeProbeLoopbackHigh, Bind: "loopback", Kind: KindProbeRange,
		Enabled: true, Label: "Node's internal probe sockets",
	})
	sort.SliceStable(table.entries, func(i, j int) bool { return table.entries[i].Port < table.entries[j].Port })
	return table, nil
}

// tunnelListenerLabel names the listener of a tunnel-mode line as the line of
// its landing, which is how an administrator knows it.
// lineServedBy reports whether a tunnel line of the landing is served by the
// listener.
func lineServedBy(landing, child *domain.Node) bool {
	if landing == nil {
		return false
	}
	for _, l := range landing.Relays {
		if l.ID == child.ParentLineID && l.Tunnel() && l.TunnelNodeID == child.ID {
			return true
		}
	}
	return false
}

func tunnelListenerLabel(child, landing *domain.Node) string {
	_, line := tunnelNames(child, landing)
	return fmt.Sprintf("tunnel line %q of node %s", line, nodeLabel(landing))
}

// tunnelNames are the names of a tunnel listener's landing and line.
func tunnelNames(child, landing *domain.Node) (landingName, lineName string) {
	lineName = child.ParentLineID
	if landing == nil {
		return "", lineName
	}
	for _, l := range landing.Relays {
		if l.ID == child.ParentLineID {
			lineName = firstNonEmpty(l.Name, l.Address, l.ID)
		}
	}
	return landing.DisplayName, lineName
}

func nameOf(n *domain.Node) string {
	if n == nil {
		return ""
	}
	return n.DisplayName
}

func protocolOf(n *domain.Node) string {
	if n == nil {
		return ""
	}
	return n.DesiredProtocol
}

// Usage lists every port the server uses: listeners, forwards, tunnel ports,
// each front with what is behind it (server names to targets), the loopback
// ports of the listeners behind fronts, and Node's internal probe range. It
// reads and changes nothing.
func (s *Service) Usage(ctx context.Context, panelID int64) ([]PortUsage, error) {
	nodes, err := s.nodes.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("front: list nodes: %w", err)
	}
	fronts, err := s.fronts.ListByPanel(ctx, panelID)
	if err != nil {
		return nil, err
	}
	var onPanel []*domain.Node
	byID := make(map[int64]*domain.Node, len(nodes))
	for _, n := range nodes {
		if n == nil {
			continue
		}
		byID[n.ID] = n
		if n.PanelID == panelID && !n.IsSeparator() {
			onPanel = append(onPanel, n)
		}
	}
	table, err := s.collectUse(ctx, panelID, onPanel, nodes, fronts)
	if err != nil {
		return nil, err
	}
	loopback := map[int64]int{}
	byFront := map[int64]domain.TransitFront{}
	for _, f := range fronts {
		byFront[f.ID] = f
		for _, item := range f.Listeners {
			loopback[item.NodeID] = item.LoopbackPort
		}
	}
	diagnosis, err := s.Diagnose(ctx, panelID)
	if err != nil {
		return nil, err
	}
	withheld := map[int64]string{}
	for _, w := range diagnosis.Withheld {
		withheld[w.FrontID] = w.Reason
	}
	for i, e := range table.entries {
		if e.Kind != KindFront {
			continue
		}
		table.entries[i].Inactive = firstNonEmpty(diagnosis.Inactive, withheld[e.FrontID])
		f := byFront[e.FrontID]
		for _, route := range f.Routes {
			behind := PortBehind{
				ServerNames: route.ServerNames, Target: route.Target, Label: TargetLabel(route.Target, byID),
				LoopbackPort: loopback[route.Target.NodeID],
			}
			if target := byID[route.Target.NodeID]; route.Target.Kind == domain.FrontTargetListener && target.IsTunnelChild() {
				behind.ParentNodeID = target.ParentNodeID
				behind.ParentName, behind.LineName = tunnelNames(target, byID[target.ParentNodeID])
			}
			table.entries[i].Behind = append(table.entries[i].Behind, behind)
		}
		table.entries[i].Behind = append(table.entries[i].Behind, PortBehind{
			Target: f.Default, Label: TargetLabel(f.Default, byID), IsDefault: true,
		})
	}
	if s.probePorts != nil {
		probes, err := s.probePorts(ctx, panelID, time.Now())
		if err != nil {
			return nil, fmt.Errorf("front: list observed probe ports: %w", err)
		}
		for _, probe := range probes {
			if domain.InNodeProbeRange(probe.LoopbackPort) {
				table.entries = append(table.entries, PortUsage{
					Port: probe.LoopbackPort, Bind: "loopback", Kind: KindProbeLoopback,
					Label: probe.ObjectKey, Enabled: true,
				})
			}
		}
		sort.SliceStable(table.entries, func(i, j int) bool { return table.entries[i].Port < table.entries[j].Port })
	}
	return table.entries, nil
}

func randInt(n int) int { return rand.IntN(n) }

// suggestPort picks a free port in the loopback pre-fill range.
func suggestPort(use useTable, pick func(int) int) (int, error) {
	span := domain.FrontLoopbackSuggestHigh - domain.FrontLoopbackSuggestLow + 1
	start := pick(span)
	for i := 0; i < span; i++ {
		port := domain.FrontLoopbackSuggestLow + (start+i)%span
		if !use.has(port) {
			return port, nil
		}
	}
	return 0, fmt.Errorf("front: no free port in %d-%d", domain.FrontLoopbackSuggestLow, domain.FrontLoopbackSuggestHigh)
}

// ListenerOption is a node of the server as a candidate for sitting behind a
// front. The names are only what the listener already serves, to pre-fill a
// route.
type ListenerOption struct {
	NodeID    int64
	Name      string
	Protocol  string
	Port      int
	Enabled   bool
	Frontable domain.FrontableListener
	// ParentNodeID and ParentLineID mark the listener of a tunnel-mode line, with
	// the names of its landing and line.
	ParentNodeID int64
	ParentLineID string
	ParentName   string
	LineName     string
	// FrontID is the front the listener is behind, 0 when none.
	FrontID int64
}

// LineOption is a managed relay line entering through the server, as a
// candidate target of a route. ServerNames pre-fill the route with the
// landing's own server names; the administrator edits them.
type LineOption struct {
	NodeID      int64
	NodeName    string
	LineID      string
	LineName    string
	Port        int
	Enabled     bool
	ServerNames []string
	// FrontID is the front that routes to the line, 0 when none.
	FrontID int64
}

// LinkOption is an incoming REALITY tunnel eligible for a shared port.
type LinkOption struct {
	ID           int64  `json:"link_id"`
	EntryPanelID int64  `json:"entry_panel_id"`
	ServerName   string `json:"server_name"`
	Port         int    `json:"port"`
	FrontID      int64  `json:"front_id,omitempty"`
}

// Options is what the front editor offers for one server.
type Options struct {
	Listeners []ListenerOption
	Lines     []LineOption
	Links     []LinkOption
}

// Options lists the candidates for the editor, with the reason a listener
// cannot be offered.
func (s *Service) Options(ctx context.Context, panelID int64) (Options, error) {
	nodes, err := s.nodes.List(ctx)
	if err != nil {
		return Options{}, fmt.Errorf("front: list nodes: %w", err)
	}
	fronts, err := s.fronts.ListByPanel(ctx, panelID)
	if err != nil {
		return Options{}, err
	}
	behind := map[int64]int64{}
	linkFronts := map[int64]int64{}
	routed := map[string]int64{}
	for _, f := range fronts {
		for _, item := range f.Listeners {
			behind[item.NodeID] = f.ID
		}
		for _, target := range f.Targets() {
			if target.Kind == domain.FrontTargetLink {
				linkFronts[target.LinkID] = f.ID
			}
			if target.Kind == domain.FrontTargetForward {
				routed[fmt.Sprintf("%d/%s", target.NodeID, target.LineID)] = f.ID
			}
		}
	}
	out := Options{Links: []LinkOption{}}
	if s.links != nil {
		links, err := s.links.ListLinks(ctx)
		if err != nil {
			return Options{}, fmt.Errorf("front: list tunnels: %w", err)
		}
		for _, link := range links {
			if link.ExitPanelID == panelID && link.Protocol == nodeprotocol.LinkProtocolVLESSReality && link.RealityServerName != "" {
				out.Links = append(out.Links, LinkOption{
					ID: link.ID, EntryPanelID: link.EntryPanelID, ServerName: link.RealityServerName,
					Port: link.Port, FrontID: linkFronts[link.ID],
				})
			}
		}
	}
	byID := make(map[int64]*domain.Node, len(nodes))
	for _, n := range nodes {
		if n != nil {
			byID[n.ID] = n
		}
	}
	for _, n := range nodes {
		if n == nil || n.IsSeparator() {
			continue
		}
		if n.PanelID == panelID {
			out.Listeners = append(out.Listeners, ListenerOption{
				NodeID: n.ID, Name: n.DisplayName, Protocol: n.DesiredProtocol, Port: n.DesiredPort, Enabled: n.Enabled,
				Frontable: domain.FrontableOf(n), FrontID: behind[n.ID],
				ParentNodeID: n.ParentNodeID, ParentLineID: n.ParentLineID,
			})
			if n.IsTunnelChild() {
				last := &out.Listeners[len(out.Listeners)-1]
				last.ParentName, last.LineName = tunnelNames(n, byID[n.ParentNodeID])
			}
		}
		landing := domain.FrontableOf(n)
		for _, line := range n.Relays {
			if !line.Managed() || line.EntryPanelID != panelID {
				continue
			}
			out.Lines = append(out.Lines, LineOption{
				NodeID: n.ID, NodeName: n.DisplayName, LineID: line.ID,
				LineName: firstNonEmpty(line.Name, line.Address, line.ID), Port: line.Port, Enabled: line.Enabled,
				ServerNames: landing.ServerNames, FrontID: routed[fmt.Sprintf("%d/%s", n.ID, line.ID)],
			})
		}
	}
	return out, nil
}
