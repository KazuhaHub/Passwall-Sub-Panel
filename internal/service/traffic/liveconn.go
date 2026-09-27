package traffic

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"sort"
	"sync"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/log"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/metrics"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/paneltz"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/safego"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// The live-connection snapshot (domain.LiveConnSnapshot): the risk center's
// default 实时连接 view, the connections the latest reading of the panels
// found, each on the panel and node that reported it.
//
// It lives in memory only, on purpose. It is a display of one moment,
// replaced whole by the next reading and lost on restart (it reappears after
// the first poll); persisting it would be one more table of addresses to age
// out, for a view whose whole point is "now". It is built inside
// observeLiveIPs from the reads and the decisions the detector has already
// made — the same fresh sightings, the same exclusions — so what an admin
// sees is what was judged, and building it costs no panel call. The only
// extra work is one geo lookup to place the connections for display.
//
// An admin can also ask for a reading now (RefreshLiveConnections): the
// same fold over one fresh live read per panel, stored as a refresh
// snapshot. That reading is for the view only and is never a detector
// sample; see there.

// LiveSnapshot is the latest stored snapshot, or nil before the first poll.
// The snapshot is shared and immutable: a caller must not modify it.
func (s *Service) LiveSnapshot() *domain.LiveConnSnapshot {
	s.liveSnapMu.RLock()
	defer s.liveSnapMu.RUnlock()
	return s.liveSnap
}

// storeLiveSnapshot replaces the stored snapshot unless the stored one is
// newer. Polls overlap (the scheduled one and a staff "poll now"), and a
// reading that started first can finish last; the view must show the newest
// reading, never the one that happened to finish last. A tie replaces.
//
// psp_live_connections follows what is stored, so it always describes the
// snapshot an admin would be shown.
func (s *Service) storeLiveSnapshot(snap *domain.LiveConnSnapshot) {
	if snap == nil {
		return
	}
	s.liveSnapMu.Lock()
	defer s.liveSnapMu.Unlock()
	if cur := s.liveSnap; cur != nil && snap.TakenAt.Before(cur.TakenAt) {
		return
	}
	s.liveSnap = snap
	metrics.LiveConnections.Set(int64(len(snap.Conns)))
}

// buildLiveSnapshot assembles one snapshot from panels that went through
// the freshness rule and the exclusions decided over the same panels. It
// reads no panel and writes nothing but the returned value; storing it is
// the caller's decision.
//
// A panel whose adapter has no live read at all (ports.ErrPanelCapabilityUnsupported,
// S-UI's permanent shape) is listed as Unsupported, apart from the panels
// whose read FAILED: showing it as a failure on every reading would teach an
// admin to skip the entry that means a real outage. For the same reason it
// does not count toward an account's unread panels.
func (s *Service) buildLiveSnapshot(ctx context.Context, panels []domain.PanelLiveIPs,
	owners map[domain.ClientKey]int64, addrs map[int64]domain.UserAddresses,
	unreferenced int, source string, at time.Time) *domain.LiveConnSnapshot {
	snap := &domain.LiveConnSnapshot{
		TakenAt:      at,
		Source:       source,
		PanelsAsked:  len(panels),
		Unreferenced: unreferenced,
	}
	unread := map[int64]bool{}
	for _, p := range panels {
		switch {
		case p.Err == nil:
		case errors.Is(p.Err, ports.ErrPanelCapabilityUnsupported):
			snap.Unsupported = append(snap.Unsupported, p.PanelID)
		default:
			snap.Unread = append(snap.Unread, p.PanelID)
			unread[p.PanelID] = true
		}
	}
	sort.Slice(snap.Unread, func(i, j int) bool { return snap.Unread[i] < snap.Unread[j] })
	sort.Slice(snap.Unsupported, func(i, j int) bool { return snap.Unsupported[i] < snap.Unsupported[j] })

	snap.Conns, snap.Truncated = domain.CollectLiveConnections(panels, owners, addrs, domain.LiveConnMaxPerUser)
	s.placeConnections(ctx, snap.Conns)

	// Distinct unread PANELS per account: a user split into two clients on
	// one panel (clientplan) is missing one panel's answer, not two.
	unreadBy := map[int64]map[int64]struct{}{}
	for k, uid := range owners {
		if !unread[k.PanelID] {
			continue
		}
		if unreadBy[uid] == nil {
			unreadBy[uid] = map[int64]struct{}{}
		}
		unreadBy[uid][k.PanelID] = struct{}{}
	}
	snap.Users = map[int64]domain.LiveUserMeta{}
	for _, c := range snap.Conns {
		if _, ok := snap.Users[c.UserID]; !ok {
			snap.Users[c.UserID] = domain.LiveUserMeta{Stale: addrs[c.UserID].Stale, Unread: len(unreadBy[c.UserID])}
		}
	}
	return snap
}

