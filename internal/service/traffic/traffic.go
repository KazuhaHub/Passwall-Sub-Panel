// Package traffic implements the periodic traffic-collection job that
// powers the panel's usage dashboard and the auto-disable / auto-reenable
// behaviour around traffic quotas and reset periods.
package traffic

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/log"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/paneltz"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/safego"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// UserDisabler is the narrow subset of user.Service this package needs.
// Defined here to keep the import direction one-way.
type UserDisabler interface {
	SetEnabledAndSync(ctx context.Context, userID int64, enabled bool, reason domain.AutoDisabledReason, detail string) error
}

// UserConfigPusher refreshes a user's full 3X-UI client config (enable +
// expiry + traffic floor). The traffic poll calls this after each successful
// snapshot so the panel-managed remaining-bytes cap propagates into
// 3X-UI on every cycle — the safety net for prolonged panel outages.
type UserConfigPusher interface {
	PushClientConfig(ctx context.Context, userID int64) error
	// WithEmergencyLock runs fn while holding user.Service's
	// per-process emergency-access mutex. Used here when the poll
	// clears EmergencyUntil / EmergencyBaselineBytes on period
	// rollover or quota exhaustion — otherwise a concurrent
	// UseEmergencyAccess on the same user would race the write.
	WithEmergencyLock(fn func())
}

// MailNotifier lets the poll email the user when it auto-disables them on
// quota exhaustion or auto-re-enables them on period rollover. Optional /
// nil-tolerant and late-bound (mailer.Service implements it but is wired up
// after traffic.Service). Calls are fired async so SMTP can't stall the poll.
type MailNotifier interface {
	SendAccountDisabledToUser(ctx context.Context, userID int64, reason, detail string) error
	SendAccountEnabledToUser(ctx context.Context, userID int64, reason, detail string) error
}

type Service struct {
	users       ports.UserRepo
	ownership   ports.OwnershipRepo
	traffic     ports.TrafficRepo
	nodes       ports.NodeRepo
	nodeTraffic ports.NodeTrafficRepo
	pool        ports.XUIPool
	disabler    UserDisabler
	// pspClient is the v3.9.0 shared-client repo, late-bound via SetPSPClientRepo
	// (nil before wiring / in tests). The poll meters each shared client's per-email traffic (read once, aggregate)
	// into the user's quota total — Stage 3, so usage isn't lost after the render
	// flip moves it from the per-node clients to the shared client.
	pspClient ports.PSPClientRepo
	// settings is optional — used for the bulk per-cycle config Load AND, for an
	// active-emergency user, a per-user LoadForUser so the poll ends the window at
	// THIS user's effective EmergencyAccessQuotaGB (global ⊕ group override). That
	// keeps the poll teardown in lockstep with user.emergencyFloor / the /sub gate,
	// which both resolve the same per-group quota. Nil-tolerant: when absent,
	// emergency access is uncapped (legacy behavior).
	settings ports.ScopedSettings
	// configPusher is wired lazily (user.Service is the implementor and
	// is created before traffic.Service). nil = skip floor refresh on poll.
	configPusher UserConfigPusher
	// mailer is optional/late-bound. nil = no quota/rollover emails.
	mailer MailNotifier
	// pushSem caps how many safety-net floor pushes can run concurrently in
	// the background. v3.5.0-beta.12 moved the per-user push out of the
	// PollOnce hot path (was the dominant remaining cost after beta.9's
	// batch flush — each push is a 3X-UI UpdateClient round-trip, run
	// sequentially per user). Cap is the same default as MaxPanelConcurrency
	// (8) so the outer fan-out and the per-user-internal fan-out together
	// stay within reasonable 3X-UI load. Shared across cycles: if a previous
	// cycle's pushes are still draining when a new cycle queues more, the
	// new ones wait on the same sem instead of doubling the load on 3X-UI.
	pushSem chan struct{}
	// bgWG is the app-level WaitGroup the background goroutines (floor
	// push, quota-event email) attach to so App.Shutdown drains them
	// before the process exits. Late-bound via SetBgWG because the WG
	// lives on App, which Build() constructs AFTER the services. nil =
	// "fire and forget without tracking" (legacy behaviour, kept so unit
	// tests that don't care about shutdown don't have to wire a WG).
	bgWG *sync.WaitGroup

	// pollCfgMu + pollCfgCache hold the most recent successfully-loaded
	// UISettings so a transient settings.Load failure inside PollOnce
	// falls back to the previous value instead of silently degrading to
	// zero (which disables the per-window EmergencyAccessQuotaGB cap
	// without surfacing anything to admin). Updated on every successful
	// load; read on every cycle even when the load fails.
	pollCfgMu    sync.RWMutex
	pollCfgCache ports.UISettings
}

// SetMailNotifier wires the late-bound mailer used for auto-disable /
// auto-re-enable emails. Same late-binding rationale as SetConfigPusher.
func (s *Service) SetMailNotifier(m MailNotifier) { s.mailer = m }

// SetBgWG wires the app-level WaitGroup the background goroutines
// (floor push, quota-event email) register with so App.Shutdown can
// drain them before the process exits. nil is tolerated and degrades
// to "fire and forget" — same semantics as before this method existed.
func (s *Service) SetBgWG(wg *sync.WaitGroup) { s.bgWG = wg }

// notifyDisabled / notifyEnabled fire the quota-event email off the poll's
// hot path. Background context (the poll's ctx may be cancelled mid-cycle)
// and a panic-shielded goroutine; best-effort, errors are logged not surfaced.
func (s *Service) notifyDisabled(userID int64, reason, detail string) {
	if s.mailer == nil {
		return
	}
	safego.GoTracked(s.bgWG, "traffic.disabled-email", func() {
		if err := s.mailer.SendAccountDisabledToUser(context.Background(), userID, reason, detail); err != nil {
			log.Warn("traffic disabled email", "user_id", userID, "err", err)
		}
	})
}

func (s *Service) notifyEnabled(userID int64, reason, detail string) {
	if s.mailer == nil {
		return
	}
	safego.GoTracked(s.bgWG, "traffic.enabled-email", func() {
		if err := s.mailer.SendAccountEnabledToUser(context.Background(), userID, reason, detail); err != nil {
			log.Warn("traffic enabled email", "user_id", userID, "err", err)
		}
	})
}

// SetConfigPusher wires the late-bound config pusher. Same late-binding
// pattern as user.Service.SetTrafficUsage — needed because both services
// have methods that reference each other.
func (s *Service) SetConfigPusher(p UserConfigPusher) {
	s.configPusher = p
}

// SetPSPClientRepo late-binds the v3.9.0 shared-client repo so the poll can
// meter shared-client traffic (Stage 3). Nil-tolerant: until set (and until the
// repo is wired the shared-client metering pass runs (no-op pre-migration).
func (s *Service) SetPSPClientRepo(r ports.PSPClientRepo) { s.pspClient = r }

// CurrentPeriodUsage returns the bytes u has consumed since the start of
// their current traffic period. Used by user.Service to compute the per-
// client traffic floor it pushes into 3X-UI.
//
// Wraps the existing periodUsage helper but loads the latest snapshot
// itself so callers don't need to thread one in.
func (s *Service) CurrentPeriodUsage(ctx context.Context, u *domain.User) (int64, error) {
	if u == nil {
		return 0, nil
	}
	latest, err := s.traffic.LatestForUser(ctx, u.ID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) || latest == nil {
			return 0, nil
		}
		return 0, err
	}
	return s.periodUsage(ctx, u, latest)
}

type inboundKey struct {
	panelID   int64
	inboundID int
}

type ownershipRef struct {
	// entry is the full ownership row — needed so recordClientStats can read
	// LastRawXxx (its monotonic-delta baseline) and write back the updated
	// LifetimeXxx counters in a single repo call per cycle. Pre-v3 this held
	// only (userID, email, createdAt) and snapshots stored the raw counter;
	// now lifetime lives on the ownership row itself so admins can SELECT
	// it directly without snapshot-table aggregation.
	entry     *domain.XUIClientEntry
	userID    int64
	email     string
	createdAt time.Time
}

func New(users ports.UserRepo, ownership ports.OwnershipRepo, traffic ports.TrafficRepo, nodes ports.NodeRepo, nodeTraffic ports.NodeTrafficRepo, pool ports.XUIPool, disabler UserDisabler) *Service {
	return &Service{
		users:       users,
		ownership:   ownership,
		traffic:     traffic,
		nodes:       nodes,
		nodeTraffic: nodeTraffic,
		pool:        pool,
		disabler:    disabler,
		// Service-scoped semaphore for async floor pushes — see Service.pushSem doc.
		// Sized to the same default (8) as paneltz.ResolveMaxPanelConcurrency(0).
		pushSem: make(chan struct{}, paneltz.ResolveMaxPanelConcurrency(0)),
	}
}

// WithSettings attaches the settings repo so the poll can enforce the
// emergency-access traffic quota. Optional — leaving it nil preserves the
// previous "uncapped emergency window" behavior. Returns the service for
// chaining at construction sites.
func (s *Service) WithSettings(settings ports.ScopedSettings) *Service {
	s.settings = settings
	return s
}

// panelNow is a thin wrapper over paneltz.Now so the existing call sites
// here don't need to thread the settings repo at each call.
func (s *Service) panelNow(ctx context.Context) time.Time {
	return paneltz.Now(ctx, s.settings)
}

