// Package reconcile runs the layered drift-detection job described in
// docs/ARCHITECTURE.md §9.4. Three triggers share the same checks:
//
//   - L1 immediate post-write verification (called from SyncSvc; not yet wired)
//   - L2 lightweight scan piggy-backed on TrafficSvc (every 5 min)
//   - L3 full reconciliation cron (default every 15 min)
//
// All checks operate only on rows present in the ownership table. Clients
// outside that table (operator's own clients and unimported users)
// are never touched.
package reconcile

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/crypto"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/log"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/paneltz"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/safego"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/xrayspec"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/group"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/inboundcfg"
)

type Level int

const (
	LevelLight Level = iota // existence + enable only
	LevelFull               // all seven checks
)

// ClientSyncer is the narrow subset of sync.Service this package needs.
// totalGB in all signatures is the per-client traffic floor (0 = unlimited).
// reconcile only fixes drift; passing 0 here keeps the existing floor
// intact only when 3X-UI hasn't been touched, but if reconcile rewrites a
// client (recovery, rotation), it'll briefly drop to unlimited until the
// next poll. Acceptable for a drift-healing path — reconcile runs every
// 15 min; the traffic poll runs every 5 min and resets the floor.
type ClientSyncer interface {
	AddClientToInbound(ctx context.Context, userID int64, panelID int64, inboundID int,
		protocol domain.Protocol, ssMethod, userUUID, email, flow string, expireTime, totalGB int64) error
	SetOwnedClientEnable(ctx context.Context, panelID int64, inboundID int, email string,
		protocol domain.Protocol, ssMethod, userUUID, flow string, enable bool, expireTime, totalGB int64) error
	RotateClientUUID(ctx context.Context, panelID int64, inboundID int, email string,
		protocol domain.Protocol, ssMethod, oldUUID, newUUID, flow string, enable bool, expireTime, totalGB int64) error
}

type Service struct {
	users     ports.UserRepo
	ownership ports.OwnershipRepo
	nodes     ports.NodeRepo
	groups    ports.GroupRepo
	settings  ports.SettingsRepo
	audit     ports.AuditRepo
	pool      ports.XUIPool
	syncer    ClientSyncer

	// axisAReversePush gates the v3.5 "PSP pushes its config back over 3X-UI
	// drift" behavior in reconcileInboundConfig (the push uses
	// XUIClient.UpdateInbound, whose read-modify-write re-injects the live
	// settings.clients[] array). It was gated OFF on the 3.2.0 hard-cut while
	// the effect of that re-injection on 3.2.0's first-class client model was
	// unverified. P2 live verification (2026-05-28, docs/3xui-3.2-clients-
	// migration.md §P2) confirmed it preserves the clients table intact (no
	// orphan / no duplicate), so New() now ENABLES it (true). When false, drift
	// is ADOPTED instead (live config captured into the snapshot, nothing
	// written to the inbound) — retained as a kill-switch + exercised by tests.
	axisAReversePush bool
}

func New(users ports.UserRepo, ownership ports.OwnershipRepo, nodes ports.NodeRepo,
	groups ports.GroupRepo, settings ports.SettingsRepo, audit ports.AuditRepo, pool ports.XUIPool, syncer ClientSyncer) *Service {
	return &Service{
		users: users, ownership: ownership, nodes: nodes, groups: groups, settings: settings,
		audit: audit, pool: pool, syncer: syncer,
		axisAReversePush: true,
	}
}

// Report summarises one reconciliation run.
type Report struct {
	Scanned int     `json:"scanned"`
	Fixed   int     `json:"fixed"`
	Issues  []Issue `json:"issues"`
}

// Issue is one drift instance, fixed or not.
type Issue struct {
	PanelID     int64  `json:"panel_id"`
	PanelName   string `json:"panel_name"`
	InboundID   int    `json:"inbound_id"`
	ClientEmail string `json:"client_email"`
	Code        string `json:"code"`
	Detail      string `json:"detail"`
	Fixed       bool   `json:"fixed"`
}

// panelNameOf resolves a panel's display name from the in-memory pool. With
// the v3 schema cleanup the panel_name column is gone from nodes /
// user_xui_clients / client_traffic_snapshots — admin reports still want a
// human-readable name, so we look it up at render time. The pool is kept in
// sync by admin_servers handler on every panel CRUD so this is always fresh.
//
// Linear scan over pool.List() is fine: typical deployments hold 1-10
// panels, the pool slice is purely in-memory, and reconcile is not on the
// request hot path. A cached map would shave microseconds per cycle at the
// cost of threading it through 14 Issue-construction call sites — not
// worth the surface-area expansion.
func (s *Service) panelNameOf(panelID int64) string {
	for _, p := range s.pool.List() {
		if p.ID == panelID {
			return p.Name
		}
	}
	return ""
}

