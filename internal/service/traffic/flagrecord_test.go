package traffic

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/metrics"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// The flag records (flag_records, R4) of the location detector: every change
// of an account's geo attention level the poll judges, and every time its
// automatic suspension is applied or lifted by expiry. What the poll writes is
// what happened — a judged sample whose streaks were saved, a transition whose
// write committed — and nothing a refresh reads is ever a record.

// appendedBatch is one Append call as the flag history received it, with the
// context's state at the call: a database refuses a cancelled context, and an
// unbounded write can hang a poll.
type appendedBatch struct {
	recs     []domain.FlagRecord
	ctxErr   error
	deadline bool
}

// fakeFlagRecorder is a flag history that keeps every Append call. Safe for
// concurrent use: overlapping polls append from their own goroutines. A
// cancelled context fails the call after it is kept, as the database does;
// err, when set, fails every call.
type fakeFlagRecorder struct {
	mu      sync.Mutex
	batches []appendedBatch
	err     error
}

// String names each record by account, source and event, for failure
// messages: a record's params are long and say nothing a mismatch needs.
func (b appendedBatch) String() string {
	parts := make([]string, 0, len(b.recs))
	for _, r := range b.recs {
		parts = append(parts, fmt.Sprintf("%d:%s/%s", r.UserID, r.Source, r.Event))
	}
	return fmt.Sprintf("{[%s] ctxErr=%v deadline=%v}", strings.Join(parts, " "), b.ctxErr, b.deadline)
}

func (r *fakeFlagRecorder) Append(ctx context.Context, recs []domain.FlagRecord) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, bounded := ctx.Deadline()
	r.batches = append(r.batches, appendedBatch{recs: slices.Clone(recs), ctxErr: ctx.Err(), deadline: bounded})
	if err := ctx.Err(); err != nil {
		return err
	}
	return r.err
}

func (r *fakeFlagRecorder) calls() []appendedBatch {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.batches)
}

// records is every record appended on a live context, in call order: what
// the table would hold.
func (r *fakeFlagRecorder) records() []domain.FlagRecord {
	var out []domain.FlagRecord
	for _, b := range r.calls() {
		if b.ctxErr == nil {
			out = append(out, b.recs...)
		}
	}
	return out
}

// requireDetached fails unless every Append ran on a live context with its
// own deadline: detached from the caller's cancellation, never unbounded.
func requireDetached(t *testing.T, r *fakeFlagRecorder) {
	t.Helper()
	for i, b := range r.calls() {
		if b.ctxErr != nil || !b.deadline {
			t.Fatalf("append %d ran on a context with err %v, deadline %v; want a live context with its own bound", i, b.ctxErr, b.deadline)
		}
	}
}

// geoParams decodes a geo record's params.
func geoParams(t *testing.T, rec domain.FlagRecord) domain.GeoFlagParams {
	t.Helper()
	var p domain.GeoFlagParams
	if err := json.Unmarshal(rec.Params, &p); err != nil {
		t.Fatalf("params %s: %v", rec.Params, err)
	}
	return p
}

// autoParams decodes a geo_auto record's params as the SPA reads them: a
// flat object whose keys are the contract.
func autoParams(t *testing.T, rec domain.FlagRecord) map[string]any {
	t.Helper()
	if rec.Params == nil {
		return nil
	}
	var p map[string]any
	if err := json.Unmarshal(rec.Params, &p); err != nil {
		t.Fatalf("params %s: %v", rec.Params, err)
	}
	return p
}

// staticGeo places addresses from a fixed map and keeps no state, so
// overlapping polls can share it under the race detector (stubGeo counts
// its lookups unsynchronized).
type staticGeo map[string]domain.GeoLocation

func (g staticGeo) Lookup(_ context.Context, ips []string) map[string]domain.GeoLocation {
	out := map[string]domain.GeoLocation{}
	for _, ip := range ips {
		if p, ok := g[ip]; ok {
			out[ip] = p
		}
	}
	return out
}

func (staticGeo) Available(context.Context) bool { return true }

// sharer is one poll's input for user 7 in Japan and Germany at once, on a
// reader with no timestamps, so every poll is a sample.
func sharer(now time.Time) liveIPInput {
	return liveIPInput{
		clients:  []*domain.PSPClient{client(7, 1, "u7@x")},
		panelIDs: panelsOf(1),
		read:     plainRead(map[string][]string{"u7@x": {"1.1.1.1", "2.2.2.2"}}),
		now:      now,
	}
}

