package traffic

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// The flush-failure fixture below starts every user and client from the same
// counters, and the panel reports the same cumulative figures for every
// client, so one set of numbers covers all of them:
//
//	before cycle 1:  user lifetime 5000 / 7000 (12000), period baselines 2000 (1000 / 1000);
//	                 client lifetime 5000 / 7000, LastRaw 100 / 100, baselines 1000 / 1000 / 2000
//	failing cycles:  echo 700 / 500 (delta 600 / 400), each further one 50 / 50 more
//	final cycle:     echo 900 / 800 (delta 200 / 300 after one durable cycle,
//	                 800 / 700 when the earlier cycles' counters were lost)
//
// Whatever happened to the earlier flushes, each client really moved 800 /
// 700, and that is what every lifetime must have gained by the end — the
// user's once per client. A fresh legacy client starts at zero instead
// (lifetime, LastRaw and baselines), so the 900 / 800 of the final echo is
// what it moved. A brand-new user starts at zero too (lifetime, baselines,
// no bootstrap cutoff), and so do its clients' lifetimes and baselines.
const (
	flushLifeUp, flushLifeDown = 5000, 7000
	flushRawUp, flushRawDown   = 100, 100
	flushEcho2Up, flushEcho2Dn = 900, 800
)

// flushUser is one user of the fixture. Its client is on the shared
// psp_client tier or the legacy ownership tier — or one on each, for a user
// caught mid-migration — and its period either runs on (started now) or
// started a year ago, so that the first cycle rolls it. Every client takes
// the user's ID, which is unique on either tier.
type flushUser struct {
	id     int64
	shared bool
	// mixed gives the user one client on each tier; shared is then ignored.
	mixed bool
	rolls bool
	// fresh makes the legacy client one the poll has never observed
	// (LastRaw 0) and that was created after the user's bootstrap cutoff,
	// so its whole cumulative counter is a bootstrap delta the user counts.
	fresh bool
	// newUser makes this cycle's bytes the user's first: lifetime 0 and no
	// bootstrap cutoff, so the next cycle reads the latest user snapshot
	// both to seed the lifetime and as the cutoff.
	newUser bool
}

// tiers reports which tiers the user has a client on.
func (s flushUser) tiers() (legacy, shared bool) {
	if s.mixed {
		return true, true
	}
	return !s.shared, s.shared
}

// userStart is the user's stored lifetime and period baselines (total, up,
// down) before the first cycle.
func (s flushUser) userStart() (life, baselines [3]int64) {
	if s.newUser {
		return [3]int64{}, [3]int64{}
	}
	return [3]int64{flushLifeUp, flushLifeDown, flushLifeUp + flushLifeDown}, [3]int64{2000, 1000, 1000}
}

// clientStart is the stored lifetime, LastRaw and period baselines of the
// user's client on one tier before the first cycle.
func (s flushUser) clientStart(shared bool) (life, raw, baselines [3]int64) {
	if s.fresh && !shared {
		return [3]int64{}, [3]int64{}, [3]int64{}
	}
	raw = [3]int64{flushRawUp, flushRawDown, flushRawUp + flushRawDown}
	if s.newUser {
		return [3]int64{}, raw, [3]int64{}
	}
	return [3]int64{flushLifeUp, flushLifeDown, flushLifeUp + flushLifeDown}, raw, [3]int64{1000, 1000, 2000}
}

// moved is what the user's client on one tier really transferred over the
// whole run.
func (s flushUser) moved(shared bool) (up, down int64) {
	_, raw, _ := s.clientStart(shared)
	return flushEcho2Up - raw[0], flushEcho2Dn - raw[1]
}

// inlineWriteUserRepo records every single-row UpdateTrafficState. In a poll
// only the rollover's inline write takes that path: the per-user state goes
// through BatchUpdateTrafficState, which the embedded fake applies without
// coming back through here — and which batchErr, when set, fails whole, as
// the production transaction does.
type inlineWriteUserRepo struct {
	*fakeUserRepo
	inline   map[int64][]domain.User
	batchErr error
}

func (r *inlineWriteUserRepo) UpdateTrafficState(ctx context.Context, u *domain.User) error {
	r.inline[u.ID] = append(r.inline[u.ID], *u)
	return r.fakeUserRepo.UpdateTrafficState(ctx, u)
}