// placeConnections fills each connection's Place with one lookup over the
// distinct addresses, for display. An internal address (private, CGNAT,
// loopback, link-local) is not somewhere, so the database is never asked
// about it; every other exclusion still has a place worth showing (a listed
// office exit, a relay, a shared carrier gateway). Skipped when no database
// is in a position to answer. The lookup's coordinates are dropped here
// (domain.ConnPlaceOf): the snapshot is served beside the address.
func (s *Service) placeConnections(ctx context.Context, conns []domain.LiveConnection) {
	if s.geo == nil || len(conns) == 0 || !s.geo.Available(ctx) {
		return
	}
	seen := map[string]struct{}{}
	var ips []string
	for _, c := range conns {
		if c.Exclusion == domain.AddressExcludedInternal || c.IP == "" {
			continue
		}
		if _, ok := seen[c.IP]; !ok {
			seen[c.IP] = struct{}{}
			ips = append(ips, c.IP)
		}
	}
	if len(ips) == 0 {
		return
	}
	located := s.geo.Lookup(ctx, ips)
	for i := range conns {
		if conns[i].Exclusion == domain.AddressExcludedInternal {
			continue
		}
		if g, ok := located[conns[i].IP]; ok {
			conns[i].Place = domain.ConnPlaceOf(g)
		}
	}
}

// liveRefreshMinGap is how long after a poll advanced the per-node
// references a refresh reads no panel (domain.ErrLiveJustPolled). 3X-UI
// rescans its connections every ten seconds, so less than one scan after the
// poll's read, every node it advanced still reads "not rescanned" against
// the references that very poll stored, and the refresh would publish an
// empty view that wins on time. Fifteen seconds is one scan plus the poll's
// own read time. A fact about the upstream, not a policy: not a setting.
const liveRefreshMinGap = 15 * time.Second