// inboundCacheEntry holds the decoded inbound + its parsed clients[] so we
// don't decode the settings JSON repeatedly for the same inbound during
// one reconciliation pass.
type inboundCacheEntry struct {
	inbound *ports.Inbound
	clients []xrayspec.InboundClient
	method  string
	flow    string
}

type inboundCacheKey struct {
	panelID   int64
	inboundID int
}

// RunOnce performs one reconciliation pass at the requested depth.
func (s *Service) RunOnce(ctx context.Context, level Level) (*Report, error) {
	report := &Report{}
	cache := map[inboundCacheKey]*inboundCacheEntry{}

	rules := s.emailRules(ctx)
	allNodes, _ := s.nodes.List(ctx)
	nodeByInbound := make(map[inboundCacheKey]*domain.Node, len(allNodes))
	uniquePanels := make(map[int64]struct{})
	for _, n := range allNodes {
		if n.IsSeparator() {
			continue
		}
		nodeByInbound[inboundCacheKey{panelID: n.PanelID, inboundID: n.InboundID}] = n
		uniquePanels[n.PanelID] = struct{}{}
	}

	// Load runtime settings ONCE per run so the user loop (and prefetch
	// fan-out below) all share the same configured panel concurrency
	// without re-hitting the settings repo per-iteration.
	cfg := ports.UISettings{}
	if s.settings != nil {
		if loaded, ierr := s.settings.Load(ctx, ports.UISettings{}); ierr == nil {
			cfg = loaded
		}
	}
	concurrency := paneltz.ResolveMaxPanelConcurrency(cfg.MaxPanelConcurrency)

	// Prefetch every panel's inbound list in parallel and seed the
	// shared cache. Before this, RunOnce did per-inbound c.GetInbound
	// calls inside the user loop: N ownership rows = N serial network
	// round-trips against 3X-UI (~200ms each), driving a single-digit
	// reconcile to multi-second wall-clock. Now one ListInbounds per
	// panel runs concurrently (capped by `concurrency`) and downstream
	// loadInbound calls become cache hits. Errors aren't fatal:
	// per-inbound loadInbound stays as the fallback if a particular
	// ListInbounds failed.
	prefetchInbounds(ctx, s.pool, uniquePanels, cache, concurrency)

	// Preload every group once into a map so checkMissingOwnerships
	// can look up by ID instead of issuing one SELECT per user. Groups
	// rarely number more than ~10 at self-host scale; the cost is
	// trivial and replaces N round-trips with one List + map walk.
	groupByID := map[int64]*domain.Group{}
	if allGroups, gerr := s.groups.List(ctx); gerr == nil {
		for _, g := range allGroups {
			groupByID[g.ID] = g
		}
	}

	page := 1
	const pageSize = 100
	for {
		users, total, err := s.users.List(ctx, ports.UserFilter{
			Pagination: ports.Pagination{Page: page, PageSize: pageSize},
		})
		if err != nil {
			return nil, err
		}
		// Batch ownership for the entire page in one round-trip rather
		// than 2N SELECTs (one in checkMissingOwnerships, one in the
		// scan loop below) — same batching shape traffic.PollOnce
		// already uses for ownership.ListByUsers.
		userIDs := make([]int64, 0, len(users))
		for _, u := range users {
			userIDs = append(userIDs, u.ID)
		}
		ownershipByUser, oerr := s.ownership.ListByUsers(ctx, userIDs)
		// batchOK separates "batch errored, must fall back per-user"
		// from "batch succeeded, this user simply has no rows". The
		// latter is a legitimate state (new account with no
		// memberships yet); without this distinction every such user
		// triggered a wasted per-user ListByUser SELECT every cycle,
		// partially defeating the N+1 fix the batch was supposed to
		// land.
		batchOK := oerr == nil
		if !batchOK {
			log.Warn("reconcile: batch list ownership", "err", oerr)
			ownershipByUser = nil
		}
		for _, u := range users {
			entries, present := ownershipByUser[u.ID]
			if !present && !batchOK {
				// Batch failed for the whole page — fall back to the
				// per-user SELECT so this user still gets scanned.
				var lerr error
				entries, lerr = s.ownership.ListByUser(ctx, u.ID)
				if lerr != nil {
					log.Warn("reconcile: list ownership", "user_id", u.ID, "err", lerr)
					continue
				}
			}
			// entries may legitimately be nil (zero ownership rows for
			// this user) — that's fine; checkMissingOwnerships needs
			// the nil to add the missing rows below, and the scan loop
			// just does nothing for a user with no rows.
			if level == LevelFull {
				s.checkMissingOwnershipsWithCtx(ctx, u, report, cache, rules, allNodes,
					groupByID[u.GroupID], entries, batchOK)
			}
			for _, e := range entries {
				report.Scanned++
				ce, err := s.loadInbound(ctx, cache, e.PanelID, e.InboundID)
				if err != nil {
					report.Issues = append(report.Issues, Issue{
						PanelID: e.PanelID, PanelName: s.panelNameOf(e.PanelID), InboundID: e.InboundID, ClientEmail: e.ClientEmail,
						Code: "inbound_unreachable", Detail: err.Error(),
					})
					continue
				}
				if issue, fixed := s.checkOne(ctx, u, e, ce,
					nodeByInbound[inboundCacheKey{panelID: e.PanelID, inboundID: e.InboundID}], level); issue != nil {
					issue.Fixed = fixed
					if fixed {
						report.Fixed++
					}
					report.Issues = append(report.Issues, *issue)
				}
			}
		}
		if int64(page*pageSize) >= total {
			break
		}
		page++
	}

	if level == LevelFull {
		s.checkNodes(ctx, report, cache)
	}

	if report.Fixed > 0 || len(report.Issues) > 0 {
		_ = s.audit.Insert(ctx, &domain.AuditEntry{
			Actor:  "reconcile",
			Action: "reconcile_" + levelName(level),
			Target: fmt.Sprintf("scanned=%d fixed=%d issues=%d",
				report.Scanned, report.Fixed, len(report.Issues)),
			At: time.Now(),
		})
	}
	return report, nil
}

