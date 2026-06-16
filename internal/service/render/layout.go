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
	// relay, when non-nil, marks this item as a transit variant of node: the
	// emit dispatchers dial the relay's endpoint (and apply its optional SNI /
	// Host override) instead of the node's own ServerAddress. nil = direct.
	// Populated by expandRelays.
	relay *domain.RelayLine
}

// applyLayout returns the ordered list of items to emit, applying:
//  1. Explicit sort weights from layout.Sort.
//  2. Fallback sort strategy from layout.DefaultSortStrategy for un-weighted
//     nodes (defaults to "by_region_then_id").
//  3. Standalone separators (from the nodes_separator table, filtered by
//     SeparatorEntry.VisibleInGroup), inserted into the node sequence by
//     their SortOrder — the same int space nodes use so admins can
//     interleave a divider between two specific nodes.
//  4. Per-group layout.Separators[] positional inserts (the legacy
//     group-config divider; still supported as it serves a different use
//     case — "always between position N and N+1" rather than
//     "between regions tagged X and Y").
//
// Out-of-range positional separators clamp to either end rather than
// failing the render.
func applyLayout(nodes []*domain.Node, separators []*domain.SeparatorEntry, layout domain.Layout) []renderItem {
	sorted := sortNodes(nodes, layout.Sort, layout.DefaultSortStrategy)

	items := make([]renderItem, 0, len(sorted)+len(separators)+len(layout.Separators))

	// Merge nodes + separators by their shared SortOrder space. Stable
	// sort so equal SortOrder values preserve "separator above node"
	// (separator preferred as anchor) for predictable layouts.
	type pre struct {
		sortOrder   int
		isSeparator bool
		name        string
		node        *domain.Node
	}
	pres := make([]pre, 0, len(sorted)+len(separators))
	for _, n := range sorted {
		pres = append(pres, pre{sortOrder: n.SortOrder, name: n.DisplayName, node: n})
	}
	for _, s := range separators {
		pres = append(pres, pre{sortOrder: s.SortOrder, isSeparator: true, name: s.DisplayName})
	}
	sort.SliceStable(pres, func(i, j int) bool {
		if pres[i].sortOrder != pres[j].sortOrder {
			return pres[i].sortOrder < pres[j].sortOrder
		}
		// On tie: separator first (so it sits above the node group it
		// labels). Matches admin's mental model of "----- TW -----"
		// followed by TW nodes.
		if pres[i].isSeparator != pres[j].isSeparator {
			return pres[i].isSeparator
		}
		return false
	})
	for _, p := range pres {
		items = append(items, renderItem{isSeparator: p.isSeparator, name: p.name, node: p.node})
	}

	// Insert positional separators highest-position-first so earlier
	// inserts don't shift the indices of later ones.
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
