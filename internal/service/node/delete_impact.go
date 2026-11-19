package node

import (
	"context"
	"fmt"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// DeleteImpactLine is one line a deletion takes with it or would strand: the
// direct line of a node, or one of its relay lines.
type DeleteImpactLine struct {
	NodeID      int64
	LineID      string
	LandingName string
	LineName    string
	LineType    string // direct | forward | tunnel | cdn | external
}

// LineTypeOf names how a relay line is carried, for lists an administrator
// reads: forward (a managed raw forward), tunnel, cdn, or external (an entry
// configured outside the panel). The direct line has no RelayLine; callers name
// it "direct" themselves.
func LineTypeOf(line domain.RelayLine) string {
	switch {
	case line.Tunnel():
		return "tunnel"
	case line.CDN():
		return "cdn"
	case line.EntryPanelID != 0:
		return "forward"
	}
	return "external"
}

// LinesEnteringServer lists every relay line, of any node, that enters through
// the given server: managed forwards (EntryPanelID == panelID) and the tunnel
// lines whose listener runs there. External lines have no entry server and
// never match.
func LinesEnteringServer(nodes []*domain.Node, panelID int64) []DeleteImpactLine {
	var out []DeleteImpactLine
	for _, n := range nodes {
		if n == nil || n.IsTunnelChild() {
			continue
		}
		for _, line := range n.Relays {
			if line.EntryPanelID == panelID && panelID != 0 {
				out = append(out, DeleteImpactLine{NodeID: n.ID, LineID: line.ID, LandingName: n.DisplayName,
					LineName: firstText(line.Name, line.Address, line.ID), LineType: LineTypeOf(line)})
			}
		}
	}
	return out
}

// NodeDeleteImpact is what deleting a node does, read-only: the lines that
// disappear with it, the tunnel listeners that are deleted too (the delete asks
// to confirm those) and the shared-port routes that would block it.
type NodeDeleteImpact struct {
	Node ImpactNode
	// Lines is the direct line followed by every relay line.
	Lines []DeleteImpactLine
	// Tunnels are the hidden listeners of the node's tunnel lines.
	Tunnels []TunnelItem
	// BlockedByRoutes names the shared-port routes that point at one of the
	// node's lines; the delete is refused until they are removed.
	BlockedByRoutes []string
}

type ImpactNode struct {
	ID   int64
	Name string
}

// DeleteImpact answers "what does deleting this node take with it" without
// changing anything. It reads what DeleteAndSync would act on, and mirrors its
// two refusals (tunnel confirmation, routes pointing at the node's lines).
func (s *Service) DeleteImpact(ctx context.Context, id int64) (*NodeDeleteImpact, error) {
	n, err := s.nodes.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if n.IsTunnelChild() {
		return nil, fmt.Errorf("%w: a tunnel listener is removed with its line, not as a node", domain.ErrValidation)
	}
	out := &NodeDeleteImpact{Node: ImpactNode{ID: n.ID, Name: n.DisplayName}}
	out.Lines = append(out.Lines, DeleteImpactLine{NodeID: n.ID, LineID: domain.DirectLineID, LandingName: n.DisplayName,
		LineName: n.DisplayName, LineType: "direct"})
	for _, line := range n.Relays {
		out.Lines = append(out.Lines, DeleteImpactLine{NodeID: n.ID, LineID: line.ID, LandingName: n.DisplayName,
			LineName: firstText(line.Name, line.Address, line.ID), LineType: LineTypeOf(line)})
		child, err := s.tunnelChild(ctx, line)
		if err != nil {
			return nil, err
		}
		if child != nil {
			out.Tunnels = append(out.Tunnels, s.tunnelItemOf(ctx, n, line, child))
		}
	}
	fronts, err := s.liveFronts(ctx)
	if err != nil {
		return nil, err
	}
	for _, f := range fronts {
		for _, route := range f.Routes {
			if route.Target.Kind == domain.FrontTargetForward && route.Target.NodeID == n.ID {
				out.BlockedByRoutes = append(out.BlockedByRoutes, frontRouteText(f, route))
			}
		}
	}
	return out, nil
}
