// Package health probes each enabled node against its 3X-UI panel and
// persists the outcome on the Node row so the admin UI can show a live
// status indicator without making the page hit 3X-UI directly.
//
// The probe is deliberately cheap: one ListInbounds call per panel (not per
// node — multiple nodes can share a panel), and the result is pattern-matched
// against the node's recorded inbound ID. This keeps the per-tick cost
// proportional to the number of panels, not the number of nodes.
package health

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/log"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

type Service struct {
	nodes ports.NodeRepo
	pool  ports.XUIPool
}

func New(nodes ports.NodeRepo, pool ports.XUIPool) *Service {
	return &Service{nodes: nodes, pool: pool}
}

// CheckOnce probes every enabled node and updates its HealthState. Disabled
// nodes are not probed (admin chose to take them out of rotation; a "down"
// dot would be misleading) and their previous health is left as-is until
// they're re-enabled.
//
// Errors per node / per panel are logged but don't abort the pass.
func (s *Service) CheckOnce(ctx context.Context) error {
	allNodes, err := s.nodes.List(ctx)
	if err != nil {
		return fmt.Errorf("list nodes: %w", err)
	}
	// Group enabled nodes by panel so one ListInbounds call covers every
	// node on that panel.
	byPanel := make(map[int64][]*domain.Node, len(allNodes))
	for _, n := range allNodes {
		if !n.Enabled {
			continue
		}
		byPanel[n.PanelID] = append(byPanel[n.PanelID], n)
	}

	now := time.Now()
	for panelID, nodes := range byPanel {
		c, err := s.pool.Get(panelID)
		if err != nil {
			// Panel not configured / missing from pool. Mark every node
			// behind it as unreachable so the admin can spot the broken
			// link without opening per-node detail pages.
			for _, n := range nodes {
				s.persist(ctx, n, domain.NodeHealthPanelUnreachable, err.Error(), now)
			}
			continue
		}
		listed, err := c.ListInbounds(ctx)
		if err != nil {
			for _, n := range nodes {
				s.persist(ctx, n, domain.NodeHealthPanelUnreachable, err.Error(), now)
			}
			continue
		}
		// Index inbounds by ID for O(1) per-node lookup.
		byInbound := make(map[int]ports.Inbound, len(listed))
		for _, inb := range listed {
			byInbound[inb.ID] = inb
		}
		for _, n := range nodes {
			state, detail := decideHealth(n.InboundID, byInbound)
			s.persist(ctx, n, state, detail, now)
		}
	}
	return nil
}

// decideHealth is split out so the tests can drive every branch without
// spinning up a fake pool / repo.
func decideHealth(inboundID int, byInbound map[int]ports.Inbound) (domain.NodeHealthState, string) {
	inb, ok := byInbound[inboundID]
	if !ok {
		return domain.NodeHealthInboundMissing, fmt.Sprintf("inbound %d not present on panel", inboundID)
	}
	if !inb.Enable {
		return domain.NodeHealthInboundDisabled, "inbound is disabled on 3X-UI"
	}
	return domain.NodeHealthOK, ""
}

func (s *Service) persist(ctx context.Context, n *domain.Node, state domain.NodeHealthState, detail string, at time.Time) {
	// Skip the write when nothing changed — health checks are frequent and
	// most ticks are "still healthy". Cuts DB churn dramatically on stable
	// deployments.
	if n.HealthState == state && n.HealthDetail == detail {
		return
	}
	n.HealthState = state
	n.HealthDetail = detail
	n.HealthCheckedAt = &at
	if err := s.nodes.Update(ctx, n); err != nil {
		// Don't propagate — one stuck node row mustn't block updates for
		// the rest of the fleet.
		log.Warn("health checker persist", "node_id", n.ID, "err", err)
	}
}

// Loop runs CheckOnce on a fixed interval until ctx is cancelled. Designed
// to be launched as a background goroutine from app startup.
func (s *Service) Loop(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		log.Warn("health checker disabled (interval <= 0)")
		return
	}
	// Run once immediately so admins don't have to wait a full interval
	// for the first dot to appear after panel boot.
	if err := s.CheckOnce(ctx); err != nil && !errors.Is(err, context.Canceled) {
		log.Warn("health checker initial run", "err", err)
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := s.CheckOnce(ctx); err != nil && !errors.Is(err, context.Canceled) {
				log.Warn("health checker tick", "err", err)
			}
		}
	}
}