func (r *inlineWriteUserRepo) BatchUpdateTrafficState(ctx context.Context, users []*domain.User) error {
	if r.batchErr != nil {
		return r.batchErr
	}
	return r.fakeUserRepo.BatchUpdateTrafficState(ctx, users)
}

// resumeCountingDisabler counts the poll's service transitions per user and
// applies them to the row the way user.Service does, so a second cycle sees
// a resumed user as resumed instead of resuming them again.
type resumeCountingDisabler struct {
	users    *fakeUserRepo
	resumes  map[int64]int
	suspends map[int64]int
}

func (d *resumeCountingDisabler) SetEnabledAndSync(context.Context, int64, bool, domain.AutoDisabledReason, string) error {
	return nil
}

func (d *resumeCountingDisabler) SetServiceSuspendedAndSync(_ context.Context, userID int64, reason domain.AutoDisabledReason, _ string) error {
	d.suspends[userID]++
	if u, ok := d.users.users[userID]; ok {
		u.ServiceDisabledReason = reason
	}
	return nil
}

func (d *resumeCountingDisabler) ResumeServiceAndSync(_ context.Context, userID int64) error {
	d.resumes[userID]++
	if u, ok := d.users.users[userID]; ok {
		u.ServiceDisabledReason = domain.DisabledNone
		u.AutoDisabledReason = domain.DisabledNone
	}
	return nil
}

// flushFixture is a poll over fakes that behave like the database: reads
// hand out copies and only a successful write lands, so a flush that fails
// really leaves the stored rows where they were.
type flushFixture struct {
	users     *inlineWriteUserRepo
	ownership *fakeOwnershipRepo
	psp       *fakePSPClientRepo
	panel     *fakeXUIClient
	disabler  *resumeCountingDisabler
	svc       *Service
	specs     []flushUser
	oldStart  time.Time
	curStart  time.Time
}

func newFlushFixture(specs []flushUser) *flushFixture {
	f := &flushFixture{
		specs:    specs,
		oldStart: time.Now().AddDate(-1, 0, 0), // a year ago → monthly rollover fires
		curStart: time.Now(),                   // never rolls during the test
	}
	base := &fakeUserRepo{users: map[int64]*domain.User{}}
	f.users = &inlineWriteUserRepo{fakeUserRepo: base, inline: map[int64][]domain.User{}}
	f.ownership = &fakeOwnershipRepo{byUser: map[int64][]*domain.XUIClientEntry{}}
	f.psp = &fakePSPClientRepo{byUser: map[int64][]*domain.PSPClient{}, durable: true}
	f.panel = &fakeXUIClient{}
	f.disabler = &resumeCountingDisabler{users: base, resumes: map[int64]int{}, suspends: map[int64]int{}}
	seen := time.Now().Add(-time.Hour) // the users' bootstrap cutoff
	for _, s := range specs {
		start, reason := f.curStart, domain.DisabledNone
		if s.rolls {
			// Suspended for last period's quota, so the rollover has a
			// side effect to count: handing the quota back resumes them.
			start, reason = f.oldStart, domain.DisabledTrafficExceeded
		}
		life, pb := s.userStart()
		u := &domain.User{ID: s.id, Enabled: true,
			TrafficLimitBytes: 1 << 30, TrafficResetPeriod: domain.ResetMonthly, TrafficPeriodStart: &start,
			ServiceDisabledReason: reason,
			LifetimeUpBytes:       life[0], LifetimeDownBytes: life[1], LifetimeTotalBytes: life[2],
			PeriodBaselineBytes: pb[0], PeriodBaselineUpBytes: pb[1], PeriodBaselineDownBytes: pb[2]}
		if !s.newUser {
			u.LifetimeBaselineAt = &seen
		}
		base.users[s.id] = u
		legacy, shared := s.tiers()
		if shared {
			life, raw, bl := s.clientStart(true)
			f.psp.byUser[s.id] = []*domain.PSPClient{{ID: s.id, UserID: s.id, PanelID: 10, Email: f.email(s, true),
				LifetimeUpBytes: life[0], LifetimeDownBytes: life[1], LifetimeTotalBytes: life[2],
				LastRawUpBytes: raw[0], LastRawDownBytes: raw[1], LastRawTotalBytes: raw[2],
				PeriodBaselineUpBytes: bl[0], PeriodBaselineDownBytes: bl[1], PeriodBaselineTotalBytes: bl[2]}}
		}
		if legacy {
			life, raw, bl := s.clientStart(false)
			created := f.oldStart
			if s.fresh {
				created = seen.Add(30 * time.Minute)
			}
			f.ownership.byUser[s.id] = []*domain.XUIClientEntry{{ID: s.id, UserID: s.id, PanelID: 10, InboundID: 20,
				ClientEmail: f.email(s, false), CreatedAt: created,
				LifetimeUpBytes: life[0], LifetimeDownBytes: life[1], LifetimeTotalBytes: life[2],
				LastRawUpBytes: raw[0], LastRawDownBytes: raw[1], LastRawTotalBytes: raw[2],
				PeriodBaselineUpBytes: bl[0], PeriodBaselineDownBytes: bl[1], PeriodBaselineTotalBytes: bl[2]}}
		}
	}
	pool := &fakeXUIPool{clients: map[int64]ports.XUIClient{10: f.panel}}
	f.svc = New(f.users, f.ownership, &fakeTrafficRepo{}, nil, nil, pool, f.disabler)
	f.svc.SetPSPClientRepo(f.psp)
	return f
}