// PollOnce walks every user, pulls aggregated traffic, writes a snapshot,
// and enforces quotas + period resets.
//
// Errors per user are logged; the overall pass keeps going so one bad user
// doesn't block the rest.
func (s *Service) PollOnce(ctx context.Context) error {
	// Per-stage timing — silent by default (Debug level), opens up when the
	// process is started with PSP_LOG_LEVEL=debug. Kept in code so a future
	// "Poll Now feels slow" diagnosis is a single env flip away rather than
	// a code change + redeploy. Originally added in beta.13 to track down a
	// 6–10s wall-clock report (turned out to be a pre-beta.12 binary still
	// running on the server — see beta.14 changelog for the resolution).
	pollStartedAt := time.Now()
	stage := pollStartedAt
	mark := func(name string) {
		log.Debug("traffic poll timing", "stage", name, "ms", time.Since(stage).Milliseconds())
		stage = time.Now()
	}
	defer func() {
		log.Debug("traffic poll timing", "stage", "TOTAL", "ms", time.Since(pollStartedAt).Milliseconds())
	}()

	users, err := s.listAllUsers(ctx)
	if err != nil {
		return err
	}
	mark("listAllUsers")

	// Load runtime settings + the resolved panel location ONCE per poll
	// and share them across the inner loops. Before this each user's
	// recordAndEnforce path did two settings.Load calls — one inside
	// panelNow (via paneltz.Location) and one for the emergency-quota
	// check — so N users meant 2N DB roundtrips even though the data
	// is identical for the whole cycle. With N users + reasonable
	// snapshot count that adds up.
	// Load runtime settings, but fall back to the previously-cached
	// value on failure rather than letting pollCfg degrade to zero. A
	// zero pollCfg silently disables EmergencyAccessQuotaGB / the
	// auto-disable cadence flags etc., which produces wrong behaviour
	// for an entire cycle with no log line for admin to grep on. The
	// cache is updated in lockstep on every successful load.
	pollCfg := ports.UISettings{}
	if s.settings != nil {
		if loaded, lerr := s.settings.Load(ctx, ports.UISettings{}); lerr == nil {
			s.pollCfgMu.Lock()
			s.pollCfgCache = loaded
			s.pollCfgMu.Unlock()
			pollCfg = loaded
		} else {
			s.pollCfgMu.RLock()
			pollCfg = s.pollCfgCache
			s.pollCfgMu.RUnlock()
			log.Warn("traffic poll: settings.Load failed, using cached config",
				"err", lerr)
		}
	}
	pollLoc := paneltz.Location(ctx, s.settings)

	// Sink collects every snapshot write + every per-row counter update
	// across the user loop so we can flush them in batched calls at the
	// end of the cycle instead of N + M individual statements. The per-
	// user / per-node processing stays single-goroutine so no locking is
	// needed on the sink fields — they're just append targets owned by
	// this poll.
	sink := &pollSink{
		userSnaps:          make([]*domain.TrafficSnapshot, 0, len(users)),
		clientSnaps:        make([]*domain.ClientTrafficSnapshot, 0, len(users)*4),
		nodeSnaps:          make([]*domain.NodeTrafficSnapshot, 0),
		nodeCounterUpdates: make(map[int64]*domain.Node),
		ownershipUpdates:   make([]*domain.XUIClientEntry, 0, len(users)*4),
		userUpdates:        make(map[int64]*domain.User, len(users)),
		lastOnlineMs:       make(map[int64]int64, len(users)),
		clientDeltas:       make(map[int64]trafficDelta, len(users)*4),
		rolledOver:         make(map[int64]bool),
		clientsByUser:      make(map[int64][]*domain.XUIClientEntry, len(users)),
		userCutoff:         make(map[int64]*time.Time, len(users)),
	}

	// Pre-fetch every user's latest snapshot in ONE batched read. Replaces
	// what used to be a per-user LatestForUser SELECT inside
	// recordAndEnforceWith — at N users that's N round-trips reduced to 1.
	// Absence in the returned map means "no prior snapshot", matching
	// LatestForUser's ErrNotFound semantics so the caller can map-index it
	// without a nil-or-err two-arm guard.
	allUserIDs := make([]int64, 0, len(users))
	for _, u := range users {
		allUserIDs = append(allUserIDs, u.ID)
	}
	latestByUser, lerr := s.traffic.LatestForUsers(ctx, allUserIDs)
	if lerr != nil {
		// Non-fatal: fall back to per-user fetch inside recordAndEnforceWith.
		// One bad batched SELECT shouldn't kill the whole cycle.
		log.Warn("traffic poll latest pre-fetch failed; falling back to per-user", "users", len(allUserIDs), "err", lerr)
		latestByUser = nil
	}
	sink.latestByUser = latestByUser
	mark("LatestForUsers prefetch")

	// One batched read instead of the N+1 ListByUser-per-user loop. Small
	// absolute win on MySQL/Postgres localhost (~1ms each → ~10ms total at
	// modest scale collapses to a single SELECT), more visible on remote DB.
	// Cross-dialect via GORM's IN clause. Falls back to the per-user loop on
	// error so a hiccup on this read can't take the whole cycle down. v3.5.0-beta.15.
	byInbound := make(map[inboundKey][]ownershipRef)
	totals := make(map[int64]trafficTotals, len(users))
	skipUsers := make(map[int64]bool)
	for _, u := range users {
		totals[u.ID] = trafficTotals{}
	}
	entriesByUser, oerr := s.ownership.ListByUsers(ctx, allUserIDs)
	if oerr != nil {
		log.Warn("traffic poll ownership batched read failed; falling back to per-user", "users", len(allUserIDs), "err", oerr)
		entriesByUser = nil
	}
	for _, u := range users {
		var entries []*domain.XUIClientEntry
		if entriesByUser != nil {
			entries = entriesByUser[u.ID]
		} else {
			perUser, err := s.ownership.ListByUser(ctx, u.ID)
			if err != nil {
				log.Warn("traffic poll ownership", "user_id", u.ID, "err", err)
				continue
			}
			entries = perUser
		}
		for _, e := range entries {
			key := inboundKey{panelID: e.PanelID, inboundID: e.InboundID}
			byInbound[key] = append(byInbound[key], ownershipRef{entry: e, userID: u.ID, email: e.ClientEmail, createdAt: e.CreatedAt})
		}
		// Same entry pointers — the post-loop period-baseline pass walks these
		// at rollover (incl. clients 3X-UI didn't return this cycle).
		sink.clientsByUser[u.ID] = entries
	}
	mark("ownership.ListByUsers batched read")

	// Group queries per panel, fetch full inbound list once (it embeds
	// clientStats — the dedicated /getClientTrafficsById endpoint is empty
	// on some 3X-UI builds). Falls back to per-inbound calls if needed.
	byPanel := make(map[int64]map[int][]ownershipRef)
	for k, refs := range byInbound {
		if byPanel[k.panelID] == nil {
			byPanel[k.panelID] = make(map[int][]ownershipRef)
		}
		byPanel[k.panelID][k.inboundID] = refs
	}

	// v3.9.0: the panels to poll come from BOTH the legacy ownership table AND the
	// shared-client table. A MIGRATED user's traffic accrues under u{uid}@ with no
	// ownership row, so post-migration byPanel is EMPTY — without unioning in the
	// shared-client panels, Phase 1 fetches nothing and neither user traffic,
	// last_online, nor node traffic update at all. Fetch the shared clients ONCE here
	// (reused by the shared-metering pass below) and add their panels to the set.
	var sharedClients []*domain.PSPClient
	if s.pspClient != nil {
		var serr error
		if sharedClients, serr = s.pspClient.ListAll(ctx); serr != nil {
			log.Warn("traffic poll shared-client list failed; shared metering skipped this cycle", "err", serr)
			sharedClients = nil
		}
	}
	panelsToFetch := make(map[int64]struct{}, len(byPanel)+len(sharedClients))
	for pid := range byPanel {
		panelsToFetch[pid] = struct{}{}
	}
	for _, c := range sharedClients {
		panelsToFetch[c.PanelID] = struct{}{}
	}

	log.Info("traffic poll start", "users", len(users), "panels", len(panelsToFetch),
		"legacy_inbounds", len(byInbound), "shared_clients", len(sharedClients))

	// Phase 1 — parallel ListInbounds across every panel, bounded by a
	// semaphore so a 50-panel deployment can't fan out into 50
	// simultaneous HTTP requests against 3X-UI workers. Serial calls
	// were turning "Poll Now" into a multi-second wait with even 3
	// panels (each request is 100-500ms network-bound); concurrent
	// calls drop the wall-clock to roughly one ListInbounds time
	// regardless of panel count, while the cap prevents tail-end
	// regressions when admins eventually attach many panels.
	type inboundCounter struct {
		up, down int64
		// lastOnline is the most recent client-online unix-ms across echoes; the
		// shared-metering pass carries it so a fully-migrated user's last_online_at
		// keeps advancing (the per-node pass that used to source it is gone).
		lastOnline int64
	}
	type panelListResult struct {
		stats map[int]([]ports.ClientTraffic)
		// counters holds each inbound's OWN cumulative up/down (ports.Inbound.Up/
		// Down) — the v3.9.0 source for node-level traffic, replacing the old
		// "sum the owned clients" approach (which double-counts once a client is
		// attached to multiple inbounds; see docs/v3.9.0-client-multi-inbound.md).
		counters map[int]inboundCounter
		err      error
	}
	panelData := make(map[int64]panelListResult, len(panelsToFetch))
	var panelMu sync.Mutex
	var panelWG sync.WaitGroup
	panelSem := make(chan struct{}, paneltz.ResolveMaxPanelConcurrency(pollCfg.MaxPanelConcurrency))
	for panelID := range panelsToFetch {
		panelWG.Add(1)
		go func(pid int64) {
			defer safego.Recover("traffic.PollOnce.panelFetch")
			defer panelWG.Done()
			panelSem <- struct{}{}
			defer func() { <-panelSem }()
			c, err := s.pool.Get(pid)
			if err != nil {
				panelMu.Lock()
				panelData[pid] = panelListResult{err: err}
				panelMu.Unlock()
				return
			}
			// Slim list: the poll only consumes clientStats, so it skips the
			// per-client settings blobs (uuid/flow/password) the full /list
			// carries — a big win on panels with thousands of clients.
			listed, lerr := c.ListInboundsSlim(ctx)
			stats := make(map[int][]ports.ClientTraffic, len(listed))
			counters := make(map[int]inboundCounter, len(listed))
			for _, inb := range listed {
				stats[inb.ID] = inb.ClientStats
				// Slim list keeps the inbound-level up/down; capture it as the
				// node-traffic source (LIVE-VERIFIED reliable on 3.3.1).
				counters[inb.ID] = inboundCounter{up: inb.Up, down: inb.Down}
			}
			panelMu.Lock()
			panelData[pid] = panelListResult{stats: stats, counters: counters, err: lerr}
			panelMu.Unlock()
		}(panelID)
	}
	panelWG.Wait()
	mark("Phase 1 parallel ListInbounds")

	// Phase 2 — per-panel sequential processing. ListInbounds results are
	// already in panelData (Phase 1); Phase 2 is pure in-memory attribution of
	// clientStats to owned clients — no further 3X-UI network calls.
	for panelID, inbounds := range byPanel {
		pd := panelData[panelID]
		if pd.err != nil {
			log.Warn("traffic poll panel", "panel_id", panelID, "err", pd.err)
			for _, refs := range inbounds {
				markSkippedUsers(skipUsers, refs)
			}
			continue
		}
		statsByInbound := pd.stats

		for inboundID, refs := range inbounds {
			traffics := statsByInbound[inboundID]
			// 3X-UI 3.2.0 removed the per-inbound getClientTrafficsById
			// fallback; ListInbounds clientStats (Phase 1) is the sole source
			// and reliably carries every inbound's per-client counters. An
			// inbound with no traffic simply yields an empty slice here.
			trafficByEmail := make(map[string]ports.ClientTraffic, len(traffics))
			for _, t := range traffics {
				trafficByEmail[t.Email] = t
			}
			matched := 0
			for _, ref := range refs {
				t, ok := trafficByEmail[ref.email]
				if !ok {
					continue
				}
				matched++
				// Take the max across all of this user's clients —
				// "last online anywhere" is what powers the admin
				// "最近活跃" column. 3X-UI < 3.1.0 panels report 0
				// (json default for missing field); Go map zero
				// value naturally skips those since 0 > 0 is false.
				if t.LastOnline > sink.lastOnlineMs[ref.userID] {
					sink.lastOnlineMs[ref.userID] = t.LastOnline
				}
				total := totals[ref.userID]
				total.up += t.Up
				total.down += t.Down
				total.hits++
				delta, derr := s.recordClientStats(ctx, ref.entry, t.Up, t.Down, sink)
				if derr != nil {
					log.Warn("traffic poll client snapshot",
						"user_id", ref.userID,
						"panel_id", panelID,
						"inbound_id", inboundID,
						"email", ref.email,
						"err", derr)
				} else {
					if delta.hadPrev {
						total.deltaUp += delta.up
						total.deltaDown += delta.down
						total.deltaTotal += delta.total
					} else {
						total.bootstrap = append(total.bootstrap, bootstrapClientDelta{
							delta:     delta,
							createdAt: ref.createdAt,
						})
					}
					// Remember this client's delta so the rollover baseline pass
					// can subtract it (this cycle's traffic belongs to the NEW
					// period, exactly as the user-level rollover treats it).
					if sink != nil {
						sink.clientDeltas[ref.entry.ID] = delta
					}
				}
				totals[ref.userID] = total
			}
			if matched < len(refs) {
				seen := make([]string, 0, len(traffics))
				for _, t := range traffics {
					seen = append(seen, t.Email)
				}
				wanted := make([]string, 0, len(refs))
				for _, r := range refs {
					wanted = append(wanted, r.email)
				}
				log.Warn("traffic poll email mismatch",
					"panel_id", panelID, "inbound_id", inboundID,
					"matched", matched, "expected", len(refs),
					"3xui_emails", seen,
					"panel_owned_emails", wanted)
			}

		}
	}

	mark("Phase 2 inbound processing (sink appends)")

	// Phase 2a — per-node traffic from each fetched inbound's OWN cumulative counter
	// (v3.9.0), not a sum of owned clients. This runs over EVERY fetched inbound, not
	// just the ownership-referenced ones, because (a) post-migration there are NO
	// ownership refs yet a node's usage must still be metered, and (b) sourcing from
	// the inbound keeps node stats correct when a client spans multiple inbounds
	// (a client-sum double-counts; LIVE-VERIFIED on 3.3.1 both inbounds echo the same
	// aggregate). recordNodeStats no-ops an inbound that isn't a PSP node, and
	// re-seeds its baseline on the first post-upgrade poll so the switch is
	// spike-free. (Was inside the ownership loop, which is empty after migration.)
	for pid, pd := range panelData {
		if pd.err != nil {
			continue
		}
		for inboundID, ctr := range pd.counters {
			if err := s.recordNodeStats(ctx, pid, inboundID, ctr.up, ctr.down, sink); err != nil {
				log.Warn("traffic poll node snapshot", "panel_id", pid, "inbound_id", inboundID, "err", err)
			}
		}
	}

	mark("Phase 2a node-traffic snapshots")

	// v3.9.0 — meter shared-client traffic into the user quota totals. Once a user
	// is migrated, their traffic accrues under the shared client's email (u{uid}@),
	// which has no ownership row, so the per-node loop above misses it. Read it ONCE
	// per shared client by email from the panel's aggregate (every attached inbound
	// echoes the same figure — never sum per-inbound) and fold the monotonic delta
	// into totals. Runs UNCONDITIONALLY whenever the repo is wired: before a user
	// migrates the shared client has no 3X-UI traffic so this is a no-op for them,
	// and summing per-node + shared stays correct across the migration window.
	if len(sharedClients) > 0 {
		{
			clients := sharedClients
			// Restrict the per-panel email aggregate to shared emails (bounds memory).
			want := make(map[int64]map[string]bool)
			for _, c := range clients {
				m := want[c.PanelID]
				if m == nil {
					m = make(map[string]bool)
					want[c.PanelID] = m
				}
				m[c.Email] = true
			}
			agg := make(map[int64]map[string]inboundCounter, len(panelData))
			for pid, pd := range panelData {
				w := want[pid]
				if pd.err != nil || len(w) == 0 {
					continue
				}
				m := make(map[string]inboundCounter)
				for _, traffics := range pd.stats {
					for _, t := range traffics {
						if !w[t.Email] {
							continue
						}
						// Shared aggregate is echoed identically per inbound; max guards
						// a partial/stale echo. last-online is the most recent echo.
						cur := m[t.Email]
						if t.Up+t.Down > cur.up+cur.down {
							cur.up, cur.down = t.Up, t.Down
						}
						if t.LastOnline > cur.lastOnline {
							cur.lastOnline = t.LastOnline
						}
						m[t.Email] = cur
					}
				}
				agg[pid] = m
			}
			for _, c := range clients {
				ct, ok := agg[c.PanelID][c.Email]
				if !ok {
					continue
				}
				delta := s.recordSharedClientStats(ctx, c, ct.up, ct.down, sink)
				// Advance last_online_at even when no NEW bytes accrued this cycle —
				// the 3X-UI server-side timestamp is independent of our delta, and a
				// migrated user has no per-node pass left to source it.
				if ct.lastOnline > sink.lastOnlineMs[c.UserID] {
					sink.lastOnlineMs[c.UserID] = ct.lastOnline
				}
				if delta.up == 0 && delta.down == 0 && delta.total == 0 {
					continue
				}
				tot := totals[c.UserID]
				tot.deltaUp += delta.up
				tot.deltaDown += delta.down
				tot.deltaTotal += delta.total
				tot.hits++ // ensure recordAndEnforceWith advances lifetime for shared-only users
				totals[c.UserID] = tot
			}
		}
		mark("Phase 2b shared-client metering")
	}

	for _, u := range users {
		if skipUsers[u.ID] {
			log.Warn("traffic poll user skipped due to inbound fetch failure", "user_id", u.ID)
			continue
		}
		if err := s.recordAndEnforceWith(ctx, u, totals[u.ID], pollCfg, pollLoc, sink); err != nil {
			log.Warn("traffic poll user", "user_id", u.ID, "err", err)
		}
	}
	mark("user loop (recordAndEnforceWith — push is async post-beta.12)")

	// Per-client period-baseline reseed for users whose period rolled this
	// cycle. Mirrors the user-level u.PeriodBaselineBytes write above, one tier
	// down: each owned client's baseline becomes its current lifetime minus
	// this cycle's own delta, so this cycle's traffic counts to the NEW period
	// and Σ(client period usage) stays exactly equal to the user's period usage.
	// Runs after the user loop so every client's Lifetime is already advanced.
	for uid := range sink.rolledOver {
		cutoff := sink.userCutoff[uid] // may be nil
		for _, e := range sink.clientsByUser[uid] {
			d := sink.clientDeltas[e.ID] // zero value when this client had no delta
			// counted = the portion of d that the user-level path folded into
			// the new period. hadPrev deltas always count; a bootstrap delta
			// counts only when createdAt > cutoff (mirrors recordAndEnforceWith),
			// so a long-idle client's first transmission doesn't inflate the
			// per-node period beyond what the user-level period recorded.
			counted := d
			if !d.hadPrev && cutoff != nil && !(!e.CreatedAt.IsZero() && e.CreatedAt.After(*cutoff)) {
				counted = trafficDelta{}
			}
			e.PeriodBaselineUpBytes = nonNeg(e.LifetimeUpBytes - counted.up)
			e.PeriodBaselineDownBytes = nonNeg(e.LifetimeDownBytes - counted.down)
			e.PeriodBaselineTotalBytes = nonNeg(e.LifetimeTotalBytes - counted.total)
			// Persistence is keyed on the RAW delta, not counted: a client with a
			// non-zero raw delta is already queued in ownershipUpdates
			// (recordClientStats appended it) and its baseline rides that write
			// via the shared pointer; a zero-raw-delta client isn't queued, so
			// enqueue it now to persist the reseeded baseline.
			if d.up == 0 && d.down == 0 && d.total == 0 {
				sink.ownershipUpdates = append(sink.ownershipUpdates, e)
			}
		}
	}
	mark("period-baseline reseed (rolled-over users)")

	// Drain the sink in three batched INSERTs. Order doesn't matter — the
	// snapshots are independent — but client first so the most numerous
	// table lands while the connection is hot. Failures are logged and
	// the poll continues; losing one batch is preferable to crashing the
	// scheduler (subsequent polls will resnapshot).
	if len(sink.clientSnaps) > 0 {
		if err := s.traffic.InsertClientBatch(ctx, sink.clientSnaps); err != nil {
			log.Warn("traffic poll flush client snapshots", "count", len(sink.clientSnaps), "err", err)
		}
	}
	if len(sink.userSnaps) > 0 {
		if err := s.traffic.InsertBatch(ctx, sink.userSnaps); err != nil {
			log.Warn("traffic poll flush user snapshots", "count", len(sink.userSnaps), "err", err)
		}
	}
	if len(sink.nodeCounterUpdates) > 0 {
		nodes := make([]*domain.Node, 0, len(sink.nodeCounterUpdates))
		for _, n := range sink.nodeCounterUpdates {
			nodes = append(nodes, n)
		}
		if err := s.nodes.BatchUpdateTrafficCounters(ctx, nodes); err != nil {
			log.Warn("traffic poll flush node counters", "count", len(nodes), "err", err)
		}
	}
	if len(sink.nodeSnaps) > 0 && s.nodeTraffic != nil {
		if err := s.nodeTraffic.InsertBatch(ctx, sink.nodeSnaps); err != nil {
			log.Warn("traffic poll flush node snapshots", "count", len(sink.nodeSnaps), "err", err)
		}
	}
	// v3.5.0-beta.9: collapse per-row counter / state UPDATEs into one
	// transaction-wrapped batch each. On SQLite this is the difference
	// between N + N×M ~5–10ms WAL commits and two single commits —
	// "Poll Now" wall time drops by an order of magnitude on a panel
	// with non-trivial scale. MySQL/Postgres get the round-trip win.
	//
	// Failure semantics match the snapshot flushes above: log + continue.
	// A skipped flush means this cycle's counters / state aren't persisted,
	// the next cycle re-derives them (LastRawXxx untouched, so monotonicDelta
	// still produces the right increment) and writes again.
	if len(sink.ownershipUpdates) > 0 {
		if err := s.ownership.BatchUpdateCounters(ctx, sink.ownershipUpdates); err != nil {
			log.Warn("traffic poll flush ownership counters", "count", len(sink.ownershipUpdates), "err", err)
		}
	}
	if len(sink.pspClientUpdates) > 0 && s.pspClient != nil {
		if err := s.pspClient.BatchUpdateCounters(ctx, sink.pspClientUpdates); err != nil {
			log.Warn("traffic poll flush shared-client counters", "count", len(sink.pspClientUpdates), "err", err)
		}
	}
	if len(sink.userUpdates) > 0 {
		// Local name `pending` avoids shadowing the outer `users` (the list
		// loaded at the top of PollOnce). Iteration order is non-deterministic
		// (map) but harmless: rows in the batch are independent.
		pending := make([]*domain.User, 0, len(sink.userUpdates))
		for _, u := range sink.userUpdates {
			pending = append(pending, u)
		}
		if err := s.users.BatchUpdateTrafficState(ctx, pending); err != nil {
			log.Warn("traffic poll flush user traffic state", "count", len(pending), "err", err)
		}
	}
	// v3.6.0-beta.4: convert per-user ms-since-epoch → time.Time on the way
	// to the repo so callers don't need to know 3X-UI's wire unit. Done as
	// its own batch (rather than folding into BatchUpdateTrafficState) so
	// the per-cycle behaviour stays orthogonal: a panel running pre-3.1.0
	// 3X-UI reports 0 lastOnline for everyone → sink.lastOnlineMs stays
	// empty → no UPDATE issued → existing last_online_at survives.
	if len(sink.lastOnlineMs) > 0 {
		toWrite := make(map[int64]time.Time, len(sink.lastOnlineMs))
		for uid, ms := range sink.lastOnlineMs {
			if ms > 0 {
				toWrite[uid] = time.UnixMilli(ms)
			}
		}
		if len(toWrite) > 0 {
			if err := s.users.BatchUpdateLastOnline(ctx, toWrite); err != nil {
				log.Warn("traffic poll flush last_online_at", "count", len(toWrite), "err", err)
			}
		}
	}
	mark("sink flush (6 batches)")
	return nil
}