// RefreshLiveConnections reads every panel's live connections now, at an
// admin's request (the risk center's 立即刷新), stores them as the
// live-connection snapshot (source refresh), and returns what is stored
// afterwards: a newer poll's snapshot when one landed while the panels were
// being read, since the view always shows the newest reading.
//
// A refresh is never a detector sample. It judges nothing and writes
// nothing but the in-memory snapshot:
//
//   - freshness is decided against a COPY of the per-node references, and
//     this reading's own references are thrown away. Merging them would
//     make the next scheduled poll find every node "not rescanned since"
//     and judge a connected fleet idle, and a click would become a sample.
//     The copy is taken BEFORE the first panel is asked: a poll finishing
//     while the refresh waits on a panel stores references as new as the
//     refresh's own reading, every node would then read "not advanced", and
//     the refresh would publish an empty view that wins on time;
//   - no streak is loaded or saved, no verdict drawn, no detector metric
//     counted (psp_live_connections, the size of the stored view, is the one
//     figure that moves), and the poll's last-good settings cache is not
//     written: it must hold what a poll loaded.
//
// Nor is a reading published that did not happen:
//
//   - within liveRefreshMinGap of a poll advancing the references, no panel
//     is read at all and the answer is domain.ErrLiveJustPolled: judged
//     against references that new, every node the poll advanced reads "not
//     rescanned since" and its connections would vanish from a view that
//     wins on time. That is decided from the references themselves, in the
//     same critical section that copies them — never from the snapshot on
//     display, which a refresh finishing after the poll replaces, and which
//     a poll stores only after it has merged its references. The copy is
//     either older than the poll's merge (the case above: judged against it,
//     the reading is right) or the gate sees the merge;
//   - a reading whose caller went away mid-read (its context CANCELLED — an
//     admin's closed tab, when a caller has not detached the read from it)
//     is not stored and the cancellation is returned: every panel still
//     being read would answer "cancelled" and be listed as unread, failures
//     of the panels that never happened. The context's DEADLINE is
//     different: a reading bounded by it did happen, and a panel silent by
//     then did fail to answer in time, so that reading is stored with the
//     silent panels unread.
//
// What it reads is exactly one live read per panel holding a shared client,
// through the reader the poll uses (readPanelLiveIPs), and nothing else — no
// inbound list, because metering is the poll's job. The owners are the
// shared clients, as in observeLiveIPs; a legacy per-node client has no live
// attribution there either, so its panels are not asked. The reads run in
// parallel under max_panel_concurrency, like the poll's Phase 1. The
// snapshot is taken at the moment the reads completed, and classified by
// the poll's own rules (liveExclusions) with the stored global settings —
// the ignore list and the fleet-wide runtime — so a source is kept or set
// aside for the same reason the poll would give; a failed settings read
// runs on the shipped defaults.
//
// There is no rate limit here — the just-polled gate is a fact about the
// references, not a ration: one call is one read of every panel, and
// deciding how often that may happen (the cooldown, one refresh at a time)
// is the caller's job. The caller also bounds the context: a panel still
// unanswered at its deadline is listed as unread.
func (s *Service) RefreshLiveConnections(ctx context.Context) (*domain.LiveConnSnapshot, error) {
	if s.pspClient == nil || s.pool == nil {
		return nil, fmt.Errorf("refresh live connections: %w", domain.ErrUnavailable)
	}

	// The copy comes first, before anything is read, and the just-polled
	// gate is decided in the same critical section; see above. A poll that
	// merges after this point is the copy's case, not the gate's.
	s.liveRefsMu.Lock()
	if at := s.liveRefsAdvancedAt; !at.IsZero() && time.Since(at) < liveRefreshMinGap {
		s.liveRefsMu.Unlock()
		return nil, fmt.Errorf("refresh live connections: %w", domain.ErrLiveJustPolled)
	}
	prev := maps.Clone(s.liveRefs)
	s.liveRefsMu.Unlock()

	clients, err := s.pspClient.ListAll(ctx)
	if err != nil {
		// Nobody's connection could be attributed; the view keeps its
		// previous reading rather than showing an empty one as current.
		return nil, fmt.Errorf("refresh live connections: list shared clients: %w", err)
	}
	owners := make(map[domain.ClientKey]int64, len(clients))
	panelIDs := map[int64]struct{}{}
	for _, c := range clients {
		if c == nil || c.Email == "" {
			continue
		}
		owners[domain.NewClientKey(c.PanelID, c.Email)] = c.UserID
		panelIDs[c.PanelID] = struct{}{}
	}

	var set ports.UISettings
	if s.settings != nil {
		if loaded, lerr := s.settings.Load(ctx, ports.UISettings{}); lerr == nil {
			set = loaded
		} else {
			log.Warn("live-connection refresh: could not read the settings; classifying on the shipped defaults", "err", lerr)
		}
	}
	rt := domain.GeoRuntimeFromSettings(set.GeoRuntimeSettings())

	panels := s.readLivePanels(ctx, panelIDs, paneltz.ResolveMaxPanelConcurrency(set.MaxPanelConcurrency))
	if cerr := ctx.Err(); cerr != nil && !errors.Is(cerr, context.DeadlineExceeded) {
		// Cancelled, not timed out: the reading did not happen; see above.
		return nil, fmt.Errorf("refresh live connections: the reading was cancelled: %w", cerr)
	}
	at := time.Now()

	panels, next := domain.FreshLiveIPsWithin(panels, prev, rt.FreshWindowSeconds)
	// next only counts the nodes judged with no reference, as the poll's
	// freshLiveIPs does against the live map; it is never merged.
	unreferenced := 0
	for k := range next {
		if _, ok := prev[k]; !ok {
			unreferenced++
		}
	}

	agg := domain.AggregateLiveIPsByUser(panels, owners)
	exclusions, ierr := s.liveExclusions(set.GeoAnomalyIgnoreAddresses, rt)
	if ierr != nil {
		log.Warn("live-connection refresh: the geo ignore list has invalid entries; applying the valid ones", "err", ierr)
	}
	addrs := domain.ClassifyAddresses(agg, exclusions)

	s.storeLiveSnapshot(s.buildLiveSnapshot(ctx, panels, owners, addrs, unreferenced, domain.LiveSnapshotFromRefresh, at))
	return s.LiveSnapshot(), nil
}

