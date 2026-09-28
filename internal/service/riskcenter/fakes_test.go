package riskcenter

import (
	"cmp"
	"context"
	"maps"
	"slices"
	"sync"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// testNow is the fixed clock every test reads.
var testNow = time.Date(2026, 9, 26, 10, 0, 0, 0, time.UTC)

// fakeLive is the traffic service as the risk center sees it: a stored
// snapshot, and a refresh that by default stores and returns a refresh
// snapshot taken now.
type fakeLive struct {
	mu        sync.Mutex
	snap      *domain.LiveConnSnapshot
	refreshes int
	refresh   func(ctx context.Context) (*domain.LiveConnSnapshot, error)
}

func (f *fakeLive) LiveSnapshot() *domain.LiveConnSnapshot {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.snap
}

func (f *fakeLive) RefreshLiveConnections(ctx context.Context) (*domain.LiveConnSnapshot, error) {
	f.mu.Lock()
	f.refreshes++
	fn := f.refresh
	f.mu.Unlock()
	if fn != nil {
		return fn(ctx)
	}
	snap := &domain.LiveConnSnapshot{TakenAt: testNow, Source: domain.LiveSnapshotFromRefresh}
	f.mu.Lock()
	f.snap = snap
	f.mu.Unlock()
	return snap, nil
}

func (f *fakeLive) refreshCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.refreshes
}

type fakeSettings struct {
	set ports.UISettings
	err error
}

func (f *fakeSettings) Load(context.Context, ports.UISettings) (ports.UISettings, error) {
	return f.set, f.err
}

type fakeUsers struct {
	mu    sync.Mutex
	byID  map[int64]*domain.User
	calls []int64
	err   error
	// listed records every ListByIDs call's ids; listErr fails it.
	listed  [][]int64
	listErr error
}

func (f *fakeUsers) GetByID(_ context.Context, id int64) (*domain.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, id)
	if f.err != nil {
		return nil, f.err
	}
	if u, ok := f.byID[id]; ok {
		return u, nil
	}
	return nil, domain.ErrNotFound
}

// ListByIDs is the store's: the existing accounts among ids, by id.
func (f *fakeUsers) ListByIDs(_ context.Context, ids []int64) ([]*domain.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.listed = append(f.listed, append([]int64(nil), ids...))
	if f.listErr != nil {
		return nil, f.listErr
	}
	var out []*domain.User
	for _, id := range ids {
		if u, ok := f.byID[id]; ok {
			out = append(out, u)
		}
	}
	slices.SortFunc(out, func(a, b *domain.User) int { return cmp.Compare(a.ID, b.ID) })
	return out, nil
}

type fakePanels struct {
	panels []*domain.XUIPanel
	err    error
	calls  int
}

func (f *fakePanels) List(context.Context) ([]*domain.XUIPanel, error) {
	f.calls++
	return f.panels, f.err
}

type fakeFetches struct {
	rows     []domain.SubLog
	err      error
	calls    int
	gotIDs   []int64
	gotSince time.Time
	gotLimit int
}

func (f *fakeFetches) RecentForUsers(_ context.Context, ids []int64, since time.Time, limit int) ([]domain.SubLog, error) {
	f.calls++
	f.gotIDs, f.gotSince, f.gotLimit = append([]int64(nil), ids...), since, limit
	return f.rows, f.err
}

type fakeHistory struct {
	got   ports.ConnectionHistoryFilter
	rows  []domain.ConnectionRecord
	total int64
	err   error
}

func (f *fakeHistory) List(_ context.Context, flt ports.ConnectionHistoryFilter) ([]domain.ConnectionRecord, int64, error) {
	f.got = flt
	return f.rows, f.total, f.err
}

type fakeFlags struct {
	got   ports.FlagRecordFilter
	rows  []domain.FlagRecord
	total int64
	err   error

	// latest is LatestByUsers' answer (filtered to the ids asked);
	// latestAsked the ids of every call.
	latest      map[int64]int64
	latestAsked [][]int64
	// steps is every account's non-review records, oldest first;
	// StepsSince serves those strictly after each asked cutoff, as the
	// store does. stepsAsked records each call's cutoffs.
	steps      map[int64][]domain.FlagStep
	stepsAsked []map[int64]int64
	stepsErr   error
}

func (f *fakeFlags) List(_ context.Context, flt ports.FlagRecordFilter) ([]domain.FlagRecord, int64, error) {
	f.got = flt
	return f.rows, f.total, f.err
}

