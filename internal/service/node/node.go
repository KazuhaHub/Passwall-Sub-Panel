// Package node implements panel-side Node CRUD plus the two flows that
// reach into 3X-UI:
//
//   - Import existing inbound: pure metadata insert, zero 3X-UI writes.
//   - Create new inbound: AddInbound → record metadata.
//
// Deletion goes through sync.Service so the write guards (inbound must end
// up empty before being deleted) apply uniformly.
package node

import (
	"context"
	"errors"
	"fmt"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// InboundCleaner is the narrow subset of sync.Service used by node deletion.
// Defined here so the node package never imports sync.
type InboundCleaner interface {
	DelAllOwnedForInbound(ctx context.Context, panel string, inboundID int) error
	DeleteInbound(ctx context.Context, panel string, inboundID int) error
}

type Service struct {
	nodes   ports.NodeRepo
	pool    ports.XUIPool
	cleaner InboundCleaner
}

func New(nodes ports.NodeRepo, pool ports.XUIPool, cleaner InboundCleaner) *Service {
	return &Service{nodes: nodes, pool: pool, cleaner: cleaner}
}

// ---- Read ----

func (s *Service) Get(ctx context.Context, id int64) (*domain.Node, error) {
	return s.nodes.GetByID(ctx, id)
}

func (s *Service) List(ctx context.Context) ([]*domain.Node, error) {
	return s.nodes.List(ctx)
}

// ---- Create flows ----

// ImportExisting registers an inbound that already lives in 3X-UI under
// panel management. No 3X-UI write happens; only the metadata row is
// persisted. The caller must supply (PanelName, InboundID, DisplayName, Region)
// at minimum.
func (s *Service) ImportExisting(ctx context.Context, n *domain.Node) error {
	if n.DisplayName == "" || n.Region == "" {
		return fmt.Errorf("%w: display_name and region required", domain.ErrValidation)
	}
	if existing, err := s.nodes.GetByPanelInbound(ctx, n.PanelName, n.InboundID); err == nil && existing != nil {
		return domain.ErrAlreadyExists
	} else if err != nil && !errors.Is(err, domain.ErrNotFound) {
		return err
	}
	// Verify the inbound actually exists in 3X-UI.
	c, err := s.pool.Get(n.PanelName)
	if err != nil {
		return err
	}
	if _, err := c.GetInbound(ctx, n.InboundID); err != nil {
		return fmt.Errorf("inbound %d not found on panel %s: %w", n.InboundID, n.PanelName, err)
	}
	n.Enabled = true
	return s.nodes.Create(ctx, n)
}

// CreateInbound creates a brand new inbound in 3X-UI and registers it.
// The InboundSpec is forwarded verbatim — protocol/Reality/etc. parameters
// are the caller's (i.e. frontend's) responsibility to compose correctly.
func (s *Service) CreateInbound(ctx context.Context, n *domain.Node, spec ports.InboundSpec) error {
	if n.DisplayName == "" || n.Region == "" || n.PanelName == "" {
		return fmt.Errorf("%w: display_name, region and panel_name required", domain.ErrValidation)
	}
	c, err := s.pool.Get(n.PanelName)
	if err != nil {
		return err
	}
	inboundID, err := c.AddInbound(ctx, spec)
	if err != nil {
		return fmt.Errorf("xui addInbound: %w", err)
	}
	n.InboundID = inboundID
	n.Enabled = true
	if err := s.nodes.Create(ctx, n); err != nil {
		// best-effort rollback so 3X-UI doesn't drift from the panel.
		_ = c.DelInbound(context.Background(), inboundID)
		return err
	}
	return nil
}

// ---- Update flows ----

// UpdateMetadata updates panel-side fields only (display_name / region /
// tags / sort_order). 3X-UI is not touched.
func (s *Service) UpdateMetadata(ctx context.Context, n *domain.Node) error {
	if _, err := s.nodes.GetByID(ctx, n.ID); err != nil {
		return err
	}
	return s.nodes.Update(ctx, n)
}

// UpdateInboundConfig pushes protocol-parameter changes to 3X-UI.
// Display-time fields like region/tags should go through UpdateMetadata.
func (s *Service) UpdateInboundConfig(ctx context.Context, id int64, spec ports.InboundSpec) error {
	n, err := s.nodes.GetByID(ctx, id)
	if err != nil {
		return err
	}
	c, err := s.pool.Get(n.PanelName)
	if err != nil {
		return err
	}
	return c.UpdateInbound(ctx, n.InboundID, spec)
}

// SetEnabled toggles the inbound enable flag both in 3X-UI and locally.
func (s *Service) SetEnabled(ctx context.Context, id int64, enabled bool) error {
	n, err := s.nodes.GetByID(ctx, id)
	if err != nil {
		return err
	}
	c, err := s.pool.Get(n.PanelName)
	if err != nil {
		return err
	}
	if err := c.SetInboundEnable(ctx, n.InboundID, enabled); err != nil {
		return fmt.Errorf("xui setEnable: %w", err)
	}
	n.Enabled = enabled
	return s.nodes.Update(ctx, n)
}

// ---- Delete flow ----

// DeleteAndSync removes a node from both the panel and 3X-UI.
// Sequence:
//  1. Delete every owned client in the inbound (so it's empty of managed clients).
//  2. Call sync.DeleteInbound — its guard ensures no UNMANAGED clients remain.
//     If unmanaged clients exist, the call fails with ErrInboundHasUnmanagedClients.
//  3. Delete the panel-side nodes row.
func (s *Service) DeleteAndSync(ctx context.Context, id int64) error {
	n, err := s.nodes.GetByID(ctx, id)
	if err != nil {
		return err
	}
	if err := s.cleaner.DelAllOwnedForInbound(ctx, n.PanelName, n.InboundID); err != nil {
		return fmt.Errorf("clear owned clients: %w", err)
	}
	if err := s.cleaner.DeleteInbound(ctx, n.PanelName, n.InboundID); err != nil {
		return err
	}
	return s.nodes.Delete(ctx, id)
}

// ---- Discovery ----

// UnmanagedInbound describes an inbound that exists in 3X-UI but is not
// (yet) registered under panel management.
type UnmanagedInbound struct {
	PanelName   string
	InboundID   int
	Protocol    string
	Port        int
	Remark      string
	Enable      bool
	ClientCount int
}

// ListUnmanagedInbounds walks every registered 3X-UI panel and returns
// inbounds whose (panel_name, inbound_id) is NOT in the nodes table.
// Used to populate the "unmanaged" tab in the node management UI.
func (s *Service) ListUnmanagedInbounds(ctx context.Context) ([]*UnmanagedInbound, error) {
	out := []*UnmanagedInbound{}
	for _, panel := range s.pool.List() {
		c, err := s.pool.Get(panel)
		if err != nil {
			continue
		}
		inbounds, err := c.ListInbounds(ctx)
		if err != nil {
			return nil, fmt.Errorf("list inbounds for %s: %w", panel, err)
		}
		for i := range inbounds {
			inb := &inbounds[i]
			_, err := s.nodes.GetByPanelInbound(ctx, panel, inb.ID)
			if err == nil {
				continue // already managed
			}
			if !errors.Is(err, domain.ErrNotFound) {
				return nil, err
			}
			out = append(out, &UnmanagedInbound{
				PanelName:   panel,
				InboundID:   inb.ID,
				Protocol:    inb.Protocol,
				Port:        inb.Port,
				Remark:      inb.Remark,
				Enable:      inb.Enable,
				ClientCount: len(inb.ClientStats),
			})
		}
	}
	return out, nil
}

// InboundClientView is one client row inside a node detail page, annotated
// with whether the panel manages it.
type InboundClientView struct {
	Email       string
	Up          int64
	Down        int64
	Enable      bool
	ExpiryTime  int64
	Managed     bool
	OwnerUserID int64
}

// ListClientsOfInbound returns the clients sitting in an inbound, each
// flagged as managed/unmanaged based on the ownership table. UUID is not
// part of ClientStats and is omitted here; the claim flow accepts uuid
// separately when needed.
func (s *Service) ListClientsOfInbound(ctx context.Context, nodeID int64, ownership ports.OwnershipRepo) ([]*InboundClientView, error) {
	n, err := s.nodes.GetByID(ctx, nodeID)
	if err != nil {
		return nil, err
	}
	c, err := s.pool.Get(n.PanelName)
	if err != nil {
		return nil, err
	}
	inb, err := c.GetInbound(ctx, n.InboundID)
	if err != nil {
		return nil, err
	}
	out := make([]*InboundClientView, 0, len(inb.ClientStats))
	for _, cs := range inb.ClientStats {
		view := &InboundClientView{
			Email:      cs.Email,
			Up:         cs.Up,
			Down:       cs.Down,
			Enable:     cs.Enable,
			ExpiryTime: cs.ExpiryTime,
		}
		entry, err := ownership.GetByMatch(ctx, n.PanelName, n.InboundID, cs.Email)
		if err == nil {
			view.Managed = true
			view.OwnerUserID = entry.UserID
		}
		out = append(out, view)
	}
	return out, nil
}