// readLivePanels is the refresh's fan-out: one live read per panel, at most
// concurrency at a time, each answer shaped exactly as the poll shapes its
// own (liveAnswer). A panel the pool cannot hand out, or whose read failed,
// carries the error; a read that ended with no answer at all (a recovered
// panic) is errPanelNotRead. Either way that panel's owners read as unread,
// never as "nobody connected there".
//
// Each read runs under safego.Run, the shield for work inside a goroutine
// whose WaitGroup the caller owns, so one malformed upstream answer cannot
// take the process down; the slot is released by the read's own defer, so a
// panicking read cannot leave the others waiting. Not the deferred
// safego.Recover the poll's Phase 1 carries: it calls recover() from a
// nested function, where Go returns nil, so it does not stop a panic (with
// it, TestRefreshLiveConnections_RecoversAPanickingPanel crashes the test
// binary). safego.Run defers the recovering function itself.
func (s *Service) readLivePanels(ctx context.Context, panelIDs map[int64]struct{}, concurrency int) []domain.PanelLiveIPs {
	answers := make(map[int64]domain.PanelLiveIPs, len(panelIDs))
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, concurrency)
	for pid := range panelIDs {
		wg.Add(1)
		go func(pid int64) {
			defer wg.Done()
			safego.Run("traffic.RefreshLiveConnections.panelFetch", func() {
				sem <- struct{}{}
				defer func() { <-sem }()
				var answer domain.PanelLiveIPs
				if c, err := s.pool.Get(pid); err != nil {
					answer = domain.PanelLiveIPs{PanelID: pid, Err: err}
				} else {
					live, sightings, rerr := readPanelLiveIPs(ctx, c)
					if liveReadFailed(rerr) {
						log.Warn("live-connection refresh: live client IPs unavailable for this panel",
							"panel_id", pid, "err", rerr)
					}
					answer = liveAnswer(pid, live, sightings, rerr)
				}
				mu.Lock()
				answers[pid] = answer
				mu.Unlock()
			})
		}(pid)
	}
	wg.Wait()

	panels := make([]domain.PanelLiveIPs, 0, len(panelIDs))
	for pid := range panelIDs {
		answer, ok := answers[pid]
		if !ok {
			answer = domain.PanelLiveIPs{PanelID: pid, Err: errPanelNotRead}
		}
		panels = append(panels, answer)
	}
	return panels
}

