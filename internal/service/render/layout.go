package render

import (
	"sort"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// renderItem is one entry in the rendered proxy sequence: either a real
// node (node != nil) or a separator placeholder (isSeparator == true).
type renderItem struct {
	isSeparator bool
	name        string
	node        *domain.Node
}

// applyLayout returns the ordered list of items to emit, applying:
//  1. Explicit sort weights from layout.Sort.
//  2. Fallback sort strategy from layout.DefaultSortStrategy for un-weighted
//     nodes (defaults to "by_region_then_id").
//  3. Separator insertion at layout.Separators[].Position, with positions
//     resolved against the post-sort node sequence.
//
// Out-of-range separator positions clamp to either end of the list rather
// than failing the render.
func applyLayout(nodes []*domain.Node, layout domain.Layout) []renderItem {
	sorted := sortNodes(nodes, layout.Sort, layout.DefaultSortStrategy)

	items := make([]renderItem, 0, len(sorted)+len(layout.Separators))
	for _, n := range sorted {
		// Node-kind separators participate in normal sort + tag_filter
		// matching (so admins drag them into place from the Nodes page),
		// but emit as isSeparator items so buildProxies / urilist /
		// singbox render them through emitSeparator instead of fetching
		// an inbound. The existing group-level Layout.Separators below
		// still works in parallel for admins who'd rather configure
		// dividers per-group.
		if n.IsSeparator() {
			items = append(items, renderItem{isSeparator: true, name: n.DisplayName})
			continue
		}
		items = append(items, renderItem{node: n, name: n.DisplayName})
	}

	// Insert separators highest-position-first so earlier inserts don't shift
	// the indices of later ones.
	seps := append([]domain.Separator(nil), layout.Separators...)
	sort.Slice(seps, func(i, j int) bool {
		return seps[i].Position > seps[j].Position
	})
	for _, sep := range seps {
		pos := sep.Position
		if pos < 0 {
			pos = 0
		}
		if pos > len(items) {
			pos = len(items)
		}
		items = append(items[:pos],
			append([]renderItem{{isSeparator: true, name: sep.Name}}, items[pos:]...)...)
	}
	return items
}

func sortNodes(nodes []*domain.Node, entries []domain.SortEntry, strategy string) []*domain.Node {
	weights := make(map[int64]int, len(entries))
	for _, e := range entries {
		weights[e.NodeID] = e.Weight
	}
	sorted := append([]*domain.Node(nil), nodes...)
	sort.SliceStable(sorted, func(i, j int) bool {
		wi, oki := weights[sorted[i].ID]
		wj, okj := weights[sorted[j].ID]
		switch {
		case oki && okj:
			return wi < wj
		case oki:
			return true
		case okj:
			return false
		}
		return fallbackLess(sorted[i], sorted[j], strategy)
	})
	return sorted
}

func fallbackLess(a, b *domain.Node, strategy string) bool {
	if strategy == "by_region_then_id" || strategy == "" {
		if a.Region != b.Region {
			return a.Region < b.Region
		}
	}
	if a.SortOrder != b.SortOrder {
		return a.SortOrder < b.SortOrder
	}
	return a.ID < b.ID
}