// ---------------------------------------------------------------------------
// The verdict's attention changes.
// ---------------------------------------------------------------------------

// Three polls in two countries against a flag_after of 3: the first makes
// the account suspect, the second changes nothing an admin would see, the
// third flags it. Two records, each stamped with its poll's instant, each
// with the branch and the streak the admin UI renders its sentence from —
// and no address anywhere in them.
func TestObserveLiveIPs_RecordsGeoAttentionTransitions(t *testing.T) {
	metrics.Reset()
	s := newObserver(twoCountries(), &memStreaks{}, flagAt(3))
	flags := &fakeFlagRecorder{}
	s.SetFlagRecorder(flags)
	start := time.Unix(1_790_000_000, 0)
	for i := range 3 {
		s.observeLiveIPs(context.Background(), sharer(start.Add(time.Duration(i)*5*time.Minute)))
		if want := []int{1, 1, 2}[i]; len(flags.records()) != want {
			t.Fatalf("after poll %d: %d records, want %d: %+v", i+1, len(flags.records()), want, flags.records())
		}
	}

	recs := flags.records()
	suspect, flagged := recs[0], recs[1]
	if suspect.UserID != 7 || suspect.Source != domain.FlagSourceGeo || suspect.Event != domain.FlagEnterSuspect ||
		suspect.Level != domain.FlagLevelSuspect || suspect.PrevLevel != domain.FlagLevelNone ||
		suspect.State != domain.GeoStateSuspect || suspect.Code != string(domain.GeoWhySuspect) ||
		suspect.AtMS != start.UnixMilli() {
		t.Fatalf("first record = %+v, want user 7 geo enter_suspect none→suspect, state/code suspect, at poll 1", suspect)
	}
	if p := geoParams(t, suspect); p.Over != 1 || p.Flagged || p.Tier != domain.GeoTierCountry || p.Evidence.Why == nil {
		t.Fatalf("first record's params = %+v, want over 1, not latched, country tier, the verdict's why", p)
	}
	if flagged.Event != domain.FlagEnterFlagged || flagged.Level != domain.FlagLevelFlagged ||
		flagged.PrevLevel != domain.FlagLevelSuspect || flagged.State != domain.GeoStateFlagged ||
		flagged.Code != string(domain.GeoWhyFlaggedSustained) || flagged.AtMS != start.Add(10*time.Minute).UnixMilli() {
		t.Fatalf("second record = %+v, want enter_flagged suspect→flagged, flagged_sustained, at poll 3", flagged)
	}
	if p := geoParams(t, flagged); p.Over != 3 || !p.Flagged {
		t.Fatalf("second record's params = %+v, want over 3 and latched", p)
	}
	for _, r := range recs {
		for _, ip := range []string{"1.1.1.1", "2.2.2.2"} {
			if strings.Contains(string(r.Params), ip) {
				t.Fatalf("a record's params carry the address %s: %s", ip, r.Params)
			}
		}
	}
	requireDetached(t, flags)
}

// A transition is recorded only once the streak it moved to is saved. A
// failed save means the next poll judges from the old streak, so the change
// did not happen; recording it would leave a history whose next record
// contradicts it (a second enter_suspect, or a leave never preceded by an
// enter). Mutation: append without checking the save.
func TestObserveLiveIPs_RecordsNothingWhenTheStreakSaveFails(t *testing.T) {
	metrics.Reset()
	store := &memStreaks{saveErr: errors.New("database is locked")}
	s := newObserver(twoCountries(), store, flagAt(3))
	flags := &fakeFlagRecorder{}
	s.SetFlagRecorder(flags)

	s.observeLiveIPs(context.Background(), sharer(time.Now()))

	if store.saved != 1 || counterFor(t, "psp_geo_verdict_total{state=suspect}") != 1 {
		t.Fatal("precondition: want the user judged suspect and the save attempted")
	}
	if got := flags.calls(); len(got) != 0 {
		t.Fatalf("appends = %+v, want none: the streak the transition moved to was never saved", got)
	}
}