func (s *Service) emailRules(ctx context.Context) domain.EmailRules {
	sys, err := s.settings.Load(ctx, ports.UISettings{})
	if err == nil {
		return domain.EmailRules{Domain: sys.EmailDomain}
	}
	return domain.EmailRules{}
}

// checkMissingOwnershipsWithCtx is the batched form RunOnce now uses:
// the caller passes the user's group (already loaded from the page-
// level groupByID map) and ownership entries (already loaded from the
// page-level ListByUsers batch). Kills 2 of the original N+1 round
// trips per user — at 100 users/page, ~200 fewer SELECTs per reconcile
// cycle.
//
// batchOK communicates whether `preloadedEntries` came from a
// successful batch load (true → trust nil-or-empty as "user truly has
// no rows") or a failed batch (false → preloadedEntries is nil for
// every user, so fall back to on-demand ListByUser). Without this
// distinction every zero-ownership user used to trigger a wasted
// SELECT per cycle.
func (s *Service) checkMissingOwnershipsWithCtx(
	ctx context.Context,
	u *domain.User,
	report *Report,
	cache map[inboundCacheKey]*inboundCacheEntry,
	rules domain.EmailRules,
	nodes []*domain.Node,
	g *domain.Group,
	preloadedEntries []*domain.XUIClientEntry,
	batchOK bool,
) {
	if !u.Enabled {
		return
	}
	if g == nil {
		// Fall back to the on-demand fetch — preserves the old behavior
		// when a group can't be found in the preloaded map (race with
		// admin deleting the group mid-cycle, etc.).
		var err error
		g, err = s.groups.GetByID(ctx, u.GroupID)
		if err != nil {
			return
		}
	}
	entries := preloadedEntries
	if entries == nil && !batchOK {
		// Batch failed for this page; recover per-user. When batch
		// succeeded a nil/empty preloadedEntries means "this user
		// genuinely has zero rows" — no SELECT needed.
		entries, _ = s.ownership.ListByUser(ctx, u.ID)
	}
	type nodeKey struct {
		panelID int64
		id      int
	}
	owned := map[nodeKey]bool{}
	for _, e := range entries {
		owned[nodeKey{e.PanelID, e.InboundID}] = true
	}

	for _, n := range nodes {
		if !n.Enabled {
			continue
		}
		if !group.Matches(n, g.TagFilter) {
			continue
		}
		if owned[nodeKey{n.PanelID, n.InboundID}] {
			continue
		}
		// Missing!
		report.Scanned++
		ce, err := s.loadInbound(ctx, cache, n.PanelID, n.InboundID)
		if err != nil {
			continue
		}
		protocol := crypto.DetectProtocol(ce.inbound.Protocol, ce.method)
		if protocol == "" {
			continue
		}

		email := u.ClientEmail(n.ID, rules)
		var expireTime int64
		if u.ExpireAt != nil {
			expireTime = u.ExpireAt.UnixMilli()
		}

		// AddClientToInbound recreates a missing client (best-effort drift
		// recovery). It does NOT dedup/adopt an already-present client: if the
		// client still exists upstream (e.g. a stale prefetch snapshot saw it
		// missing), /clients/add returns a duplicate error wrapped as
		// ErrValidation and this pass logs the failure; the next pass's
		// UUID/enable healers below converge it.
		flow := n.Flow

		// Pass totalGB=0 (= 3X-UI unlimited). The next traffic-poll cycle
		// re-pushes the proper floor; reconcile only heals drift.
		err = s.syncer.AddClientToInbound(ctx, u.ID, n.PanelID, n.InboundID, protocol, ce.method, u.UUID, email, flow, expireTime, 0)

		fixed := err == nil
		report.Issues = append(report.Issues, Issue{
			PanelID:     n.PanelID,
			PanelName:   s.panelNameOf(n.PanelID),
			InboundID:   n.InboundID,
			ClientEmail: email,
			Code:        "missing_ownership",
			Detail:      "User missing from node entirely",
			Fixed:       fixed,
		})
		if fixed {
			report.Fixed++
		}
	}
}

