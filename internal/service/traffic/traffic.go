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

type Service struct {
	users       ports.UserRepo
	ownership   ports.OwnershipRepo
	traffic     ports.TrafficRepo
	nodes       ports.NodeRepo
	nodeTraffic ports.NodeTrafficRepo
	pool        ports.XUIPool
	disabler    UserDisabler
	// settings is optional — only used to look up EmergencyAccessQuotaGB so the
	// poll can end an emergency window early when the per-window cap is hit.
	// Nil-tolerant: when absent, emergency access is uncapped (legacy behavior).
	settings ports.SettingsRepo
	// configPusher is wired lazily (user.Service is the implementor and
	// is created before traffic.Service). nil = skip floor refresh on poll.
	configPusher UserConfigPusher
}

// SetConfigPusher wires the late-bound config pusher. Same late-binding
// pattern as user.Service.SetTrafficUsage — needed because both services
// have methods that reference each other.
func (s *Service) SetConfigPusher(p UserConfigPusher) {
	s.configPusher = p
}

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
	return &Service{users: users, ownership: ownership, traffic: traffic, nodes: nodes, nodeTraffic: nodeTraffic, pool: pool, disabler: disabler}
}

// WithSettings attaches the settings repo so the poll can enforce the
// emergency-access traffic quota. Optional — leaving it nil preserves the
// previous "uncapped emergency window" behavior. Returns the service for
// chaining at construction sites.
func (s *Service) WithSettings(settings ports.SettingsRepo) *Service {
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
	users, err := s.listAllUsers(ctx)
	if err != nil {
		return err
	}

	// Load runtime settings + the resolved panel location ONCE per poll
	// and share them across the inner loops. Before this each user's
	// recordAndEnforce path did two settings.Load calls — one inside
	// panelNow (via paneltz.Location) and one for the emergency-quota
	// check — so N users meant 2N DB roundtrips even though the data
	// is identical for the whole cycle. With N users + reasonable
	// snapshot count that adds up.
	pollCfg := ports.UISettings{}
	if s.settings != nil {
		if loaded, err := s.settings.Load(ctx, ports.UISettings{}); err == nil {
			pollCfg = loaded
		}
	}
	pollLoc := paneltz.Location(ctx, s.settings)

	// Sink collects every snapshot write across the user loop so we can
	// flush them in three GORM CreateInBatches calls at the end of the
	// cycle instead of N + M individual INSERTs. The per-user / per-node
	// processing stays single-goroutine so no locking is needed on the
	// sink slices — they're just append targets owned by this poll.
	sink := &pollSink{
		userSnaps:   make([]*domain.TrafficSnapshot, 0, len(users)),
		clientSnaps: make([]*domain.ClientTrafficSnapshot, 0, len(users)*4),
		nodeSnaps:   make([]*domain.NodeTrafficSnapshot, 0),
	}

	byInbound := make(map[inboundKey][]ownershipRef)
	totals := make(map[int64]trafficTotals, len(users))
	skipUsers := make(map[int64]bool)
	for _, u := range users {
		totals[u.ID] = trafficTotals{}
		entries, err := s.ownership.ListByUser(ctx, u.ID)
		if err != nil {
			log.Warn("traffic poll ownership", "user_id", u.ID, "err", err)
			continue
		}
		for _, e := range entries {
			key := inboundKey{panelID: e.PanelID, inboundID: e.InboundID}
			byInbound[key] = append(byInbound[key], ownershipRef{entry: e, userID: u.ID, email: e.ClientEmail, createdAt: e.CreatedAt})
		}
	}

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

	log.Info("traffic poll start", "users", len(users), "panels", len(byPanel), "inbounds_to_query", len(byInbound))

	// Phase 1 — parallel ListInbounds across every panel, bounded by a
	// semaphore so a 50-panel deployment can't fan out into 50
	// simultaneous HTTP requests against 3X-UI workers. Serial calls
	// were turning "Poll Now" into a multi-second wait with even 3
	// panels (each request is 100-500ms network-bound); concurrent
	// calls drop the wall-clock to roughly one ListInbounds time
	// regardless of panel count, while the cap prevents tail-end
	// regressions when admins eventually attach many panels.
	type panelListResult struct {
		stats map[int]([]ports.ClientTraffic)
		err   error
	}
	panelData := make(map[int64]panelListResult, len(byPanel))
	var panelMu sync.Mutex
	var panelWG sync.WaitGroup
	panelSem := make(chan struct{}, paneltz.ResolveMaxPanelConcurrency(pollCfg.MaxPanelConcurrency))
	for panelID := range byPanel {
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
			listed, lerr := c.ListInbounds(ctx)
			stats := make(map[int][]ports.ClientTraffic, len(listed))
			for _, inb := range listed {
				stats[inb.ID] = inb.ClientStats
			}
			panelMu.Lock()
			panelData[pid] = panelListResult{stats: stats, err: lerr}
			panelMu.Unlock()
		}(panelID)
	}
	panelWG.Wait()

	// Phase 2 — per-panel sequential processing. ListInbounds results
	// are already in panelData; only the per-inbound fallback (for 3X-UI
	// builds that drop clientStats from the list endpoint) is still
	// network-bound and stays serial inside the panel scope.
	for panelID, inbounds := range byPanel {
		pd := panelData[panelID]
		if pd.err != nil {
			log.Warn("traffic poll panel", "panel_id", panelID, "err", pd.err)
			for _, refs := range inbounds {
				markSkippedUsers(skipUsers, refs)
			}
			continue
		}
		c, err := s.pool.Get(panelID)
		if err != nil {
			// Pool entry vanished between phases — treat like a fresh
			// fetch failure rather than panic.
			log.Warn("traffic poll panel re-resolve", "panel_id", panelID, "err", err)
			for _, refs := range inbounds {
				markSkippedUsers(skipUsers, refs)
			}
			continue
		}
		statsByInbound := pd.stats

		for inboundID, refs := range inbounds {
			traffics := statsByInbound[inboundID]
			// Per-inbound fallback if list didn't return data for this inbound.
			if len(traffics) == 0 {
				t, ferr := c.GetInboundTraffics(ctx, inboundID)
				if ferr != nil {
					log.Warn("traffic poll inbound fallback",
						"panel_id", panelID, "inbound_id", inboundID, "err", ferr)
				} else {
					traffics = t
				}
			}
			trafficByEmail := make(map[string]ports.ClientTraffic, len(traffics))
			for _, t := range traffics {
				trafficByEmail[t.Email] = t
			}
			matched := 0
			var nodeUp, nodeDown int64
			for _, ref := range refs {
				t, ok := trafficByEmail[ref.email]
				if !ok {
					continue
				}
				matched++
				nodeUp += t.Up
				nodeDown += t.Down
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

			// Persist per-node snapshots from the managed client rows we
			// matched above. Some 3X-UI builds return zero for inbound-level
			// Up/Down even when clientStats is populated; summing the owned
			// clients is both more reliable and matches the dashboard contract
			// that only panel-managed clients are counted.
			if matched > 0 {
				if err := s.recordNodeStats(ctx, panelID, inboundID, nodeUp, nodeDown, sink); err != nil {
					log.Warn("traffic poll node snapshot",
						"panel_id", panelID, "inbound_id", inboundID, "err", err)
				}
			}
		}
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
	if len(sink.nodeSnaps) > 0 && s.nodeTraffic != nil {
		if err := s.nodeTraffic.InsertBatch(ctx, sink.nodeSnaps); err != nil {
			log.Warn("traffic poll flush node snapshots", "count", len(sink.nodeSnaps), "err", err)
		}
	}
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
type pollSink struct {
	userSnaps   []*domain.TrafficSnapshot
	clientSnaps []*domain.ClientTrafficSnapshot
	nodeSnaps   []*domain.NodeTrafficSnapshot
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
	if err := s.ownership.UpdateCounters(ctx, ownership); err != nil {
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
		prev, perr := s.traffic.LatestForUser(ctx, u.ID)
		if perr != nil && !errors.Is(perr, domain.ErrNotFound) {
			log.Warn("traffic poll latest snapshot lookup", "user_id", u.ID, "err", perr)
			prev = nil
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

		if err := s.users.Update(ctx, u); err != nil {
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
		if u.AutoDisabledReason == domain.DisabledTrafficExceeded {
			u.EmergencyUntil = nil
			u.EmergencyBaselineBytes = 0
		}
		// Persist the new periodStart FIRST. SetEnabledAndSync re-reads the
		// user from the DB, so anything we change in memory after Update() is
		// lost. Without this, periodStart never actually moves and rollover
		// fires on every poll forever.
		//
		// Holding the emergency lock for the whole "load → mutate → write"
		// here closes the race with UseEmergencyAccess: previously the user
		// could request emergency access between us clearing the fields in
		// memory and us writing the row, and the Update would clobber their
		// grant.
		updatePeriodStart := func() {
			if err := s.users.Update(ctx, u); err != nil {
				log.Warn("traffic period start update", "user_id", u.ID, "err", err)
			}
		}
		if s.configPusher != nil {
			s.configPusher.WithEmergencyLock(updatePeriodStart)
		} else {
			updatePeriodStart()
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
	if emergencyActive && cfg.EmergencyAccessQuotaGB > 0 {
		quotaBytes := int64(cfg.EmergencyAccessQuotaGB) * 1024 * 1024 * 1024
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
				if err := s.users.Update(ctx, u); err != nil {
					log.Warn("emergency quota clear update", "user_id", u.ID, "err", err)
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
		return nil
	}
	// Safety net: push the remaining-bytes floor into 3X-UI so the inbound
	// itself cuts the client off if the panel is offline long enough for
	// the user to exceed their cap between polls. Best-effort: a failed
	// push is logged but does not stop the poll cycle for other users.
	if s.configPusher != nil {
		if err := s.configPusher.PushClientConfig(ctx, u.ID); err != nil {
			log.Warn("traffic floor push failed", "user_id", u.ID, "err", err)
		}
	}
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

// recordNodeStats writes a per-node snapshot for the inbound on the given
// panel and updates the node's monotonic lifetime counters. Mirrors the
// per-user logic in recordAndEnforce: counter resets (latest < prev) collapse
// to "delta = current value", and only non-zero deltas trigger an Update.
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

	var dUp, dDown, dTotal int64
	hasRawBaseline := node.LastTrafficUpBytes != 0 || node.LastTrafficDownBytes != 0 || node.LastTrafficTotalBytes != 0
	switch {
	case hasRawBaseline:
		dUp = monotonicDelta(up, node.LastTrafficUpBytes)
		dDown = monotonicDelta(down, node.LastTrafficDownBytes)
		dTotal = monotonicDelta(totalBytes, node.LastTrafficTotalBytes)
	case node.LifetimeTotalBytes > 0:
		// Existing installs may already have lifetime values but no raw
		// baseline fields. Initialize the baseline without double-counting
		// the current cumulative counters.
		dUp, dDown, dTotal = 0, 0, 0
	default:
		dUp, dDown, dTotal = up, down, totalBytes
	}
	node.LifetimeUpBytes += dUp
	node.LifetimeDownBytes += dDown
	node.LifetimeTotalBytes += dTotal
	node.LastTrafficUpBytes = up
	node.LastTrafficDownBytes = down
	node.LastTrafficTotalBytes = totalBytes

	nodeSnap := &domain.NodeTrafficSnapshot{
		NodeID:     node.ID,
		UpBytes:    node.LifetimeUpBytes,
		DownBytes:  node.LifetimeDownBytes,
		TotalBytes: node.LifetimeTotalBytes,
		CapturedAt: time.Now(),
	}
	if sink != nil {
		sink.nodeSnaps = append(sink.nodeSnaps, nodeSnap)
	} else if err := s.nodeTraffic.Insert(ctx, nodeSnap); err != nil {
		return fmt.Errorf("insert node snapshot: %w", err)
	}

	if err := s.nodes.Update(ctx, node); err != nil {
		log.Warn("node traffic lifetime update", "node_id", node.ID, "err", err)
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

	var (
		snapshots []*domain.NodeTrafficSnapshot
		prev      *domain.NodeTrafficSnapshot
	)
	if s.nodeTraffic != nil {
		var lerr error
		snapshots, lerr = s.nodeTraffic.ListByNode(ctx, nodeID, since, untilExclusive)
		if lerr != nil {
			return nil, lerr
		}
		prev, _ = s.nodeTraffic.LastBefore(ctx, nodeID, since)
	}
	prevUp, prevDown, prevTotal := nodeSnapshotCounters(prev)

	items := make([]HistoryItem, 0)
	idx := 0
	for bucketStart := bucketStartFor(since, period); bucketStart.Before(untilExclusive); bucketStart = nextBucketStart(bucketStart, period) {
		bucketEnd := nextBucketStart(bucketStart, period)
		if bucketEnd.After(untilExclusive) {
			bucketEnd = untilExclusive
		}

		var lastInBucket *domain.NodeTrafficSnapshot
		for idx < len(snapshots) && snapshots[idx].CapturedAt.Before(bucketEnd) {
			if !snapshots[idx].CapturedAt.Before(since) {
				lastInBucket = snapshots[idx]
			}
			idx++
		}

		item := HistoryItem{Date: bucketLabel(bucketStart, period)}
		if lastInBucket != nil {
			item.UpBytes = deltaCounter(lastInBucket.UpBytes, prevUp)
			item.DownBytes = deltaCounter(lastInBucket.DownBytes, prevDown)
			item.TotalBytes = deltaCounter(lastInBucket.TotalBytes, prevTotal)
			prevUp = lastInBucket.UpBytes
			prevDown = lastInBucket.DownBytes
			prevTotal = lastInBucket.TotalBytes
		}
		items = append(items, item)
	}

	return &NodeHistoryReport{
		NodeID: nodeID,
		Period: period,
		Since:  since.Format("2006-01-02"),
		Until:  until.Format("2006-01-02"),
		Items:  items,
	}, nil
}

func nodeSnapshotCounters(s *domain.NodeTrafficSnapshot) (up, down, total int64) {
	if s == nil {
		return 0, 0, 0
	}
	return s.UpBytes, s.DownBytes, s.TotalBytes
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

	snapshots, err := s.traffic.ListByUser(ctx, userID, since, untilExclusive)
	if err != nil {
		return nil, err
	}

	var prev *domain.TrafficSnapshot
	if base, err := s.traffic.LastBefore(ctx, userID, since); err == nil && base != nil {
		prev = base
	}
	prevUp, prevDown, prevTotal := snapshotCounters(prev)

	items := make([]HistoryItem, 0)
	idx := 0
	for bucketStart := bucketStartFor(since, period); bucketStart.Before(untilExclusive); bucketStart = nextBucketStart(bucketStart, period) {
		bucketEnd := nextBucketStart(bucketStart, period)
		if bucketEnd.After(untilExclusive) {
			bucketEnd = untilExclusive
		}

		var lastInBucket *domain.TrafficSnapshot
		for idx < len(snapshots) && snapshots[idx].CapturedAt.Before(bucketEnd) {
			if !snapshots[idx].CapturedAt.Before(since) {
				lastInBucket = snapshots[idx]
			}
			idx++
		}

		item := HistoryItem{Date: bucketLabel(bucketStart, period)}
		if lastInBucket != nil {
			item.UpBytes = deltaCounter(lastInBucket.UpBytes, prevUp)
			item.DownBytes = deltaCounter(lastInBucket.DownBytes, prevDown)
			item.TotalBytes = deltaCounter(lastInBucket.TotalBytes, prevTotal)
			prevUp = lastInBucket.UpBytes
			prevDown = lastInBucket.DownBytes
			prevTotal = lastInBucket.TotalBytes
		}
		items = append(items, item)
	}

	return &HistoryReport{
		UserID: userID,
		Period: period,
		Since:  since.Format("2006-01-02"),
		Until:  until.Format("2006-01-02"),
		Items:  items,
	}, nil
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

func snapshotCounters(s *domain.TrafficSnapshot) (up, down, total int64) {
	if s == nil {
		return 0, 0, 0
	}
	return s.UpBytes, s.DownBytes, s.TotalBytes
}

func deltaCounter(current, previous int64) int64 {
	delta := current - previous
	if delta < 0 {
		delta = current
	}
	if delta < 0 {
		return 0
	}
	return delta
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
	baseTotal := latestTotal - usedBytes
	if baseTotal < 0 {
		baseTotal = 0
	}
	currentTotal := baseTotal + usedBytes
	// Preserve the up/down ratio from the latest real snapshot so HistoryFor's
	// per-direction bucket math doesn't see synthetic rows where Up jumps from
	// 0 to its full cumulative value in one tick. When no prior data is
	// available, fall back to an even split — better than dumping everything
	// onto Down.
	splitUpDown := func(total int64) (up, down int64) {
		if total <= 0 {
			return 0, 0
		}
		if latestTotal > 0 {
			up = total * latestUp / latestTotal
			down = total - up
			return up, down
		}
		up = total / 2
		down = total - up
		return up, down
	}
	baseUp, baseDown := splitUpDown(baseTotal)
	currentUp, currentDown := splitUpDown(currentTotal)
	now := time.Now()
	periodStart := now
	baseAt := now.Add(-time.Millisecond)

	if err := s.traffic.Insert(ctx, &domain.TrafficSnapshot{
		UserID:     userID,
		UpBytes:    baseUp,
		DownBytes:  baseDown,
		TotalBytes: baseTotal,
		CapturedAt: baseAt,
	}); err != nil {
		return err
	}
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
	if err := s.users.Update(ctx, u); err != nil {
		return err
	}
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
