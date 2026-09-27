package traffic

import (
	"context"
	"errors"
	"maps"
	"reflect"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/metrics"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// The connection history (connection_history, R3) is fed by the poll and by
// nothing else: each detector sample records the connections of the
// accounts it JUDGED, so a row's count is the number of samples that saw
// the connection. A user the spacing rule skipped was not sampled, and an
// admin's refresh is never a sample at all.

// recordedBatch is one Record call as the history received it.
type recordedBatch struct {
	conns []domain.LiveConnection
	at    time.Time
	// ctxErr and deadline are the context's state at the call: a database
	// refuses a cancelled context, and an unbounded write can hang a poll.
	ctxErr   error
	deadline bool
}

// fakeRecorder is a connection history that keeps every Record call. Safe
// for concurrent use: overlapping polls record from their own goroutines.
// err, when set, fails every call after keeping it, as a refusing database
// would.
type fakeRecorder struct {
	mu      sync.Mutex
	batches []recordedBatch
	err     error
}

func (r *fakeRecorder) Record(ctx context.Context, conns []domain.LiveConnection, at time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, bounded := ctx.Deadline()
	r.batches = append(r.batches, recordedBatch{conns: slices.Clone(conns), at: at, ctxErr: ctx.Err(), deadline: bounded})
	return r.err
}

func (r *fakeRecorder) calls() []recordedBatch {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.batches)
}

// recordedUsers is the set of accounts one batch holds connections of.
func recordedUsers(b recordedBatch) map[int64]int {
	out := map[int64]int{}
	for _, c := range b.conns {
		out[c.UserID]++
	}
	return out
}

// A judged sample is recorded once, stamped with the poll's instant, and
// holds exactly what the live view shows for the judged accounts: every
// live connection on the panel and node that reported it, the excluded
// ones with their reason (the history's exclusion filter names them), each
// with its place — and never the address the upstream merely remembers.
func TestObserveLiveIPs_RecordsJudgedConnections(t *testing.T) {
	metrics.Reset()
	now := time.Unix(1_790_000_000, 0)
	geo := &stubGeo{available: true, places: map[string]domain.GeoLocation{
		"1.1.1.1": placeIn("JP", "Tokyo", "Shinjuku"),
		"3.3.3.3": placeIn("DE", "Berlin", "Berlin"),
	}}
	rec := &fakeRecorder{}
	s := newObserver(geo, &upsertStreaks{}, domain.DefaultGeoPolicy())
	s.SetConnectionRecorder(rec)
	s.observeLiveIPs(context.Background(), liveIPInput{
		users:    []*domain.User{{ID: 7}, {ID: 8}},
		clients:  []*domain.PSPClient{client(7, 1, "u7@x"), client(7, 2, "u7@y"), client(8, 1, "u8@x")},
		panelIDs: panelsOf(1, 2),
		read: func(pid int64) domain.PanelLiveIPs {
			if pid == 2 {
				return detailRead(map[string][]domain.LiveIPSighting{
					"u7@y": {{IP: "1.1.1.1", Node: "n2", SeenAt: 500}},
				})(pid)
			}
			return detailRead(map[string][]domain.LiveIPSighting{
				"u7@x": {seen("1.1.1.1", 1000), seen("2.2.2.2", 700), seen("203.0.113.9", 1000)}, // 2.2.2.2: memory
				"u8@x": {seen("3.3.3.3", 990)},
			})(pid)
		},
		ignore: "203.0.113.9",
		now:    now,
	})

	calls := rec.calls()
	if len(calls) != 1 {
		t.Fatalf("Record calls = %d, want one per judged sample", len(calls))
	}
	snap := mustSnapshot(t, s)
	if !reflect.DeepEqual(calls[0].conns, snap.Conns) {
		t.Fatalf("recorded %+v, want the judged accounts' connections as the view shows them %+v", calls[0].conns, snap.Conns)
	}
	if !calls[0].at.Equal(now) {
		t.Fatalf("recorded at %v, want the poll's instant %v", calls[0].at, now)
	}
	// What the comparison above stands on, spelled out: four connections,
	// the listed exit kept with its reason, places filled, no memory.
	var listed, placed int
	for _, c := range calls[0].conns {
		if c.IP == "2.2.2.2" {
			t.Fatalf("the remembered address was recorded: %+v", c)
		}
		if c.Exclusion == domain.AddressExcludedListed {
			listed++
		}
		if c.Place.CountryCode != "" {
			placed++
		}
	}
	if len(calls[0].conns) != 4 || listed != 1 || placed != 3 {
		t.Fatalf("recorded %+v, want 4 connections, 1 listed, 3 placed", calls[0].conns)
	}
}