func (f *fakeFlags) LatestByUsers(_ context.Context, ids []int64) (map[int64]int64, error) {
	f.latestAsked = append(f.latestAsked, append([]int64(nil), ids...))
	out := map[int64]int64{}
	for _, id := range ids {
		if at, ok := f.latest[id]; ok {
			out[id] = at
		}
	}
	return out, nil
}

func (f *fakeFlags) StepsSince(_ context.Context, since map[int64]int64) (map[int64][]domain.FlagStep, error) {
	f.stepsAsked = append(f.stepsAsked, maps.Clone(since))
	if f.stepsErr != nil {
		return nil, f.stepsErr
	}
	out := map[int64][]domain.FlagStep{}
	for uid, cutoff := range since {
		for _, st := range f.steps[uid] {
			if st.AtMS > cutoff {
				out[uid] = append(out[uid], st)
			}
		}
	}
	return out, nil
}

// fakeGeo is geo_streaks: one set of full rows, served by both reads the
// way the store serves them — AttentionLevels the fresh rows at attention
// (latched or mid-ramp), without their evidence; ListByUsers the asked
// accounts' rows in any state — so the queue's evidence-free read and the
// drawer's full read can never disagree because the fake did.
type fakeGeo struct {
	rows       []domain.GeoRecord
	unknown    int64
	err        error
	gotSince   time.Time
	unkSince   time.Time
	listed     [][]int64
	attnCalled int
}

func (f *fakeGeo) AttentionLevels(_ context.Context, since time.Time) ([]ports.GeoAttentionRow, error) {
	f.attnCalled++
	f.gotSince = since
	if f.err != nil {
		return nil, f.err
	}
	var out []ports.GeoAttentionRow
	for _, r := range f.rows {
		if (r.Streak.Flagged || r.Streak.Over > 0) && r.UpdatedAtMS >= since.UnixMilli() {
			out = append(out, ports.GeoAttentionRow{UserID: r.UserID, Flagged: r.Streak.Flagged, Over: r.Streak.Over, UpdatedAtMS: r.UpdatedAtMS})
		}
	}
	return out, nil
}

func (f *fakeGeo) CountFreshUnknown(_ context.Context, since time.Time) (int64, error) {
	f.unkSince = since
	return f.unknown, f.err
}

func (f *fakeGeo) ListByUsers(_ context.Context, ids []int64) ([]domain.GeoRecord, error) {
	f.listed = append(f.listed, append([]int64(nil), ids...))
	if f.err != nil {
		return nil, f.err
	}
	var out []domain.GeoRecord
	for _, r := range f.rows {
		if slices.Contains(ids, r.UserID) {
			out = append(out, r)
		}
	}
	return out, nil
}

// fakeSignals is risk_signals, on fakeGeo's principle: one set of rows,
// served by both reads as the store serves them.
type fakeSignals struct {
	rows     []domain.RiskSignal
	err      error
	gotSince time.Time
	listed   [][]int64
}

func (f *fakeSignals) AttentionLevels(_ context.Context, since time.Time) ([]ports.SignalAttentionRow, error) {
	f.gotSince = since
	if f.err != nil {
		return nil, f.err
	}
	var out []ports.SignalAttentionRow
	for _, s := range f.rows {
		if (s.State == domain.GeoStateSuspect || s.State == domain.GeoStateFlagged) && s.UpdatedAtMS >= since.UnixMilli() {
			out = append(out, ports.SignalAttentionRow{UserID: s.UserID, Kind: s.Kind, State: s.State, UpdatedAtMS: s.UpdatedAtMS})
		}
	}
	return out, nil
}

func (f *fakeSignals) ListByUsers(_ context.Context, ids []int64) ([]domain.RiskSignal, error) {
	f.listed = append(f.listed, append([]int64(nil), ids...))
	if f.err != nil {
		return nil, f.err
	}
	var out []domain.RiskSignal
	for _, s := range f.rows {
		if slices.Contains(ids, s.UserID) {
			out = append(out, s)
		}
	}
	return out, nil
}

// fakeReviews is risk_reviews: Get serves any row, List the rows in force
// (dismissed or trusted), by user id — the store's contract.
type fakeReviews struct {
	rows map[int64]domain.RiskReview
	err  error
}

