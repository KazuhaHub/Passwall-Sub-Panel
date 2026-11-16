package group

import (
	"context"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// Edit-time effect previews (ADR 0042). Each answers "what would this draft
// do" with the same functions the save and the subscription use, reads local
// state and caches only, and reports a failed read as an error, never as an
// empty answer.

// ExcludedAllowlist is the reason a node that matches a group's tag_filter is
// still left out: the group has an allowlist the node cannot enforce.
const ExcludedAllowlist = "allowlist_unenforceable"

// GroupsForNode lists the groups n passes (Eligible), in group list order.
func (s *Service) GroupsForNode(ctx context.Context, n *domain.Node) ([]int64, error) {
	groups, err := s.List(ctx)
	if err != nil {
		return nil, err
	}
	out := []int64{}
	for _, g := range groups {
		ok, err := s.Eligible(ctx, n, g)
		if err != nil {
			return nil, err
		}
		if ok {
			out = append(out, g.ID)
		}
	}
	return out, nil
}

// GroupPreviewNode is one node a draft group would include, with how many of
// its lines that group would see (ADR 0041) out of its undeleted lines.
type GroupPreviewNode struct {
	Node         *domain.Node
	VisibleLines int
	TotalLines   int
}

// GroupPreviewExcluded is a node the draft's tag_filter matches but that the
// group still leaves out, with a stable reason code.
type GroupPreviewExcluded struct {
	Node   *domain.Node
	Reason string
}

// GroupNodePreview answers the group editor. Added / Removed compare the draft
// with the saved group's node set and Members is its member count; all three
// are empty for a group that does not exist yet.
type GroupNodePreview struct {
	Nodes    []GroupPreviewNode
	Excluded []GroupPreviewExcluded
	Added    []int64
	Removed  []int64
	Members  int64
}

// PreviewGroupNodes evaluates draft (a group with an unsaved tag_filter) over
// the enabled nodes NodesFor would consider. saved is the stored group, or nil
// when creating one.
func (s *Service) PreviewGroupNodes(ctx context.Context, draft, saved *domain.Group) (*GroupNodePreview, error) {
	all, err := s.listEnabledCached(ctx)
	if err != nil {
		return nil, err
	}
	result := &GroupNodePreview{Nodes: []GroupPreviewNode{}, Excluded: []GroupPreviewExcluded{}, Added: []int64{}, Removed: []int64{}}
	included := map[int64]bool{}
	for _, n := range all {
		if n == nil || n.IsSeparator() || !Matches(n, draft.TagFilter) {
			continue
		}
		ok, err := s.Eligible(ctx, n, draft)
		if err != nil {
			return nil, err
		}
		if !ok {
			result.Excluded = append(result.Excluded, GroupPreviewExcluded{Node: n, Reason: ExcludedAllowlist})
			continue
		}
		included[n.ID] = true
		result.Nodes = append(result.Nodes, previewNode(n, draft))
	}
	if saved == nil {
		return result, nil
	}
	current, err := s.NodesFor(ctx, saved)
	if err != nil {
		return nil, err
	}
	before := map[int64]bool{}
	for _, n := range current {
		if n == nil || n.IsSeparator() {
			continue
		}
		before[n.ID] = true
		if !included[n.ID] {
			result.Removed = append(result.Removed, n.ID)
		}
	}
	for _, node := range result.Nodes {
		if !before[node.Node.ID] {
			result.Added = append(result.Added, node.Node.ID)
		}
	}
	if result.Members, err = s.CountMembers(ctx, saved.ID); err != nil {
		return nil, err
	}
	return result, nil
}

// previewNode counts the lines g would see on n (ADR 0041) out of its
// undeleted lines.
func previewNode(n *domain.Node, g *domain.Group) GroupPreviewNode {
	relays, routes := visibleLineIndexes(n, g)
	node := GroupPreviewNode{Node: n}
	for _, visible := range relays {
		node.TotalLines++
		if visible {
			node.VisibleLines++
		}
	}
	for i, visible := range routes {
		if n.RouteState.Lines[i].Deleted {
			continue
		}
		node.TotalLines++
		if visible {
			node.VisibleLines++
		}
	}
	return node
}

// MembershipPreview is what moving a user from one group to another changes
// (ADR 0042 §3): nodes gained (lines counted under the target group), nodes
// lost (counted under the current group), and how many stay.
type MembershipPreview struct {
	Added   []GroupPreviewNode
	Removed []GroupPreviewNode
	Kept    int
}

// PreviewGroupChange compares the node sets of two saved groups exactly as
// NodesFor resolves them. It provisions nothing.
func (s *Service) PreviewGroupChange(ctx context.Context, fromID, toID int64) (*MembershipPreview, error) {
	from, err := s.Get(ctx, fromID)
	if err != nil {
		return nil, err
	}
	to, err := s.Get(ctx, toID)
	if err != nil {
		return nil, err
	}
	before, err := s.NodesFor(ctx, from)
	if err != nil {
		return nil, err
	}
	after, err := s.NodesFor(ctx, to)
	if err != nil {
		return nil, err
	}
	result := &MembershipPreview{Added: []GroupPreviewNode{}, Removed: []GroupPreviewNode{}}
	had := map[int64]bool{}
	for _, n := range before {
		if n != nil && !n.IsSeparator() {
			had[n.ID] = true
		}
	}
	has := map[int64]bool{}
	for _, n := range after {
		if n == nil || n.IsSeparator() {
			continue
		}
		has[n.ID] = true
		if had[n.ID] {
			result.Kept++
		} else {
			result.Added = append(result.Added, previewNode(n, to))
		}
	}
	for _, n := range before {
		if n != nil && !n.IsSeparator() && !has[n.ID] {
			result.Removed = append(result.Removed, previewNode(n, from))
		}
	}
	return result, nil
}

// GroupsForSeparator lists the groups whose subscriptions would show a
// separator with the draft's mode and node IDs (ADR 0042 §4), using the same
// SeparatorEntry.VisibleForNodes the renderer calls with the group's node IDs.
// The draft is evaluated as enabled: the question is where it would appear.
func (s *Service) GroupsForSeparator(ctx context.Context, draft domain.SeparatorEntry) ([]int64, error) {
	groups, err := s.List(ctx)
	if err != nil {
		return nil, err
	}
	draft.Enabled = true
	out := []int64{}
	for _, g := range groups {
		nodes, err := s.NodesFor(ctx, g)
		if err != nil {
			return nil, err
		}
		ids := make([]int64, 0, len(nodes))
		for _, n := range nodes {
			if n != nil {
				ids = append(ids, n.ID)
			}
		}
		if draft.VisibleForNodes(ids) {
			out = append(out, g.ID)
		}
	}
	return out, nil
}
