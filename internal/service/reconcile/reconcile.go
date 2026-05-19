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
		protocol domain.Protocol, userUUID, email, flow string, expireTime, totalGB int64) error
	SetOwnedClientEnable(ctx context.Context, panelID int64, inboundID int, email string,
		protocol domain.Protocol, userUUID, flow string, enable bool, expireTime, totalGB int64) error
	RotateClientUUID(ctx context.Context, panelID int64, inboundID int, email string,
		protocol domain.Protocol, oldUUID, newUUID, flow string, enable bool, expireTime, totalGB int64) error
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
}

func New(users ports.UserRepo, ownership ports.OwnershipRepo, nodes ports.NodeRepo,
	groups ports.GroupRepo, settings ports.SettingsRepo, audit ports.AuditRepo, pool ports.XUIPool, syncer ClientSyncer) *Service {
	return &Service{
		users: users, ownership: ownership, nodes: nodes, groups: groups, settings: settings,
		audit: audit, pool: pool, syncer: syncer,
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

	page := 1
	const pageSize = 100
	for {
		users, total, err := s.users.List(ctx, ports.UserFilter{
			Pagination: ports.Pagination{Page: page, PageSize: pageSize},
		})
		if err != nil {
			return nil, err
		}
		for _, u := range users {
			if level == LevelFull {
				s.checkMissingOwnerships(ctx, u, report, cache, rules, allNodes)
			}
			entries, err := s.ownership.ListByUser(ctx, u.ID)
			if err != nil {
				log.Warn("reconcile: list ownership", "user_id", u.ID, "err", err)
				continue
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
		s.checkNodes(ctx, report)
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

func (s *Service) checkMissingOwnerships(ctx context.Context, u *domain.User, report *Report, cache map[inboundCacheKey]*inboundCacheEntry, rules domain.EmailRules, nodes []*domain.Node) {
	if !u.Enabled {
		return
	}
	g, err := s.groups.GetByID(ctx, u.GroupID)
	if err != nil {
		return
	}
	entries, _ := s.ownership.ListByUser(ctx, u.ID)
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

		// find if it already exists in 3x-ui to avoid blind overwrite (though AddClient handles duplicates in xui wrapper)
		flow := n.Flow

		// Pass totalGB=0 (= 3X-UI unlimited). The next traffic-poll cycle
		// re-pushes the proper floor; reconcile only heals drift.
		err = s.syncer.AddClientToInbound(ctx, u.ID, n.PanelID, n.InboundID, protocol, u.UUID, email, flow, expireTime, 0)

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
			protocol, u.UUID, e.ClientEmail, desiredFlow, expireTime, 0); err != nil {
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
			protocol, u.UUID, desiredFlow, desiredEnable, expireTime, 0); err != nil {
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
			protocol, u.UUID, desiredFlow, desiredEnable, expireTime, 0); err != nil {
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
			protocol, found.ID, u.UUID, desiredFlow, desiredEnable, expireTime, 0); err != nil {
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
		expected := crypto.DeriveProxyPassword(u.UUID, protocol)
		if found.Password != expected {
			if err := s.syncer.SetOwnedClientEnable(ctx, e.PanelID, e.InboundID, e.ClientEmail,
				protocol, u.UUID, desiredFlow, desiredEnable, expireTime, 0); err != nil {
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
			protocol, u.UUID, desiredFlow, desiredEnable, expireTime, 0); err != nil {
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
func (s *Service) checkNodes(ctx context.Context, report *Report) {
	nodes, err := s.nodes.List(ctx)
	if err != nil {
		return
	}
	inboundsPerPanel := map[int64]map[int]bool{}
	for _, n := range nodes {
		known, ok := inboundsPerPanel[n.PanelID]
		if !ok {
			c, err := s.pool.Get(n.PanelID)
			if err != nil {
				continue
			}
			inbs, err := c.ListInbounds(ctx)
			if err != nil {
				continue
			}
			known = make(map[int]bool, len(inbs))
			for _, inb := range inbs {
				known[inb.ID] = true
			}
			inboundsPerPanel[n.PanelID] = known
		}
		if !known[n.InboundID] && n.Enabled {
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
	}
}

func levelName(l Level) string {
	if l == LevelFull {
		return "full"
	}
	return "light"
}