// prefetchInbounds pulls ListInbounds from every supplied panel in
// parallel (bounded by the caller-provided concurrency, normalized
// upstream via paneltz.ResolveMaxPanelConcurrency) and seeds the
// shared inboundCache. Errors per panel are non-fatal: the user
// loop's per-inbound loadInbound is left as a fallback if a
// particular ListInbounds failed.
func prefetchInbounds(ctx context.Context, pool ports.XUIPool,
	panels map[int64]struct{},
	cache map[inboundCacheKey]*inboundCacheEntry,
	concurrency int) {

	if len(panels) == 0 {
		return
	}
	if concurrency <= 0 {
		concurrency = 1
	}
	type result struct {
		panelID  int64
		inbounds []ports.Inbound
		err      error
	}
	results := make(chan result, len(panels))
	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup
	for pid := range panels {
		wg.Add(1)
		go func(p int64) {
			defer safego.Recover("reconcile.prefetchInbounds")
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			c, err := pool.Get(p)
			if err != nil {
				results <- result{panelID: p, err: err}
				return
			}
			list, lerr := c.ListInbounds(ctx)
			results <- result{panelID: p, inbounds: list, err: lerr}
		}(pid)
	}
	wg.Wait()
	close(results)

	for r := range results {
		if r.err != nil {
			log.Warn("reconcile prefetch inbounds", "panel_id", r.panelID, "err", r.err)
			continue
		}
		for i := range r.inbounds {
			inb := &r.inbounds[i]
			settings, perr := xrayspec.ParseSettings(inb.Settings)
			if perr != nil {
				log.Warn("reconcile prefetch parse settings",
					"panel_id", r.panelID, "inbound_id", inb.ID, "err", perr)
				continue
			}
			cache[inboundCacheKey{panelID: r.panelID, inboundID: inb.ID}] = &inboundCacheEntry{
				inbound: inb,
				clients: settings.Clients,
				method:  settings.Method,
				flow:    firstClientFlow(settings.Clients),
			}
		}
	}
}

func (s *Service) loadInbound(ctx context.Context, cache map[inboundCacheKey]*inboundCacheEntry,
	panelID int64, inboundID int) (*inboundCacheEntry, error) {

	key := inboundCacheKey{panelID: panelID, inboundID: inboundID}
	if e, ok := cache[key]; ok {
		return e, nil
	}
	c, err := s.pool.Get(panelID)
	if err != nil {
		return nil, err
	}
	inb, err := c.GetInbound(ctx, inboundID)
	if err != nil {
		return nil, err
	}
	settings, err := xrayspec.ParseSettings(inb.Settings)
	if err != nil {
		return nil, fmt.Errorf("parse settings: %w", err)
	}
	entry := &inboundCacheEntry{
		inbound: inb,
		clients: settings.Clients,
		method:  settings.Method,
		flow:    firstClientFlow(settings.Clients),
	}
	cache[key] = entry
	return entry, nil
}

func firstClientFlow(clients []xrayspec.InboundClient) string {
	for _, c := range clients {
		if c.Flow != "" {
			return c.Flow
		}
	}
	return ""
}