func markSkippedUsers(skipUsers map[int64]bool, refs []ownershipRef) {
	for _, ref := range refs {
		skipUsers[ref.userID] = true
	}
}

func (s *Service) listAllUsers(ctx context.Context) ([]*domain.User, error) {
	out := []*domain.User{}
	page := 1
	const pageSize = 100
	for {
		users, total, err := s.users.List(ctx, ports.UserFilter{
			Pagination: ports.Pagination{Page: page, PageSize: pageSize},
		})
		if err != nil {
			return nil, fmt.Errorf("list users: %w", err)
		}
		out = append(out, users...)
		if int64(page*pageSize) >= total {
			break
		}
		page++
	}
	return out, nil
}

type trafficTotals struct {
	up         int64
	down       int64
	deltaUp    int64
	deltaDown  int64
	deltaTotal int64
	bootstrap  []bootstrapClientDelta
	hits       int
}

type trafficDelta struct {
	up      int64
	down    int64
	total   int64
	hadPrev bool
}

type bootstrapClientDelta struct {
	delta     trafficDelta
	createdAt time.Time
}

// pollSink collects per-poll snapshot writes so PollOnce can flush them
// in batch at the end of the cycle rather than per-event. Non-nil while
// the poll cycle owns it; record* helpers fall back to single-row
// Insert when sink is nil so non-poll callers (tests, ad-hoc) keep
// working without a sink object. Sink is poll-scoped + accessed
// sequentially by the user loop, no locking needed.
//
// v3.5.0-beta.9 added the ownership / user update buckets + the pre-fetched
// latestByUser map. The per-cycle DB write count used to be O(N users + N×M
// clients) inline UPDATEs (each its own ~5–10ms SQLite WAL commit); the sink
// reduces it to two end-of-cycle BatchUpdate calls plus the snapshot inserts
// — at "100 users × 8 clients = 900 ops" scale, ~10s → ~200ms.
type pollSink struct {
	userSnaps   []*domain.TrafficSnapshot
	clientSnaps []*domain.ClientTrafficSnapshot
	nodeSnaps   []*domain.NodeTrafficSnapshot
	// nodeCounterUpdates buffers per-node lifetime-counter writes from the
	// per-inbound node accounting; flushed via BatchUpdateTrafficCounters at
	// end-of-cycle. Keyed by node ID (each inbound maps to a unique node, so in
	// practice one entry per touched node), mirroring userUpdates: the pointer
	// state at flush time wins. Pre-fix this was an inline UPDATE per inbound.
	nodeCounterUpdates map[int64]*domain.Node
	// ownershipUpdates buffers per-client counter writes; flushed via
	// BatchUpdateCounters at end-of-cycle. Each XUIClientEntry has a
	// unique ID so no dedup is needed — the slice is one append per
	// client that produced a non-zero delta this cycle.
	ownershipUpdates []*domain.XUIClientEntry
	// pspClientUpdates buffers per-shared-client counter writes (v3.9.0 Stage 3);
	// flushed via PSPClientRepo.BatchUpdateCounters at end-of-cycle. One append per
	// shared client that produced a non-zero delta this cycle.
	pspClientUpdates []*domain.PSPClient
	// userUpdates buffers per-user traffic-state writes from the snapshot
	// hot path; flushed via BatchUpdateTrafficState at end-of-cycle. Keyed
	// by user ID so repeated appends for the same user collapse into ONE
	// write — the pointer state at flush time wins. The rollover branch
	// (persistRollover) deliberately bypasses the sink and writes
	// synchronously, then deletes itself from this map, because the
	// immediately-following re-enable does a stale-sensitive GetByID.
	userUpdates map[int64]*domain.User
	// latestByUser is the per-cycle pre-fetched latest snapshot per user,
	// loaded ONCE via TrafficRepo.LatestForUsers at the top of PollOnce.
	// recordAndEnforceWith reads this map instead of issuing a per-user
	// LatestForUser SELECT. Absence means "no prior snapshot" (same
	// semantics as LatestForUser returning ErrNotFound).
	latestByUser map[int64]*domain.TrafficSnapshot
	// lastOnlineMs holds the per-user max(clientStats.lastOnline) across
	// every owned client seen this cycle, in 3X-UI's unit (unix ms).
	// Flushed end-of-cycle via BatchUpdateLastOnline. Map keying naturally
	// dedups multiple appends for the same user across panels — we always
	// keep the max. Zero values are never inserted (3X-UI reports 0 for
	// "never seen", which has no useful UI signal — the panel just shows
	// "—" / "未活跃" in that case).
	lastOnlineMs map[int64]int64
	// clientDeltas records this cycle's per-client delta keyed by ownership
	// row ID. Populated in Phase 2 from recordClientStats' return. Read by the
	// end-of-cycle period-baseline pass so a rolled-over client's baseline is
	// set to lifetime-minus-this-cycle's-delta (matching the user-level
	// rollover, which subtracts totals.deltaTotal — keeps Σ per-client period
	// usage equal to the user's period usage). Absent key = no delta this cycle.
	clientDeltas map[int64]trafficDelta
	// rolledOver flags users whose traffic period advanced this cycle (set in
	// recordAndEnforceWith). The post-loop baseline pass reseeds those users'
	// per-client period baselines. Keyed by user ID.
	rolledOver map[int64]bool
	// clientsByUser is every owned ownership row this cycle bucketed by user —
	// the post-loop baseline pass needs all of a rolled-over user's clients,
	// including ones 3X-UI didn't return this cycle. Built once before the
	// user loop; entry pointers are shared with byInbound so their Lifetime
	// fields are already current by the time the pass runs.
	clientsByUser map[int64][]*domain.XUIClientEntry
	// userCutoff captures the bootstrap-delta cutoff recordAndEnforceWith used
	// this cycle (LifetimeBaselineAt-before-update, or the prev snapshot time),
	// per user. The baseline reseed needs it to count a bootstrap client's delta
	// toward the new period ONLY when the user-level path also counted it
	// (createdAt > cutoff) — otherwise Σ(per-client period) drifts from the
	// user's period for a long-provisioned-but-idle client's first transmission.
	// A nil value (or absent key) means "no cutoff" (count every bootstrap).
	userCutoff map[int64]*time.Time
}