// A failed streak load makes the poll judge everyone from a clean streak.
// A latched account then reads as newly suspect, which is not a change that
// happened: it was flagged all along, and the next good poll would record
// its "enter" a second time. A cycle judged without its history records
// nothing. Mutation: drop the load check.
func TestObserveLiveIPs_RecordsNothingWhenTheStreakLoadFails(t *testing.T) {
	metrics.Reset()
	store := &memStreaks{
		data:    map[int64]domain.GeoRecord{7: {UserID: 7, State: domain.GeoStateFlagged, Streak: domain.GeoStreak{Over: 5, Flagged: true, Tier: domain.GeoTierCountry}}},
		loadErr: errors.New("db down"),
	}
	s := newObserver(twoCountries(), store, flagAt(3))
	flags := &fakeFlagRecorder{}
	s.SetFlagRecorder(flags)

	s.observeLiveIPs(context.Background(), sharer(time.Now()))

	if store.saved != 1 || counterFor(t, "psp_geo_verdict_total{state=suspect}") != 1 {
		t.Fatal("precondition: want the latched user judged suspect from a clean streak, and saved")
	}
	if got := flags.calls(); len(got) != 0 {
		t.Fatalf("appends = %+v, want none: this cycle judged without the stored streaks", got)
	}
}

// Without a streak store every poll starts every streak from nothing, so a
// sharer would "enter" flagged on every poll and never leave. Nothing is
// recorded where nothing is persisted. Two polls, each flagging on its own.
func TestObserveLiveIPs_RecordsNothingWithoutAStreakStore(t *testing.T) {
	metrics.Reset()
	s := newObserver(twoCountries(), nil, flagAt(1))
	flags := &fakeFlagRecorder{}
	s.SetFlagRecorder(flags)

	now := time.Now()
	s.observeLiveIPs(context.Background(), sharer(now))
	s.observeLiveIPs(context.Background(), sharer(now.Add(5*time.Minute)))

	if got := counterFor(t, "psp_geo_verdict_total{state=flagged}"); got != 2 {
		t.Fatalf("precondition: flagged verdicts = %d, want 2", got)
	}
	if got := flags.calls(); len(got) != 0 {
		t.Fatalf("appends = %+v, want none without persisted streaks", got)
	}
}

// A latched account that disconnects is still flagged — idle freezes the
// streak, so disconnecting is not an acquittal — and the bell still shows
// it. Its record says nothing happened. The other account, newly in two
// countries, is the control: the recorder is wired and the poll did record.
func TestObserveLiveIPs_IdleFlaggedUserRecordsNothing(t *testing.T) {
	metrics.Reset()
	latched := domain.GeoStreak{Over: 3, Flagged: true, Tier: domain.GeoTierCountry}
	store := &memStreaks{data: map[int64]domain.GeoRecord{7: {UserID: 7, State: domain.GeoStateFlagged, Streak: latched}}}
	geo := &stubGeo{available: true, places: map[string]domain.GeoLocation{"1.1.1.8": at("JP"), "2.2.2.8": at("DE")}}
	s := newObserver(geo, store, flagAt(3))
	flags := &fakeFlagRecorder{}
	s.SetFlagRecorder(flags)

	s.observeLiveIPs(context.Background(), liveIPInput{
		clients:  []*domain.PSPClient{client(7, 1, "u7@x"), client(8, 1, "u8@x")},
		panelIDs: panelsOf(1),
		read:     plainRead(map[string][]string{"u8@x": {"1.1.1.8", "2.2.2.8"}}),
		now:      time.Now(),
	})

	if got := store.data[7]; got.State != domain.GeoStateIdle || !got.Streak.Flagged {
		t.Fatalf("precondition: user 7 saved as %q latched=%v, want idle and still latched", got.State, got.Streak.Flagged)
	}
	recs := flags.records()
	if len(recs) != 1 || recs[0].UserID != 8 || recs[0].Event != domain.FlagEnterSuspect {
		t.Fatalf("records = %+v, want only user 8's enter_suspect", recs)
	}
}

// Overlapping polls judge one at a time (geoJudgeMu), so the second finds
// the first one's write and spaces the account: one sample, one change, one
// record. Unserialized, both would judge from the same empty state and the
// history would say the account entered suspect twice. The gate holds the
// first load, rows already read, until the second poll has read too or half
// a second passes. Mutation: remove geoJudgeMu.
func TestObserveLiveIPs_OverlappingPollsRecordOneTransition(t *testing.T) {
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
	s := newObserver(nil, store, flagAt(3))
	s.SetGeoResolver(staticGeo{"1.1.1.1": at("JP"), "2.2.2.2": at("DE")})
	flags := &fakeFlagRecorder{}
	s.SetFlagRecorder(flags)

	now := time.Now()
	start := make(chan struct{})
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			in := sharer(now)
			in.minSpacing = 150 * time.Second
			s.observeLiveIPs(context.Background(), in)
		}()
	}
	close(start)
	wg.Wait()

	if loads.Load() != 2 {
		t.Fatalf("streak loads = %d, want one per poll", loads.Load())
	}
	recs := flags.records()
	if len(recs) != 1 || recs[0].Event != domain.FlagEnterSuspect {
		t.Fatalf("records = %+v, want one enter_suspect: two overlapping polls judged one moment twice", recs)
	}
}