func (s *Service) checkOne(ctx context.Context, u *domain.User, e *domain.XUIClientEntry,
	ce *inboundCacheEntry, n *domain.Node, level Level) (*Issue, bool) {

	protocol := crypto.DetectProtocol(ce.inbound.Protocol, ce.method)
	found := xrayspec.FindClient(ce.clients, e.ClientEmail)
	desiredFlow := ce.flow
	if protocol == domain.ProtoVLESS && n != nil && n.Flow != "" {
		desiredFlow = n.Flow
	}

	// Single source of truth for "what expire_time should 3X-UI see for
	// this user" — same helper user.pushClientConfigToAll uses. Crucially
	// includes the EmergencyUntil extension; without this, reconcile and
	// the traffic poll fight over the same field (poll pushes the
	// emergency-extended time, reconcile would push the raw ExpireAt
	// back, poll pushes again, ad infinitum).
	expireTime := u.PushExpireTime()
	// Effective enable folds expiry + emergency into the admin's u.Enabled
	// toggle, matching what 3X-UI itself derives from (enable + expiry_time)
	// — without this, an expired-but-Enabled user gets stuck in a "panel
	// pushes enable=true, 3X-UI's cron flips it back" loop on every cycle.
	desiredEnable := u.EffectiveEnabled(time.Now())

	// Check 1: existence
	if found == nil {
		if err := s.syncer.AddClientToInbound(ctx, u.ID, e.PanelID, e.InboundID,
			protocol, ce.method, u.UUID, e.ClientEmail, desiredFlow, expireTime, 0); err != nil {
			return &Issue{
				PanelID:   e.PanelID,
				PanelName: s.panelNameOf(e.PanelID), InboundID: e.InboundID, ClientEmail: e.ClientEmail,
				Code: "missing_client_recover_failed", Detail: err.Error(),
			}, false
		}
		return &Issue{
			PanelID:   e.PanelID,
			PanelName: s.panelNameOf(e.PanelID), InboundID: e.InboundID, ClientEmail: e.ClientEmail,
			Code: "missing_client_recovered",
		}, true
	}

	// Check 3: enable mismatch
	if found.IsEnabled() != desiredEnable {
		if err := s.syncer.SetOwnedClientEnable(ctx, e.PanelID, e.InboundID, e.ClientEmail,
			protocol, ce.method, u.UUID, desiredFlow, desiredEnable, expireTime, 0); err != nil {
			return &Issue{
				PanelID:   e.PanelID,
				PanelName: s.panelNameOf(e.PanelID), InboundID: e.InboundID, ClientEmail: e.ClientEmail,
				Code: "enable_mismatch_fix_failed", Detail: err.Error(),
			}, false
		}
		return &Issue{
			PanelID:   e.PanelID,
			PanelName: s.panelNameOf(e.PanelID), InboundID: e.InboundID, ClientEmail: e.ClientEmail,
			Code: "enable_mismatch_fixed",
		}, true
	}

	if protocol == domain.ProtoVLESS && n != nil && n.Flow != "" && found.Flow != n.Flow {
		if err := s.syncer.SetOwnedClientEnable(ctx, e.PanelID, e.InboundID, e.ClientEmail,
			protocol, ce.method, u.UUID, desiredFlow, desiredEnable, expireTime, 0); err != nil {
			return &Issue{
				PanelID:   e.PanelID,
				PanelName: s.panelNameOf(e.PanelID), InboundID: e.InboundID, ClientEmail: e.ClientEmail,
				Code: "flow_mismatch_fix_failed", Detail: err.Error(),
			}, false
		}
		return &Issue{
			PanelID:   e.PanelID,
			PanelName: s.panelNameOf(e.PanelID), InboundID: e.InboundID, ClientEmail: e.ClientEmail,
			Code: "flow_mismatch_fixed",
		}, true
	}

	if level == LevelLight {
		return nil, false
	}

	// Check 2: uuid mismatch (VLESS/VMess). Rotation needs the OLD uuid as
	// the 3X-UI updateClient path key, so we pass found.ID explicitly.
	if (protocol == domain.ProtoVLESS || protocol == domain.ProtoVMess) && found.ID != u.UUID {
		if err := s.syncer.RotateClientUUID(ctx, e.PanelID, e.InboundID, e.ClientEmail,
			protocol, ce.method, found.ID, u.UUID, desiredFlow, desiredEnable, expireTime, 0); err != nil {
			return &Issue{
				PanelID:   e.PanelID,
				PanelName: s.panelNameOf(e.PanelID), InboundID: e.InboundID, ClientEmail: e.ClientEmail,
				Code: "uuid_mismatch_fix_failed", Detail: err.Error(),
			}, false
		}
		return &Issue{
			PanelID:   e.PanelID,
			PanelName: s.panelNameOf(e.PanelID), InboundID: e.InboundID, ClientEmail: e.ClientEmail,
			Code: "uuid_mismatch_fixed",
		}, true
	}

	// Check 4: derived password mismatch (Trojan / SS / SS-2022)
	if protocol == domain.ProtoTrojan || protocol == domain.ProtoSS || protocol == domain.ProtoSS2022 {
		expected := crypto.DeriveProxyPassword(u.UUID, protocol, ce.method)
		if found.Password != expected {
			if err := s.syncer.SetOwnedClientEnable(ctx, e.PanelID, e.InboundID, e.ClientEmail,
				protocol, ce.method, u.UUID, desiredFlow, desiredEnable, expireTime, 0); err != nil {
				return &Issue{
					PanelID:   e.PanelID,
					PanelName: s.panelNameOf(e.PanelID), InboundID: e.InboundID, ClientEmail: e.ClientEmail,
					Code: "password_mismatch_fix_failed", Detail: err.Error(),
				}, false
			}
			return &Issue{
				PanelID:   e.PanelID,
				PanelName: s.panelNameOf(e.PanelID), InboundID: e.InboundID, ClientEmail: e.ClientEmail,
				Code: "password_mismatch_fixed",
			}, true
		}
	}

	// Check 5: mismatch on ExpiryTime only. TotalGB is the per-client
	// traffic floor managed by user.Service/traffic.PollOnce as the
	// panel-offline safety net (a NON-ZERO byte count = remaining quota,
	// see fad13a3). Reconcile must NOT check TotalGB or it'll fight the
	// poll cycle every run: poll pushes floor=remaining, reconcile sees
	// "TotalGB > 0 = drift" and clears it to 0, poll re-pushes, and so
	// on — manifested as "auto-fixed 10 issues" every single reconcile
	// click. The drift-healing path that does fire here (expire only)
	// reuses the existing SetOwnedClientEnable signature; we pass totalGB=0
	// only because the helper requires it — the per-client floor is
	// re-asserted by the very next traffic poll regardless.
	if found.ExpiryTime != expireTime {
		if err := s.syncer.SetOwnedClientEnable(ctx, e.PanelID, e.InboundID, e.ClientEmail,
			protocol, ce.method, u.UUID, desiredFlow, desiredEnable, expireTime, 0); err != nil {
			return &Issue{
				PanelID:   e.PanelID,
				PanelName: s.panelNameOf(e.PanelID), InboundID: e.InboundID, ClientEmail: e.ClientEmail,
				Code: "expire_mismatch_fix_failed", Detail: err.Error(),
			}, false
		}
		return &Issue{
			PanelID:   e.PanelID,
			PanelName: s.panelNameOf(e.PanelID), InboundID: e.InboundID, ClientEmail: e.ClientEmail,
			Code: "expire_mismatch_fixed",
		}, true
	}

	return nil, false
}

