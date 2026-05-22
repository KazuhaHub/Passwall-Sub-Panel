package render

import (
	"context"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/inboundcfg"
)

// inboundFromNode reconstructs a ports.Inbound from the node's locally stored
// config snapshot (v3.5: PSP is the source of truth for inbound config).
// The mapping lives in inboundcfg so the node service and reconcile share it.
func inboundFromNode(n *domain.Node) *ports.Inbound {
	return inboundcfg.InboundFromNode(n)
}

// nodeHasLocalConfig reports whether render can build this node's proxy block
// from the local snapshot (zero 3X-UI calls). Thin alias for the canonical
// definition in inboundcfg, shared with the admin edit dialog.
func nodeHasLocalConfig(n *domain.Node) bool {
	return inboundcfg.HasLocalConfig(n)
}

// resolveInbounds returns a node-id → inbound map covering every real
// (non-separator) node in items. Captured nodes are served from their local
// config snapshot (zero 3X-UI calls); un-captured nodes — the post-upgrade /
// freshly-imported transition window — are batched into a single ListInbounds
// per panel via prefetchInboundsForRender. A node absent from the result (its
// panel was unreachable on the fallback path) is skipped + warned by the
// caller. All three render paths (mihomo / sing-box / URI-list) share this, so
// the local-first + bulk-fallback policy lives in exactly one place.
func (s *Service) resolveInbounds(ctx context.Context, items []renderItem) map[int64]*ports.Inbound {
	out := make(map[int64]*ports.Inbound, len(items))
	var fallback []renderItem
	for _, it := range items {
		if it.isSeparator || it.node == nil {
			continue
		}
		if nodeHasLocalConfig(it.node) {
			out[it.node.ID] = inboundFromNode(it.node)
		} else {
			fallback = append(fallback, it)
		}
	}
	if len(fallback) > 0 {
		for id, inb := range s.prefetchInboundsForRender(ctx, fallback) {
			out[id] = inb
		}
	}
	return out
}