func (f *flushFixture) email(s flushUser, shared bool) string {
	if shared {
		return fmt.Sprintf("u%d@psp.local", s.id)
	}
	return fmt.Sprintf("u%d-c0@example.test", s.id)
}

// poll runs one cycle with the panel reporting up / down for every client.
func (f *flushFixture) poll(t *testing.T, up, down int64) {
	t.Helper()
	stats := make([]ports.ClientTraffic, 0, 2*len(f.specs))
	for _, s := range f.specs {
		legacy, shared := s.tiers()
		if legacy {
			stats = append(stats, ports.ClientTraffic{Email: f.email(s, false), Up: up, Down: down})
		}
		if shared {
			stats = append(stats, ports.ClientTraffic{Email: f.email(s, true), Up: up, Down: down})
		}
	}
	f.panel.inbounds = []ports.Inbound{{ID: 20, ClientStats: stats}}
	if err := f.svc.PollOnce(context.Background()); err != nil {
		t.Fatalf("PollOnce: %v", err)
	}
}

// clientCounters is the stored lifetime, LastRaw and period baselines of the
// user's client on one tier.
func (f *flushFixture) clientCounters(s flushUser, shared bool) (life, raw, baselines [3]int64) {
	if shared {
		c := f.psp.byUser[s.id][0]
		return [3]int64{c.LifetimeUpBytes, c.LifetimeDownBytes, c.LifetimeTotalBytes},
			[3]int64{c.LastRawUpBytes, c.LastRawDownBytes, c.LastRawTotalBytes},
			[3]int64{c.PeriodBaselineUpBytes, c.PeriodBaselineDownBytes, c.PeriodBaselineTotalBytes}
	}
	e := f.ownership.byUser[s.id][0]
	return [3]int64{e.LifetimeUpBytes, e.LifetimeDownBytes, e.LifetimeTotalBytes},
		[3]int64{e.LastRawUpBytes, e.LastRawDownBytes, e.LastRawTotalBytes},
		[3]int64{e.PeriodBaselineUpBytes, e.PeriodBaselineDownBytes, e.PeriodBaselineTotalBytes}
}

// clientPeriod sums the stored period usage (lifetime minus baseline) of the
// user's clients across both tiers.
func (f *flushFixture) clientPeriod(s flushUser) int64 {
	var sum int64
	legacy, shared := s.tiers()
	for _, tier := range []bool{false, true} {
		if (tier && shared) || (!tier && legacy) {
			life, _, bl := f.clientCounters(s, tier)
			sum += life[2] - bl[2]
		}
	}
	return sum
}