// checkNodes verifies every nodes row still maps to an existing 3X-UI
// inbound. Disappeared inbounds get nodes.enabled flipped to false; the
// row is preserved so an admin can inspect what happened.
//
// The cache is the same one prefetchInbounds populated at the top of
// RunOnce — re-using it avoids a redundant ListInbounds per panel. A
// missing entry in the cache means either (a) the panel's prefetch
// failed (axis-B already logged a warn; surface it as an axis-A Issue
// so it appears in the admin's reconcile report) or (b) the inbound
// genuinely no longer exists on 3X-UI — both go through the "disabled
// the node" branch.
func (s *Service) checkNodes(ctx context.Context, report *Report, cache map[inboundCacheKey]*inboundCacheEntry) {
	nodes, err := s.nodes.List(ctx)
	if err != nil {
		return
	}
	// Track panels we've already complained about so each unreachable panel
	// produces exactly one Issue regardless of how many nodes it owns.
	reachable := map[int64]bool{}
	for _, n := range nodes {
		if n.IsSeparator() {
			continue
		}
		if _, seen := reachable[n.PanelID]; !seen {
			// A panel is considered reachable iff at least one inbound for it
			// is in the cache. prefetchInbounds populates entries per inbound
			// on success and logs+skips per-panel on ListInbounds failure.
			ok := false
			for k := range cache {
				if k.panelID == n.PanelID {
					ok = true
					break
				}
			}
			reachable[n.PanelID] = ok
			if !ok {
				report.Issues = append(report.Issues, Issue{
					PanelID:   n.PanelID,
					PanelName: s.panelNameOf(n.PanelID),
					Code:      "panel_unreachable",
					Detail:    "axis-A skipped: reconcile prefetch could not list inbounds",
				})
			}
		}
		if !reachable[n.PanelID] {
			continue
		}
		entry, present := cache[inboundCacheKey{panelID: n.PanelID, inboundID: n.InboundID}]
		if !present {
			if n.Enabled {
				n.Enabled = false
				if err := s.nodes.Update(ctx, n); err == nil {
					report.Issues = append(report.Issues, Issue{
						PanelID:   n.PanelID,
						PanelName: s.panelNameOf(n.PanelID), InboundID: n.InboundID,
						Code:   "inbound_missing_disabled_node",
						Detail: fmt.Sprintf("node id=%d", n.ID),
						Fixed:  true,
					})
					report.Fixed++
				}
			}
			continue
		}
		s.reconcileInboundConfig(ctx, n, entry.inbound, report)
	}
}

