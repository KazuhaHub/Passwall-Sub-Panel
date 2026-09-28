package traffic

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/metrics"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// The accounts an admin trusts (risk_reviews) are judged exempt by the poll:
// the location verdict answers exempt with the trusted code, every streak is
// reset so no suspension can fall due, and the set is read once per judging
// step, never once per account.

// fakeTrusted is the trusted-account list: ids, or err on every read. Safe
// for concurrent use, and it counts the reads.
type fakeTrusted struct {
	mu    sync.Mutex
	ids   []int64
	err   error
	calls int
}

func (f *fakeTrusted) ListTrusted(context.Context) ([]int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	return slices.Clone(f.ids), nil
}

func (f *fakeTrusted) set(ids []int64, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ids, f.err = ids, err
}

func (f *fakeTrusted) reads() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

// twoSharers is one poll's input for users 7 and 8, each in Japan and
// Germany at once on addresses of their own (two accounts are not a shared
// exit), on a reader with no timestamps, so every poll is a sample.
func twoSharers(now time.Time) liveIPInput {
	return liveIPInput{
		clients:  []*domain.PSPClient{client(7, 1, "u7@x"), client(8, 1, "u8@x")},
		panelIDs: panelsOf(1),
		read: plainRead(map[string][]string{
			"u7@x": {"1.1.1.1", "2.2.2.2"},
			"u8@x": {"1.1.1.3", "2.2.2.3"},
		}),
		now: now,
	}
}

func twoSharersGeo() *stubGeo {
	return &stubGeo{available: true, places: map[string]domain.GeoLocation{
		"1.1.1.1": at("JP"), "2.2.2.2": at("DE"), "1.1.1.3": at("JP"), "2.2.2.3": at("DE"),
	}}
}

// A latched account an admin trusts is judged exempt with the trusted code on
// the very next poll: every streak reset (the ban streak too, so nothing can
// be due), and its leave recorded — a clear, stamped with the poll's instant,
// naming the branch that cleared it. Mutation: skip setting Trusted on the
// judged policy, and the account stays flagged with no record.
func TestJudge_TrustedAccountIsExemptWithTheTrustedCode(t *testing.T) {
	metrics.Reset()
	latched := domain.GeoStreak{Over: 3, BanOver: 2, Flagged: true, Tier: domain.GeoTierCountry}
	store := &memStreaks{data: map[int64]domain.GeoRecord{7: {UserID: 7, State: domain.GeoStateFlagged, Streak: latched}}}
	s := newObserver(twoCountries(), store, flagAt(3))
	flags := &fakeFlagRecorder{}
	s.SetFlagRecorder(flags)
	s.SetTrustedLister(&fakeTrusted{ids: []int64{7}})
	now := time.Unix(1_790_000_000, 0)

	s.observeLiveIPs(context.Background(), sharer(now))

	got := store.data[7]
	if got.State != domain.GeoStateExempt || got.Evidence.Why == nil || got.Evidence.Why.Code != domain.GeoWhyTrusted {
		t.Fatalf("saved = %q with why %+v, want exempt with the trusted code", got.State, got.Evidence.Why)
	}
	if got.Streak != (domain.GeoStreak{}) {
		t.Fatalf("saved streak = %+v, want every streak reset", got.Streak)
	}
	recs := flags.records()
	if len(recs) != 1 {
		t.Fatalf("records = %+v, want exactly the leave", recs)
	}
	r := recs[0]
	if r.UserID != 7 || r.Source != domain.FlagSourceGeo || r.Event != domain.FlagLeaveFlagged ||
		r.Level != domain.FlagLevelNone || r.PrevLevel != domain.FlagLevelFlagged ||
		r.State != domain.GeoStateExempt || r.Code != string(domain.GeoWhyTrusted) || r.AtMS != now.UnixMilli() {
		t.Fatalf("record = %+v, want user 7 geo leave_flagged flagged→none, exempt/trusted, at the poll", r)
	}
	if step := (domain.FlagStep{Source: r.Source, Level: r.Level, State: r.State, AtMS: r.AtMS}); !step.IsClear() {
		t.Fatalf("record %+v is not a clear: a trusted leave must count as one", r)
	}
}

// An armed group and a sustained spread: the untrusted control is suspended
// on its first due poll, the trusted account never falls due — not on the
// first poll, not on any after — so no ban is collected for it and the
// suspender is never asked. Driven through PollOnce, which leaves the
// trusted set to the lister. Mutation: read the set but judge without it,
// and user 1 is suspended alongside user 2.
func TestJudge_TrustedAccountIsNeverBanDue(t *testing.T) {
	metrics.Reset()
	users := &fakeUserRepo{users: map[int64]*domain.User{
		1: {ID: 1, GroupID: 3, Enabled: true},
		2: {ID: 2, GroupID: 3, Enabled: true},
	}}
	g := newGeoPoll(users, map[string][]string{
		emailOf(1): {"1.1.1.1", "2.2.2.2"},
		emailOf(2): {"1.1.1.3", "2.2.2.3"},
	}, twoSharersGeo(), map[int64]ports.UISettings{3: armed()})
	trusted := &fakeTrusted{ids: []int64{1}}
	g.svc.SetTrustedLister(trusted)

	for range 3 {
		g.poll(t)
	}

	if got := users.users[2].ServiceDisabledReason; got != domain.DisabledGeoAutoSuspend {
		t.Fatalf("control's service reason = %q, want geo_auto (or the group is not armed and this proves nothing)", got)
	}
	if got := users.users[1].ServiceDisabledReason; got != domain.DisabledNone {
		t.Fatalf("trusted account's service reason = %q, want none", got)
	}
	if slices.Contains(g.sus.suspendCalls, 1) {
		t.Fatalf("suspender asked for %v: a ban was collected for the trusted account", g.sus.suspendCalls)
	}
	if got := g.store.data[1]; got.State != domain.GeoStateExempt || got.Streak != (domain.GeoStreak{}) {
		t.Fatalf("trusted account saved as %q with streak %+v, want exempt with every streak reset", got.State, got.Streak)
	}
	if got := trusted.reads(); got != 3 {
		t.Fatalf("trusted set read %d times over 3 polls, want once per poll", got)
	}
}