// check asserts, after the final cycle, that every user and client counted
// the bytes exactly once, and that a rolled user rolled once: one inline
// rollover write, one resume, baselines frozen at the lifetime before the
// first cycle on the user and on each of its clients, whose period usage
// then adds up to the user's.
func (f *flushFixture) check(t *testing.T) {
	t.Helper()
	for _, s := range f.specs {
		u := f.users.users[s.id]
		life0, pb0 := s.userStart()
		legacy, shared := s.tiers()
		var up, down, clientPeriod0 int64
		for _, tier := range []bool{false, true} {
			if (tier && !shared) || (!tier && !legacy) {
				continue
			}
			mu, md := s.moved(tier)
			up, down = up+mu, down+md
			cl0, _, bl0 := s.clientStart(tier)
			clientPeriod0 += cl0[2] - bl0[2]
			life, raw, baselines := f.clientCounters(s, tier)
			if want := [3]int64{cl0[0] + mu, cl0[1] + md, cl0[2] + mu + md}; life != want {
				t.Errorf("user %d shared=%v client lifetime = %v, want %v", s.id, tier, life, want)
			}
			if want := [3]int64{flushEcho2Up, flushEcho2Dn, flushEcho2Up + flushEcho2Dn}; raw != want {
				t.Errorf("user %d shared=%v client LastRaw = %v, want %v (the final cycle's echo)", s.id, tier, raw, want)
			}
			wantBL := bl0 // period runs on: untouched
			if s.rolls {
				wantBL = cl0 // frozen before the first cycle
			}
			if baselines != wantBL {
				t.Errorf("user %d shared=%v client period baselines = %v, want %v", s.id, tier, baselines, wantBL)
			}
			if tier {
				if n := countBatched(f.psp.batchUpdated, s.id); n != 1 {
					t.Errorf("user %d shared client appears %d times in the final cycle's flush, want 1", s.id, n)
				}
			}
		}
		wantLife := [3]int64{life0[0] + up, life0[1] + down, life0[2] + up + down}
		if got := [3]int64{u.LifetimeUpBytes, u.LifetimeDownBytes, u.LifetimeTotalBytes}; got != wantLife {
			t.Errorf("user %d lifetime = %v, want %v (the bytes counted exactly once)", s.id, got, wantLife)
		}
		wantUser := pb0 // period runs on: untouched
		// For a period that runs on, the clients' period usage started off
		// the user's by however much they differed before the run.
		offset := (life0[2] - pb0[0]) - clientPeriod0
		wantInline, wantResumes := 0, 0
		if s.rolls {
			// Frozen at the lifetime before the first cycle: all of the run's
			// bytes are the new period's.
			wantUser = [3]int64{life0[2], life0[0], life0[1]}
			offset = 0
			wantInline, wantResumes = 1, 1
			if gotUp, gotDown := u.PeriodUsedSplit(); gotUp != up || gotDown != down {
				t.Errorf("user %d PeriodUsedSplit = (%d, %d), want (%d, %d)", s.id, gotUp, gotDown, up, down)
			}
			if u.TrafficPeriodStart == nil || !u.TrafficPeriodStart.After(f.oldStart) {
				t.Errorf("user %d period start = %v, want advanced past %v", s.id, u.TrafficPeriodStart, f.oldStart)
			}
		} else if !u.TrafficPeriodStart.Equal(f.curStart) {
			t.Errorf("user %d period start = %v, want %v (no rollover)", s.id, u.TrafficPeriodStart, f.curStart)
		}
		if got := [3]int64{u.PeriodBaselineBytes, u.PeriodBaselineUpBytes, u.PeriodBaselineDownBytes}; got != wantUser {
			t.Errorf("user %d period baselines (total, up, down) = %v, want %v", s.id, got, wantUser)
		}
		if cp := f.clientPeriod(s); cp+offset != u.PeriodUsed() {
			t.Errorf("user %d client period %d (+%d) != user period %d", s.id, cp, offset, u.PeriodUsed())
		}
		inline := f.users.inline[s.id]
		if len(inline) != wantInline {
			t.Errorf("user %d inline rollover writes = %d, want %d (the rollover is persisted once and never repeated)", s.id, len(inline), wantInline)
		} else if wantInline == 1 {
			if r := inline[0]; r.TrafficPeriodStart == nil || !r.TrafficPeriodStart.Equal(*u.TrafficPeriodStart) || r.PeriodBaselineBytes != wantUser[0] {
				t.Errorf("user %d inline rollover wrote period start %v baseline %d, want %v / %d",
					s.id, r.TrafficPeriodStart, r.PeriodBaselineBytes, u.TrafficPeriodStart, wantUser[0])
			}
		}
		if got := f.disabler.resumes[s.id]; got != wantResumes {
			t.Errorf("user %d resumed %d times, want %d", s.id, got, wantResumes)
		}
		if got := f.disabler.suspends[s.id]; got != 0 {
			t.Errorf("user %d suspended %d times, want 0", s.id, got)
		}
	}
}