// reconcileInboundConfig maintains the v3.5 axis-A invariant: PSP is the
// source of truth for a managed inbound's connection config (see
// docs/inbound-ownership.md §2.1).
//
//   - No local snapshot yet (legacy row / freshly imported before capture):
//     pull the live inbound into the node so render stops live-fetching it.
//   - Snapshot present but 3X-UI drifted: push PSP's config back. The client
//     adapter's UpdateInbound read-modify-write preserves every live client
//     (PSP-managed and manually-created alike), so only the connection config
//     is overwritten — never a client. We then re-capture the post-push live
//     config so a JSON normalisation by 3X-UI converges instead of looping.
func (s *Service) reconcileInboundConfig(ctx context.Context, n *domain.Node, live *ports.Inbound, report *Report) {
	// Steady state — captured AND already in sync — is the overwhelmingly
	// common case: do nothing, and notably skip the stale-read guard's extra
	// DB round-trip. Only un-captured (backfill) or drifted nodes fall through
	// to a mutation below.
	if n.ConfigSyncedAt != nil && inboundcfg.InSync(n, live) {
		return
	}

	// We're about to mutate. checkNodes pulled `n` from List() at the top of
	// the cycle; an admin write path (CreateInbound / ImportExisting /
	// UpdateInboundConfig) may have stored a fresh snapshot since. If we pushed
	// our stale copy now, the admin's edit would be silently reverted on both
	// 3X-UI and (after re-capture) locally. Re-read and bail on any version
	// mismatch — the next reconcile cycle will see the fresh row.
	fresh, err := s.nodes.GetByID(ctx, n.ID)
	if err != nil || fresh == nil {
		return
	}
	if !sameSyncStamp(n.ConfigSyncedAt, fresh.ConfigSyncedAt) {
		return
	}
	n = fresh

	if n.ConfigSyncedAt == nil {
		inboundcfg.Capture(n, live)
		// Column-scoped write: a concurrent health pass writes port/protocol
		// from the same probe target, full-row Save would race with it.
		if err := s.nodes.UpdateInboundConfig(ctx, n); err != nil {
			s.recordInboundConfigEvent(ctx, report, n, "inbound_config_backfill_failed", err.Error(), false)
			return
		}
		s.recordInboundConfigEvent(ctx, report, n, "inbound_config_backfilled", fmt.Sprintf("node id=%d", n.ID), true)
		return
	}

	// Captured but drifted. AXIS-A REVERSE-PUSH GATE: while disabled (the 3.2.0
	// default — see the axisAReversePush field), DEGRADE to adopting 3X-UI's
	// drifted connection config into the snapshot instead of pushing PSP's over
	// it. This writes NOTHING to the inbound (avoiding the unverified
	// settings.clients re-injection on 3.2.0) while keeping render correct —
	// subscriptions follow the live config. The trade-off: an operator's direct
	// edit to a managed inbound is followed, not reverted. `live` is the same
	// snapshot InSync just compared against, so capturing it converges to
	// in-sync with no extra fetch.
	if !s.axisAReversePush {
		inboundcfg.Capture(n, live)
		if err := s.nodes.UpdateInboundConfig(ctx, n); err != nil {
			s.markConfigSyncStatePending(ctx, n)
			s.recordInboundConfigEvent(ctx, report, n, "inbound_config_drift_adopt_failed", err.Error(), false)
			return
		}
		s.recordInboundConfigEvent(ctx, report, n, "inbound_config_drift_adopted",
			fmt.Sprintf("node id=%d — axis-A reverse-push disabled pending 3X-UI 3.2.0 verification", n.ID), true)
		return
	}

	// Reverse-push enabled (verified): push PSP's config back over the
	// server-side drift, then re-capture whatever 3X-UI persisted.
	c, err := s.pool.Get(n.PanelID)
	if err != nil {
		return
	}
	spec := inboundcfg.SpecFromNode(n)
	// remark is operator-owned: an axis-A drift push must never overwrite a
	// rename made directly in 3X-UI (InSync already ignores remark, so a
	// remark-only change isn't even why we're here). Carry the live remark
	// rather than PSP's stored one; the post-push re-capture below then syncs
	// the operator's value back into the snapshot.
	spec.Remark = live.Remark
	if err := c.UpdateInbound(ctx, n.InboundID, spec); err != nil {
		s.markConfigSyncStatePending(ctx, n)
		s.recordInboundConfigEvent(ctx, report, n, "inbound_config_push_failed", err.Error(), false)
		return
	}
	// Converge the local snapshot to whatever 3X-UI actually persisted (it may
	// normalise JSON / inject defaults), so the next compare reads as in-sync.
	// If the re-capture fails we DON'T mark fixed and leave the snapshot as it
	// was — otherwise we'd loop forever pushing the same drifted spec.
	freshLive, ferr := c.GetInbound(ctx, n.InboundID)
	if ferr != nil {
		s.markConfigSyncStatePending(ctx, n)
		s.recordInboundConfigEvent(ctx, report, n, "inbound_config_recapture_failed", ferr.Error(), false)
		return
	}
	inboundcfg.Capture(n, freshLive)
	if err := s.nodes.UpdateInboundConfig(ctx, n); err != nil {
		s.markConfigSyncStatePending(ctx, n)
		s.recordInboundConfigEvent(ctx, report, n, "inbound_config_recapture_failed", err.Error(), false)
		return
	}
	s.recordInboundConfigEvent(ctx, report, n, "inbound_config_drift_pushed", fmt.Sprintf("node id=%d", n.ID), true)
}