// The transition has happened once its streak is saved, so a poll cancelled
// right after the save (a closed "poll now" tab, a shutdown) still records
// it: on a context detached from the cancellation and bounded on its own,
// like the audit row of a committed suspension.
func TestObserveLiveIPs_RecordsTransitionsOnADetachedContext(t *testing.T) {
	metrics.Reset()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	store := &upsertStreaks{afterSave: cancel}
	s := newObserver(twoCountries(), store, flagAt(3))
	flags := &fakeFlagRecorder{}
	s.SetFlagRecorder(flags)

	in := sharer(time.Now())
	s.observeLiveIPs(ctx, in)

	if ctx.Err() == nil || !store.wrote(7) {
		t.Fatal("precondition: want the streaks saved and the poll's context cancelled after")
	}
	if recs := flags.records(); len(recs) != 1 || recs[0].Event != domain.FlagEnterSuspect {
		t.Fatalf("records = %+v, want the saved transition recorded", flags.calls())
	}
	requireDetached(t, flags)
}

// The record is a by-product; the poll is not. A refused append is counted,
// so a history that has stopped growing shows up somewhere besides a Warn,
// and the poll's own work — the saved streaks — is unaffected.
func TestObserveLiveIPs_FlagRecorderFailureIsCounted(t *testing.T) {
	metrics.Reset()
	store := &upsertStreaks{}
	s := newObserver(twoCountries(), store, flagAt(3))
	s.SetFlagRecorder(&fakeFlagRecorder{err: errors.New("database is locked")})

	s.observeLiveIPs(context.Background(), sharer(time.Now()))

	if !store.wrote(7) {
		t.Fatal("the streaks were not saved")
	}
	if got := counterFor(t, "psp_flag_record_write_errors_total"); got != 1 {
		t.Fatalf("psp_flag_record_write_errors_total = %d, want 1", got)
	}
}

// ---------------------------------------------------------------------------
// The automatic suspension.
// ---------------------------------------------------------------------------

// An applied suspension is recorded as geo_auto moving from nothing to
// suspended, coded by the tier, with the numbers the portal text was built
// from — and never the ban's reason, which names the places beside the
// account in a sentence built for the audit log. A due ban the row refused
// (someone else's hold) did not happen and is not recorded.
func TestApplyGeoBans_RecordsTheSuspension(t *testing.T) {
	metrics.Reset()
	users := &fakeUserRepo{users: map[int64]*domain.User{
		1: {ID: 1, Enabled: true},
		2: {ID: 2, Enabled: true, ServiceDisabledReason: domain.DisabledServiceManual},
	}}
	s, _, _ := newEnforcer(users, nil)
	flags := &fakeFlagRecorder{}
	s.SetFlagRecorder(flags)
	now := time.Now()

	s.enforceGeo(context.Background(), listed(users), []geoBan{ban(1), ban(2)}, nil, now)

	recs := flags.records()
	if len(recs) != 1 {
		t.Fatalf("records = %+v, want one: user 1's suspension", recs)
	}
	r := recs[0]
	if r.UserID != 1 || r.Source != domain.FlagSourceGeoAuto || r.Event != domain.FlagAutoSuspended ||
		r.Level != domain.FlagLevelSuspended || r.PrevLevel != domain.FlagLevelNone || r.State != "" ||
		r.Code != string(domain.GeoTierCountry) || r.AtMS != now.UnixMilli() {
		t.Fatalf("record = %+v, want user 1 geo_auto auto_suspended none→suspended, code country, at the poll's instant", r)
	}
	want := map[string]any{"tier": "country", "spread": float64(2), "duration_minutes": float64(60)}
	if got := autoParams(t, r); !reflect.DeepEqual(got, want) {
		t.Fatalf("params = %v, want exactly %v (no reason: it names places)", got, want)
	}
	requireDetached(t, flags)
}