// readPanelLiveIPs is one panel's live read: who is connected right now.
// The poll's Phase 1 and the refresh both read through it, so the two can
// never read a panel differently.
//
// Optional capability, best reader first. The detail reader (3X-UI) keeps
// each address's node and last-seen time, which is what separates
// "connected now" from "still remembered"; the plain reader (PSP-native
// nodes) has no timestamps, so its addresses all read as live. An adapter
// with neither (S-UI has no equivalent) answers
// ports.ErrPanelCapabilityUnsupported: its owners' totals are floors, never
// zero, which would read as "nobody is connected", and the live view lists
// the panel as unsupported. That is the adapter's permanent shape, not an
// event, so callers do not log it (liveReadFailed).
func readPanelLiveIPs(ctx context.Context, c ports.PanelClient) (live map[string][]string, sightings map[string][]domain.LiveIPSighting, err error) {
	switch reader := c.(type) {
	case ports.LiveIPDetailReader:
		sightings, err = reader.ListLiveClientIPDetails(ctx)
	case ports.LiveIPReader:
		live, err = reader.ListLiveClientIPs(ctx)
	default:
		err = ports.ErrPanelCapabilityUnsupported
	}
	return live, sightings, err
}

// liveReadFailed says whether a live read's error is worth a Warn: any
// failure except the adapter having no live read at all.
func liveReadFailed(err error) bool {
	return err != nil && !errors.Is(err, ports.ErrPanelCapabilityUnsupported)
}

// liveAnswer shapes one panel's live read as the detector and the view take
// it. A detail reader's sightings are flattened by the one rule both readers
// share (domain.LiveIPsOf), so the window count cannot drift between them,
// and are kept for the freshness rule.
func liveAnswer(pid int64, live map[string][]string, sightings map[string][]domain.LiveIPSighting, err error) domain.PanelLiveIPs {
	byEmail := live
	if sightings != nil {
		byEmail = domain.LiveIPsOf(sightings)
	}
	return domain.PanelLiveIPs{PanelID: pid, ByEmail: byEmail, Sightings: sightings, Err: err}
}

// liveExclusions is the one set of rules live sources are classified with,
// for the poll's verdict and for the refresh alike: internal ranges, the
// admin's global ignore list, PSP's own node and relay addresses, and an
// exit shared by rt.SharedExitMinUsers accounts or more. A bad entry in the
// ignore list does not switch the list off: the valid entries apply, and
// the error names the rest for the caller to log. The settings PUT rejects
// bad entries up front, so this is a value that predates that check or was
// written around it.
func (s *Service) liveExclusions(ignoreRaw string, rt domain.GeoRuntime) (domain.AddressExclusions, error) {
	ignore, err := domain.ParseGeoIgnoreList(ignoreRaw)
	ex := domain.AddressExclusions{
		Internal:       true,
		Ignore:         ignore,
		SharedMinUsers: rt.SharedExitMinUsers,
	}
	if s.infra != nil {
		// A read lock and a map lookup per source; the DNS behind it ran
		// in the refresh loop, never here.
		ex.Infra = s.infra.Contains
	}
	return ex, err
}

// ConnectionRecorder is where the poll records the connections of the
// accounts it judged: the connection history (connection_history, the risk
// center's 连接历史), the one table the detector's data keeps addresses in.
// Record merges one detector sample into it, stamped with the sample's
// instant, and writes that table and nothing else. An interface, like the
// streak store, so the poll runs on a fake in tests and on nothing at all
// where the history is not wired.
type ConnectionRecorder interface {
	Record(ctx context.Context, conns []domain.LiveConnection, at time.Time) error
}

// SetConnectionRecorder late-binds the connection history. Nil is a
// supported state: the poll judges and fills the live view exactly as
// without it and records nothing, so an empty connection history is the only
// symptom of forgetting the wiring (TestBuildWiresTheConnectionRecorder).
func (s *Service) SetConnectionRecorder(r ConnectionRecorder) { s.connRec = r }