// recordInboundConfigEvent appends one Issue to the report (mirroring the prior
// per-event behaviour) AND writes a single audit_log row so the per-inbound
// outcome of every axis-A reconcile cycle is retrievable. The aggregate
// reconcile_full / reconcile_light audit row written by RunOnce still captures
// the cycle-wide summary; these per-event rows give the per-inbound provenance
// the admin / dashboard needs (was this node ever drifted? when? what failed?).
func (s *Service) recordInboundConfigEvent(ctx context.Context, report *Report, n *domain.Node, code, detail string, fixed bool) {
	if fixed {
		report.Fixed++
	}
	report.Issues = append(report.Issues, Issue{
		PanelID:   n.PanelID,
		PanelName: s.panelNameOf(n.PanelID),
		InboundID: n.InboundID,
		Code:      code,
		Detail:    detail,
		Fixed:     fixed,
	})
	if s.audit == nil {
		return
	}
	_ = s.audit.Insert(ctx, &domain.AuditEntry{
		Actor:     "reconcile",
		Action:    code,
		Target:    fmt.Sprintf("node=%d panel=%d inbound=%d", n.ID, n.PanelID, n.InboundID),
		AfterJSON: detail,
		At:        time.Now(),
	})
}

// markConfigSyncStatePending flips the snapshot's sync-state column to
// "pending" so the UI can surface "PSP wants this config but couldn't push it
// yet"; the next successful drift-push or capture flips it back to "synced"
// via inboundcfg.Capture / markSynced. Best-effort: a DB failure here doesn't
// matter — the same condition will re-trigger next cycle.
func (s *Service) markConfigSyncStatePending(ctx context.Context, n *domain.Node) {
	if n.ConfigSyncState == "pending" {
		return
	}
	n.ConfigSyncState = "pending"
	_ = s.nodes.UpdateInboundConfig(ctx, n)
}

// sameSyncStamp compares two ConfigSyncedAt pointers by value. Used as a row
// version stamp: if the in-memory copy of a node and a freshly-re-read copy
// disagree, the row was rewritten by an admin path mid-cycle.
func sameSyncStamp(a, b *time.Time) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.Equal(*b)
}

func levelName(l Level) string {
	if l == LevelFull {
		return "full"
	}
	return "light"
}