// A suspension lifted because its time was up is recorded as suspended →
// nothing, coded "expired", with how long it was and when it began (null for
// a row written without a timestamp, which is lifted at once). One that is
// not due yet is not lifted and not recorded.
func TestLiftDueGeoSuspensions_RecordsTheExpiryLift(t *testing.T) {
	metrics.Reset()
	began := time.Now().Add(-2 * time.Hour).Truncate(time.Millisecond)
	users := &fakeUserRepo{users: map[int64]*domain.User{
		2: {ID: 2, Enabled: true, ServiceDisabledReason: domain.DisabledGeoAutoSuspend, ServiceDisabledAt: &began},
		3: {ID: 3, Enabled: true, ServiceDisabledReason: domain.DisabledGeoAutoSuspend, ServiceDisabledAt: minutesAgo(59)},
		4: {ID: 4, Enabled: true, ServiceDisabledReason: domain.DisabledGeoAutoSuspend},
	}}
	s, _, _ := newEnforcer(users, nil)
	flags := &fakeFlagRecorder{}
	s.SetFlagRecorder(flags)
	now := time.Now()

	s.enforceGeo(context.Background(), listed(users), nil, nil, now)

	byUser := map[int64]domain.FlagRecord{}
	for _, r := range flags.records() {
		byUser[r.UserID] = r
	}
	if len(byUser) != 2 || len(flags.records()) != 2 {
		t.Fatalf("records = %+v, want one each for users 2 and 4", flags.records())
	}
	for uid, suspendedAt := range map[int64]any{2: float64(began.UnixMilli()), 4: nil} {
		r, ok := byUser[uid]
		if !ok {
			t.Fatalf("no record for user %d: %+v", uid, flags.records())
		}
		if r.Source != domain.FlagSourceGeoAuto || r.Event != domain.FlagAutoLiftedExpiry || r.Level != domain.FlagLevelNone ||
			r.PrevLevel != domain.FlagLevelSuspended || r.Code != "expired" || r.AtMS != now.UnixMilli() {
			t.Fatalf("user %d: record = %+v, want geo_auto auto_lifted_expiry suspended→none, code expired, at the poll's instant", uid, r)
		}
		want := map[string]any{"duration_minutes": float64(60), "suspended_at_ms": suspendedAt}
		if got := autoParams(t, r); !reflect.DeepEqual(got, want) {
			t.Fatalf("user %d: params = %v, want %v", uid, got, want)
		}
	}
	requireDetached(t, flags)
}

// Cancelled in the middle of the phase: the suspension whose write committed
// is real, so its record is written on a context the cancellation does not
// reach; the one after it was never started and has no record.
func TestApplyGeoBans_RecordOfACommittedTransitionSurvivesCancellation(t *testing.T) {
	metrics.Reset()
	users := &fakeUserRepo{users: map[int64]*domain.User{
		1: {ID: 1, GroupID: 3, Enabled: true},
		2: {ID: 2, GroupID: 3, Enabled: true},
	}}
	s, sus, _ := newEnforcer(users, map[int64]ports.UISettings{3: banAfter(4)})
	s.SetGeoStreakStore(&upsertStreaks{data: map[int64]domain.GeoRecord{1: consumedRecord(1), 2: consumedRecord(2)}})
	flags := &fakeFlagRecorder{}
	s.SetFlagRecorder(flags)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sus.afterWrite = cancel

	s.enforceGeo(ctx, listed(users), []geoBan{consumedBan(1), consumedBan(2)}, nil, time.Now())

	if got := users.users[1].ServiceDisabledReason; got != domain.DisabledGeoAutoSuspend || len(sus.suspendCalls) != 1 {
		t.Fatalf("precondition: user 1 = %q after %v suspend calls, want only user 1 suspended", got, sus.suspendCalls)
	}
	recs := flags.records()
	if len(recs) != 1 || recs[0].UserID != 1 || recs[0].Event != domain.FlagAutoSuspended {
		t.Fatalf("records = %+v (appends %+v), want user 1's auto_suspended", recs, flags.calls())
	}
	requireDetached(t, flags)
}