// recordClientStats reconciles one client's raw 3X-UI counter against the
// last-observed baseline stored on its ownership row.
//
// In v3 the snapshot table semantics flipped: previously it stored raw
// cumulative counters and the next poll diffed against the latest snapshot;
// now lifetime accumulates on the ownership row (mirroring users / nodes)
// and the snapshot stores lifetime, consistent across all three snapshot
// tables. The baseline for the monotonicDelta computation moves from
// "previous snapshot's TotalBytes" to "ownership.LastRawXxx" — narrower,
// no extra SELECT, and the lifetime counter is directly queryable from SQL.
//
// Counter-reset handling is unchanged: a Xray restart sends current_raw
// below LastRaw and monotonicDelta falls back to current as the delta.
//
// The bootstrap path (LastRawXxx all zero, no prior poll) treats the current
// cumulative as the full delta — matches pre-v3 semantics for a newly
// imported client.
func (s *Service) recordClientStats(ctx context.Context, ownership *domain.XUIClientEntry, up, down int64, sink *pollSink) (trafficDelta, error) {
	totalBytes := up + down

	hadPrev := ownership.LastRawUpBytes != 0 ||
		ownership.LastRawDownBytes != 0 ||
		ownership.LastRawTotalBytes != 0

	var deltaUp, deltaDown, deltaTotal int64
	if hadPrev {
		deltaUp = monotonicDelta(up, ownership.LastRawUpBytes)
		deltaDown = monotonicDelta(down, ownership.LastRawDownBytes)
		deltaTotal = monotonicDelta(totalBytes, ownership.LastRawTotalBytes)
	} else {
		// First observation — current cumulative IS the delta.
		deltaUp, deltaDown, deltaTotal = up, down, totalBytes
	}

	// Zero-delta short-circuit (P1-2): an offline / idle client returns the
	// same raw counters every poll. Skip both the ownership write AND the
	// snapshot write so those cycles are pure no-ops. At typical activity
	// ratios (20-30% of users actually consuming traffic in any given
	// 5-minute window) this drops `client_traffic_snapshots` write volume
	// to roughly one third.
	//
	// The check intentionally covers BOTH branches (hadPrev / !hadPrev):
	// a freshly-imported client that hasn't transmitted yet has raw
	// counters of 0 and no prior LastRaw, so without this we'd write a
	// zero-valued lifetime snapshot every cycle forever (LastRawXxx
	// stays 0 → hadPrev never flips true → bootstrap path repeats). We
	// preserve `hadPrev` in the returned delta verbatim so the caller's
	// bootstrap-vs-steady-state classification at PollOnce stays correct.
	if deltaUp == 0 && deltaDown == 0 && deltaTotal == 0 {
		return trafficDelta{hadPrev: hadPrev}, nil
	}

	// Accumulate lifetime + advance the raw baseline. Both writes go in one
	// repo call so a process crash between the two can't leak counter drift.
	ownership.LifetimeUpBytes += deltaUp
	ownership.LifetimeDownBytes += deltaDown
	ownership.LifetimeTotalBytes += deltaTotal
	ownership.LastRawUpBytes = up
	ownership.LastRawDownBytes = down
	ownership.LastRawTotalBytes = totalBytes
	// Sink-aware write: PollOnce collects per-client counter updates into the
	// sink and flushes them as ONE BatchUpdateCounters at end-of-cycle. The
	// nil-sink path keeps the inline single-row write for non-poll callers
	// (tests, ad-hoc) so this helper stays usable outside the poll loop.
	if sink != nil {
		sink.ownershipUpdates = append(sink.ownershipUpdates, ownership)
	} else if err := s.ownership.UpdateCounters(ctx, ownership); err != nil {
		return trafficDelta{}, fmt.Errorf("update ownership counters: %w", err)
	}

	// Snapshot stores lifetime (consistent with traffic_snapshots /
	// node_traffic_snapshots).
	snap := &domain.ClientTrafficSnapshot{
		UserID:      ownership.UserID,
		PanelID:     ownership.PanelID,
		InboundID:   ownership.InboundID,
		ClientEmail: ownership.ClientEmail,
		UpBytes:     ownership.LifetimeUpBytes,
		DownBytes:   ownership.LifetimeDownBytes,
		TotalBytes:  ownership.LifetimeTotalBytes,
		CapturedAt:  time.Now(),
	}
	if sink != nil {
		sink.clientSnaps = append(sink.clientSnaps, snap)
	} else if err := s.traffic.InsertClient(ctx, snap); err != nil {
		return trafficDelta{}, fmt.Errorf("insert client snapshot: %w", err)
	}
	return trafficDelta{up: deltaUp, down: deltaDown, total: deltaTotal, hadPrev: hadPrev}, nil
}

// recordSharedClientStats folds one shared client's (up, down) — read ONCE by
// email from the panel's aggregate counter (every attached inbound echoes the
// same figure; summing per-inbound would double-count) — into its psp_client
// lifetime, advancing the raw baseline. Mirror of recordClientStats but for the
// v3.9.0 shared client; returns the monotonic delta so the caller folds it into
// the user's quota total. No per-inbound ClientTrafficSnapshot is written — a
// shared client spans many inbounds (the InboundID would be ambiguous) and
// per-server usage is sourced from node counters, not per-client snapshots.
func (s *Service) recordSharedClientStats(ctx context.Context, c *domain.PSPClient, up, down int64, sink *pollSink) trafficDelta {
	totalBytes := up + down
	hadPrev := c.LastRawUpBytes != 0 || c.LastRawDownBytes != 0 || c.LastRawTotalBytes != 0

	// Seed-on-first-observation: adopt the current counter as the baseline with a
	// ZERO delta. Unlike the ownership bootstrap path (which folds the first
	// cumulative into lifetime), a shared client may be read mid-stream with a
	// non-zero counter (e.g. the gate flipped a few minutes before this poll), so
	// counting that whole figure at once would spike the user's quota. We give up
	// at most one poll-interval of the very first reading to stay spike-proof —
	// the same trade-off as the node-counter LastInbound seed.
	if !hadPrev {
		if up == 0 && down == 0 {
			return trafficDelta{} // genuinely idle — nothing to seed, no write
		}
		c.LastRawUpBytes, c.LastRawDownBytes, c.LastRawTotalBytes = up, down, totalBytes
		s.flushSharedCounters(ctx, c, sink)
		return trafficDelta{}
	}

	deltaUp := monotonicDelta(up, c.LastRawUpBytes)
	deltaDown := monotonicDelta(down, c.LastRawDownBytes)
	deltaTotal := monotonicDelta(totalBytes, c.LastRawTotalBytes)
	if deltaUp == 0 && deltaDown == 0 && deltaTotal == 0 {
		return trafficDelta{hadPrev: true} // idle client → pure no-op (no write)
	}

	c.LifetimeUpBytes += deltaUp
	c.LifetimeDownBytes += deltaDown
	c.LifetimeTotalBytes += deltaTotal
	c.LastRawUpBytes, c.LastRawDownBytes, c.LastRawTotalBytes = up, down, totalBytes
	s.flushSharedCounters(ctx, c, sink)
	return trafficDelta{up: deltaUp, down: deltaDown, total: deltaTotal, hadPrev: true}
}

// flushSharedCounters buffers the shared client's counter write into the sink
// (batched at end-of-cycle) or writes it inline for non-poll callers.
func (s *Service) flushSharedCounters(ctx context.Context, c *domain.PSPClient, sink *pollSink) {
	if sink != nil {
		sink.pspClientUpdates = append(sink.pspClientUpdates, c)
	} else if s.pspClient != nil {
		if err := s.pspClient.UpdateCounters(ctx, c); err != nil {
			log.Warn("traffic poll update shared-client counters", "client_id", c.ID, "err", err)
		}
	}
}

// recordAndEnforce is the back-compat entry preserved for tests and any
// caller that doesn't have pre-loaded poll context. Production PollOnce
// goes through recordAndEnforceWith directly so it only touches the
// settings repo once per cycle instead of once per user.
func (s *Service) recordAndEnforce(ctx context.Context, u *domain.User, totals trafficTotals) error {
	cfg := ports.UISettings{}
	if s.settings != nil {
		if loaded, err := s.settings.Load(ctx, ports.UISettings{}); err == nil {
			cfg = loaded
		}
	}
	return s.recordAndEnforceWith(ctx, u, totals, cfg, paneltz.Location(ctx, s.settings), nil)
}