// flushFail is what fails in one cycle: the legacy ownership counter flush,
// the shared psp_client counter flush, or the shared-client list read.
type flushFail struct{ owner, psp, list bool }

var (
	ownerFails = flushFail{owner: true}
	pspFails   = flushFail{psp: true}
	listFails  = flushFail{list: true}
)

// flushCase is one run of the fixture: a cycle per entry of fails (one when
// empty), with the panel echoing 700 / 500 (50 / 50 more each further
// cycle, so a client whose counters landed is not idle in the next) and
// that entry's failures injected, then a final cycle echoing 900 / 800 with
// nothing failing.
type flushCase struct {
	name  string
	users []flushUser
	fails []flushFail
}

func (tc flushCase) run(t *testing.T) {
	boom := errors.New("database is locked")
	f := newFlushFixture(tc.users)
	fails := tc.fails
	if len(fails) == 0 {
		fails = []flushFail{{}}
	}
	for i, fail := range fails {
		f.ownership.batchErr, f.psp.batchErr, f.psp.listErr = nil, nil, nil
		if fail.owner {
			f.ownership.batchErr = boom
		}
		if fail.psp {
			f.psp.batchErr = boom
		}
		if fail.list {
			f.psp.listErr = boom
		}
		f.poll(t, 700+50*int64(i), 500+50*int64(i))
	}
	f.ownership.batchErr, f.psp.batchErr, f.psp.listErr = nil, nil, nil
	f.poll(t, flushEcho2Up, flushEcho2Dn)
	f.check(t)
}

// A counter flush that fails in the cycle that rolls a user's period used to
// count that cycle's bytes twice. The rollover wrote the user's row inline,
// lifetime and all, before the end-of-cycle counter flushes ran, while the
// client's LastRaw — written only by the flush — stayed behind; the next
// cycle measured the same bytes again from the old LastRaw and added them to
// a lifetime that already had them. Lifetime and period usage both came out
// high (the new period could suspend a user who had barely used it), and the
// client baselines the rollover reseeded were lost with the flush, so the
// per-server period went on showing last period's usage.
//
// Pinned on each tier: the shared psp_client flush failing (once, and twice
// in a row), the legacy ownership flush failing (also for a client whose
// first reading is a bootstrap delta), and both for a user whose bytes are
// the first they ever moved — the cycle's user snapshot, which the next
// cycle reads back to seed a zero lifetime and as a missing bootstrap
// cutoff, used to be written regardless and counted them twice (shared) or
// dropped them (legacy). The hold-back is per user: a failed flush holds
// back only the users it carried a client of, so a rolled legacy user next
// to a failed shared flush keeps its bytes, and a failed ownership flush
// also holds back the shared client of the same (mid-migration) user, and
// nobody else's. A shared-client list that fails in the cycle that would
// have retried a lost reseed must not use the retry up, and a retry owed on
// the shared tier alone must not re-freeze the ownership tier when the
// retrying cycle is held back in turn. After the cycle
// that succeeds, every lifetime holds the bytes once, the rollover was
// written once and resumed the user once, and the client baselines sit on
// the new period. A run where nothing fails rolls exactly as before.
func TestPollOnce_RolloverCycleCounterFlushFailureCountsOnce(t *testing.T) {
	cases := []flushCase{
		{
			name:  "shared client flush fails",
			users: []flushUser{{id: 1, shared: true, rolls: true}},
			fails: []flushFail{pspFails},
		},
		{
			// The retried reseed is lost again and retried again.
			name:  "shared client flush fails twice in a row",
			users: []flushUser{{id: 1, shared: true, rolls: true}},
			fails: []flushFail{pspFails, pspFails},
		},
		{
			// The cycle that takes the retry lists no shared client, so it
			// cannot reseed one; it must queue the shared tier again.
			name:  "shared client list fails in the cycle after the flush",
			users: []flushUser{{id: 1, shared: true, rolls: true}},
			fails: []flushFail{pspFails, listFails},
		},
		{
			// The retry owed after the list failure is the shared tier's
			// alone, and stays so when the retrying cycle's ownership flush
			// fails: the ownership baselines landed with the rollover, and
			// re-freezing them would drop the bytes counted since.
			name:  "ownership flush fails in the cycle retrying a mixed user's shared reseed",
			users: []flushUser{{id: 1, mixed: true, rolls: true}},
			fails: []flushFail{listFails, ownerFails},
		},
		{
			name:  "ownership flush fails",
			users: []flushUser{{id: 1, rolls: true}},
			fails: []flushFail{ownerFails},
		},
		{
			// The re-measured first reading is still a bootstrap delta, and
			// must be judged against the cutoff that counted it the first
			// time — so the inline rollover keeps the loaded cutoff too.
			name:  "ownership flush fails on a first-seen client",
			users: []flushUser{{id: 1, rolls: true, fresh: true}},
			fails: []flushFail{ownerFails},
		},
		{
			name:  "shared client flush fails on a new user's first traffic",
			users: []flushUser{{id: 1, shared: true, rolls: true, newUser: true}},
			fails: []flushFail{pspFails},
		},
		{
			name:  "ownership flush fails on a new user's first traffic",
			users: []flushUser{{id: 1, rolls: true, fresh: true, newUser: true}},
			fails: []flushFail{ownerFails},
		},
		{
			// Its shared client must not land either, or its LastRaw moves
			// past bytes the held-back row never got.
			name:  "ownership flush fails on a mixed user",
			users: []flushUser{{id: 1, mixed: true, rolls: true}},
			fails: []flushFail{ownerFails},
		},
		{
			name:  "ownership flush fails next to a rolled shared user",
			users: []flushUser{{id: 1}, {id: 2, shared: true, rolls: true}},
			fails: []flushFail{ownerFails},
		},
		{
			name:  "shared client flush fails next to a rolled legacy user",
			users: []flushUser{{id: 1, rolls: true}, {id: 2, shared: true}},
			fails: []flushFail{pspFails},
		},
		{
			name:  "no flush fails",
			users: []flushUser{{id: 1, rolls: true}, {id: 2, shared: true, rolls: true}, {id: 3}, {id: 4, shared: true}, {id: 5, mixed: true, rolls: true}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) { tc.run(t) })
	}
}

