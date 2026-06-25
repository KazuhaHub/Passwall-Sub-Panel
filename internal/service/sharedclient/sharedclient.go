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
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/clientplan"
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
// all its attached inbounds, reads it back, and marks Provisioned only the
// attachments 3X-UI confirms. A brand-new client is created in one
// AddClientToInbounds (one Xray restart); an existing client whose inbound set
// drifted is converged with the idempotent AttachClient — AddClientToInbounds
// would re-create it and 3X-UI rejects "email already in use" on inbounds it is
// already attached to. Idempotent: a re-run heals a partial attach.
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
	cur, _ := cli.GetClient(ctx, c.Email)
	if cur != nil && sameInboundSet(cur.InboundIDs, desiredSet) {
		for _, nodeID := range nodeByInbound {
			if err := s.clients.MarkInboundProvisioned(ctx, c.ID, nodeID, true); err != nil {
				log.Warn("sharedclient: mark provisioned", "client_id", c.ID, "node_id", nodeID, "err", err)
				continue
			}
			res.Provisioned++
		}
		return res, nil
	}

	if cur == nil {
		// Brand-new client: create it attached to every desired inbound in one POST.
		if err := cli.AddClientToInbounds(ctx, inboundIDs, buildSharedClientSpec(c, flow)); err != nil {
			return res, fmt.Errorf("create shared client %s: %w", c.Email, err)
		}
	} else {
		// Client already exists but on a DIFFERENT inbound set — the steady state of
		// the v3.9.0 merge: a user's per-class email gets REUSED as the merged email
		// (e.g. the VLESS-vision client's u…-kf… email when the panel has no SS-2022)
		// and now needs MORE inbounds than it has — those of the per-class clients
		// being collapsed into it. A blanket AddClientToInbounds re-CREATES the client
		// on inbounds it is already attached to and 3X-UI rejects the whole call
		// ("email already in use"). AttachClient is idempotent — it no-ops inbounds
		// already attached and attaches the rest. Because the email is a pure function
		// of (password-class, flow), a reused email carries the IDENTICAL credentials,
		// so no spec push is needed here; stale inbounds are detached by the read-back
		// reconcile below. (Bug + fix verified live on 3X-UI 3.4.0.)
		if err := cli.AttachClient(ctx, c.Email, inboundIDs); err != nil {
			return res, fmt.Errorf("attach shared client %s: %w", c.Email, err)
		}
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

// MIGRATION(v3→v4): the per-user migration is driven by user.ResyncMembership
// (provision → lifecycle push → delete legacy), NOT a standalone one-shot here.
// The earlier MigrateUser helper (provision → delete legacy, with NO lifecycle
// push between) was superseded by that safer ordering and removed — reviving it
// would reopen the audit-#1 enforcement bypass (a disabled/expired user left with
// a fully-enabled shared client and no fallback during the push-failure window).
// At V4, DeleteLegacyForUser + the ownership dependency below go away entirely.

// DeleteLegacyForUser is the gate-free core: delete every legacy per-node client
// whose (panel, inbound) is now served by a CONFIRMED-provisioned shared client,
// plus its ownership row. Nodes not yet provisioned under a shared client are
// KEPT (render still falls back to them), so a partial migration never strands a
// user. Idempotent.
// MIGRATION(v3→v4): one-time teardown of a user's legacy per-node clients after
// the shared client is provisioned — delete with the legacy ownership path.
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

// BulkProvisionNodeInbound front-loads the shared-client creates for many users
// onto ONE node's inbound in a single bulkCreate + single bulkAttach (one Xray
// restart total), instead of one AddClientToInbounds per user. It is a best-effort
// WARM-UP for node provisioning / outage heal: the caller's per-user resync still
// runs as the authoritative pass (confirm + mark provisioned + lifecycle + orphan),
// so anything this misses is corrected there. It partitions members against ONE
// panel-wide client list — emails already present are attached, the rest created —
// and reuses buildSharedClientSpec so a created client is byte-for-byte what
// ProvisionClient would have made (no credential drift, hence safe to overlap the
// resync). Only psp_clients on n's panel that actually attach to n are touched.
func (s *Service) BulkProvisionNodeInbound(ctx context.Context, n *domain.Node, userIDs []int64) error {
	if n == nil || n.InboundID == 0 || len(userIDs) == 0 {
		return nil
	}
	cli, err := s.pool.Get(n.PanelID)
	if err != nil {
		return err
	}
	live, err := cli.ListClientInbounds(ctx)
	if err != nil {
		return err
	}
	// The per-user ListByUser + per-client ListInbounds below are an O(members)
	// DB-side N+1. Left deliberately un-batched: this is a one-shot, best-effort
	// warm-up (the authoritative path is the per-member ResyncMembership backstop
	// the caller runs right after), it runs only on an admin-triggered node
	// provision/recreate, and these are sub-ms LOCAL reads. The expensive half —
	// 3X-UI writes / Xray restarts — IS collapsed below to one BulkCreate + one
	// BulkAttach (the whole point of this method). Batching the reads would mean two
	// new repo methods (ListByUsers + ListInboundsForClients) + adapter tests for
	// ~100ms-once on a cold path; not worth the interface surface. Revisit only if
	// a node ever fans out to thousands of members on a remote DB.
	var createItems []ports.BulkCreateClientItem
	var attachEmails []string
	for _, uid := range userIDs {
		clients, err := s.clients.ListByUser(ctx, uid)
		if err != nil {
			return err
		}
		for _, c := range clients {
			if c.PanelID != n.PanelID {
				continue
			}
			atts, err := s.clients.ListInbounds(ctx, c.ID)
			if err != nil {
				return err
			}
			attachesN := false
			for _, a := range atts {
				if a.NodeID == n.ID {
					attachesN = true
					break
				}
			}
			if !attachesN {
				continue // this partition doesn't serve n (tag filter / different flow class)
			}
			if _, present := live[c.Email]; present {
				attachEmails = append(attachEmails, c.Email)
			} else {
				createItems = append(createItems, ports.BulkCreateClientItem{
					Spec:       buildSharedClientSpec(c, atts[0].FlowOverride), // partition flow is uniform
					InboundIDs: []int{n.InboundID},
				})
			}
		}
	}
	if len(createItems) > 0 {
		if _, err := cli.BulkCreateClients(ctx, createItems); err != nil {
			return fmt.Errorf("bulk create node members: %w", err)
		}
	}
	if len(attachEmails) > 0 {
		if _, err := cli.BulkAttach(ctx, attachEmails, []int{n.InboundID}); err != nil {
			return fmt.Errorf("bulk attach node members: %w", err)
		}
	}
	log.Info("bulk-provisioned node members", "node_id", n.ID, "panel_id", n.PanelID, "created", len(createItems), "attached", len(attachEmails))
	return nil
}

// ReconcileOrphans deletes a user's STALE shared clients: 3X-UI clients that match
// PSP's shared-client email scheme for the user but are NOT in the user's current
// desired psp_client set. They arise when the v3.9.0 merge re-keys a user (collapsing
// per-class emails into one) but the old 3X-UI clients are never deleted — e.g. the
// prune-delete was skipped because ANOTHER panel was down, after which the DB no
// longer tracks them (a permanently-untracked orphan). It DISCOVERS them by listing
// each panel's live clients (robust to email-suffix and domain drift, which a
// reconstruct-the-email sweep is not), and is gated PER PANEL on coverage: a stale
// client is deleted only when EVERY inbound it serves is also served by a
// confirmed-live desired client on that panel. So one panel being down never blocks
// cleanup on a healthy panel, and a user never loses access on an inbound the
// replacement has not covered yet (those are retried on the next pass).
//
// It only ever deletes emails matching clientplan.IsSharedClientEmail, which by
// construction excludes the legacy per-NODE fallback (u{id}-n{nodeID}@) — that is
// owned by DeleteLegacyForUser. Deleting a stale shared client is enforcement-safe:
// the lifecycle-managed desired client carries the user's real enable/expiry, while
// a stale client is unmanaged, so removing it can only tighten enforcement.
func (s *Service) ReconcileOrphans(ctx context.Context, userID int64) error {
	clients, err := s.clients.ListByUser(ctx, userID)
	if err != nil {
		return fmt.Errorf("list clients: %w", err)
	}
	if len(clients) == 0 {
		return nil // no desired client anywhere → no coverage authorises any delete
	}
	desiredByPanel := map[int64]map[string]struct{}{}
	for _, c := range clients {
		if desiredByPanel[c.PanelID] == nil {
			desiredByPanel[c.PanelID] = map[string]struct{}{}
		}
		desiredByPanel[c.PanelID][c.Email] = struct{}{}
	}

	var firstErr error
	noteErr := func(e error) {
		if e != nil && firstErr == nil {
			firstErr = e
		}
	}
	for panelID, desired := range desiredByPanel {
		cli, err := s.pool.Get(panelID)
		if err != nil {
			noteErr(err)
			continue
		}
		// ONE panel-wide read drives BOTH the coverage gate and the stale-client
		// scan: ListClientInbounds returns every live client's inbound set, so a
		// busy panel costs a single call instead of one GetClient per desired email.
		live, lerr := cli.ListClientInbounds(ctx)
		if lerr != nil {
			noteErr(lerr)
			continue
		}
		// Coverage gate: confirm every desired client is live + attached, and gather
		// the inbounds the desired clients actually cover. If any is missing/unattached
		// the replacement isn't fully up — skip this panel and retry next pass.
		covered := map[int]struct{}{}
		allUp := true
		for email := range desired {
			inbs := live[email]
			if len(inbs) == 0 {
				allUp = false
				break
			}
			for _, ib := range inbs {
				covered[ib] = struct{}{}
			}
		}
		if !allUp {
			continue
		}
		for email, inbounds := range live {
			if _, isDesired := desired[email]; isDesired {
				continue
			}
			if !clientplan.IsSharedClientEmail(email, userID) {
				continue // operator client, another user, or the legacy -n{node} fallback
			}
			if !inboundsCovered(inbounds, covered) {
				continue // a desired client doesn't (yet) serve one of this client's inbounds
			}
			if err := cli.DelClientByEmail(ctx, 0, email); err != nil {
				log.Warn("orphan reconcile: delete stale shared client", "panel_id", panelID, "email", email, "user_id", userID, "err", err)
				noteErr(err)
				continue
			}
			log.Info("orphan reconcile: deleted stale shared client", "panel_id", panelID, "email", email, "user_id", userID)
		}
	}
	return firstErr
}

// DeleteSharedForUser tears down ALL of a user's shared clients — used by the
// user-delete path, which otherwise (post-migration, ownership empty) would leave
// the shared 3X-UI client u{uid}@ live and ENABLED on every panel, so a deleted
// user keeps authenticating with their UUID-derived creds. For each panel it
// BulkDelByEmail's the user's client emails (one call → one Xray restart per panel),
// then drops the psp_client rows (DeleteByEmail cascades psp_client_inbounds). It
// returns the first error; on a 3X-UI failure for a panel it leaves that panel's DB
// rows so the caller's durable retry re-lists and re-attempts. The caller MUST run
// this BEFORE deleting the user row — there is no FK cascade from users to
// psp_client, so once the user row is gone the rows are unreachable by userID.
func (s *Service) DeleteSharedForUser(ctx context.Context, userID int64) error {
	clients, err := s.clients.ListByUser(ctx, userID)
	if err != nil {
		return fmt.Errorf("list clients: %w", err)
	}
	byPanel := map[int64][]string{}
	for _, c := range clients {
		byPanel[c.PanelID] = append(byPanel[c.PanelID], c.Email)
	}
	var firstErr error
	for panelID, emails := range byPanel {
		cli, err := s.pool.Get(panelID)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if _, err := cli.BulkDelByEmail(ctx, emails); err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("bulk delete shared clients on panel %d: %w", panelID, err)
			}
			continue // keep the DB rows for retry — don't orphan the 3X-UI clients
		}
		for _, email := range emails {
			if err := s.clients.DeleteByEmail(ctx, panelID, email); err != nil && firstErr == nil {
				firstErr = fmt.Errorf("drop psp_client row %s: %w", email, err)
			}
		}
		log.Info("deleted shared clients for user", "user_id", userID, "panel_id", panelID, "count", len(emails))
	}
	return firstErr
}

func inboundsCovered(inbounds []int, covered map[int]struct{}) bool {
	for _, ib := range inbounds {
		if _, ok := covered[ib]; !ok {
			return false
		}
	}
	return true
}