// Spacing decides who is sampled, and the history counts samples: a user
// judged a minute ago (by a manual poll, say) is still in the live view but
// is not recorded again, so clicking "poll now" cannot inflate a
// connection's count. The user judged ten minutes ago is recorded.
func TestObserveLiveIPs_DoesNotRecordSpacedUsers(t *testing.T) {
	metrics.Reset()
	now := time.Now()
	store := &upsertStreaks{data: map[int64]domain.GeoRecord{
		7: {UserID: 7, State: domain.GeoStateClean, UpdatedAtMS: now.Add(-time.Minute).UnixMilli()},
		8: {UserID: 8, State: domain.GeoStateClean, UpdatedAtMS: now.Add(-10 * time.Minute).UnixMilli()},
	}}
	rec := &fakeRecorder{}
	s := newObserver(twoCountries(), store, domain.DefaultGeoPolicy())
	s.SetConnectionRecorder(rec)
	s.observeLiveIPs(context.Background(), liveIPInput{
		clients:  []*domain.PSPClient{client(7, 1, "u7@x"), client(8, 1, "u8@x")},
		panelIDs: panelsOf(1),
		read: plainRead(map[string][]string{
			"u7@x": {"1.1.1.1", "2.2.2.2"},
			"u8@x": {"1.1.1.8", "2.2.2.8"},
		}),
		minSpacing: 150 * time.Second,
		now:        now,
	})

	if store.wrote(7) || !store.wrote(8) {
		t.Fatal("precondition: want user 7 spaced and user 8 judged")
	}
	if got := len(mustSnapshot(t, s).Conns); got != 4 {
		t.Fatalf("precondition: the view lists %d connections, want both users' 4", got)
	}
	calls := rec.calls()
	if len(calls) != 1 {
		t.Fatalf("Record calls = %d, want 1", len(calls))
	}
	if got, want := recordedUsers(calls[0]), map[int64]int{8: 2}; !reflect.DeepEqual(got, want) {
		t.Fatalf("recorded connections per user = %v, want %v — the spaced user was not sampled", got, want)
	}
}

// stampingStreaks is the streak table as overlapping polls share it: Save
// merges and stamps every row it writes with the time of the write (the
// repository's autoUpdateTime), Load hands back a copy, and a lock keeps the
// calls apart the way the database connection does. gate, when set, runs in
// every Load AFTER the rows were read and before they are returned: the
// moment two unserialized polls can both hold the same state.
type stampingStreaks struct {
	mu   sync.Mutex
	data map[int64]domain.GeoRecord
	gate func()
}

func (st *stampingStreaks) Load(context.Context) (map[int64]domain.GeoRecord, error) {
	st.mu.Lock()
	rows := maps.Clone(st.data)
	st.mu.Unlock()
	if st.gate != nil {
		st.gate()
	}
	return rows, nil
}

func (st *stampingStreaks) Save(_ context.Context, recs map[int64]domain.GeoRecord) error {
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.data == nil {
		st.data = map[int64]domain.GeoRecord{}
	}
	stamp := time.Now().UnixMilli()
	for uid, r := range recs {
		r.UpdatedAtMS = stamp
		st.data[uid] = r
	}
	return nil
}

// The scheduled poll and a staff "poll now" can run at once. If both read
// the streaks before either wrote them, both would judge the user from the
// same state — two samples of one moment: two verdicts counted and two
// history counts for one connection. Loading, judging and saving are one
// step per poll, so the second poll finds the first one's write and spaces
// the user. The gate holds the first Load, rows already read, until the
// second poll has read too, or half a second passes: without the
// serialization both polls meet there holding the same (empty) state; with
// it the second poll is still waiting its turn, and reads the first one's
// write.
func TestObserveLiveIPs_OverlappingPollsRecordEachSourceOnce(t *testing.T) {
	metrics.Reset()
	var loads atomic.Int32
	both := make(chan struct{})
	store := &stampingStreaks{gate: func() {
		if loads.Add(1) == 2 {
			close(both)
			return
		}
		select {
		case <-both:
		case <-time.After(500 * time.Millisecond):
		}
	}}
	rec := &fakeRecorder{}
	// No geo resolver: the panel reads, the freshness merge and the live
	// view run concurrently in both polls by design, and stubGeo counts its
	// lookups unsynchronized. The verdict (unknown) is still counted.
	s := newObserver(nil, store, domain.DefaultGeoPolicy())
	s.SetConnectionRecorder(rec)

	now := time.Now()
	start := make(chan struct{})
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			s.observeLiveIPs(context.Background(), liveIPInput{
				clients:    []*domain.PSPClient{client(7, 1, "u7@x")},
				panelIDs:   panelsOf(1),
				read:       plainRead(map[string][]string{"u7@x": {"1.1.1.1", "2.2.2.2"}}),
				minSpacing: 150 * time.Second,
				now:        now,
			})
		}()
	}
	close(start)
	wg.Wait()

	if loads.Load() != 2 {
		t.Fatalf("streak loads = %d, want one per poll", loads.Load())
	}
	var holding int
	for _, b := range rec.calls() {
		if recordedUsers(b)[7] > 0 {
			holding++
		}
	}
	if holding != 1 {
		t.Fatalf("Record calls holding user 7 = %d, want 1: two overlapping polls sampled one moment twice", holding)
	}
	if got := counterFor(t, "psp_geo_verdict_total"); got != 1 {
		t.Fatalf("verdicts counted = %d, want 1 — one moment is one sample", got)
	}
}

