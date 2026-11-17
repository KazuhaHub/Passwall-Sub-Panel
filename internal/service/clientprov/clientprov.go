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
//     longer includes because a credential class no longer applies;
//   - when nodes is empty, detach every client but retain its stable row and
//     counters. The returned emails tell the caller to remove the live upstream
//     projection without erasing PSP's cumulative-counter baseline.
//
// It is idempotent: a no-change call updates the same IDs and deletes nothing.
// Credentials and attachments are authoritative here; the per-client traffic
// counters are owned by the poll — PSPClientRepo.UpdateDefinition updates only
// mutable definition/credential columns, so a dual-write never clobbers usage.
// Sync reconciles the user's psp_client rows on ONE panel to clientplan.Build's
// desired set and RETURNS the emails of live upstream projections it retired.
// This is the DB-only shadow dual-write, so the caller must remove those emails
// from the panel. A retired email either belongs to a stale row pruned after a
// non-empty repartition, or to a retained row whose attachments became empty.
func (s *Service) Sync(ctx context.Context, userID int64, userUUID string, panelID int64, rules domain.EmailRules, lines []clientplan.LineCred) ([]string, error) {
	retired, _, err := s.sync(ctx, userID, userUUID, panelID, rules, lines)
	return retired, err
}

// sync is Sync that also says whether the retired clients were retired because
// the user has no node left on the panel (true) or because another client on
// the panel replaced them (false).
func (s *Service) sync(ctx context.Context, userID int64, userUUID string, panelID int64, rules domain.EmailRules, lines []clientplan.LineCred) (retired []string, panelRemoved bool, err error) {
	allExisting, err := s.clients.ListByUser(ctx, userID)
	if err != nil {
		return nil, false, fmt.Errorf("list existing clients: %w", err)
	}
	existing := make([]*domain.PSPClient, 0, len(allExisting))
	stable := make([]clientplan.ExistingClient, 0, len(allExisting))
	for _, c := range allExisting {
		if c.PanelID != panelID {
			continue
		}
		existing = append(existing, c)
		line, _ := c.Line()
		stable = append(stable, clientplan.ExistingClient{ClientID: c.ID, Line: line, UUID: c.UUID, Email: c.Email})
	}
	desired := clientplan.Build(userID, userUUID, panelID, rules, lines, stable, s.uuidTaken(ctx))
	if len(desired) == 0 {
		retired := make([]string, 0, len(existing))
		for _, e := range existing {
			if err := s.clients.SetInbounds(ctx, e.ID, nil); err != nil {
				return retired, true, fmt.Errorf("detach retired client %s: %w", e.Email, err)
			}
			retired = append(retired, e.Email)
		}
		return retired, true, nil
	}

	keep := make(map[int64]struct{}, len(desired))
	for _, d := range desired {
		c := d.Client
		id := c.ID
		if id == 0 {
			id, err = s.clients.Create(ctx, &c)
			if err != nil {
				return nil, false, fmt.Errorf("create psp_client %s: %w", c.Email, err)
			}
		} else if err = s.clients.UpdateDefinition(ctx, &c); err != nil {
			return nil, false, fmt.Errorf("update stable psp_client %d: %w", id, err)
		}
		inbs := make([]domain.PSPClientInbound, len(d.Inbounds))
		for i, in := range d.Inbounds {
			in.ClientID = id
			inbs[i] = in
		}
		if err := s.clients.SetInbounds(ctx, id, inbs); err != nil {
			return nil, false, fmt.Errorf("set inbounds for %s: %w", d.Client.Email, err)
		}
		keep[id] = struct{}{}
	}

	// A line the user no longer has is detached, not deleted: its counters
	// may still hold traffic the poll has not booked. The traffic poll deletes
	// a detached row once a successful read of its server has booked it.
	var dropped []string
	for _, e := range existing {
		if _, ok := keep[e.ID]; ok {
			continue
		}
		if err := s.clients.SetInbounds(ctx, e.ID, nil); err != nil {
			return dropped, false, fmt.Errorf("detach dropped client %s: %w", e.Email, err)
		}
		dropped = append(dropped, e.Email)
	}
	return dropped, false, nil
}

// uuidTaken checks a derived line identity against the other clients on the
// same listener, when the repository can answer that.
func (s *Service) uuidTaken(ctx context.Context) clientplan.UUIDTaken {
	checker, ok := s.clients.(ports.LineUUIDChecker)
	if !ok {
		return nil
	}
	return func(listenerNodeID int64, uuid string, exceptClientID int64) bool {
		taken, err := checker.LineUUIDTaken(ctx, listenerNodeID, domain.LineUUIDKey(uuid), exceptClientID)
		// An unreadable answer is treated as taken: the next attempt is as
		// good an identity, and a duplicate on one listener is not.
		return err != nil || taken
	}
}

func (s *Service) SyncUser(ctx context.Context, userID int64, userUUID string, rules domain.EmailRules, desiredNodes []*domain.Node) (map[int64][]string, error) {
	retired, err := s.SyncUserRetirements(ctx, userID, userUUID, rules, desiredNodes)
	return retired.All(), err
}

// SyncUserRetirements is SyncUser that says, per panel, which retired clients
// went because the user has no node left there and which because another
// client replaced them (domain.RetiredClients).
func (s *Service) SyncUserRetirements(ctx context.Context, userID int64, userUUID string, rules domain.EmailRules, desiredNodes []*domain.Node) (domain.RetiredClients, error) {
	byPanel := map[int64][]*domain.Node{}
	for _, n := range desiredNodes {
		if n == nil || n.Kind == domain.NodeKindSeparator {
			continue
		}
		byPanel[n.PanelID] = append(byPanel[n.PanelID], n)
	}

	// A tunnel line's client lives on the line's own listener, on the entry
	// server, not on the landing's server.
	tunnelLines := clientplan.TunnelLineCreds(desiredNodes)

	// Union the desired panels with the panels the user currently has clients on,
	// so a now-empty panel is visited and retired without deleting its counters.
	panels := make(map[int64]struct{}, len(byPanel)+len(tunnelLines))
	for p := range byPanel {
		panels[p] = struct{}{}
	}
	for p := range tunnelLines {
		panels[p] = struct{}{}
	}
	existing, err := s.clients.ListByUser(ctx, userID)
	if err != nil {
		return domain.RetiredClients{}, fmt.Errorf("list existing clients: %w", err)
	}
	for _, c := range existing {
		panels[c.PanelID] = struct{}{}
	}

	retired := domain.RetiredClients{PanelRemoved: map[int64][]string{}, Regrouped: map[int64][]string{}}
	var firstErr error
	for panelID := range panels {
		creds := clientplan.LineCredsFromNodes(byPanel[panelID]) // empty slice → retires live access
		creds = append(creds, tunnelLines[panelID]...)
		p, removed, serr := s.sync(ctx, userID, userUUID, panelID, rules, creds)
		if len(p) > 0 {
			if removed {
				retired.PanelRemoved[panelID] = p
			} else {
				retired.Regrouped[panelID] = p
			}
		}
		if serr != nil && firstErr == nil {
			firstErr = serr
		}
	}
	return retired, firstErr
}