// The set is read once per judging step, however many accounts the step
// judges, and not at all by an observation that judges nobody (no
// attributable owner, or the shared clients could not be listed). Mutation:
// read it per account, and two accounts read it twice.
func TestTrustedSet_ReadOncePerJudgingStep(t *testing.T) {
	metrics.Reset()
	store := &memStreaks{}
	s := newObserver(twoSharersGeo(), store, flagAt(3))
	trusted := &fakeTrusted{ids: []int64{7}}
	s.SetTrustedLister(trusted)
	now := time.Unix(1_790_000_000, 0)

	s.observeLiveIPs(context.Background(), twoSharers(now))
	if got := trusted.reads(); got != 1 {
		t.Fatalf("reads after one step over two accounts = %d, want 1", got)
	}
	if store.data[7].State != domain.GeoStateExempt || store.data[8].State != domain.GeoStateSuspect {
		t.Fatalf("saved 7=%q 8=%q, want the trusted account exempt and the other judged (suspect)",
			store.data[7].State, store.data[8].State)
	}

	s.observeLiveIPs(context.Background(), twoSharers(now.Add(5*time.Minute)))
	if got := trusted.reads(); got != 2 {
		t.Fatalf("reads after two steps = %d, want 2", got)
	}

	nobody := twoSharers(now.Add(10 * time.Minute))
	nobody.clients = nil
	s.observeLiveIPs(context.Background(), nobody)
	unlisted := twoSharers(now.Add(15 * time.Minute))
	unlisted.clientsErr = errors.New("psp_clients unreadable")
	s.observeLiveIPs(context.Background(), unlisted)
	if got := trusted.reads(); got != 2 {
		t.Fatalf("reads after two observations that judged nobody = %d, want still 2", got)
	}
}

// A failed read keeps the last set read: re-judging a trusted account as
// untrusted for one poll would write a suspect or flagged verdict and its
// record, and the next good read a leave — history that never happened.
// Before the first success there is nothing to keep, and nobody is trusted.
// Mutation: return nil on a failed read, and user 7 is judged suspect.
func TestTrustedSet_FailedReadKeepsTheLastSet(t *testing.T) {
	metrics.Reset()
	ctx := context.Background()
	store := &memStreaks{}
	s := newObserver(twoCountries(), store, flagAt(3))
	flags := &fakeFlagRecorder{}
	s.SetFlagRecorder(flags)
	trusted := &fakeTrusted{err: errors.New("database is locked")}
	s.SetTrustedLister(trusted)

	if got := s.trustedSet(ctx); got != nil {
		t.Fatalf("a failed first read = %v, want nil: nothing was ever read", got)
	}

	trusted.set([]int64{7}, nil)
	if got := s.trustedSet(ctx); !reflect.DeepEqual(got, map[int64]bool{7: true}) {
		t.Fatalf("a good read = %v, want {7}", got)
	}

	trusted.set(nil, errors.New("database is locked"))
	if got := s.trustedSet(ctx); !reflect.DeepEqual(got, map[int64]bool{7: true}) {
		t.Fatalf("a failed read after a good one = %v, want the last set, {7}", got)
	}

	now := time.Unix(1_790_000_000, 0)
	s.observeLiveIPs(ctx, sharer(now))
	s.observeLiveIPs(ctx, sharer(now.Add(5*time.Minute)))
	if got := store.data[7]; got.State != domain.GeoStateExempt || got.Evidence.Why == nil || got.Evidence.Why.Code != domain.GeoWhyTrusted {
		t.Fatalf("user 7 saved as %q (why %+v) with the lister failing, want exempt/trusted from the last set", got.State, got.Evidence.Why)
	}
	if recs := flags.records(); len(recs) != 0 {
		t.Fatalf("records = %+v, want none: the account stayed trusted throughout", recs)
	}
}

// With no lister wired nobody is trusted: the set is nil, and an account in
// two countries is judged as it always was. An explicit nil is the same.
func TestTrustedSet_NilListerTrustsNobody(t *testing.T) {
	metrics.Reset()
	ctx := context.Background()
	store := &memStreaks{}
	s := newObserver(twoCountries(), store, flagAt(3))
	if got := s.trustedSet(ctx); got != nil {
		t.Fatalf("trusted set without a lister = %v, want nil", got)
	}
	s.SetTrustedLister(nil)
	if got := s.trustedSet(ctx); got != nil {
		t.Fatalf("trusted set with a nil lister = %v, want nil", got)
	}

	s.observeLiveIPs(ctx, sharer(time.Unix(1_790_000_000, 0)))
	if got := store.data[7]; got.State != domain.GeoStateSuspect || got.Evidence.Why == nil || got.Evidence.Why.Code != domain.GeoWhySuspect {
		t.Fatalf("user 7 saved as %q (why %+v), want suspect: nobody is trusted", got.State, got.Evidence.Why)
	}
}