// Mid-period, a failed counter flush holds back the traffic state of the
// users it carried a client of, and the next cycle re-derives it from the
// LastRaw that did not move. That held for the shared psp_client flush and
// must keep holding; a failed legacy ownership flush did NOT hold anything
// back, so the user's row took the cycle's bytes while the ownership LastRaw
// stayed behind and the next cycle counted them again. The user snapshot
// was never held back at all, so a user whose first bytes rode a failed
// flush got them counted twice off the snapshot-seeded lifetime (shared) or
// lost them to the snapshot-derived cutoff (legacy). And the hold-back was
// global: a failed shared flush also held back every legacy-only user,
// whose ownership counters had landed, and so lost their cycle for good.
func TestPollOnce_MidPeriodCounterFlushFailureCountsOnce(t *testing.T) {
	cases := []flushCase{
		{
			name:  "shared client flush fails",
			users: []flushUser{{id: 1, shared: true}},
			fails: []flushFail{pspFails},
		},
		{
			name:  "ownership flush fails",
			users: []flushUser{{id: 1}, {id: 2, shared: true}},
			fails: []flushFail{ownerFails},
		},
		{
			name:  "ownership flush fails on a mixed user",
			users: []flushUser{{id: 1, mixed: true}},
			fails: []flushFail{ownerFails},
		},
		{
			name:  "shared client flush fails next to a legacy user",
			users: []flushUser{{id: 1}, {id: 2, shared: true}},
			fails: []flushFail{pspFails},
		},
		{
			name:  "shared client flush fails on a new user's first traffic",
			users: []flushUser{{id: 1, shared: true, newUser: true}},
			fails: []flushFail{pspFails},
		},
		{
			name:  "ownership flush fails on a new user's first traffic",
			users: []flushUser{{id: 1, fresh: true, newUser: true}},
			fails: []flushFail{ownerFails},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) { tc.run(t) })
	}
}

// A shared-client list that fails in a rollover cycle meters no shared
// client, so the shared tier has nothing to reseed; the user still rolls.
// Its psp_client baselines used to keep the previous period's freeze until
// the next rollover, so the per-server period went on showing last month's
// usage all month. The next cycle now reseeds the shared tier — and only
// that tier: the ownership baselines landed with the rollover, and
// re-freezing them a cycle late would drop the bytes the user has counted
// on that tier since, leaving the per-server period short of the user's.
func TestPollOnce_SharedClientListFailureReseedsSharedTierNextCycle(t *testing.T) {
	cases := []flushCase{
		{
			name:  "shared user",
			users: []flushUser{{id: 1, shared: true, rolls: true}},
			fails: []flushFail{listFails},
		},
		{
			name:  "mixed user",
			users: []flushUser{{id: 1, mixed: true, rolls: true}},
			fails: []flushFail{listFails},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) { tc.run(t) })
	}
}