// The history is a by-product of the poll, never a reason for it to fail:
// metering is what the poll is for. A refused write is counted, so a
// history that has stopped growing shows up somewhere besides a Warn.
func TestObserveLiveIPs_RecorderFailureDoesNotFailThePoll(t *testing.T) {
	metrics.Reset()
	users := &fakeUserRepo{users: map[int64]*domain.User{1: {ID: 1, Enabled: true}}}
	psp := &fakePSPClientRepo{byUser: map[int64][]*domain.PSPClient{1: {{ID: 1, UserID: 1, PanelID: 10, Email: "u1@10"}}}}
	pool := &fakeXUIPool{clients: map[int64]ports.XUIClient{
		10: &liveIPDetailReaderFake{fakeXUIClient: &fakeXUIClient{inbounds: []ports.Inbound{{ID: 20}}},
			sightings: map[string][]domain.LiveIPSighting{"u1@10": {{IP: "1.1.1.1", Node: "guid-a", SeenAt: 1000}}}},
	}}
	svc := New(users, &fakeOwnershipRepo{byUser: map[int64][]*domain.XUIClientEntry{}},
		&fakeTrafficRepo{}, nil, nil, pool, &fakeDisabler{})
	svc.SetPSPClientRepo(psp)
	store := &memStreaks{}
	svc.SetGeoStreakStore(store)
	rec := &fakeRecorder{err: errors.New("database is locked")}
	svc.SetConnectionRecorder(rec)

	if err := svc.PollOnce(context.Background()); err != nil {
		t.Fatalf("PollOnce = %v, want the poll to succeed with the history write failing", err)
	}
	if len(rec.calls()) != 1 || store.saved != 1 {
		t.Fatalf("Record calls %d, streak saves %d; want the sample attempted and the streaks saved", len(rec.calls()), store.saved)
	}
	if got := counterFor(t, "psp_connection_history_write_errors_total"); got != 1 {
		t.Fatalf("psp_connection_history_write_errors_total = %d, want 1", got)
	}
}

// A poll cancelled after its streaks were saved (a closed "poll now" tab, a
// shutdown) has still judged that sample, so the sample is still recorded:
// on a context detached from the poll's cancellation and bounded on its
// own, like the audit row of a committed transition.
func TestObserveLiveIPs_RecordsOnADetachedContext(t *testing.T) {
	metrics.Reset()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	store := &upsertStreaks{afterSave: cancel}
	rec := &fakeRecorder{}
	s := newObserver(nil, store, domain.DefaultGeoPolicy())
	s.SetConnectionRecorder(rec)
	s.observeLiveIPs(ctx, liveIPInput{
		clients:  []*domain.PSPClient{client(7, 1, "u7@x")},
		panelIDs: panelsOf(1),
		read:     plainRead(map[string][]string{"u7@x": {"1.1.1.1"}}),
	})

	if ctx.Err() == nil || !store.wrote(7) {
		t.Fatal("precondition: want the streaks saved and the poll's context cancelled after")
	}
	calls := rec.calls()
	if len(calls) != 1 {
		t.Fatalf("Record calls = %d, want the judged sample recorded", len(calls))
	}
	if calls[0].ctxErr != nil || !calls[0].deadline {
		t.Fatalf("recorded on a context with err %v, deadline %v; want a live context with its own bound", calls[0].ctxErr, calls[0].deadline)
	}
}

// Guard: a refresh is never a detector sample, so it records no history —
// a click must not become a count. The poll on the same service does
// record, so the zero is the refresh's doing and not an unwired recorder.
// Mutation: record the refresh's connections in RefreshLiveConnections.
func TestRefreshLiveConnections_WritesNoHistory(t *testing.T) {
	panel := detailPanel(map[string][]domain.LiveIPSighting{"u7@x": {seen("1.1.1.1", 1000)}})
	s := newRefresher(map[int64]ports.XUIClient{1: panel}, client(7, 1, "u7@x"))
	rec := &fakeRecorder{}
	s.SetConnectionRecorder(rec)

	if snap := mustRefresh(t, s); len(snap.Conns) != 1 {
		t.Fatalf("precondition: the refresh lists %+v, want the one live connection", snap.Conns)
	}
	if got := len(rec.calls()); got != 0 {
		t.Fatalf("Record calls after a refresh = %d, want none", got)
	}

	s.observeLiveIPs(context.Background(), liveIPInput{
		clients:  []*domain.PSPClient{client(7, 1, "u7@x")},
		panelIDs: panelsOf(1),
		read:     detailRead(map[string][]domain.LiveIPSighting{"u7@x": {seen("1.1.1.1", 1060)}}),
	})
	if got := len(rec.calls()); got != 1 {
		t.Fatalf("Record calls after a poll = %d, want 1 (control: the recorder is wired)", got)
	}
}
