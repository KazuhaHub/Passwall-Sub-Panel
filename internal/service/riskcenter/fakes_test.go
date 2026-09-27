package riskcenter

import (
	"context"
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
}

func (f *fakeFlags) List(_ context.Context, flt ports.FlagRecordFilter) ([]domain.FlagRecord, int64, error) {
	f.got = flt
	return f.rows, f.total, f.err
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
		now:      testNow,
	}
	h.svc = New(Deps{
		Live: h.live, Settings: h.settings, Users: h.users, Panels: h.panels,
		Fetches: h.fetches, History: h.history, Flags: h.flags,
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