// A user-state batch that fails after the client counters landed loses the
// cycle's bytes for every user in it — the undercounting side, accepted. In
// the cycle that rolled a user, though, the client baselines the rollover
// reseeded DID land, frozen ahead of bytes the user's row then never got,
// so the per-client period ran ahead of the user's for the rest of the
// period. The next cycle now reseeds those clients. The user's snapshot
// stays back with the row: written anyway, it would carry the lost bytes,
// and the next cycle would seed a new user's zero lifetime from it — after
// the clients were re-frozen without those bytes, leaving the two periods
// apart the other way.
func TestPollOnce_RolloverCycleUserStateFailureKeepsClientPeriodInStep(t *testing.T) {
	cases := []struct {
		name string
		user flushUser
	}{
		{name: "shared user", user: flushUser{id: 1, shared: true, rolls: true}},
		{name: "legacy user", user: flushUser{id: 1, rolls: true}},
		{name: "mixed user", user: flushUser{id: 1, mixed: true, rolls: true}},
		{name: "new user", user: flushUser{id: 1, shared: true, rolls: true, newUser: true}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFlushFixture([]flushUser{tc.user})
			f.users.batchErr = errors.New("database is locked")
			f.poll(t, 700, 500)
			f.users.batchErr = nil
			f.poll(t, flushEcho2Up, flushEcho2Dn)

			u := f.users.users[1]
			life0, _ := tc.user.userStart()
			// The bytes really moved, and the final cycle's share of them:
			// whatever the failed cycle loses, nothing may count twice, and
			// the final cycle's bytes must land.
			var moved, last int64
			legacy, shared := tc.user.tiers()
			for _, tier := range []bool{false, true} {
				if (tier && shared) || (!tier && legacy) {
					mu, md := tc.user.moved(tier)
					moved, last = moved+mu+md, last+500
				}
			}
			if got := u.LifetimeTotalBytes - life0[2]; got < last || got > moved {
				t.Errorf("user lifetime gained %d, want between %d (the final cycle's) and %d (all of it, once)", got, last, moved)
			}
			if cp := f.clientPeriod(tc.user); cp != u.PeriodUsed() {
				t.Errorf("client period %d != user period %d (client baselines frozen ahead of bytes the user's row never got)", cp, u.PeriodUsed())
			}
			if got := f.disabler.resumes[1]; got != 1 {
				t.Errorf("resumed %d times, want 1", got)
			}
		})
	}
}

// An admin who sets a user's period usage between a rollover cycle whose
// counter flush failed and the next cycle re-baselines the user's clients
// to that figure. The reseed the failed cycle queued is older than the
// admin's; it used to run anyway and put the clients back on the
// rollover's freeze, so the per-server period stayed short of the user's by
// the admin's figure for the rest of the period.
func TestPollOnce_SetPeriodUsageSupersedesQueuedReseed(t *testing.T) {
	s := flushUser{id: 1, shared: true, rolls: true}
	f := newFlushFixture([]flushUser{s})
	f.psp.batchErr = errors.New("database is locked")
	f.poll(t, 700, 500)
	f.psp.batchErr = nil
	const set = 5000
	if err := f.svc.SetPeriodUsage(context.Background(), 1, set); err != nil {
		t.Fatalf("SetPeriodUsage: %v", err)
	}
	f.poll(t, flushEcho2Up, flushEcho2Dn)

	u := f.users.users[1]
	// The failed cycle's bytes reached no row before the set, so the next
	// cycle re-measures them on top of it: 800 / 700 in all.
	if want := int64(set + 1500); u.PeriodUsed() != want {
		t.Errorf("user period = %d, want %d", u.PeriodUsed(), want)
	}
	if cp := f.clientPeriod(s); cp != u.PeriodUsed() {
		t.Errorf("client period %d != user period %d (the queued rollover reseed overwrote the admin's)", cp, u.PeriodUsed())
	}
}