// recordAndEnforceWith takes pre-loaded poll-scoped config + location so
// we don't have to re-fetch settings on every user. The PollOnce loop
// loads both ONCE at the top and threads them in here for every user.
// sink (nullable) defers snapshot INSERT to PollOnce's end-of-cycle
// batch flush; non-poll callers pass nil for immediate insert.
func (s *Service) recordAndEnforceWith(ctx context.Context, u *domain.User, totals trafficTotals, cfg ports.UISettings, loc *time.Location, sink *pollSink) error {
	if loc == nil {
		loc = time.Local
	}
	now := time.Now().In(loc)

	// Skip the snapshot entirely when 3X-UI returned no matching client rows.
	// Inserting a zero would corrupt subsequent today/period delta math.
	// Period rollover and limit enforcement still need to run, so don't
	// short-circuit the whole function — just don't write the snapshot.
	wroteSnap := false
	var snap *domain.TrafficSnapshot
	if totals.hits > 0 {
		// User lifetime must be based on per-client deltas. If one inbound's
		// counter resets while another keeps growing, a delta on the aggregate
		// raw total would be wrong.
		//
		// Hot-path read: prefer the sink's pre-fetched latestByUser map
		// populated ONCE at the top of PollOnce. Absence in the map = no
		// prior snapshot (same semantics as LatestForUser's ErrNotFound).
		// Non-poll callers pass a nil sink (or one with no pre-fetch) and
		// fall back to the per-user SELECT.
		var prev *domain.TrafficSnapshot
		if sink != nil && sink.latestByUser != nil {
			prev = sink.latestByUser[u.ID]
		} else {
			p, perr := s.traffic.LatestForUser(ctx, u.ID)
			if perr != nil && !errors.Is(perr, domain.ErrNotFound) {
				log.Warn("traffic poll latest snapshot lookup", "user_id", u.ID, "err", perr)
			} else {
				prev = p
			}
		}
		// Decide which bootstrap deltas (clients with no per-client snapshot
		// yet) we should fold into lifetime. The cutoff comes from
		// LifetimeBaselineAt when available; otherwise fall back to the last
		// aggregate snapshot timestamp; if neither exists, the user has no
		// baseline at all and every bootstrap counts.
		//
		// Using LifetimeBaselineAt instead of prev.CapturedAt fixes the edge
		// case where the snapshots table was wiped but lifetime survives —
		// previously bootstraps were silently dropped, missing the first
		// cumulative read for any genuinely-new ownership.
		var cutoff *time.Time
		switch {
		case u.LifetimeBaselineAt != nil:
			cutoff = u.LifetimeBaselineAt
		case prev != nil:
			t := prev.CapturedAt
			cutoff = &t
		}
		// Hand the cutoff to the post-loop baseline reseed so it counts a
		// bootstrap client's delta toward the new period iff this same logic
		// did (createdAt > cutoff). Copy the value — u.LifetimeBaselineAt is
		// reassigned below, and we want the pre-update cutoff.
		if sink != nil {
			if cutoff != nil {
				c := *cutoff
				sink.userCutoff[u.ID] = &c
			} else {
				sink.userCutoff[u.ID] = nil
			}
		}
		for _, b := range totals.bootstrap {
			if cutoff == nil {
				// Truly fresh user — count every cumulative read as new.
				totals.deltaUp += b.delta.up
				totals.deltaDown += b.delta.down
				totals.deltaTotal += b.delta.total
				continue
			}
			if !b.createdAt.IsZero() && b.createdAt.After(*cutoff) {
				totals.deltaUp += b.delta.up
				totals.deltaDown += b.delta.down
				totals.deltaTotal += b.delta.total
			}
			// else: ownership predates the cutoff → already in lifetime.
		}

		if u.LifetimeTotalBytes == 0 && prev != nil && prev.TotalBytes > 0 {
			// Migration/bootstrap path: old installs only have aggregate
			// snapshots. Seed lifetime from the previous user snapshot once,
			// then add per-client deltas from this poll.
			u.LifetimeUpBytes = prev.UpBytes
			u.LifetimeDownBytes = prev.DownBytes
			u.LifetimeTotalBytes = prev.TotalBytes
		}
		u.LifetimeUpBytes += totals.deltaUp
		u.LifetimeDownBytes += totals.deltaDown
		u.LifetimeTotalBytes += totals.deltaTotal
		// Always advance the baseline once we've written a successful snapshot
		// — that's the cutoff for the next poll's bootstrap-delta logic. If we
		// only updated it on dirty cycles, an ownership added during a
		// no-traffic cycle would later be classified as "before baseline" and
		// silently dropped from lifetime.
		baselineNow := now
		u.LifetimeBaselineAt = &baselineNow

		// NOTE on snapshot semantics: TotalBytes here is the user's
		// lifetime-cumulative value, NOT the raw 3X-UI counter sum. This
		// changed from the original schema (which stored raw counters). Old
		// rows written by previous code persist in the DB as raw values; the
		// migration block above seeds Lifetime from those on the first new-
		// code poll, so today/period delta math self-heals within one poll
		// cycle. Anyone reading this table directly should know it now means
		// "monotonic lifetime", which never goes backward across resets.
		snap = &domain.TrafficSnapshot{
			UserID:     u.ID,
			UpBytes:    u.LifetimeUpBytes,
			DownBytes:  u.LifetimeDownBytes,
			TotalBytes: u.LifetimeTotalBytes,
			CapturedAt: now,
		}
		if sink != nil {
			sink.userSnaps = append(sink.userSnaps, snap)
		} else if err := s.traffic.Insert(ctx, snap); err != nil {
			return fmt.Errorf("insert snapshot: %w", err)
		}
		wroteSnap = true

		// Sink-aware write: PollOnce drains userUpdates as ONE
		// BatchUpdateTrafficState at end-of-cycle. Keyed by user ID so this
		// path and the rollover-path append (below) dedupe — same pointer,
		// last-write-wins, exactly one row write per user per cycle.
		if sink != nil {
			sink.userUpdates[u.ID] = u
		} else if err := s.users.UpdateTrafficState(ctx, u); err != nil {
			log.Warn("traffic lifetime update", "user_id", u.ID, "err", err)
		}
	}

	// Roll the period if a boundary has been crossed.
	if u.TrafficPeriodStart != nil && shouldRollPeriod(now, *u.TrafficPeriodStart, u.TrafficResetPeriod) {
		periodStart := currentPeriodStart(now, u.TrafficResetPeriod)
		u.TrafficPeriodStart = &periodStart
		// Baseline = lifetime BEFORE this poll's delta. This poll's traffic
		// counts toward the NEW period (matching pre-v3 semantics where the
		// first poll after rollover sees its newly-written snapshot land
		// on/after period_start). Subtracting the just-added delta gives
		// the lifetime as it stood at the moment period_start advanced.
		// If the lifetime path was skipped this cycle (totals.hits == 0),
		// totals.deltaTotal is 0 and the subtraction is a no-op.
		u.PeriodBaselineBytes = u.LifetimeTotalBytes - totals.deltaTotal
		if u.PeriodBaselineBytes < 0 {
			u.PeriodBaselineBytes = 0
		}
		// Flag this user so the post-loop pass reseeds each owned client's
		// per-client period baseline in lockstep with the user-level one above.
		// Done out-of-band (not here) because recordAndEnforceWith only has the
		// user aggregate; the pass has every client entry + its cycle delta.
		if sink != nil {
			sink.rolledOver[u.ID] = true
		}
		clearedEmergency := u.AutoDisabledReason == domain.DisabledTrafficExceeded
		if clearedEmergency {
			u.EmergencyUntil = nil
			u.EmergencyBaselineBytes = 0
		}
		// Persist the new periodStart FIRST. SetEnabledAndSync re-reads the
		// user from the DB, so anything we change in memory after Update() is
		// lost. Without this, periodStart never actually moves and rollover
		// fires on every poll forever.
		//
		// UpdateTrafficState no longer writes the emergency columns, so this
		// period write can't clobber a window granted concurrently mid-cycle.
		// When THIS rollover ends the window (the user was traffic-disabled and
		// the new period hands quota back), clear it explicitly under the
		// emergency lock so we don't race a concurrent UseEmergencyAccess.
		persistRollover := func() {
			// Rollover MUST write synchronously, even in the sink-batched poll.
			// Reason: the immediately-following SetEnabledAndSync(true) re-enable
			// (line ~825 below) does a GetByID + full-row Update + push of the
			// per-client traffic floor. If our rolled-over lifetime / baseline /
			// periodStart are still pending in sink.userUpdates, GetByID returns
			// the OLD period state, u.PeriodUsed() computes "near the OLD limit",
			// and the floor pushed to 3X-UI is ~0 — effectively keeping the user
			// blocked for another poll cycle even though they were just
			// re-enabled. The original (pre-beta.9) inline write avoided this by
			// landing the rolled-over state in DB before the disabler ran.
			//
			// We also delete this user from the sink so the end-of-cycle batch
			// flush doesn't redundantly rewrite the same row a second time. If
			// the main-path snapshot branch ran above, it appended u to the
			// sink; that entry is superseded by this inline write (the in-memory
			// u carries the rolled-over fields already, so the inline write is
			// strictly newer).
			//
			// ClearEmergencyAccess writes a disjoint column set and MUST stay
			// inline under the emergency lock so a concurrent UseEmergencyAccess
			// can't race the clear (the v3.3.0-beta.6 invariant).
			if sink != nil {
				delete(sink.userUpdates, u.ID)
			}
			if err := s.users.UpdateTrafficState(ctx, u); err != nil {
				log.Warn("traffic period start update", "user_id", u.ID, "err", err)
			}
			if clearedEmergency {
				if err := s.users.ClearEmergencyAccess(ctx, u.ID); err != nil {
					log.Warn("traffic clear emergency (rollover)", "user_id", u.ID, "err", err)
				}
			}
		}
		if s.configPusher != nil {
			s.configPusher.WithEmergencyLock(persistRollover)
		} else {
			persistRollover()
		}
		// If they were traffic-disabled (including an active emergency access
		// window), the new period gives them quota back.
		if u.AutoDisabledReason == domain.DisabledTrafficExceeded {
			if err := s.disabler.SetEnabledAndSync(ctx, u.ID, true, domain.DisabledNone, ""); err != nil {
				log.Warn("traffic re-enable", "user_id", u.ID, "err", err)
			} else {
				u.Enabled = true
				u.AutoDisabledReason = domain.DisabledNone
				u.DisableDetail = ""
				// Tell the user their quota reset and access is back (async).
				s.notifyEnabled(u.ID, "traffic_reset", "new traffic period")
			}
		}
	}

	// Enforce limit
	emergencyActive := u.EmergencyUntil != nil && now.Before(*u.EmergencyUntil)
	// If the admin has set a per-window emergency-access quota, end the window
	// early once the user crosses it. Falling through to the normal limit
	// check below will then auto-disable them (they're still over their period
	// limit, which is why they were granted emergency access in the first
	// place).
	// Resolve THIS user's effective quota (global ⊕ group override). Gating on the
	// per-user value, not cfg's global one, is what lets a group set a quota where
	// the global is 0 (uncapped) — or vice-versa — without the poll silently using
	// the wrong cap. Only active-emergency users (a tiny subset) pay the lookup,
	// and the override set is cached, so it's a near-free merge.
	effQuotaGB := cfg.EmergencyAccessQuotaGB
	if emergencyActive && s.settings != nil {
		if eff, err := s.settings.LoadForUser(ctx, u, ports.UISettings{}); err == nil {
			effQuotaGB = eff.EmergencyAccessQuotaGB
		}
	}
	if emergencyActive && effQuotaGB > 0 {
		quotaBytes := int64(effQuotaGB * 1024 * 1024 * 1024)
		used := u.LifetimeTotalBytes - u.EmergencyBaselineBytes
		if used >= quotaBytes {
			log.Info("emergency access quota exhausted",
				"user_id", u.ID, "used_bytes", used, "quota_bytes", quotaBytes)
			// Same race story as the period-rollover path above — hold the
			// emergency lock while we clear and write so a concurrent
			// UseEmergencyAccess can't bring the window back to life.
			clearEmergency := func() {
				u.EmergencyUntil = nil
				u.EmergencyBaselineBytes = 0
				if err := s.users.ClearEmergencyAccess(ctx, u.ID); err != nil {
					log.Warn("emergency quota clear", "user_id", u.ID, "err", err)
				}
			}
			if s.configPusher != nil {
				s.configPusher.WithEmergencyLock(clearEmergency)
			} else {
				clearEmergency()
			}
			emergencyActive = false
		}
	}
	if u.TrafficLimitBytes <= 0 || !u.Enabled || !wroteSnap || emergencyActive {
		return nil
	}
	periodUsed, err := s.periodUsage(ctx, u, snap)
	if err != nil {
		return err
	}
	if periodUsed >= u.TrafficLimitBytes {
		if err := s.disabler.SetEnabledAndSync(ctx, u.ID, false, domain.DisabledTrafficExceeded, "traffic limit exceeded"); err != nil {
			return fmt.Errorf("auto-disable: %w", err)
		}
		log.Info("auto-disabled user (traffic exceeded)",
			"user_id", u.ID, "period_used", periodUsed, "limit", u.TrafficLimitBytes)
		// Notify the user (async — SMTP must not stall the poll). This is the
		// only path that produces the traffic_exhausted email; SetEnabledAndSync
		// itself never mails. Edge-triggered: the next poll short-circuits on
		// !u.Enabled, so this fires once per disable, not every cycle.
		s.notifyDisabled(u.ID, string(domain.DisabledTrafficExceeded), "traffic limit exceeded")
		return nil
	}
	// Safety net: push the remaining-bytes floor into 3X-UI so the inbound
	// itself cuts the client off if the panel is offline long enough for
	// the user to exceed their cap between polls. Best-effort: a failed
	// push is logged but does not stop the poll cycle for other users.
	//
	// v3.5.0-beta.12: two compounding wins drag this off the hot path.
	//   1. If this cycle's per-user delta is 0, the floor (= limit - used)
	//      didn't change since last push, so the 3X-UI side is still
	//      correct. Skip the push entirely. This filters out "active
	//      panel, idle user" users (clients exist + matched in
	//      ListInbounds but didn't move bytes this cycle).
	//   2. The remaining pushes fan out as fire-and-forget goroutines
	//      capped by s.pushSem so a 3X-UI panel isn't hammered. PollOnce
	//      no longer waits on the per-user push round-trip (was the
	//      dominant remaining wall-clock cost after beta.9's batch flush
	//      — N active-with-limit users × ~300ms per UpdateClient ≈
	//      several seconds, all serial under the per-user loop above).
	//
	// context.Background(): the calling PollOnce may return long before
	// the push goroutine drains. Inheriting ctx would mean a poll-handler
	// cancellation (admin closes the "Poll Now" browser tab) silently
	// aborts the push half-way. Background keeps it independent — failures
	// are logged and the next cycle's push retries naturally.
	if totals.deltaTotal == 0 || s.configPusher == nil {
		return nil
	}
	uid := u.ID
	safego.GoTracked(s.bgWG, "traffic.floor-push", func() {
		s.pushSem <- struct{}{}
		defer func() { <-s.pushSem }()
		if err := s.configPusher.PushClientConfig(context.Background(), uid); err != nil {
			log.Warn("traffic floor push failed", "user_id", uid, "err", err)
		}
	})
	return nil
}

// monotonicDelta returns the bytes added since the previous snapshot.
// A negative result means the upstream counter rolled over (Xray restart),
// in which case the current value IS the delta.
func monotonicDelta(current, previous int64) int64 {
	d := current - previous
	if d < 0 {
		return current
	}
	return d
}

// nonNeg clamps a byte count at zero — guards the period-baseline subtraction
// against a transient where a client's recorded delta exceeds its lifetime.
func nonNeg(v int64) int64 {
	if v < 0 {
		return 0
	}
	return v
}

// NodeUsageRow is one (user, node) usage line for the per-user node breakdown.
// Each managed client is exactly one (user, node) pair (the email encodes
// both), so a user's ownership rows map 1:1 to the nodes they're provisioned on.
type NodeUsageRow struct {
	NodeID      int64
	DisplayName string
	PanelID     int64
	InboundID   int
	Region      string
	ClientEmail string

	LifetimeUpBytes    int64
	LifetimeDownBytes  int64
	LifetimeTotalBytes int64
	PeriodUpBytes      int64
	PeriodDownBytes    int64
	PeriodTotalBytes   int64
	TodayUpBytes       int64
	TodayDownBytes     int64
	TodayTotalBytes    int64
}