// recordConnections records one detector sample into the connection
// history: conns are the stored snapshot's connections of the accounts this
// poll JUDGED (judgedConnections). So a history row's count counts samples.
// An account the spacing rule skipped was not sampled and is not recorded
// again — a manual "poll now" cannot turn clicks into counts — and a refresh
// never gets here at all: it is never a sample.
//
// Detached from the poll's cancellation and bounded on its own
// (geoFollowUpWriteTimeout), like the audit row of a committed transition.
// By the time it runs the sample has been judged and its streaks saved, so
// it records something that has already happened; a poll cancelled at that
// point (a closed "poll now" tab, a shutdown) would otherwise leave a
// judged sample the history never counted.
//
// Never fails the poll: metering is what the poll is for. A refused write
// is counted (psp_connection_history_write_errors_total) and logged with
// the number of connections and the store's error — never the connections
// themselves, which are addresses, and whose store names columns rather
// than values in its errors.
func (s *Service) recordConnections(ctx context.Context, conns []domain.LiveConnection, at time.Time) {
	if s.connRec == nil || len(conns) == 0 {
		return
	}
	wctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), geoFollowUpWriteTimeout)
	defer cancel()
	if err := s.connRec.Record(wctx, conns, at); err != nil {
		metrics.ConnectionHistoryWriteErrorsTotal.Inc()
		log.Warn("connection history: could not record this poll's judged connections; the poll itself is unaffected",
			"connections", len(conns), "err", err)
	}
}

// judgedConnections is the part of a poll's live view that was a detector
// sample: the connections of the accounts judged this poll (the keys of the
// records it saved). The view lists every connected account, the spaced ones
// included, because spacing decides who is judged, not who is connected; the
// history counts samples, so it takes only these. A new slice, in the
// view's order: the stored snapshot is shared and never modified.
func judgedConnections(conns []domain.LiveConnection, judged map[int64]domain.GeoRecord) []domain.LiveConnection {
	out := make([]domain.LiveConnection, 0, len(conns))
	for _, c := range conns {
		if _, ok := judged[c.UserID]; ok {
			out = append(out, c)
		}
	}
	return out
}

// FlagRecorder is where the poll records the changes it makes: the flag
// history (flag_records, the risk center's 标记记录). Append adds records
// and can do nothing else — no read, no rewrite, no delete — so no verdict
// can be drawn from the history (only the streaks drive geo_auto), and
// nothing the detector runs can change what it says happened
// (TestFlagRecorderIsAppendOnly). An interface, like the streak store, so
// the poll runs on a fake in tests and on nothing at all where the history
// is not wired.
//
// Two producers write through it: the judging step, for every change of an
// account's geo attention level (domain.GeoFlagTransition), and Phase 4, for
// every geo_auto suspension applied or lifted by expiry (domain.GeoAutoFlag).
// A staff resume and a suspension written over geo_auto are the user
// service's to record; the risk signals' changes are written by their own
// store, in the transaction of the upsert that made them.
type FlagRecorder interface {
	Append(ctx context.Context, recs []domain.FlagRecord) error
}

// SetFlagRecorder late-binds the flag history. Nil is a supported state: the
// poll judges, suspends and lifts exactly as without it and records nothing,
// so an empty history is the only symptom of forgetting the wiring
// (TestBuildWiresTheFlagRecorders).
func (s *Service) SetFlagRecorder(r FlagRecorder) { s.flagRec = r }

// appendFlags writes records of changes that have already happened: a
// judged sample whose streaks were saved, a suspension or lift whose write
// committed. So it runs detached from the caller's cancellation and bounded
// on its own (geoFollowUpWriteTimeout), like the audit row of a committed
// transition; a poll cancelled at that point (a closed "poll now" tab, a
// shutdown) would otherwise leave a change the history never shows.
//
// Never fails the poll, and never undoes what it records: a refused write
// loses its records, is counted (psp_flag_record_write_errors_total) and is
// logged with the number of records and the store's error only. The records
// carry no address, but the count is all an operator needs to know the
// history has a gap.
func (s *Service) appendFlags(ctx context.Context, recs []domain.FlagRecord) {
	if s.flagRec == nil || len(recs) == 0 {
		return
	}
	wctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), geoFollowUpWriteTimeout)
	defer cancel()
	if err := s.flagRec.Append(wctx, recs); err != nil {
		metrics.FlagRecordWriteErrorsTotal.Inc()
		log.Warn("flag records: could not record the location detector's changes; the changes themselves stand",
			"records", len(recs), "err", err)
	}
}
