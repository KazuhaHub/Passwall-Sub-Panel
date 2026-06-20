// Package clientprov reconciles a user's DESIRED v3.9.0 psp_client state on one
// panel into the database. It is the local "dual-write" half of the v3.9.0
// migration: it owns no 3X-UI calls — it only makes PSP's psp_clients +
// psp_client_inbounds match what clientplan.Build says should exist. A later
// phase (reconcile) diffs this desired attachment set against the panel's live
// GetClient().InboundIDs and issues the attach/detach. Added dormant — no caller
// wires it yet.
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
//   - upsert each desired client (identity + stored credentials) and REPLACE its
//     attachment set;
//   - delete any of the user's clients ON THIS PANEL that the desired set no
//     longer includes — a credential class that no longer applies, or (when
//     nodes is empty) every client on the panel because access was revoked.
//
// It is idempotent: a no-change call upserts the same rows and deletes nothing.
// Credentials and attachments are authoritative here; the per-client traffic
// counters are owned by the poll — PSPClientRepo.Upsert updates only identity +
// credential columns, so a dual-write never clobbers accumulated usage.
func (s *Service) Sync(ctx context.Context, userID int64, userUUID string, panelID int64, rules domain.EmailRules, nodes []clientplan.NodeCred) error {
	desired := clientplan.Build(userID, userUUID, panelID, rules, nodes)

	keep := make(map[string]struct{}, len(desired))
	for _, d := range desired {
		c := d.Client // copy: Upsert may stamp ID/CreatedAt
		id, err := s.clients.Upsert(ctx, &c)
		if err != nil {
			return fmt.Errorf("upsert psp_client %s: %w", d.Client.Email, err)
		}
		inbs := make([]domain.PSPClientInbound, len(d.Inbounds))
		for i, in := range d.Inbounds {
			in.ClientID = id
			inbs[i] = in
		}
		if err := s.clients.SetInbounds(ctx, id, inbs); err != nil {
			return fmt.Errorf("set inbounds for %s: %w", d.Client.Email, err)
		}
		keep[d.Client.Email] = struct{}{}
	}

	existing, err := s.clients.ListByUser(ctx, userID)
	if err != nil {
		return fmt.Errorf("list existing clients: %w", err)
	}
	for _, e := range existing {
		if e.PanelID != panelID {
			continue // only this panel's clients are in scope
		}
		if _, ok := keep[e.Email]; ok {
			continue
		}
		if err := s.clients.DeleteByEmail(ctx, panelID, e.Email); err != nil {
			return fmt.Errorf("prune stale client %s: %w", e.Email, err)
		}
	}
	return nil
}

// SyncUser reconciles ALL of a user's psp_clients across every panel from their
// desired nodes (the group selector's output). It buckets nodes by panel and
// calls Sync per panel; it ALSO calls Sync (with no nodes) for any panel where
// the user still holds a client but now has zero desired nodes, so a user who
// lost access to a whole server gets that server's client pruned. Separators and
// undeterminable-protocol nodes are dropped by NodeCredsFromNodes. Returns the
// first per-panel error but attempts every panel.
func (s *Service) SyncUser(ctx context.Context, userID int64, userUUID string, rules domain.EmailRules, desiredNodes []*domain.Node) error {
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
		return fmt.Errorf("list existing clients: %w", err)
	}
	for _, c := range existing {
		panels[c.PanelID] = struct{}{}
	}

	var firstErr error
	for panelID := range panels {
		creds := clientplan.NodeCredsFromNodes(byPanel[panelID]) // empty slice → prunes the panel
		if serr := s.Sync(ctx, userID, userUUID, panelID, rules, creds); serr != nil && firstErr == nil {
			firstErr = serr
		}
	}
	return firstErr
}