// The same for a lift: the one in flight when the poll was cancelled
// committed, so it is recorded; the next one waits for the next poll.
func TestLiftDueGeoSuspensions_RecordOfACommittedLiftSurvivesCancellation(t *testing.T) {
	metrics.Reset()
	users := &fakeUserRepo{users: map[int64]*domain.User{
		3: {ID: 3, Enabled: true, ServiceDisabledReason: domain.DisabledGeoAutoSuspend, ServiceDisabledAt: minutesAgo(120)},
		4: {ID: 4, Enabled: true, ServiceDisabledReason: domain.DisabledGeoAutoSuspend, ServiceDisabledAt: minutesAgo(121)},
	}}
	s, sus, _ := newEnforcer(users, nil)
	flags := &fakeFlagRecorder{}
	s.SetFlagRecorder(flags)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sus.afterWrite = cancel

	s.enforceGeo(ctx, listed(users), nil, nil, time.Now())

	if len(sus.liftCalls) != 1 || sus.liftCalls[0] != 4 {
		t.Fatalf("precondition: lift calls = %v, want only user 4", sus.liftCalls)
	}
	recs := flags.records()
	if len(recs) != 1 || recs[0].UserID != 4 || recs[0].Event != domain.FlagAutoLiftedExpiry {
		t.Fatalf("records = %+v (appends %+v), want user 4's auto_lifted_expiry", recs, flags.calls())
	}
	requireDetached(t, flags)
}

// ---------------------------------------------------------------------------
// The refresh, and what the recorder may do.
// ---------------------------------------------------------------------------

// A refresh is never a detector sample, so it judges nothing and records no
// change. A poll on the same service, over the same connections, does
// record: the zero is the refresh's doing, not an unwired recorder.
func TestRefreshLiveConnections_RecordsNoFlags(t *testing.T) {
	metrics.Reset()
	sightings := func(at int64) map[string][]domain.LiveIPSighting {
		return map[string][]domain.LiveIPSighting{"u7@x": {seen("1.1.1.1", at), seen("2.2.2.2", at)}}
	}
	s := newRefresher(map[int64]ports.XUIClient{1: detailPanel(sightings(1000))}, client(7, 1, "u7@x"))
	s.SetGeoResolver(twoCountries())
	s.SetGeoPolicy(domain.DefaultGeoPolicy())
	s.SetGeoStreakStore(&upsertStreaks{})
	flags := &fakeFlagRecorder{}
	s.SetFlagRecorder(flags)

	if snap := mustRefresh(t, s); len(snap.Conns) != 2 {
		t.Fatalf("precondition: the refresh lists %+v, want both live connections", snap.Conns)
	}
	if got := flags.calls(); len(got) != 0 {
		t.Fatalf("appends after a refresh = %+v, want none", got)
	}

	s.observeLiveIPs(context.Background(), liveIPInput{
		clients:  []*domain.PSPClient{client(7, 1, "u7@x")},
		panelIDs: panelsOf(1),
		read:     detailRead(sightings(1060)),
	})
	if recs := flags.records(); len(recs) != 1 || recs[0].Event != domain.FlagEnterSuspect {
		t.Fatalf("records after a poll = %+v, want one enter_suspect (control: the recorder is wired)", recs)
	}
}

// Guard: the poll is handed a flag history it can only APPEND to. The
// records are only worth reading if nothing the detector runs can rewrite or
// delete them, and the detector must never read its own history back into a
// verdict (only the streaks drive geo_auto). So the interface is pinned to
// {Append}, and the field that holds it to that interface: widening either
// — to the concrete store, or with a List or a DeleteBefore — fails here.
// Mutation: add a method to FlagRecorder (and to the fake above).
func TestFlagRecorderIsAppendOnly(t *testing.T) {
	var got []string
	for m := range reflect.TypeFor[FlagRecorder]().Methods() {
		got = append(got, m.Name)
	}
	if want := []string{"Append"}; !slices.Equal(got, want) {
		t.Fatalf("FlagRecorder exposes %v, want exactly %v", got, want)
	}
	f, ok := reflect.TypeFor[Service]().FieldByName("flagRec")
	if !ok {
		t.Fatal("Service has no flagRec field; update this test with the recorder's new home")
	}
	if f.Type != reflect.TypeFor[FlagRecorder]() {
		t.Fatalf("Service.flagRec is a %v, want FlagRecorder", f.Type)
	}
	// The setter takes that interface too, so the composition root cannot
	// hand over more than it declares.
	set, ok := reflect.TypeFor[*Service]().MethodByName("SetFlagRecorder")
	if !ok || set.Type.NumIn() != 2 || set.Type.In(1) != reflect.TypeFor[FlagRecorder]() {
		t.Fatalf("SetFlagRecorder = %v, want func(*Service, FlagRecorder)", set.Type)
	}
}