func (f *fakeReviews) Get(_ context.Context, uid int64) (domain.RiskReview, bool, error) {
	if f.err != nil {
		return domain.RiskReview{}, false, f.err
	}
	r, ok := f.rows[uid]
	return r, ok, nil
}

func (f *fakeReviews) List(context.Context) ([]domain.RiskReview, error) {
	if f.err != nil {
		return nil, f.err
	}
	var out []domain.RiskReview
	for _, r := range f.rows {
		if r.Dismissed() || r.Trusted {
			out = append(out, r)
		}
	}
	slices.SortFunc(out, func(a, b domain.RiskReview) int { return cmp.Compare(a.UserID, b.UserID) })
	return out, nil
}

// fakeHolds lists the service holds from the users fake itself, so an
// account's hold and its row can never disagree: every account whose
// service axis carries the asked reason, with the time it was written.
type fakeHolds struct {
	users     *fakeUsers
	gotReason domain.AutoDisabledReason
	err       error
}

func (f *fakeHolds) ListServiceHolds(_ context.Context, reason domain.AutoDisabledReason) ([]ports.ServiceHold, error) {
	f.gotReason = reason
	if f.err != nil {
		return nil, f.err
	}
	f.users.mu.Lock()
	defer f.users.mu.Unlock()
	var out []ports.ServiceHold
	for id, u := range f.users.byID {
		if u.ServiceDisabledReason != reason {
			continue
		}
		h := ports.ServiceHold{UserID: id}
		if u.ServiceDisabledAt != nil {
			h.SinceMS = u.ServiceDisabledAt.UnixMilli()
		}
		out = append(out, h)
	}
	slices.SortFunc(out, func(a, b ports.ServiceHold) int { return cmp.Compare(a.UserID, b.UserID) })
	return out, nil
}

type fakeGroups struct {
	groups []*domain.Group
	calls  int
}

func (f *fakeGroups) List(context.Context) ([]*domain.Group, error) {
	f.calls++
	return f.groups, nil
}

// harness is one service over fakes, on a clock the test can move.
type harness struct {
	svc      *Service
	live     *fakeLive
	settings *fakeSettings
	users    *fakeUsers
	panels   *fakePanels
	fetches  *fakeFetches
	history  *fakeHistory
	flags    *fakeFlags
	geo      *fakeGeo
	signals  *fakeSignals
	reviews  *fakeReviews
	holds    *fakeHolds
	groups   *fakeGroups
	now      time.Time
}

func newHarness() *harness {
	h := &harness{
		live:     &fakeLive{},
		settings: &fakeSettings{},
		users:    &fakeUsers{byID: map[int64]*domain.User{}},
		panels:   &fakePanels{},
		fetches:  &fakeFetches{},
		history:  &fakeHistory{},
		flags:    &fakeFlags{},
		geo:      &fakeGeo{},
		signals:  &fakeSignals{},
		reviews:  &fakeReviews{rows: map[int64]domain.RiskReview{}},
		groups:   &fakeGroups{},
		now:      testNow,
	}
	h.holds = &fakeHolds{users: h.users}
	h.svc = New(Deps{
		Live: h.live, Settings: h.settings, Users: h.users, Panels: h.panels,
		Fetches: h.fetches, History: h.history, Flags: h.flags,
		Geo: h.geo, Signals: h.signals, Reviews: h.reviews, Holds: h.holds, Groups: h.groups,
		Now: func() time.Time { return h.now },
	})
	return h
}

// user registers an account the users fake can find.
func (h *harness) user(id int64, upn string) {
	h.users.byID[id] = &domain.User{ID: id, UPN: upn, DisplayName: "Name " + upn}
}

// conn is one connection in a snapshot.
func conn(uid, panel int64, key, exclusion string) domain.LiveConnection {
	return domain.LiveConnection{UserID: uid, PanelID: panel, Node: "n1", SourceKey: key, IP: key, Exclusion: exclusion}
}

// pollSnapshot is a poll snapshot of conns taken at, with a meta entry per
// account, sorted as the traffic service stores it.
func pollSnapshot(at time.Time, conns ...domain.LiveConnection) *domain.LiveConnSnapshot {
	snap := &domain.LiveConnSnapshot{TakenAt: at, Source: domain.LiveSnapshotFromPoll, PanelsAsked: 2,
		Conns: conns, Users: map[int64]domain.LiveUserMeta{}}
	for _, c := range conns {
		snap.Users[c.UserID] = domain.LiveUserMeta{Stale: int(c.UserID)}
	}
	return snap
}