// UserNodeUsage returns one row per node the user is provisioned on, each with
// lifetime / current-period / today up+down usage.
//
//   - lifetime: straight off the ownership row's monotonic counters.
//   - period:   lifetime − the per-client PeriodBaseline (reseeded at the
//     user's rollover); summed across rows it equals the user's period usage.
//   - today:    delta vs the last per-client snapshot before local midnight.
//     When no such snapshot exists, a client created today shows its full
//     lifetime (all of it IS today); a pre-existing client shows 0. The 0 is
//     right for the common case (idle today). It under-reports one narrow edge:
//     a client idle >= rawTrafficRetentionDays (snapshots all pruned) that
//     resumes AND is viewed the same day reads 0 despite real bytes — it
//     self-heals next day once today's snapshot becomes a valid pre-midnight
//     baseline. Lifetime/period stay correct regardless.
func (s *Service) UserNodeUsage(ctx context.Context, userID int64) ([]NodeUsageRow, error) {
	entries, err := s.ownership.ListByUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	if len(entries) == 0 {
		return []NodeUsageRow{}, nil
	}

	now := s.panelNow(ctx)
	todayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	// Best-effort: a failure here degrades "today" to the no-baseline path,
	// not the whole view. Nil-tolerant traffic repo (shouldn't be nil here).
	var todayBase map[string]*domain.ClientTrafficSnapshot
	if s.traffic != nil {
		todayBase, _ = s.traffic.LastBeforeForUserClients(ctx, userID, todayStart)
	}

	// Batch-load nodes once and index by (panel, inbound). Previously this loop
	// did a GetByPanelInbound per entry — an N+1 whose DB round-trips grew with
	// the user's node count. One List keeps it at 2 queries total (ListByUser +
	// List) no matter how many nodes the user is on; a List failure degrades
	// node identity to empty (same as the old per-call error path), not the
	// whole view.
	type nodePI struct {
		panelID   int64
		inboundID int
	}
	nodeByPI := map[nodePI]*domain.Node{}
	if s.nodes != nil {
		if all, lerr := s.nodes.List(ctx); lerr == nil {
			for _, n := range all {
				nodeByPI[nodePI{panelID: n.PanelID, inboundID: n.InboundID}] = n
			}
		}
	}

	rows := make([]NodeUsageRow, 0, len(entries))
	for _, e := range entries {
		row := NodeUsageRow{
			PanelID:            e.PanelID,
			InboundID:          e.InboundID,
			ClientEmail:        e.ClientEmail,
			LifetimeUpBytes:    e.LifetimeUpBytes,
			LifetimeDownBytes:  e.LifetimeDownBytes,
			LifetimeTotalBytes: e.LifetimeTotalBytes,
			PeriodUpBytes:      nonNeg(e.LifetimeUpBytes - e.PeriodBaselineUpBytes),
			PeriodDownBytes:    nonNeg(e.LifetimeDownBytes - e.PeriodBaselineDownBytes),
			PeriodTotalBytes:   nonNeg(e.LifetimeTotalBytes - e.PeriodBaselineTotalBytes),
		}
		if n := nodeByPI[nodePI{panelID: e.PanelID, inboundID: e.InboundID}]; n != nil {
			row.NodeID = n.ID
			row.DisplayName = n.DisplayName
			row.Region = n.Region
		}
		if base := todayBase[domain.ClientMatchKey(e.PanelID, e.InboundID, e.ClientEmail)]; base != nil {
			row.TodayUpBytes = monotonicDelta(e.LifetimeUpBytes, base.UpBytes)
			row.TodayDownBytes = monotonicDelta(e.LifetimeDownBytes, base.DownBytes)
			row.TodayTotalBytes = monotonicDelta(e.LifetimeTotalBytes, base.TotalBytes)
		} else if !e.CreatedAt.IsZero() && e.CreatedAt.Before(todayStart) {
			// Pre-existing client, no snapshot before today → idle today.
			row.TodayUpBytes, row.TodayDownBytes, row.TodayTotalBytes = 0, 0, 0
		} else {
			// Born today (or unknown creation) → all lifetime counts as today.
			row.TodayUpBytes = e.LifetimeUpBytes
			row.TodayDownBytes = e.LifetimeDownBytes
			row.TodayTotalBytes = e.LifetimeTotalBytes
		}
		rows = append(rows, row)
	}
	return rows, nil
}

// ServerUsageRow is one user's usage on a single 3X-UI server (panel) — the
// per-node rows aggregated by panel. This is the per-(user, server) view: it is
// the natural unit because a user's clients on one server share its accounting.
// Today it sums the legacy per-node ownership counters; under the v3.9.0
// shared-client model it becomes a direct per-psp_client read (one row per
// (user, panel)) yielding the identical number.
type ServerUsageRow struct {
	PanelID    int64
	ServerName string
	NodeCount  int

	LifetimeUpBytes    int64
	LifetimeDownBytes  int64
	LifetimeTotalBytes int64
	PeriodUpBytes      int64
	PeriodDownBytes    int64
	PeriodTotalBytes   int64
	TodayUpBytes       int64
	TodayDownBytes     int64
	TodayTotalBytes    int64
}

// UserServerUsage returns one user's usage broken down per SERVER (3X-UI panel):
// the per-node rows from UserNodeUsage summed by panel, with the panel's
// friendly name joined from the live pool. Rows are ordered by first appearance
// (stable). Empty when the user is on no nodes.
func (s *Service) UserServerUsage(ctx context.Context, userID int64) ([]ServerUsageRow, error) {
	// v3.9.0: prefer the shared-client source. A migrated user's usage lives in
	// psp_client (one row per (user, panel, credClass)) and they hold NO ownership
	// rows, so the legacy per-node aggregation below returns empty. Build per-server
	// rows straight from psp_client when the user has any; pre-migration users (no
	// psp_client yet) fall through to the ownership path so nothing regresses.
	if s.pspClient != nil {
		if clients, err := s.pspClient.ListByUser(ctx, userID); err == nil && len(clients) > 0 {
			return s.serverUsageFromShared(ctx, clients), nil
		}
	}
	nodeRows, err := s.UserNodeUsage(ctx, userID)
	if err != nil {
		return nil, err
	}
	if len(nodeRows) == 0 {
		return []ServerUsageRow{}, nil
	}

	byPanel := map[int64]*ServerUsageRow{}
	order := make([]int64, 0, len(nodeRows))
	for _, r := range nodeRows {
		sr, ok := byPanel[r.PanelID]
		if !ok {
			sr = &ServerUsageRow{PanelID: r.PanelID}
			byPanel[r.PanelID] = sr
			order = append(order, r.PanelID)
		}
		sr.NodeCount++
		sr.LifetimeUpBytes += r.LifetimeUpBytes
		sr.LifetimeDownBytes += r.LifetimeDownBytes
		sr.LifetimeTotalBytes += r.LifetimeTotalBytes
		sr.PeriodUpBytes += r.PeriodUpBytes
		sr.PeriodDownBytes += r.PeriodDownBytes
		sr.PeriodTotalBytes += r.PeriodTotalBytes
		sr.TodayUpBytes += r.TodayUpBytes
		sr.TodayDownBytes += r.TodayDownBytes
		sr.TodayTotalBytes += r.TodayTotalBytes
	}

	names := map[int64]string{}
	if s.pool != nil {
		for _, p := range s.pool.List() {
			names[p.ID] = p.Name
		}
	}
	out := make([]ServerUsageRow, 0, len(order))
	for _, pid := range order {
		sr := byPanel[pid]
		sr.ServerName = names[pid]
		out = append(out, *sr)
	}
	return out, nil
}

// serverUsageFromShared builds the per-(user, server) usage rows from the user's
// shared psp_client rows — the v3.9.0 source. One psp_client is per (user, panel,
// credClass); rows are aggregated by panel (a panel may carry >1 credClass). Period
// = Lifetime − PeriodBaseline (identical semantics to the legacy ownership row).
// Today is left 0: a shared client spans inbounds and no per-client daily snapshot
// is written (recordSharedClientStats), so a per-server "today" isn't reconstructable.
// NodeCount = the distinct nodes attached to the user's clients on that panel.
func (s *Service) serverUsageFromShared(ctx context.Context, clients []*domain.PSPClient) []ServerUsageRow {
	byPanel := map[int64]*ServerUsageRow{}
	nodeSet := map[int64]map[int64]struct{}{}
	order := make([]int64, 0, len(clients))
	for _, c := range clients {
		sr := byPanel[c.PanelID]
		if sr == nil {
			sr = &ServerUsageRow{PanelID: c.PanelID}
			byPanel[c.PanelID] = sr
			nodeSet[c.PanelID] = map[int64]struct{}{}
			order = append(order, c.PanelID)
		}
		sr.LifetimeUpBytes += c.LifetimeUpBytes
		sr.LifetimeDownBytes += c.LifetimeDownBytes
		sr.LifetimeTotalBytes += c.LifetimeTotalBytes
		sr.PeriodUpBytes += nonNeg(c.LifetimeUpBytes - c.PeriodBaselineUpBytes)
		sr.PeriodDownBytes += nonNeg(c.LifetimeDownBytes - c.PeriodBaselineDownBytes)
		sr.PeriodTotalBytes += nonNeg(c.LifetimeTotalBytes - c.PeriodBaselineTotalBytes)
		if atts, err := s.pspClient.ListInbounds(ctx, c.ID); err == nil {
			for _, a := range atts {
				nodeSet[c.PanelID][a.NodeID] = struct{}{}
			}
		}
	}
	names := map[int64]string{}
	if s.pool != nil {
		for _, p := range s.pool.List() {
			names[p.ID] = p.Name
		}
	}
	out := make([]ServerUsageRow, 0, len(order))
	for _, pid := range order {
		sr := byPanel[pid]
		sr.NodeCount = len(nodeSet[pid])
		sr.ServerName = names[pid]
		out = append(out, *sr)
	}
	return out
}

// recordNodeStats writes a per-node snapshot for the inbound on the given panel
// and updates the node's monotonic lifetime counters from the inbound's own
// cumulative up/down (v3.9.0). Counter resets (latest < prev) collapse to
// "delta = current value". An already-seeded node whose counter is byte-for-byte
// unchanged this cycle is idle → skipped entirely (no snapshot, no Update),
// mirroring the per-client zero-delta suppression.
func (s *Service) recordNodeStats(ctx context.Context, panelID int64, inboundID int, up, down int64, sink *pollSink) error {
	if s.nodes == nil || s.nodeTraffic == nil {
		return nil
	}
	node, err := s.nodes.GetByPanelInbound(ctx, panelID, inboundID)
	if err != nil {
		// Inbound exists in 3X-UI but not in our nodes table — could be an
		// orphan or a freshly imported one we haven't loaded yet. Skip.
		if errors.Is(err, domain.ErrNotFound) {
			return nil
		}
		return fmt.Errorf("lookup node: %w", err)
	}
	totalBytes := up + down

	// Idle short-circuit: an already-seeded node whose inbound counter is
	// identical to last poll moved no bytes → emit nothing (avoids one
	// node_traffic_snapshots row + one counter write per poll for idle inbounds,
	// which the dropped `matched>0` gate would otherwise produce). Excludes the
	// first-seed poll (handled below, must persist) and resets (up != last).
	if node.LastInboundSeeded &&
		up == node.LastInboundUpBytes &&
		down == node.LastInboundDownBytes &&
		totalBytes == node.LastInboundTotalBytes {
		return nil
	}

	var dUp, dDown, dTotal int64
	if node.LastInboundSeeded {
		dUp = monotonicDelta(up, node.LastInboundUpBytes)
		dDown = monotonicDelta(down, node.LastInboundDownBytes)
		dTotal = monotonicDelta(totalBytes, node.LastInboundTotalBytes)
	} else {
		// FIRST observation of this node's inbound counter (fresh / imported /
		// upgraded from the ≤v3.8 client-sum source). Seed the baseline with a
		// ZERO delta — never fold the inbound's pre-existing cumulative counter
		// into lifetime; PSP only counts what flows while it manages the node.
		// This makes the v3.8→v3.9 source switch spike-free for EVERY row,
		// including a node with Lifetime==0 but a large historical counter.
		dUp, dDown, dTotal = 0, 0, 0
		node.LastInboundSeeded = true
	}
	node.LifetimeUpBytes += dUp
	node.LifetimeDownBytes += dDown
	node.LifetimeTotalBytes += dTotal
	node.LastInboundUpBytes = up
	node.LastInboundDownBytes = down
	node.LastInboundTotalBytes = totalBytes

	nodeSnap := &domain.NodeTrafficSnapshot{
		NodeID:     node.ID,
		UpBytes:    node.LifetimeUpBytes,
		DownBytes:  node.LifetimeDownBytes,
		TotalBytes: node.LifetimeTotalBytes,
		CapturedAt: time.Now(),
	}
	if sink != nil {
		sink.nodeSnaps = append(sink.nodeSnaps, nodeSnap)
		// Buffer the lifetime-counter write for the end-of-cycle batch flush
		// instead of an inline UPDATE per inbound. Keyed by node ID so multiple
		// inbounds on the same node collapse to one write (the pointer's state
		// at flush time wins, exactly like userUpdates).
		sink.nodeCounterUpdates[node.ID] = node
	} else {
		if err := s.nodeTraffic.Insert(ctx, nodeSnap); err != nil {
			return fmt.Errorf("insert node snapshot: %w", err)
		}
		if err := s.nodes.UpdateTrafficCounters(ctx, node); err != nil {
			log.Warn("node traffic lifetime update", "node_id", node.ID, "err", err)
		}
	}
	return nil
}

