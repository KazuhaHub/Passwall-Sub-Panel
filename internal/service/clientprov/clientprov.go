// Package clientprov reconciles a user's DESIRED v3.9.0 psp_client state on one
// panel into the database. It is the local "dual-write" half of the v3.9.0
// migration: it owns no 3X-UI calls — it only makes PSP's psp_clients +
// psp_client_inbounds match what clientplan.Build says should exist. A later
// phase (reconcile) diffs this desired attachment set against the panel's live
// GetClient().InboundIDs and issues the attach/detach. It is wired into user
// membership resync before the panel-facing convergence phase.
package clientprov

import (
	"context"
	"fmt"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/clientplan"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

type Service struct {
	clients ports.PSPClientRepo
}

func New(clients ports.PSPClientRepo) *Service { return &Service{clients: clients} }

// Sync makes the user's psp_clients on ONE panel match the desired set computed
// from the nodes they can access there:
//   - carry persisted row IDs onto the closest desired attachment partition,
//     update those rows by ID, and create only genuinely additional rows;
//   - delete any of the user's clients ON THIS PANEL that the desired set no
//     longer includes — a credential class that no longer applies, or (when
//     nodes is empty) every client on the panel because access was revoked.
//
// It is idempotent: a no-change call updates the same IDs and deletes nothing.
// Credentials and attachments are authoritative here; the per-client traffic
// counters are owned by the poll — PSPClientRepo.UpdateDefinition updates only
// mutable definition/credential columns, so a dual-write never clobbers usage.
// Sync reconciles the user's psp_client rows on ONE panel to clientplan.Build's
// desired set and RETURNS the emails of the psp_clients it pruned (rows the new
// plan no longer wants). The prune here is DB-only — this is the shadow dual-write,
// it never touches 3X-UI — so the reconcile caller must delete the corresponding
// 3X-UI clients. A pruned email arises when a node leaves the user's group, a whole
// panel is dropped, OR the partition grouping changes (the v3.9.0 merge collapses a
// user's per-class clients into one); in every case the old 3X-UI client is now an
// orphan and must be removed by the caller.
func (s *Service) Sync(ctx context.Context, userID int64, userUUID string, panelID int64, rules domain.EmailRules, nodes []clientplan.NodeCred) ([]string, error) {
	allExisting, err := s.clients.ListByUser(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("list existing clients: %w", err)
	}
	existing := make([]*domain.PSPClient, 0, len(allExisting))
	stable := make([]clientplan.ExistingClient, 0, len(allExisting))
	for _, c := range allExisting {
		if c.PanelID != panelID {
			continue
		}
		inbounds, ierr := s.clients.ListInbounds(ctx, c.ID)
		if ierr != nil {
			return nil, fmt.Errorf("list inbounds for stable client %d: %w", c.ID, ierr)
		}
		nodeIDs := make([]int64, len(inbounds))
		for i, in := range inbounds {
			nodeIDs[i] = in.NodeID
		}
		existing = append(existing, c)
		stable = append(stable, clientplan.ExistingClient{
			ClientID:  c.ID,
			CredClass: c.CredClass,
			NodeIDs:   nodeIDs,
		})
	}
	desired := clientplan.Build(userID, userUUID, panelID, rules, nodes, stable)

	keep := make(map[int64]struct{}, len(desired))
	for _, d := range desired {
		c := d.Client
		id := c.ID
		if id == 0 {
			id, err = s.clients.Create(ctx, &c)
			if err != nil {
				return nil, fmt.Errorf("create psp_client %s: %w", c.Email, err)
			}
		} else if err = s.clients.UpdateDefinition(ctx, &c); err != nil {
			return nil, fmt.Errorf("update stable psp_client %d: %w", id, err)
		}
		inbs := make([]domain.PSPClientInbound, len(d.Inbounds))
		for i, in := range d.Inbounds {
			in.ClientID = id
			inbs[i] = in
		}
		if err := s.clients.SetInbounds(ctx, id, inbs); err != nil {
			return nil, fmt.Errorf("set inbounds for %s: %w", d.Client.Email, err)
		}
		keep[id] = struct{}{}
	}

	var pruned []string
	for _, e := range existing {
		if _, ok := keep[e.ID]; ok {
			continue
		}
		if err := s.clients.DeleteByID(ctx, e.ID); err != nil {
			return pruned, fmt.Errorf("prune stale client %s: %w", e.Email, err)
		}
		pruned = append(pruned, e.Email)
	}
	return pruned, nil
}

// SyncUser reconciles ALL of a user's psp_clients across every panel from their
// desired nodes (the group selector's output). It buckets nodes by panel and
// calls Sync per panel; it ALSO calls Sync (with no nodes) for any panel where
// the user still holds a client but now has zero desired nodes, so a user who
// lost access to a whole server gets that server's client pruned. Separators and
// undeterminable-protocol nodes are dropped by NodeCredsFromNodes. Returns, per
// panel, the emails of the psp_clients it pruned (so the reconcile caller can
// delete the now-orphaned 3X-UI clients), plus the first per-panel error (it
// attempts every panel regardless).
func (s *Service) SyncUser(ctx context.Context, userID int64, userUUID string, rules domain.EmailRules, desiredNodes []*domain.Node) (map[int64][]string, error) {
	byPanel := map[int64][]*domain.Node{}
	for _, n := range desiredNodes {
		if n == nil || n.Kind == domain.NodeKindSeparator {
			continue
		}
		byPanel[n.PanelID] = append(byPanel[n.PanelID], n)
	}

	// Union the desired panels with the panels the user currently has clients on,
	// so a now-empty panel is visited and pruned.
	panels := make(map[int64]struct{}, len(byPanel))
	for p := range byPanel {
		panels[p] = struct{}{}
	}
	existing, err := s.clients.ListByUser(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("list existing clients: %w", err)
	}
	for _, c := range existing {
		panels[c.PanelID] = struct{}{}
	}

	pruned := map[int64][]string{}
	var firstErr error
	for panelID := range panels {
		creds := clientplan.NodeCredsFromNodes(byPanel[panelID]) // empty slice → prunes the panel
		p, serr := s.Sync(ctx, userID, userUUID, panelID, rules, creds)
		if len(p) > 0 {
			pruned[panelID] = p
		}
		if serr != nil && firstErr == nil {
			firstErr = serr
		}
	}
	return pruned, firstErr
}
