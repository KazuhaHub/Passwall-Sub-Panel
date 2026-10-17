package render

import (
	"context"
	"fmt"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// expandNodeLines is shared by subscription generation and rule-set previews.
// Each route selects one endpoint; relays are never expanded as a cross product.
func expandNodeLines(items []renderItem) []renderItem {
	out := []renderItem{}
	for _, item := range items {
		out = append(out, expandRelays([]renderItem{item})...)
		if item.node == nil || item.isSeparator {
			continue
		}
		for _, line := range item.node.RouteState.Lines {
			if line.LocallyAvailable(item.node) {
				relay, available := line.ResolveEntry(item.node)
				if !available {
					continue
				}
				copy := line
				out = append(out, renderItem{node: item.node, name: routeEntryName(item.name, line), route: &copy, relay: relay})
			}
		}
	}
	// Labels are client-side references, not identity. Disambiguate only when
	// necessary; unchanged historical names keep their exact spelling.
	counts := map[string]int{}
	for i := range out {
		out[i].baseName = out[i].name
		counts[out[i].name]++
	}
	for i := range out {
		if counts[out[i].name] > 1 && out[i].node != nil && !out[i].isSeparator {
			out[i].name = fmt.Sprintf("%s [%d:%s]", out[i].name, out[i].node.ID, out[i].lineID())
		}
	}
	return out
}

func routeEntryName(base string, line domain.VlessRouteLine) string {
	if line.OnlyLineName {
		return line.Label()
	}
	return base + " " + line.Label()
}

func (s *Service) filterRouteItems(ctx context.Context, u *domain.User, items []renderItem) []renderItem {
	hasRoutes := false
	for _, item := range items {
		hasRoutes = hasRoutes || item.route != nil
	}
	if !hasRoutes {
		return items
	}
	applied := s.appliedClientCredentialsByNode(ctx, u)
	panels := map[int64]*domain.XUIPanel{}
	out := make([]renderItem, 0, len(items))
	for _, item := range items {
		if item.route == nil {
			out = append(out, item)
			continue
		}
		if !item.route.LocallyAvailable(item.node) {
			continue
		}
		if s.repos.XUIPanel == nil {
			continue
		}
		panel, ok := panels[item.node.PanelID]
		if !ok {
			panel, _ = s.repos.XUIPanel.GetByID(ctx, item.node.PanelID)
			panels[item.node.PanelID] = panel
		}
		if panel == nil || domain.NormalizePanelKind(panel.Kind) != domain.PanelKind3XUI || item.node.RouteState.Binding != domain.RouteBinding(panel.URL, item.node) {
			continue
		}
		credential, ok := applied[item.node.ID]
		if !ok || credential.UUID == "" {
			continue
		}
		original, err := domain.UUIDRoute(credential.UUID)
		if err != nil || original == item.route.Route {
			continue
		}
		out = append(out, item)
	}
	return out
}

// PublishedRouteLines uses the subscription's local-only publication gates for
// the user status page too, including backend binding and applied credentials.
// No remote probes or credential values cross this metadata-only boundary.
func (s *Service) PublishedRouteLines(ctx context.Context, u *domain.User, nodes []*domain.Node) map[int64][]domain.VlessRouteLine {
	items := []renderItem{}
	for _, n := range nodes {
		if n != nil && !n.IsSeparator() {
			items = append(items, renderItem{node: n, name: n.DisplayName})
		}
	}
	result := map[int64][]domain.VlessRouteLine{}
	for _, item := range s.filterRouteItems(ctx, u, expandNodeLines(items)) {
		if item.route != nil {
			result[item.node.ID] = append(result[item.node.ID], *item.route)
		}
	}
	return result
}

func (it renderItem) userForLine(u *domain.User) (*domain.User, error) {
	if it.route == nil {
		return u, nil
	}
	copy := *u
	id, err := domain.WithUUIDRoute(u.UUID, it.route.Route)
	if err != nil {
		return nil, err
	}
	copy.UUID = id
	return &copy, nil
}
