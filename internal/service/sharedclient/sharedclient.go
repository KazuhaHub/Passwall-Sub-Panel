// Package sharedclient is the v3.9.0 cutover Stage-1b reconcile service: it
// CREATES the shared client in 3X-UI for a psp_client and confirms the result
// before marking each attachment provisioned. It is the only writer of the
// per-(client,node) Provisioned flag, which render/traffic later consult.
//
// It is additive/dormant: the shared client is created enable=true with no
// expiry/quota (the full lifecycle is wired in Stage 1c, BEFORE any render flip),
// and nothing renders the shared client yet — so a created-but-unmanaged client
// carries no traffic and is harmless. It coexists with the legacy per-node
// clients until Stage 4 removes them.
package sharedclient

import (
	"context"
	"fmt"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/log"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

type Service struct {
	clients ports.PSPClientRepo
	pool    ports.XUIPool
	nodes   ports.NodeRepo
	// ownership is late-bound (SetOwnershipRepo): the migration's DeleteLegacyForUser
	// reads + removes legacy per-node ownership rows. nil = legacy delete is skipped.
	ownership ports.OwnershipRepo
}

func New(clients ports.PSPClientRepo, pool ports.XUIPool, nodes ports.NodeRepo) *Service {
	return &Service{clients: clients, pool: pool, nodes: nodes}
}

// SetOwnershipRepo late-binds the legacy ownership repo the migration uses to find
// and delete a user's per-node clients. Until set, DeleteLegacyForUser is a no-op.
func (s *Service) SetOwnershipRepo(ownership ports.OwnershipRepo) {
	s.ownership = ownership
}

// ProvisionResult summarizes one provisioning pass.
type ProvisionResult struct {
	Created     bool // a 3X-UI client create/attach was issued
	Provisioned int  // attachments confirmed present in 3X-UI and marked
	Skipped     int  // attachments whose node could not be resolved
}

// buildSharedClientSpec maps a psp_client to the 3X-UI client spec, carrying the
// STORED credentials (not derived). One client object holds every protocol's
// field — id (VLESS/VMess), password (Trojan/SS/SS-2022), auth (Hysteria2 = the
// UUID, matching what render emits) — and 3X-UI projects only the relevant field
// into each inbound. Flow is the partition's single effective flow. Enable is
// true; expiry/quota are left 0 — Stage 1c owns the lifecycle.
func buildSharedClientSpec(c *domain.PSPClient, flow string) ports.ClientSpec {
	return ports.ClientSpec{
		ID:       c.UUID,
		Email:    c.Email,
		Enable:   true,
		Flow:     flow,
		Password: c.Password,
		Auth:     c.UUID,
	}
}

// ProvisionClient creates/attaches the shared client for one psp_client across
// all its attached inbounds in a single AddClientToInbounds (one Xray restart),
// reads it back, and marks Provisioned only the attachments 3X-UI confirms.
// Idempotent: AddClient on an existing email re-attaches (3X-UI keys by email),
// so a re-run heals a partial attach.
func (s *Service) ProvisionClient(ctx context.Context, c *domain.PSPClient) (ProvisionResult, error) {
	var res ProvisionResult
	if c == nil {
		return res, nil
	}
	atts, err := s.clients.ListInbounds(ctx, c.ID)
	if err != nil {
		return res, fmt.Errorf("list attachments: %w", err)
	}
	if len(atts) == 0 {
		return res, nil
	}
	flow := atts[0].FlowOverride // uniform across a partition (the key's flow)

	inboundIDs := make([]int, 0, len(atts))
	nodeByInbound := make(map[int]int64, len(atts))
	for _, a := range atts {
		n, err := s.nodes.GetByID(ctx, a.NodeID)
		if err != nil || n == nil {
			log.Warn("sharedclient: resolve node", "client_id", c.ID, "node_id", a.NodeID, "err", err)
			res.Skipped++
			continue
		}
		if n.PanelID != c.PanelID {
			log.Warn("sharedclient: node panel mismatch", "client_id", c.ID, "node_id", a.NodeID,
				"node_panel", n.PanelID, "client_panel", c.PanelID)
			res.Skipped++
			continue
		}
		// Dedupe by inbound: two PSP node rows can map to the SAME (panel, inbound),
		// and passing a duplicate inbound id to AddClientToInbounds is malformed.
		if _, dup := nodeByInbound[n.InboundID]; dup {
			continue
		}
		inboundIDs = append(inboundIDs, n.InboundID)
		nodeByInbound[n.InboundID] = a.NodeID
	}
	if len(inboundIDs) == 0 {
		return res, nil
	}

	cli, err := s.pool.Get(c.PanelID)
	if err != nil {
		return res, fmt.Errorf("xui pool get %d: %w", c.PanelID, err)
	}
	desiredSet := make(map[int]bool, len(inboundIDs))
	for _, id := range inboundIDs {
		desiredSet[id] = true
	}

	// No-op-skip: read the live client FIRST. If it already exists and is attached
	// to EXACTLY the desired inbound set, AddClientToInbounds would be a no-op that
	// still triggers an Xray restart — skip it. This restores the legacy per-node
	// clientUnchanged behaviour: a steady-state resync (group re-tag, profile edit
	// with no node delta) costs 0 restarts. Credentials are (re)pushed separately
	// by SyncLifecycle, so skipping the attach never leaves stale creds — a UUID
	// reset keeps the same attachment (skipped here) but differs in lifecycle/creds
	// (pushed there). A nil read (absent client / transient error) falls through to
	// the attach path, which is idempotent.
	if cur, _ := cli.GetClient(ctx, c.Email); cur != nil && sameInboundSet(cur.InboundIDs, desiredSet) {
		for _, nodeID := range nodeByInbound {
			if err := s.clients.MarkInboundProvisioned(ctx, c.ID, nodeID, true); err != nil {
				log.Warn("sharedclient: mark provisioned", "client_id", c.ID, "node_id", nodeID, "err", err)
				continue
			}
			res.Provisioned++
		}
		return res, nil
	}

	if err := cli.AddClientToInbounds(ctx, inboundIDs, buildSharedClientSpec(c, flow)); err != nil {
		return res, fmt.Errorf("create shared client %s: %w", c.Email, err)
	}
	res.Created = true

	// Read-back: only mark Provisioned the inbounds 3X-UI actually confirms the
	// client is attached to (the gate render/traffic trust — never "we asked").
	detail, err := cli.GetClient(ctx, c.Email)
	if err != nil {
		return res, fmt.Errorf("confirm shared client %s: %w", c.Email, err)
	}
	if detail == nil {
		return res, fmt.Errorf("shared client %s absent after create", c.Email)
	}
	// Full reconcile, not just attach: detach the client from any inbound it is
	// attached to in 3X-UI but no longer desired (a node left the user's group).
	// Without this a removed node would keep serving the user until a manual fix.
	var stale []int
	for _, id := range detail.InboundIDs {
		if !desiredSet[id] {
			stale = append(stale, id)
		}
	}
	if len(stale) > 0 {
		if err := cli.DetachClient(ctx, c.Email, stale); err != nil {
			log.Warn("sharedclient: detach stale inbounds", "client_id", c.ID, "email", c.Email, "inbounds", stale, "err", err)
		}
	}

	confirmed := make(map[int]bool, len(detail.InboundIDs))
	for _, id := range detail.InboundIDs {
		confirmed[id] = true
	}
	for inb, nodeID := range nodeByInbound {
		if !confirmed[inb] {
			continue
		}
		if err := s.clients.MarkInboundProvisioned(ctx, c.ID, nodeID, true); err != nil {
			log.Warn("sharedclient: mark provisioned", "client_id", c.ID, "node_id", nodeID, "err", err)
			continue
		}
		res.Provisioned++
	}
	return res, nil
}

// SyncLifecycle pushes the user's current enable / expiry / quota-floor onto the
// shared client in 3X-UI (UpdateClient by email — propagates to every inbound the
// client is attached to). This is HOLE #1: without it, a disabled / expired /
// over-quota user whose subs render the shared client would keep working because
// only the legacy per-node clients get toggled. UpdateClient is full-replace, so
// the stored creds + the partition's flow are re-sent unchanged. A client with no
// attachments (hence no flow) is skipped.
func (s *Service) SyncLifecycle(ctx context.Context, c *domain.PSPClient, enable bool, expiryTime, totalGB int64) error {
	if c == nil {
		return nil
	}
	atts, err := s.clients.ListInbounds(ctx, c.ID)
	if err != nil {
		return fmt.Errorf("list attachments: %w", err)
	}
	// Only push once the shared client actually EXISTS in 3X-UI — i.e. at least
	// one attachment is confirmed Provisioned by the reconcile read-back. Before
	// provisioning (the default on every install where the operator hasn't run the
	// cutover, yet the shadow dual-write has already created psp_client rows +
	// attachments), the client's email is unknown to 3X-UI, so an UpdateClient
	// would fail on every resync / enable / disable and spam non-fatal warnings +
	// waste a 3X-UI round-trip. Skip until reconcile has confirmed an attach; the
	// cutover runbook provisions BEFORE the gate flip, so lifecycle is in lockstep
	// exactly from the moment the shared client is live.
	flow := ""
	provisioned := false
	for _, a := range atts {
		if a.Provisioned {
			provisioned, flow = true, a.FlowOverride // uniform flow across the partition
			break
		}
	}
	if !provisioned {
		return nil
	}
	cli, err := s.pool.Get(c.PanelID)
	if err != nil {
		return fmt.Errorf("xui pool get %d: %w", c.PanelID, err)
	}
	spec := buildSharedClientSpec(c, flow)
	spec.Enable = enable
	spec.ExpiryTime = expiryTime
	spec.TotalGB = totalGB
	// No-op-skip: if 3X-UI already holds this exact lifecycle AND creds, skip the
	// UpdateClient. ResyncMembership calls this on every resync and the traffic poll
	// calls it every cycle for active users; without the skip an unchanged user
	// would issue a redundant full-replace each time. Creds are compared too, so a
	// UUID reset (id/password differ) still propagates; an active user's shrinking
	// quota-floor (totalGB differs) still refreshes the Xray-side cap.
	if cur, err := cli.GetClient(ctx, c.Email); err == nil && cur != nil &&
		cur.Enable == spec.Enable && cur.ExpiryTime == spec.ExpiryTime && cur.TotalGB == spec.TotalGB &&
		cur.ID == spec.ID && cur.Password == spec.Password && cur.Flow == spec.Flow && cur.Auth == spec.Auth {
		return nil
	}
	return cli.UpdateClient(ctx, 0, c.UUID, spec) // inbound/uuid args vestigial; keyed by spec.Email
}

// sameInboundSet reports whether the live attachment set equals the desired set
// (used by the provision no-op-skip to avoid a needless Xray-restarting re-add).
func sameInboundSet(have []int, want map[int]bool) bool {
	if len(have) != len(want) {
		return false
	}
	for _, id := range have {
		if !want[id] {
			return false
		}
	}
	return true
}

// SyncUserLifecycle pushes the given lifecycle state onto ALL of a user's shared
// clients (across panels/partitions). enable/expiry/quota are user-level, so they
// apply identically to every client. Returns the first error, attempts all.
func (s *Service) SyncUserLifecycle(ctx context.Context, userID int64, enable bool, expiryTime, totalGB int64) error {
	clients, err := s.clients.ListByUser(ctx, userID)
	if err != nil {
		return fmt.Errorf("list clients: %w", err)
	}
	var firstErr error
	for _, c := range clients {
		if err := s.SyncLifecycle(ctx, c, enable, expiryTime, totalGB); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// CleanupResult summarizes a Stage-4 legacy-cleanup pass.
type CleanupResult struct {
	Deleted int // legacy per-node clients removed from 3X-UI + ownership
	Kept    int // ownership rows whose node isn't provisioned under a shared client (fallback still needed)
	Skipped int // delete attempted but failed (panel unreachable, etc.)
}

type panelInbound struct {
	panel   int64
	inbound int
}

// MigrateResult summarizes one user's full migration to the shared-client model.
type MigrateResult struct {
	Provisioned int // shared-client attachments confirmed in 3X-UI
	Deleted     int // legacy per-node clients removed from 3X-UI + ownership
	Skipped     int // provision/delete steps that failed (retried by the sync-task queue)
}

// MigrateUser is the V3-transitional one-shot: it fully migrates ONE user to the
// shared-client model — provision the shared client(s) in 3X-UI (silent: the
// stored creds are byte-identical to what render already emits), confirm the
// attach, then delete that user's legacy per-node clients. Order is failure-safe:
// if provisioning fails, the per-node clients are LEFT intact (the user keeps
// working) and the sync-task queue retries. Idempotent — a re-run re-provisions
// (no-op if present) and deletes any per-node clients now covered.
//
// V3-ONLY: this drives the upgrade migration and is removed at V4 (by then every
// install is on the shared model and there are no per-node clients to delete).
func (s *Service) MigrateUser(ctx context.Context, userID int64) (MigrateResult, error) {
	pr, err := s.ProvisionUser(ctx, userID)
	if err != nil {
		// Provision failed → do NOT touch the per-node clients; the user keeps
		// working on them and the task retries.
		return MigrateResult{Skipped: pr.Skipped}, fmt.Errorf("provision: %w", err)
	}
	cr, err := s.DeleteLegacyForUser(ctx, userID)
	res := MigrateResult{Provisioned: pr.Provisioned, Deleted: cr.Deleted, Skipped: pr.Skipped + cr.Skipped}
	if err != nil {
		return res, fmt.Errorf("delete legacy: %w", err)
	}
	return res, nil
}

// DeleteLegacyForUser is the gate-free core: delete every legacy per-node client
// whose (panel, inbound) is now served by a CONFIRMED-provisioned shared client,
// plus its ownership row. Nodes not yet provisioned under a shared client are
// KEPT (render still falls back to them), so a partial migration never strands a
// user. Idempotent.
func (s *Service) DeleteLegacyForUser(ctx context.Context, userID int64) (CleanupResult, error) {
	var res CleanupResult
	if s.ownership == nil {
		return res, fmt.Errorf("ownership repo not wired")
	}

	// Which (panel, inbound) pairs are now served by a PROVISIONED shared client?
	provisioned := map[panelInbound]bool{}
	clients, err := s.clients.ListByUser(ctx, userID)
	if err != nil {
		return res, fmt.Errorf("list clients: %w", err)
	}
	for _, c := range clients {
		atts, err := s.clients.ListInbounds(ctx, c.ID)
		if err != nil {
			return res, fmt.Errorf("list attachments: %w", err)
		}
		for _, a := range atts {
			if !a.Provisioned {
				continue
			}
			n, err := s.nodes.GetByID(ctx, a.NodeID)
			if err != nil || n == nil {
				continue
			}
			provisioned[panelInbound{n.PanelID, n.InboundID}] = true
		}
	}

	entries, err := s.ownership.ListByUser(ctx, userID)
	if err != nil {
		return res, fmt.Errorf("list ownership: %w", err)
	}
	// Group the deletable legacy clients by panel and remove them with ONE
	// BulkDelByEmail per panel — one Xray restart per panel instead of one per
	// client. Legacy emails are u{uid}-n{nodeID}@domain: panel-wide unique and
	// DISTINCT from the shared client's u{uid}[-k…]@domain, so a panel-wide
	// (email-keyed) delete can never touch the just-provisioned shared client.
	type delRow struct {
		entryID int64
		email   string
	}
	byPanel := map[int64][]delRow{}
	for _, e := range entries {
		if !provisioned[panelInbound{e.PanelID, e.InboundID}] {
			res.Kept++ // no provisioned shared replacement yet → keep the fallback
			continue
		}
		byPanel[e.PanelID] = append(byPanel[e.PanelID], delRow{e.ID, e.ClientEmail})
	}
	for panelID, rows := range byPanel {
		cli, err := s.pool.Get(panelID)
		if err != nil {
			log.Warn("sharedclient cleanup: pool get", "panel_id", panelID, "err", err)
			res.Skipped += len(rows)
			continue
		}
		emails := make([]string, len(rows))
		for i, r := range rows {
			emails[i] = r.email
		}
		if _, err := cli.BulkDelByEmail(ctx, emails); err != nil {
			log.Warn("sharedclient cleanup: bulk delete legacy clients", "panel_id", panelID,
				"count", len(emails), "err", err)
			res.Skipped += len(rows)
			continue
		}
		// Bulk delete succeeded (emails already absent upstream are silently
		// skipped — still effectively gone). Drop each ownership row + count.
		for _, r := range rows {
			if err := s.ownership.Remove(ctx, r.entryID); err != nil {
				// 3X-UI delete succeeded; the stale ownership row is harmless and the
				// next reconcile drops it. Don't double-count as deleted-but-tracked.
				log.Warn("sharedclient cleanup: remove ownership row", "id", r.entryID, "err", err)
			}
			res.Deleted++
		}
	}
	return res, nil
}

// ProvisionUser provisions every shared client a user holds (across panels).
// Returns the first error but attempts all clients.
func (s *Service) ProvisionUser(ctx context.Context, userID int64) (ProvisionResult, error) {
	clients, err := s.clients.ListByUser(ctx, userID)
	if err != nil {
		return ProvisionResult{}, fmt.Errorf("list clients: %w", err)
	}
	var total ProvisionResult
	var firstErr error
	for _, c := range clients {
		r, err := s.ProvisionClient(ctx, c)
		if err != nil && firstErr == nil {
			firstErr = err
		}
		if r.Created {
			total.Created = true
		}
		total.Provisioned += r.Provisioned
		total.Skipped += r.Skipped
	}
	return total, firstErr
}
