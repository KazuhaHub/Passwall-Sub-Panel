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
	"runtime/debug"
	"sync"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/clientplan"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/log"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/metrics"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/paneltz"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// capGapKey dedupes the capability-gap warning per (panel, capability).
type capGapKey struct {
	panelID    int64
	capability ports.PanelCapability
}

type Service struct {
	// settings is optional (WithSettings). When absent the fan-out below falls
	// back to the same default every other 3X-UI fan-out resolves to.
	settings ports.ScopedSettings

	// capGapSeen keeps the capability-gap warning to once per (panel,
	// capability) per process. sync.Map because the push path fans out
	// concurrently and this is a write-once-read-many set.
	capGapSeen sync.Map

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
	flow := "" // uniform across a partition (the key's flow); set by the first usable attachment

	inboundIDs := make([]int, 0, len(atts))
	nodeByInbound := make(map[int]int64, len(atts))
	flowSet := false
	for _, a := range atts {
		n, err := s.nodes.GetByID(ctx, a.NodeID)
		if err != nil || n == nil {
			log.Warn("sharedclient: resolve node", "client_id", c.ID, "node_id", a.NodeID, "err", err)
			res.Skipped++
			continue
		}
		if n.IsSeparator() || !n.Enabled {
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
		if !flowSet {
			flow = a.FlowOverride
			flowSet = true
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

// want.QuotaHeadroom is bytes remaining IN THE CURRENT PERIOD, not the value
// that reaches the panel: the panel compares against a lifetime counter, so it
// is rebased onto that counter below.
//
// SyncLifecycle pushes the user's current enable / expiry / quota-floor onto the
// shared client in 3X-UI (UpdateClient by email — propagates to every inbound the
// client is attached to). This is HOLE #1: without it, a disabled / expired /
// over-quota user whose subs render the shared client would keep working because
// only the legacy per-node clients get toggled. UpdateClient is full-replace, so
// the stored creds + the partition's flow are re-sent unchanged. A client with no
// attachments (hence no flow) is skipped.
func (s *Service) SyncLifecycle(ctx context.Context, c *domain.PSPClient, want domain.UserLifecycle) error {
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
		metrics.LifecycleNotProvisionedTotal.Inc()
		return nil
	}
	// Counted from here rather than at function entry, so the denominator is
	// "calls that reached the compare-then-write decision". Exactly one of
	// skipped / write follows, and those two sum to this total; error is a
	// SUB-count that overlaps write (a failed UpdateClient is both) and also
	// covers the pool failure below, which reaches neither. Skip rate is
	// skipped/total; do not try to reconcile all three against the total.
	metrics.LifecycleTotal.Inc()
	cli, err := s.pool.Get(c.PanelID)
	if err != nil {
		metrics.LifecycleErrorTotal.Inc()
		return fmt.Errorf("xui pool get %d: %w", c.PanelID, err)
	}
	s.reportCapabilityGaps(cli, c.PanelID, want)

	spec := buildSharedClientSpec(c, flow)
	spec.Enable = want.Enable
	spec.ExpiryTime = want.ExpiryTime
	spec.LimitIP = want.IPLimit
	spec.LimitHwid = want.DeviceLimit
	// QuotaHeadroom is period-relative; the panel enforces against its own
	// never-reset lifetime counter. domain.PanelQuotaCap bridges the two —
	// see docs/traffic-floor-defect.md for what pushing the raw headroom did.
	spec.TotalGB = c.PanelQuotaCap(want.QuotaHeadroom)
	// No-op-skip: if 3X-UI already holds this exact lifecycle AND creds, skip the
	// UpdateClient. ResyncMembership calls this on every resync and the traffic poll
	// calls it every cycle for active users; without the skip an unchanged user
	// would issue a redundant full-replace each time. Creds are compared too, so a
	// UUID reset (id/password differ) still propagates; an active user's shrinking
	// quota-floor (totalGB differs) still refreshes the Xray-side cap.
	//
	// Which caps the panel can actually store. A field it silently drops reads
	// back as 0 forever, so letting it into the comparison below would mean the
	// skip can NEVER fire: every cycle would see a difference, issue a
	// full-replace, and restart the core — for a value that cannot converge.
	// The gap is already reported above; it must not also become a write loop.
	capIP := ports.SupportsCapability(cli, ports.CapabilityClientIPLimit)
	capDevice := ports.SupportsCapability(cli, ports.CapabilityClientDeviceLimit)

	unreadReason := "panel_unread"
	if cur, err := cli.GetClient(ctx, c.Email); err == nil && cur != nil {
		// Hand the operator's 3X-UI note straight back. UpdateClient is a
		// full-replace and upstream writes clients.comment unconditionally, so
		// without this the note is blanked on every push — which for an active
		// user is every traffic-poll cycle, because the shrinking quota floor
		// keeps defeating the skip below. Free here: this GetClient already
		// runs. Not carried on the legacy per-node paths, which have no read to
		// ride and are slated for removal with the ownership model.
		spec.Comment = cur.Comment
		spec.Group = cur.Group
		// Sample the quota drift on EVERY compare, skip or not: the
		// distribution a Phase 1a deadband has to be sized against is the
		// drift the deadband would have to swallow, which includes the
		// cycles where the floor barely moved.
		metrics.LifecycleQuotaDeltaBytes.Observe(absInt64(cur.TotalGB - spec.TotalGB))
		// The band's yield, counted separately from the skip: a difference
		// the band absorbed. Without this, a rising skip rate cannot be
		// attributed — it could equally be quieter users. Counted even when
		// another field goes on to force a write, because what is being
		// measured is absorbed quota drift, not elided calls.
		if cur.TotalGB != spec.TotalGB &&
			domain.PanelQuotaWithinBand(cur.TotalGB, spec.TotalGB, want.QuotaHeadroom) {
			metrics.LifecycleQuotaBandSkipTotal.Inc()
		}
		// The write REASON is the load-bearing measurement of Phase 0:
		// docs/data-plane-plan.md §1.2 claims the shrinking quota floor
		// defeats this skip on every cycle for every active user. A
		// breakdown dominated by "total_gb" confirms that, and with it
		// that a deadband on that one field recovers the skip. Any other
		// reason showing up in bulk would mean a deadband buys nothing
		// and Phase 1a is aimed at the wrong field.
		reason := lifecycleWriteReason(cur, spec, capIP, capDevice, want.QuotaHeadroom)
		if reason == "" {
			metrics.LifecycleSkippedTotal.Inc()
			return nil
		}
		metrics.LifecycleWriteReasonTotal.With(reason).Inc()
		unreadReason = ""
	}
	if unreadReason != "" {
		// GetClient failed or reported the client absent, so the skip could not
		// be evaluated. Labelling it keeps sum(write_reason) == write_total,
		// which otherwise silently under-counts exactly the flaky panels.
		metrics.LifecycleWriteReasonTotal.With(unreadReason).Inc()
	}
	metrics.LifecycleWriteTotal.Inc()
	if err := cli.UpdateClient(ctx, spec); err != nil {
		metrics.LifecycleErrorTotal.Inc()
		return err
	}
	return nil
}

// WithSettings attaches the settings repo so the per-user fan-out honours the
// admin's MaxPanelConcurrency. Optional, and returns the service for chaining
// at construction sites — same shape as traffic.Service.WithSettings.
func (s *Service) WithSettings(settings ports.ScopedSettings) *Service {
	s.settings = settings
	return s
}

// panelConcurrency resolves the per-user fan-out width.
//
// It reads the SAME admin setting as the traffic poll, reconcile and
// pushClientConfigToAll, because that setting's whole promise is that moving
// one slider bounds every 3X-UI fan-out. Hard-coding the default here would
// break that promise in the least visible way possible: an operator who lowers
// the cap to protect a weak VPS would still see this path fan out eight wide.
//
// Note this nests inside the poll's pushSem, so the real ceiling a panel sees
// is the product. Honouring the setting at least keeps both factors under the
// operator's control.
func (s *Service) panelConcurrency(ctx context.Context) int {
	if s.settings == nil {
		return paneltz.ResolveMaxPanelConcurrency(0)
	}
	cfg, err := s.settings.Load(ctx, ports.UISettings{})
	if err != nil {
		return paneltz.ResolveMaxPanelConcurrency(0)
	}
	return paneltz.ResolveMaxPanelConcurrency(cfg.MaxPanelConcurrency)
}

// reportCapabilityGaps notices when PSP is about to push a setting the panel in
// front of it cannot enforce, and says so once instead of silently succeeding.
//
// This is the enforcement half of the design stance in docs/connection-limits.md:
// PSP's domain model is the source of truth and adapters translate what their
// panel can express, so a gap has to be VISIBLE. An S-UI panel has no concept of
// either cap, and a 3X-UI below 3.7.0 ignores the device one — in both cases the
// write succeeds and enforces nothing, which is exactly the shape of failure the
// capability list exists to prevent.
//
// The counter is the durable signal; the log line fires once per (panel,
// capability) per process because the condition is steady-state — it persists
// until the operator moves the user or upgrades the panel, so logging it every
// cycle for every affected client would bury everything else.
func (s *Service) reportCapabilityGaps(cli ports.XUIClient, panelID int64, want domain.UserLifecycle) {
	check := func(set bool, capability ports.PanelCapability, field string) {
		if !set || ports.SupportsCapability(cli, capability) {
			return
		}
		metrics.CapabilityGapTotal.With(string(capability)).Inc()
		if _, seen := s.capGapSeen.LoadOrStore(capGapKey{panelID, capability}, struct{}{}); seen {
			return
		}
		log.Warn("panel cannot enforce a setting PSP is pushing; the write will succeed and do nothing",
			"panel_id", panelID, "capability", string(capability), "field", field)
	}
	check(want.IPLimit > 0, ports.CapabilityClientIPLimit, "ip_limit")
	check(want.DeviceLimit > 0, ports.CapabilityClientDeviceLimit, "device_limit")
}

// lifecycleWriteReason names the first field on which the panel's stored
// client differs from what PSP intends to push, or "" when they match and
// the no-op skip fires.
//
// Field order is deliberate: totalGB is checked FIRST because it is the
// suspected reason a skip essentially never fires, and a reason breakdown
// is only useful if the suspect cannot be masked by a field checked ahead
// of it. The remaining order is arbitrary — a call that differs in two
// fields is attributed to whichever comes first, which is fine for a
// measurement whose question is "which single field dominates".
//
// The comparison set is kept identical to the skip condition it replaced;
// adding a field here silently makes the skip stricter.
// capIP / capDevice say whether the panel can STORE each cap. A panel that
// silently drops one reads it back as 0 forever, so including it here would
// make the skip structurally impossible to hit: every cycle would see a
// difference and issue a full-replace that restarts the core, for a value that
// can never converge. Excluding it is not "ignoring drift" — there is no drift
// to heal on a panel that has nowhere to put the value, and the gap is already
// counted by reportCapabilityGaps.
func lifecycleWriteReason(cur *ports.ClientDetail, spec ports.ClientSpec, capIP, capDevice bool, headroom int64) string {
	switch {
	case !domain.PanelQuotaWithinBand(cur.TotalGB, spec.TotalGB, headroom):
		return "total_gb"
	case capIP && cur.LimitIP != spec.LimitIP:
		return "ip_limit"
	case capDevice && cur.LimitHwid != spec.LimitHwid:
		return "device_limit"
	case cur.Enable != spec.Enable:
		return "enable"
	case cur.ExpiryTime != spec.ExpiryTime:
		return "expiry"
	case cur.ID != spec.ID:
		return "id"
	case cur.Password != spec.Password:
		return "password"
	case cur.Flow != spec.Flow:
		return "flow"
	case cur.Auth != spec.Auth:
		return "auth"
	}
	return ""
}

// absInt64 returns |v| as a float64 for histogram observation. The floor
// can move in either direction (usage grows; an admin raises the cap), and
// a deadband is sized on magnitude, not sign.
func absInt64(v int64) float64 {
	if v < 0 {
		return float64(-v)
	}
	return float64(v)
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
// apply identically to every client — want.QuotaHeadroom is the user's
// remaining period bytes, which each client rebases onto its OWN panel-side
// counter. Returns the first error, attempts all.
//
// The per-client work runs concurrently (docs/data-plane-plan.md Phase 1b).
// Each SyncLifecycle is a GetClient plus an UpdateClient, so the serial loop
// this replaced cost the user P x 2 round trips end to end; concurrently it is
// 2, which for a user spread across several panels is the difference between
// "one slow panel delays only its own client" and "one slow panel delays
// everything after it in the list".
//
// Safe to run in parallel because no two entries in one user's list can name
// the same panel-side client. A PSPClient is keyed by (user, panel, credClass),
// and domain.PSPClientEmail derives the email suffix as a collision-free
// function of the partition key — so two clients on the SAME panel necessarily
// carry different emails. The dangerous shape (two goroutines writing one
// panel-side client, racing the adapter's per-email write lock into a
// last-writer-wins on a full-replace body) is therefore unreachable from here.
// The pool hands back a shared adapter per panel, which is already driven
// concurrently by the traffic poll's own panel fan-out.
func (s *Service) SyncUserLifecycle(ctx context.Context, userID int64, want domain.UserLifecycle) error {
	started := time.Now()
	clients, err := s.clients.ListByUser(ctx, userID)
	if err != nil {
		return fmt.Errorf("list clients: %w", err)
	}
	// P and the per-user fan-out cost from docs/data-plane-plan.md §1.6.
	// Sampled here rather than derived from the panel-op counters because
	// this is the loop the cost model describes: the histogram's own P50
	// against P95 is what says whether the fan-out is a uniform cost or a
	// tail problem.
	metrics.UserClientCount.Observe(float64(len(clients)))
	// The same total, split by panel. P conflates "on many panels" with
	// "split on one panel", and only the second is PSP's doing — see
	// metrics.UserClientsPerPanel. The connection caps are budgeted per
	// panel-side email, so this is the factor that decides whether a cap the
	// admin typed once is being enforced once.
	for _, n := range clientsPerPanel(clients) {
		metrics.UserClientsPerPanel.Observe(n)
	}
	defer func() { metrics.SyncUserDuration.ObserveSince(started) }()

	// P is 1 for most users. Spawning a goroutine and a channel to run one
	// call is pure overhead, so the common case keeps the direct call.
	if len(clients) <= 1 {
		if len(clients) == 0 {
			return nil
		}
		return s.SyncLifecycle(ctx, clients[0], want)
	}

	// Results are collected BY INDEX, not by arrival, so "the first error"
	// stays the first error in client order. Reporting whichever goroutine
	// happened to fail first would make the returned error depend on panel
	// latency, and callers surface it to an admin.
	errs := make([]error, len(clients))
	var wg sync.WaitGroup
	sem := make(chan struct{}, s.panelConcurrency(ctx))
	for i, c := range clients {
		wg.Add(1)
		go func(i int, c *domain.PSPClient) {
			// Record the panic as this client's error rather than letting
			// safego.Recover swallow it. A nil errs[i] means "pushed
			// successfully", and ResyncMembership deletes the legacy per-node
			// fallback on exactly that signal — so a swallowed panic would
			// strand the user on a shared client still holding its provision
			// defaults (enabled, no expiry, no quota). That is the audit-#1
			// enforcement bypass the ordering comments warn about, and the
			// serial loop this replaced could not produce it because a panic
			// propagated.
			// wg.Done is registered FIRST so that it runs LAST: defers are
			// LIFO, and the recover below has to finish writing errs[i]
			// before wg.Wait() is allowed to return. Registered the other way
			// round — as it was — a panicking goroutine released the WaitGroup
			// while errs[i] was still nil, so the caller could read the slice
			// mid-write (a race) and see a clean nil for a push that panicked.
			// That is precisely the enforcement bypass the comment above says
			// this recover exists to prevent, reintroduced by defer ordering.
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					errs[i] = fmt.Errorf("sharedclient.SyncUserLifecycle: panic: %v", r)
					log.Error("panic in shared lifecycle push",
						"client_id", c.ID, "panel_id", c.PanelID, "panic", r,
						"stack", string(debug.Stack()))
				}
			}()
			sem <- struct{}{}
			defer func() { <-sem }()
			errs[i] = s.SyncLifecycle(ctx, c, want)
		}(i, c)
	}
	wg.Wait()

	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
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
	if n == nil || n.InboundID == 0 || len(userIDs) == 0 || n.IsSeparator() || !n.Enabled {
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

// clientsPerPanel buckets a user's clients by panel and returns one count per
// panel they are present on.
//
// Extracted rather than inlined because what it separates is the whole point:
// P (clients per user) cannot distinguish a user on four panels with one
// client each from a user split four ways on one panel, and only the second is
// PSP multiplying a connection cap the admin typed once. A helper can be
// tested on that distinction directly; four lines inside the sync loop cannot.
func clientsPerPanel(clients []*domain.PSPClient) []float64 {
	if len(clients) == 0 {
		return nil
	}
	perPanel := make(map[int64]int, len(clients))
	for _, c := range clients {
		perPanel[c.PanelID]++
	}
	out := make([]float64, 0, len(perPanel))
	for _, n := range perPanel {
		out = append(out, float64(n))
	}
	return out
}
