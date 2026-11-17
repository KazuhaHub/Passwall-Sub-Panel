package group

import (
	"context"
	"slices"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// Line visibility (ADR 0041). A relay line reaches a group's
// subscriptions only when the group already passes the node (Eligible: the
// tag_filter on the node's labels plus allowlist eligibility) AND the same
// tag_filter also holds on the line's own Region / Tags. It only narrows: a
// line can never admit a group that the node does not. This decides what is
// published, not what a credential can reach; every line shares the node's
// inbound and client.

// lineMatches evaluates filter on the line's labels through the one node
// matcher, so region:/tag:/custom keys keep their exact node semantics.
// node:<address> still reads the node's ServerAddress: a line has no such
// label of its own.
func lineMatches(n *domain.Node, region string, tags []string, filter domain.TagFilter) bool {
	if filter.All {
		return true
	}
	view := *n
	view.Region, view.Tags = region, tags
	return matchFilter(&view, filter)
}

// visibleLineIndexes reports, per relay of n, whether g may see it, assuming g
// already passes the node.
func visibleLineIndexes(n *domain.Node, g *domain.Group) []bool {
	relays := make([]bool, len(n.Relays))
	for i, relay := range n.Relays {
		relays[i] = lineMatches(n, relay.Region, relay.Tags, g.TagFilter)
	}
	return relays
}

// VisibleLines returns nodes with every relay line g may not see removed. Nodes that lose a line are shallow copies with fresh slices, so
// the shared (cached) originals are never modified; relay health snapshots are
// re-indexed to the surviving relays because consumers match them by index.
// The nodes must already be the ones g passes (NodesFor).
func VisibleLines(g *domain.Group, nodes []*domain.Node) []*domain.Node {
	out := make([]*domain.Node, 0, len(nodes))
	for _, n := range nodes {
		if n == nil || n.IsSeparator() {
			out = append(out, n)
			continue
		}
		relays := visibleLineIndexes(n, g)
		if !slices.Contains(relays, false) {
			out = append(out, n)
			continue
		}
		copied := *n
		copied.Relays = make([]domain.RelayLine, 0, len(n.Relays))
		newIndex := make(map[int]int, len(n.Relays))
		for i, relay := range n.Relays {
			if relays[i] {
				newIndex[i] = len(copied.Relays)
				copied.Relays = append(copied.Relays, relay)
			}
		}
		copied.RelayHealth = make([]domain.RelayHealth, 0, len(n.RelayHealth))
		for _, health := range n.RelayHealth {
			if index, ok := newIndex[health.Index]; ok {
				health.Index = index
				copied.RelayHealth = append(copied.RelayHealth, health)
			}
		}
		out = append(out, &copied)
	}
	return out
}

// SubscriptionNodesFor is NodesFor narrowed by line visibility: the node set
// every subscription format, the group preview and the user status page must
// share (ADR 0041). Provisioning and mounting keep using NodesFor, which is
// node-level by design.
func (s *Service) SubscriptionNodesFor(ctx context.Context, g *domain.Group) ([]*domain.Node, error) {
	nodes, err := s.NodesFor(ctx, g)
	if err != nil {
		return nil, err
	}
	return VisibleLines(g, withoutUnreadyEgress(nodes)), nil
}

// ClientLinesFor is the set of lines a group's users hold a client for (ADR
// 0043): every node the group passes, narrowed to the lines the group may see.
// Unlike SubscriptionNodesFor it keeps a node whose tunnel is not ready yet:
// that node is withheld from the subscription for a while, not taken away, and
// its clients must stay in place for when it comes back.
func (s *Service) ClientLinesFor(ctx context.Context, g *domain.Group) ([]*domain.Node, error) {
	nodes, err := s.NodesFor(ctx, g)
	if err != nil {
		return nil, err
	}
	return VisibleLines(g, nodes), nil
}

// withoutUnreadyEgress drops nodes whose traffic leaves through a tunnel that
// is not yet running on both servers, or whose tunnel's probes have been down
// long enough to be withheld (EgressWithheld, set only while withholding is on
// and only for the node's default exit). Such a node is withheld, never shown
// as working: its users' traffic would fail, and leaving from the entry
// instead would replace the exit the administrator chose.
func withoutUnreadyEgress(nodes []*domain.Node) []*domain.Node {
	shown := func(n *domain.Node) bool { return n.EgressPublishable() && !n.EgressWithheld }
	for _, n := range nodes {
		if !shown(n) {
			return slices.DeleteFunc(slices.Clone(nodes), func(n *domain.Node) bool { return !shown(n) })
		}
	}
	return nodes
}

// LineVisibility answers the node editor's "who would see each line" for a
// draft node, by group ID. NodeGroups are the groups that pass the node;
// Relays[i] are the groups that see that draft line.
type LineVisibility struct {
	Groups     []*domain.Group
	NodeGroups []int64
	Relays     [][]int64
}

// PreviewLineVisibility evaluates a draft node (its own and its lines' unsaved
// labels) against every group. It reads only, and a failed read is an error,
// never an empty answer: "no group" and "unknown" must stay distinguishable.
func (s *Service) PreviewLineVisibility(ctx context.Context, draft *domain.Node) (*LineVisibility, error) {
	groups, err := s.List(ctx)
	if err != nil {
		return nil, err
	}
	result := &LineVisibility{
		Groups:     groups,
		NodeGroups: []int64{},
		Relays:     make([][]int64, len(draft.Relays)),
	}
	for i := range result.Relays {
		result.Relays[i] = []int64{}
	}
	for _, g := range groups {
		ok, err := s.Eligible(ctx, draft, g)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		result.NodeGroups = append(result.NodeGroups, g.ID)
		relays := visibleLineIndexes(draft, g)
		for i, visible := range relays {
			if visible {
				result.Relays[i] = append(result.Relays[i], g.ID)
			}
		}
	}
	return result, nil
}