// NodeReport summarises one node's traffic for the dashboard.
type NodeReport struct {
	NodeID              int64
	PermanentTotalBytes int64
	PeriodUsedBytes     int64
	TodayUsedBytes      int64
}

// NodeReportForNodes is the batched form of NodeReportFor for the
// admin /traffic/nodes/top dashboard. Caller passes already-loaded
// node rows; this does ONE LatestForNodes + TWO LastBeforeForNodes
// (today + month) regardless of how many nodes are in the slice.
// Pre-fix the dashboard ran 3 SELECTs per node serially.
func (s *Service) NodeReportForNodes(ctx context.Context, nodes []*domain.Node) map[int64]*NodeReport {
	out := make(map[int64]*NodeReport, len(nodes))
	if len(nodes) == 0 || s.nodeTraffic == nil {
		// Still seed with lifetime so callers can populate the
		// "no snapshots yet" cells from the node row.
		for _, n := range nodes {
			out[n.ID] = &NodeReport{NodeID: n.ID, PermanentTotalBytes: n.LifetimeTotalBytes}
		}
		return out
	}
	ids := make([]int64, len(nodes))
	for i, n := range nodes {
		ids[i] = n.ID
	}
	latest, _ := s.nodeTraffic.LatestForNodes(ctx, ids)
	now := s.panelNow(ctx)
	todayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
	todayBase, _ := s.nodeTraffic.LastBeforeForNodes(ctx, ids, todayStart)
	monthBase, _ := s.nodeTraffic.LastBeforeForNodes(ctx, ids, monthStart)
	for _, n := range nodes {
		report := &NodeReport{NodeID: n.ID, PermanentTotalBytes: n.LifetimeTotalBytes}
		l := latest[n.ID]
		if l == nil {
			out[n.ID] = report
			continue
		}
		if report.PermanentTotalBytes == 0 {
			report.PermanentTotalBytes = l.TotalBytes
		}
		if base := todayBase[n.ID]; base != nil {
			report.TodayUsedBytes = monotonicDelta(l.TotalBytes, base.TotalBytes)
		} else {
			report.TodayUsedBytes = l.TotalBytes
		}
		if base := monthBase[n.ID]; base != nil {
			report.PeriodUsedBytes = monotonicDelta(l.TotalBytes, base.TotalBytes)
		} else {
			report.PeriodUsedBytes = l.TotalBytes
		}
		out[n.ID] = report
	}
	return out
}

// NodeReportFor returns the lifetime / current-period / today usage for one
// node. Lifetime comes from the monotonic node counter; today / period are
// computed as deltas with reset-clamping.
func (s *Service) NodeReportFor(ctx context.Context, nodeID int64) (*NodeReport, error) {
	report := &NodeReport{NodeID: nodeID}
	if s.nodes != nil {
		if n, nerr := s.nodes.GetByID(ctx, nodeID); nerr == nil {
			report.PermanentTotalBytes = n.LifetimeTotalBytes
		}
	}
	if s.nodeTraffic == nil {
		return report, nil
	}

	latest, err := s.nodeTraffic.LatestForNode(ctx, nodeID)
	if err != nil || latest == nil {
		return report, nil
	}
	// Pre-migration / freshly imported node: lifetime not yet seeded but a
	// snapshot exists. Mirror ReportFor's fallback so the dashboard doesn't
	// show 0 cumulative alongside non-zero today/period.
	if report.PermanentTotalBytes == 0 {
		report.PermanentTotalBytes = latest.TotalBytes
	}

	now := s.panelNow(ctx)
	todayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	if base, err := s.nodeTraffic.LastBefore(ctx, nodeID, todayStart); err == nil && base != nil {
		report.TodayUsedBytes = monotonicDelta(latest.TotalBytes, base.TotalBytes)
	} else {
		report.TodayUsedBytes = latest.TotalBytes
	}

	// Period for nodes follows the calendar month — there's no per-node reset
	// configuration. Start of the current month.
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
	if base, err := s.nodeTraffic.LastBefore(ctx, nodeID, monthStart); err == nil && base != nil {
		report.PeriodUsedBytes = monotonicDelta(latest.TotalBytes, base.TotalBytes)
	} else {
		report.PeriodUsedBytes = latest.TotalBytes
	}
	return report, nil
}

// NodeHistoryReport mirrors HistoryReport but for nodes.
type NodeHistoryReport struct {
	NodeID int64
	Period HistoryPeriod
	Since  string
	Until  string
	Items  []HistoryItem
}

// NodeHistoryFor returns a per-bucket history of cumulative-counter deltas
// for one node. Reuses the same bucketing helpers as HistoryFor.
func (s *Service) NodeHistoryFor(ctx context.Context, nodeID int64, period HistoryPeriod, since, until time.Time) (*NodeHistoryReport, error) {
	period, err := normalizeHistoryPeriod(period)
	if err != nil {
		return nil, err
	}
	since = startOfDay(since)
	until = startOfDay(until)
	if until.Before(since) {
		return nil, fmt.Errorf("%w: until must be on or after since", domain.ErrValidation)
	}
	untilExclusive := until.AddDate(0, 0, 1)

	// Served from the node hourly rollup (see HistoryFor for the tier/TZ rationale).
	var hourly []domain.HourlyTraffic
	if s.nodeTraffic != nil {
		var lerr error
		hourly, lerr = s.nodeTraffic.ListHourlyByNode(ctx, nodeID, since, untilExclusive)
		if lerr != nil {
			return nil, lerr
		}
	}
	items := bucketizeHourly(hourly, period, since, untilExclusive)

	return &NodeHistoryReport{
		NodeID: nodeID,
		Period: period,
		Since:  since.Format("2006-01-02"),
		Until:  until.Format("2006-01-02"),
		Items:  items,
	}, nil
}

// NodesHistoryForAll is the all-scope variant of NodeHistoryFor: it sums every
// node's hourly buckets in ONE GROUP BY query (SumHourlyAllNodes) and bucketizes
// once, replacing the admin handler's per-node NodeHistoryFor N+1.
func (s *Service) NodesHistoryForAll(ctx context.Context, period HistoryPeriod, since, until time.Time) (*NodeHistoryReport, error) {
	period, err := normalizeHistoryPeriod(period)
	if err != nil {
		return nil, err
	}
	since = startOfDay(since)
	until = startOfDay(until)
	if until.Before(since) {
		return nil, fmt.Errorf("%w: until must be on or after since", domain.ErrValidation)
	}
	untilExclusive := until.AddDate(0, 0, 1)
	var hourly []domain.HourlyTraffic
	if s.nodeTraffic != nil {
		var lerr error
		hourly, lerr = s.nodeTraffic.SumHourlyAllNodes(ctx, since, untilExclusive)
		if lerr != nil {
			return nil, lerr
		}
	}
	return &NodeHistoryReport{
		Period: period,
		Since:  since.Format("2006-01-02"),
		Until:  until.Format("2006-01-02"),
		Items:  bucketizeHourly(hourly, period, since, untilExclusive),
	}, nil
}

// periodUsage returns bytes used since the user's current period start.
// O(1): lifetime - baseline, no DB read. PeriodBaselineBytes is updated on
// period rollover (see recordAndEnforceWith) so subtracting it from the
// monotonic lifetime gives this-period usage without any timeline scan.
//
// The `latest` and `ctx` arguments are preserved so signature compatibility
// with old test callers is unchanged, but neither is consulted anymore.
func (s *Service) periodUsage(ctx context.Context, u *domain.User, latest *domain.TrafficSnapshot) (int64, error) {
	_ = ctx
	_ = latest
	if u == nil {
		return 0, nil
	}
	return u.PeriodUsed(), nil
}

func shouldRollPeriod(now, periodStart time.Time, period domain.ResetPeriod) bool {
	switch period {
	case domain.ResetMonthly:
		return now.Year() != periodStart.Year() || now.Month() != periodStart.Month()
	case domain.ResetQuarterly:
		nowQ := (int(now.Month()) - 1) / 3
		psQ := (int(periodStart.Month()) - 1) / 3
		return now.Year() != periodStart.Year() || nowQ != psQ
	case domain.ResetYearly:
		return now.Year() != periodStart.Year()
	}
	return false
}

func currentPeriodStart(now time.Time, period domain.ResetPeriod) time.Time {
	switch period {
	case domain.ResetMonthly:
		return time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
	case domain.ResetQuarterly:
		month := time.Month(((int(now.Month())-1)/3)*3 + 1)
		return time.Date(now.Year(), month, 1, 0, 0, 0, 0, now.Location())
	case domain.ResetYearly:
		return time.Date(now.Year(), time.January, 1, 0, 0, 0, 0, now.Location())
	default:
		return now
	}
}

// UsageReport summarises a single user's traffic for the dashboard.
type UsageReport struct {
	UserID              int64
	PermanentTotalBytes int64
	PeriodUsedBytes     int64
	TodayUsedBytes      int64
}

type HistoryPeriod string

const (
	HistoryHour  HistoryPeriod = "hour"
	HistoryDay   HistoryPeriod = "day"
	HistoryWeek  HistoryPeriod = "week"
	HistoryMonth HistoryPeriod = "month"
)

type HistoryItem struct {
	Date       string
	UpBytes    int64
	DownBytes  int64
	TotalBytes int64
}

type HistoryReport struct {
	UserID int64
	Period HistoryPeriod
	Since  string
	Until  string
	Items  []HistoryItem
}

// ReportFor returns the lifetime / current-period / today usage for one user.
//
// Lifetime is read from the user row (monotonic, accumulated by the poll
// worker) so an Xray restart that resets 3X-UI's counters can't roll it back.
// Today / period are computed as deltas against earlier snapshots and are
// clamped to non-negative; if the cumulative counter dropped (counter reset)
// the current cumulative value IS the bytes-since-reset, so we report that.
// ReportForUsers is the batched form of ReportFor used by admin
// dashboard endpoints (top-N, history aggregations) that pre-fix would
// loop ReportFor over every user — at 100 users that meant 200+
// round-trips for one /traffic/top click. Caller passes already-loaded
// user rows; this method does ONE LatestForUsers + ONE LastBeforeForUsers
// regardless of how many users are in the slice, then derives the
// report in memory.
func (s *Service) ReportForUsers(ctx context.Context, users []*domain.User) map[int64]*UsageReport {
	out := make(map[int64]*UsageReport, len(users))
	if len(users) == 0 {
		return out
	}
	ids := make([]int64, len(users))
	for i, u := range users {
		ids[i] = u.ID
	}
	latest, _ := s.traffic.LatestForUsers(ctx, ids)
	now := s.panelNow(ctx)
	todayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	lastBefore, _ := s.traffic.LastBeforeForUsers(ctx, ids, todayStart)
	for _, u := range users {
		report := &UsageReport{
			UserID:              u.ID,
			PermanentTotalBytes: u.LifetimeTotalBytes,
		}
		l := latest[u.ID]
		if l == nil {
			out[u.ID] = report
			continue
		}
		// Pre-migration fallback (matches the single-user form's logic).
		if report.PermanentTotalBytes == 0 {
			report.PermanentTotalBytes = l.TotalBytes
		}
		if base := lastBefore[u.ID]; base != nil {
			report.TodayUsedBytes = monotonicDelta(l.TotalBytes, base.TotalBytes)
		} else {
			report.TodayUsedBytes = l.TotalBytes
		}
		// v3: PeriodUsed is O(1) lifetime - baseline. Same fallback as
		// the single-user form for legacy rows without a seeded baseline.
		if u.TrafficPeriodStart != nil && u.PeriodBaselineBytes > 0 {
			report.PeriodUsedBytes = u.PeriodUsed()
		} else {
			report.PeriodUsedBytes = l.TotalBytes
		}
		out[u.ID] = report
	}
	return out
}

func (s *Service) ReportFor(ctx context.Context, userID int64) (*UsageReport, error) {
	report := &UsageReport{UserID: userID}
	u, uerr := s.users.GetByID(ctx, userID)
	if uerr == nil {
		report.PermanentTotalBytes = u.LifetimeTotalBytes
	}

	latest, err := s.traffic.LatestForUser(ctx, userID)
	if err != nil || latest == nil {
		return report, nil
	}
	// Pre-migration fallback: a user whose poll worker hasn't run yet under
	// the new code has Lifetime=0 but might already have aggregate snapshots
	// from before. Show the snapshot's cumulative as a stand-in until the
	// next poll seeds Lifetime properly.
	if report.PermanentTotalBytes == 0 {
		report.PermanentTotalBytes = latest.TotalBytes
	}

	now := s.panelNow(ctx)
	todayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	if base, err := s.traffic.LastBefore(ctx, userID, todayStart); err == nil && base != nil {
		report.TodayUsedBytes = monotonicDelta(latest.TotalBytes, base.TotalBytes)
	} else {
		report.TodayUsedBytes = latest.TotalBytes
	}

	if uerr == nil {
		// v3: PeriodUsed is O(1) lifetime - baseline. Fall through to the
		// snapshot-based path only if PeriodBaselineBytes hasn't been seeded
		// yet (legacy row from before the v3 backfill) — then latest acts as
		// "everything is in this period" which matches pre-v3 fallback.
		if u.TrafficPeriodStart != nil && u.PeriodBaselineBytes > 0 {
			report.PeriodUsedBytes = u.PeriodUsed()
		} else {
			report.PeriodUsedBytes = latest.TotalBytes
		}
	}
	return report, nil
}

func (s *Service) HistoryFor(ctx context.Context, userID int64, period HistoryPeriod, since, until time.Time) (*HistoryReport, error) {
	period, err := normalizeHistoryPeriod(period)
	if err != nil {
		return nil, err
	}
	since = startOfDay(since)
	until = startOfDay(until)
	if until.Before(since) {
		return nil, fmt.Errorf("%w: until must be on or after since", domain.ErrValidation)
	}
	untilExclusive := until.AddDate(0, 0, 1)

	// Charts are served from the hourly rollup, NOT the raw 5-min table (raw is
	// kept only ~7 days; hourly out to TrafficHistoryDays). Each hourly row is
	// the consumed up/down/total within a UTC hour, so a chart bucket's traffic
	// is the SUM of the hourly deltas whose UTC start falls inside it.
	hourly, err := s.traffic.ListHourlyByUser(ctx, userID, since, untilExclusive)
	if err != nil {
		return nil, err
	}

	return &HistoryReport{
		UserID: userID,
		Period: period,
		Since:  since.Format("2006-01-02"),
		Until:  until.Format("2006-01-02"),
		Items:  bucketizeHourly(hourly, period, since, untilExclusive),
	}, nil
}

// HistoryForAll is the all-scope variant of HistoryFor: it sums every user's
// hourly buckets in ONE GROUP BY query (SumHourlyAllUsers) and bucketizes once,
// instead of the admin handler calling HistoryFor per user (an N+1). Returns the
// same period-bucketed Items as a single HistoryFor would over the same range.
func (s *Service) HistoryForAll(ctx context.Context, period HistoryPeriod, since, until time.Time) (*HistoryReport, error) {
	period, err := normalizeHistoryPeriod(period)
	if err != nil {
		return nil, err
	}
	since = startOfDay(since)
	until = startOfDay(until)
	if until.Before(since) {
		return nil, fmt.Errorf("%w: until must be on or after since", domain.ErrValidation)
	}
	untilExclusive := until.AddDate(0, 0, 1)
	hourly, err := s.traffic.SumHourlyAllUsers(ctx, since, untilExclusive)
	if err != nil {
		return nil, err
	}
	return &HistoryReport{
		Period: period,
		Since:  since.Format("2006-01-02"),
		Until:  until.Format("2006-01-02"),
		Items:  bucketizeHourly(hourly, period, since, untilExclusive),
	}, nil
}

// bucketizeHourly folds pre-aggregated hourly delta rows (sorted by UTC
// bucket_start ASC) into the chart's [since, untilExclusive) buckets at the
// requested period granularity. Buckets are computed in the since/until
// location (the caller's TZ), and each hourly row is attributed to the chart
// bucket whose [start,end) contains its UTC start instant — summing additive
// deltas, no cumulative threading. TZ-correct for integer-offset zones; a
// half-hour-offset zone can mis-attribute at most one hour at a day boundary
// (inherent to hourly storage, the documented rollup trade-off).
func bucketizeHourly(hourly []domain.HourlyTraffic, period HistoryPeriod, since, untilExclusive time.Time) []HistoryItem {
	items := make([]HistoryItem, 0)
	idx := 0
	for bucketStart := bucketStartFor(since, period); bucketStart.Before(untilExclusive); bucketStart = nextBucketStart(bucketStart, period) {
		bucketEnd := nextBucketStart(bucketStart, period)
		if bucketEnd.After(untilExclusive) {
			bucketEnd = untilExclusive
		}
		var up, down, total int64
		for idx < len(hourly) && hourly[idx].BucketStart.Before(bucketEnd) {
			if !hourly[idx].BucketStart.Before(bucketStart) {
				up += hourly[idx].UpBytes
				down += hourly[idx].DownBytes
				total += hourly[idx].TotalBytes
			}
			idx++
		}
		items = append(items, HistoryItem{
			Date:       bucketLabel(bucketStart, period),
			UpBytes:    up,
			DownBytes:  down,
			TotalBytes: total,
		})
	}
	return items
}

func normalizeHistoryPeriod(period HistoryPeriod) (HistoryPeriod, error) {
	switch period {
	case "", HistoryDay:
		return HistoryDay, nil
	case HistoryHour, HistoryWeek, HistoryMonth:
		return period, nil
	default:
		return "", fmt.Errorf("%w: invalid history period", domain.ErrValidation)
	}
}

func startOfDay(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
}

func bucketStartFor(t time.Time, period HistoryPeriod) time.Time {
	if period == HistoryHour {
		return time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), 0, 0, 0, t.Location())
	}
	t = startOfDay(t)
	switch period {
	case HistoryWeek:
		offset := (int(t.Weekday()) + 6) % 7
		return t.AddDate(0, 0, -offset)
	case HistoryMonth:
		return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, t.Location())
	default:
		return t
	}
}

func nextBucketStart(t time.Time, period HistoryPeriod) time.Time {
	switch period {
	case HistoryHour:
		return t.Add(time.Hour)
	case HistoryWeek:
		return t.AddDate(0, 0, 7)
	case HistoryMonth:
		return t.AddDate(0, 1, 0)
	default:
		return t.AddDate(0, 0, 1)
	}
}

func bucketLabel(t time.Time, period HistoryPeriod) string {
	switch period {
	case HistoryMonth:
		return t.Format("2006-01")
	case HistoryHour:
		return t.Format("2006-01-02 15:04")
	}
	return t.Format("2006-01-02")
}

// SetPeriodUsage adjusts the current billing-period usage by moving the
// user's period baseline to "now". This keeps future 3X-UI poll results
// additive from the admin-selected value instead of being overwritten by the
// next cumulative snapshot.
func (s *Service) SetPeriodUsage(ctx context.Context, userID int64, usedBytes int64) error {
	if usedBytes < 0 {
		return fmt.Errorf("%w: traffic usage must be >= 0", domain.ErrValidation)
	}
	u, err := s.users.GetByID(ctx, userID)
	if err != nil {
		return err
	}

	var latestTotal, latestUp int64
	if latest, err := s.traffic.LatestForUser(ctx, userID); err == nil && latest != nil {
		latestTotal = latest.TotalBytes
		latestUp = latest.UpBytes
	}
	// currentTotal is the cumulative total recorded at the re-baseline moment.
	// In the normal case (latestTotal >= usedBytes) it equals latestTotal, so
	// the snapshot we write matches the latest real poll value and does NOT
	// perturb the current hour's rollup bucket. When the admin sets usage ABOVE
	// the current cumulative it rises to usedBytes — a deliberate upward
	// adjustment that stays monotonic.
	//
	// We deliberately do NOT write a second "base" snapshot below the existing
	// in-hour poll snapshots: the hourly rollup derives each (entity, hour)
	// bucket as MAX(total)-MIN(total) and relies on intra-hour snapshots being
	// monotonic-increasing. A below-baseline row became the bucket MIN and
	// inflated that hour by the whole re-baseline amount — permanently, once the
	// 7-day raw rows were pruned. Period usage is carried by PeriodBaselineBytes
	// (set below), which PeriodUsed() reads — it never needed a synthetic
	// snapshot below the baseline.
	currentTotal := latestTotal
	if usedBytes > currentTotal {
		currentTotal = usedBytes
	}
	// Preserve the up/down ratio from the latest real snapshot so the chart's
	// per-direction split stays sensible; fall back to an even split when no
	// prior data exists.
	splitUpDown := func(total int64) (up, down int64) {
		if total <= 0 {
			return 0, 0
		}
		if latestTotal > 0 {
			// float64 keeps the proportion overflow-safe: total*latestUp as
			// int64 overflows for multi-GB users (e.g. 8GiB*4GiB > maxint64),
			// wrapping `up` negative/zero. Byte-level rounding is invisible.
			up = int64(float64(total) * float64(latestUp) / float64(latestTotal))
			if up < 0 {
				up = 0
			} else if up > total {
				up = total
			}
			down = total - up
			return up, down
		}
		up = total / 2
		down = total - up
		return up, down
	}
	currentUp, currentDown := splitUpDown(currentTotal)
	now := time.Now()
	periodStart := now

	if err := s.traffic.Insert(ctx, &domain.TrafficSnapshot{
		UserID:     userID,
		UpBytes:    currentUp,
		DownBytes:  currentDown,
		TotalBytes: currentTotal,
		CapturedAt: now,
	}); err != nil {
		return err
	}

	u.TrafficPeriodStart = &periodStart
	if currentTotal > u.LifetimeTotalBytes {
		// Manual usage edits are expressed as the current period's visible
		// total. Keep lifetime snapshots monotonic so the next real poll adds
		// to the admin-selected baseline instead of dropping below it. Apply
		// the same up/down ratio used for the synthetic snapshots so the user
		// row stays internally consistent.
		deltaTotal := currentTotal - u.LifetimeTotalBytes
		dUp, dDown := splitUpDown(deltaTotal)
		u.LifetimeUpBytes += dUp
		u.LifetimeDownBytes += dDown
		u.LifetimeTotalBytes = currentTotal
	}
	// Move the bootstrap-delta cutoff forward — without this, ownerships added
	// between the admin override and the next poll would be classified
	// against an out-of-date baseline.
	u.LifetimeBaselineAt = &now
	// v3 period usage is PeriodUsed() = LifetimeTotalBytes - PeriodBaselineBytes
	// (periodUsage no longer reads snapshots). Set the baseline so the admin's
	// chosen usedBytes is what the dashboard shows AND what the next poll's
	// auto-disable check sees — otherwise the next poll recomputes against the
	// stale baseline and can flip the enable state we just set below.
	u.PeriodBaselineBytes = u.LifetimeTotalBytes - usedBytes
	if u.PeriodBaselineBytes < 0 {
		u.PeriodBaselineBytes = 0
	}
	// Must be UpdateTrafficState, NOT Update: userRepo.Update does
	// Omit(pollOwnedColumns...) which skips exactly the columns we just set
	// (lifetime_*, period_baseline_bytes, lifetime_baseline_at,
	// traffic_period_start) so the whole override would silently no-op in
	// production. UpdateTrafficState is the column-scoped writer for these.
	if err := s.users.UpdateTrafficState(ctx, u); err != nil {
		return err
	}
	// Keep the per-node usage breakdown consistent with the period total we
	// just set: a manual override is an aggregate with no real per-node split,
	// so distribute it across the user's clients in proportion to each client's
	// lifetime (the same baseline fraction f the user row got). Without this the
	// per-node "this period" footer keeps summing Lifetime - stale baseline and
	// visibly contradicts the user-level figure shown right above it until the
	// next natural rollover. Best-effort + out-of-band: a failure only degrades
	// the per-node display, not the override itself. Rewriting lifetime/last-raw
	// to their loaded values alongside the new baseline is race-safe — they
	// revert as a consistent pair, so a concurrent poll re-derives correctly.
	s.reseedClientBaselines(ctx, userID, usedBytes)
	if u.TrafficLimitBytes <= 0 {
		return nil
	}
	if usedBytes >= u.TrafficLimitBytes && u.Enabled {
		return s.disabler.SetEnabledAndSync(ctx, u.ID, false, domain.DisabledTrafficExceeded, "traffic limit exceeded")
	}
	if usedBytes < u.TrafficLimitBytes && !u.Enabled && u.AutoDisabledReason == domain.DisabledTrafficExceeded {
		return s.disabler.SetEnabledAndSync(ctx, u.ID, true, domain.DisabledNone, "")
	}
	return nil
}

// reseedClientBaselines redistributes a manual period-usage override across the
// user's clients so Σ(per-client period usage) stays equal to the override.
// Each client's baseline becomes the same fraction f = (Σlifetime - used)/Σlifetime
// of its own lifetime, so its period usage = lifetime·(1-f) is its lifetime
// share of `used`. float64 keeps the proportion overflow-safe (the byte-level
// rounding is invisible in a display). No-op when the user owns nothing or has
// zero lifetime. Best-effort: errors are logged, not surfaced.
func (s *Service) reseedClientBaselines(ctx context.Context, userID, usedBytes int64) {
	if s.ownership == nil {
		return
	}
	entries, err := s.ownership.ListByUser(ctx, userID)
	if err != nil || len(entries) == 0 {
		return
	}
	var clientLifetime int64
	for _, e := range entries {
		clientLifetime += e.LifetimeTotalBytes
	}
	f := 0.0
	if clientLifetime > 0 {
		used := usedBytes
		if used > clientLifetime {
			used = clientLifetime // can't have used more this period than total
		}
		f = float64(clientLifetime-used) / float64(clientLifetime)
	}
	for _, e := range entries {
		e.PeriodBaselineUpBytes = int64(float64(e.LifetimeUpBytes) * f)
		e.PeriodBaselineDownBytes = int64(float64(e.LifetimeDownBytes) * f)
		e.PeriodBaselineTotalBytes = int64(float64(e.LifetimeTotalBytes) * f)
	}
	if err := s.ownership.BatchUpdateCounters(ctx, entries); err != nil {
		log.Warn("set period usage: per-client baseline reseed", "user_id", userID, "err", err)
	}
}
