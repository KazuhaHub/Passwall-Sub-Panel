package traffic

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	pkglog "github.com/KazuhaHub/passwall-sub-panel/internal/pkg/log"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// reseedSkippedWarn is the start of the Warn a rollover cycle logs when the
// shared-client list failed and no psp_client baseline could be reseeded.
const reseedSkippedWarn = "shared-client period baselines not reseeded"

// captureLog runs fn with pkg/log writing into a pipe and returns what it
// wrote. pkg/log binds os.Stdout when its logger is built, so the logger is
// rebuilt around the swap and again after it. Only sound while this
// package's tests stay non-parallel.
func captureLog(t *testing.T, fn func()) string {
	t.Helper()
	rd, wr, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	out := make(chan string, 1)
	go func() {
		b, _ := io.ReadAll(rd)
		out <- string(b)
	}()
	orig := os.Stdout
	func() {
		defer func() {
			os.Stdout = orig
			pkglog.SetLevel(slog.LevelInfo)
		}()
		os.Stdout = wr
		pkglog.SetLevel(slog.LevelInfo)
		fn()
	}()
	_ = wr.Close()
	s := <-out
	_ = rd.Close()
	return s
}

// countBatched returns how many times the shared client with this ID appears in
// a BatchUpdateCounters payload — the end-of-cycle flush is the only write that
// persists a rollover reseed, so presence (and uniqueness) there is what counts.
// UserServerUsage alone can't tell: fakePSPClientRepo shares pointers, so an
// unqueued client would still read back reseeded in memory.
func countBatched(batch []*domain.PSPClient, id int64) int {
	n := 0
	for _, c := range batch {
		if c.ID == id {
			n++
		}
	}
	return n
}

// periodBaselines reads a shared client's three period baselines as one value.
func periodBaselines(c *domain.PSPClient) [3]int64 {
	return [3]int64{c.PeriodBaselineUpBytes, c.PeriodBaselineDownBytes, c.PeriodBaselineTotalBytes}
}

// The user-visible bug: a migrated user's natural rollover reset the
// user-level period but left every psp_client baseline at the PREVIOUS
// period's freeze, so the per-server breakdown (UserServerUsage, sourced from
// psp_client for migrated users) kept reporting last month's usage plus this
// month's. Pins that the rollover reseeds the shared client to lifetime minus
// THIS cycle's delta — the cycle's bytes land in the new period, exactly like
// the user-level baseline — and that the per-server period rows read back as
// just that delta, per direction.
func TestPollOnce_SharedClientRolloverReseedsPeriodBaselines(t *testing.T) {
	oldStart := time.Now().AddDate(-1, 0, 0) // a year ago → monthly rollover fires
	users := &fakeUserRepo{users: map[int64]*domain.User{
		1: {ID: 1, Enabled: true, TrafficResetPeriod: domain.ResetMonthly, TrafficPeriodStart: &oldStart,
			LifetimeUpBytes: 5000, LifetimeDownBytes: 7000, LifetimeTotalBytes: 12000,
			PeriodBaselineBytes: 2000, PeriodBaselineUpBytes: 1000, PeriodBaselineDownBytes: 1000},
	}}
	// Migrated user: NO ownership rows. The shared client's baselines are the
	// previous period's freeze (2000 total); LastRaw is pre-seeded so this poll
	// produces a real delta instead of the first-observation seed.
	psp := &fakePSPClientRepo{byUser: map[int64][]*domain.PSPClient{
		1: {{ID: 1, UserID: 1, PanelID: 10, Email: "u1@psp.local",
			LifetimeUpBytes: 5000, LifetimeDownBytes: 7000, LifetimeTotalBytes: 12000,
			LastRawUpBytes: 100, LastRawDownBytes: 100, LastRawTotalBytes: 200,
			PeriodBaselineUpBytes: 1000, PeriodBaselineDownBytes: 1000, PeriodBaselineTotalBytes: 2000}},
	}}
	pool := &fakeXUIPool{clients: map[int64]ports.XUIClient{
		10: &fakeXUIClient{inbounds: []ports.Inbound{
			{ID: 20, ClientStats: []ports.ClientTraffic{{Email: "u1@psp.local", Up: 700, Down: 500}}},
		}},
	}}
	svc := New(users, &fakeOwnershipRepo{byUser: map[int64][]*domain.XUIClientEntry{}}, &fakeTrafficRepo{}, nil, nil, pool, &fakeDisabler{})
	svc.SetPSPClientRepo(psp)

	var pollErr error
	logged := captureLog(t, func() { pollErr = svc.PollOnce(context.Background()) })
	if pollErr != nil {
		t.Fatalf("PollOnce: %v", pollErr)
	}
	if strings.Contains(logged, reseedSkippedWarn) {
		t.Errorf("a rollover whose shared-client list succeeded logged %q; want it only when the list failed", reseedSkippedWarn)
	}

	// This cycle's delta: up 700-100 = 600, down 500-100 = 400, total 1000.
	u := users.users[1]
	if u.TrafficPeriodStart == nil || !u.TrafficPeriodStart.After(oldStart) {
		t.Fatalf("user period start = %v, want advanced past %v (rollover)", u.TrafficPeriodStart, oldStart)
	}
	if got := u.PeriodUsed(); got != 1000 {
		t.Fatalf("user PeriodUsed after rollover = %d, want 1000 (this cycle's delta)", got)
	}
	// The shared-tier fold is a migrated user's only source of per-direction
	// lifetime, so the Subscription-Userinfo split rides on it carrying (and
	// not swapping) direction into the user's totals.
	if up, down := u.PeriodUsedSplit(); up != 600 || down != 400 {
		t.Fatalf("user PeriodUsedSplit after rollover = (%d, %d), want (600, 400) (shared-tier fold must carry direction)", up, down)
	}
	if u.PeriodBaselineUpBytes != 5000 || u.PeriodBaselineDownBytes != 7000 {
		t.Fatalf("user period baselines up/down = %d/%d, want 5000/7000 (lifetime minus this cycle's delta)",
			u.PeriodBaselineUpBytes, u.PeriodBaselineDownBytes)
	}
	// Baseline = lifetime minus this cycle's delta = the pre-cycle lifetime.
	c := psp.byUser[1][0]
	if got, want := periodBaselines(c), [3]int64{5000, 7000, 12000}; got != want {
		t.Fatalf("shared client period baselines = %v, want %v (lifetime minus this cycle's delta)", got, want)
	}
	if n := countBatched(psp.batchUpdated, 1); n != 1 {
		t.Fatalf("shared client appears %d times in the counter flush, want 1", n)
	}

	rows, err := svc.UserServerUsage(context.Background(), 1)
	if err != nil {
		t.Fatalf("UserServerUsage: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("UserServerUsage rows = %d, want 1", len(rows))
	}
	// Pre-fix: 13000-2000 = 11000 total (last period + this one).
	if r := rows[0]; r.PeriodUpBytes != 600 || r.PeriodDownBytes != 400 || r.PeriodTotalBytes != 1000 {
		t.Fatalf("per-server period = up %d / down %d / total %d, want 600 / 400 / 1000 (this cycle only)",
			r.PeriodUpBytes, r.PeriodDownBytes, r.PeriodTotalBytes)
	}
	if rows[0].PeriodTotalBytes != u.PeriodUsed() {
		t.Fatalf("per-server period %d != user period %d", rows[0].PeriodTotalBytes, u.PeriodUsed())
	}
}

// Every shared client of a rolled-over user must land in the end-of-cycle
// counter flush EXACTLY once, whatever metering did with it this cycle. Two
// regressions are pinned here:
//
//   - A client metering never queued (idle, or not reported by the panel)
//     must be appended, or its reseeded baseline lives only in memory and
//     the next cycle reloads the stale one.
//   - A client metering already queued with a ZERO delta (first-observation
//     seed, counter-epoch adoption) must NOT be appended again. The ownership
//     tier's "zero raw delta ⇒ not queued" rule is wrong for shared clients
//     for exactly this reason; the reseed has to dedupe against what's queued.
func TestPollOnce_SharedClientRolloverQueuesEveryClientOnce(t *testing.T) {
	oldStart := time.Now().AddDate(-1, 0, 0)
	cases := []struct {
		name   string
		client *domain.PSPClient
		echo   *ports.ClientTraffic // nil = the panel doesn't report this client
		want   [3]int64             // period baselines after the rollover
	}{
		{
			name: "metered client is reseeded to lifetime minus its delta",
			client: &domain.PSPClient{ID: 1, Email: "u1@psp.local",
				LifetimeUpBytes: 1000, LifetimeDownBytes: 2000, LifetimeTotalBytes: 3000,
				LastRawUpBytes: 100, LastRawDownBytes: 100, LastRawTotalBytes: 200,
				PeriodBaselineUpBytes: 400, PeriodBaselineDownBytes: 800, PeriodBaselineTotalBytes: 1200},
			echo: &ports.ClientTraffic{Email: "u1@psp.local", Up: 400, Down: 600}, // delta 300 / 500
			want: [3]int64{1000, 2000, 3000},
		},
		{
			name: "idle client is reseeded to lifetime and appended",
			client: &domain.PSPClient{ID: 2, Email: "u1-k1@psp.local",
				LifetimeUpBytes: 2000, LifetimeDownBytes: 3000, LifetimeTotalBytes: 5000,
				LastRawUpBytes: 250, LastRawDownBytes: 350, LastRawTotalBytes: 600,
				PeriodBaselineUpBytes: 500, PeriodBaselineDownBytes: 500, PeriodBaselineTotalBytes: 1000},
			echo: &ports.ClientTraffic{Email: "u1-k1@psp.local", Up: 250, Down: 350}, // unchanged
			want: [3]int64{2000, 3000, 5000},
		},
		{
			name: "unreported client is reseeded to lifetime and appended",
			client: &domain.PSPClient{ID: 3, Email: "u1-k2@psp.local",
				LifetimeUpBytes: 700, LifetimeDownBytes: 300, LifetimeTotalBytes: 1000,
				LastRawUpBytes: 70, LastRawDownBytes: 30, LastRawTotalBytes: 100,
				PeriodBaselineUpBytes: 100, PeriodBaselineDownBytes: 100, PeriodBaselineTotalBytes: 200},
			want: [3]int64{700, 300, 1000},
		},
		{
			name: "client seeded this cycle is not queued twice",
			client: &domain.PSPClient{ID: 4, Email: "u1-k3@psp.local",
				LifetimeUpBytes: 60, LifetimeDownBytes: 40, LifetimeTotalBytes: 100,
				PeriodBaselineUpBytes: 10, PeriodBaselineDownBytes: 10, PeriodBaselineTotalBytes: 20},
			echo: &ports.ClientTraffic{Email: "u1-k3@psp.local", Up: 300, Down: 200}, // seeds, zero delta
			want: [3]int64{60, 40, 100},
		},
		{
			name: "client adopting a new counter epoch is not queued twice",
			client: &domain.PSPClient{ID: 5, Email: "u1-k4@psp.local", LastCounterEpoch: 1,
				LifetimeUpBytes: 4000, LifetimeDownBytes: 1000, LifetimeTotalBytes: 5000,
				LastRawUpBytes: 900, LastRawDownBytes: 100, LastRawTotalBytes: 1000,
				PeriodBaselineUpBytes: 3000, PeriodBaselineDownBytes: 500, PeriodBaselineTotalBytes: 3500},
			echo: &ports.ClientTraffic{Email: "u1-k4@psp.local", Up: 10, Down: 5, CounterEpoch: 2}, // re-baselines, zero delta
			want: [3]int64{4000, 1000, 5000},
		},
	}

	users := &fakeUserRepo{users: map[int64]*domain.User{
		1: {ID: 1, Enabled: true, TrafficResetPeriod: domain.ResetMonthly, TrafficPeriodStart: &oldStart,
			LifetimeUpBytes: 9000, LifetimeDownBytes: 11000, LifetimeTotalBytes: 20000, PeriodBaselineBytes: 15000},
	}}
	psp := &fakePSPClientRepo{byUser: map[int64][]*domain.PSPClient{}}
	var echoes []ports.ClientTraffic
	for _, tc := range cases {
		tc.client.UserID, tc.client.PanelID = 1, 10
		psp.byUser[1] = append(psp.byUser[1], tc.client)
		if tc.echo != nil {
			echoes = append(echoes, *tc.echo)
		}
	}
	pool := &fakeXUIPool{clients: map[int64]ports.XUIClient{
		10: &fakeXUIClient{inbounds: []ports.Inbound{{ID: 20, ClientStats: echoes}}},
	}}
	svc := New(users, &fakeOwnershipRepo{byUser: map[int64][]*domain.XUIClientEntry{}}, &fakeTrafficRepo{}, nil, nil, pool, &fakeDisabler{})
	svc.SetPSPClientRepo(psp)

	if err := svc.PollOnce(context.Background()); err != nil {
		t.Fatalf("PollOnce: %v", err)
	}
	if got := users.users[1].PeriodUsed(); got != 800 {
		t.Fatalf("user PeriodUsed after rollover = %d, want 800 (only the metered client moved bytes)", got)
	}
	if len(psp.batchUpdated) != len(cases) {
		t.Errorf("counter flush carries %d clients, want %d (one per shared client of the rolled user)",
			len(psp.batchUpdated), len(cases))
	}
	var sumPeriod int64
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := periodBaselines(tc.client); got != tc.want {
				t.Errorf("period baselines = %v, want %v", got, tc.want)
			}
			if n := countBatched(psp.batchUpdated, tc.client.ID); n != 1 {
				t.Errorf("client %d appears %d times in the counter flush, want exactly 1", tc.client.ID, n)
			}
		})
		sumPeriod += tc.client.PeriodUsedTotal()
	}
	if sumPeriod != users.users[1].PeriodUsed() {
		t.Errorf("Σ shared-client period = %d, want the user's period %d", sumPeriod, users.users[1].PeriodUsed())
	}
}

// The reseed is scoped to the users that rolled. In a cycle where one user's
// period turns over and another's does not, the other user's shared clients
// — metered or idle — keep their baselines: resetting them would drop that
// user's per-server period to this cycle's delta mid-period. And only what
// metering queued of theirs reaches the flush; an idle client stays out.
func TestPollOnce_SharedClientRolloverLeavesOtherUsersBaselines(t *testing.T) {
	oldStart := time.Now().AddDate(-1, 0, 0) // user 1 rolls
	curStart := time.Now()                   // user 2 does not
	users := &fakeUserRepo{users: map[int64]*domain.User{
		1: {ID: 1, Enabled: true, TrafficResetPeriod: domain.ResetMonthly, TrafficPeriodStart: &oldStart,
			LifetimeUpBytes: 5000, LifetimeDownBytes: 7000, LifetimeTotalBytes: 12000, PeriodBaselineBytes: 2000},
		2: {ID: 2, Enabled: true, TrafficResetPeriod: domain.ResetMonthly, TrafficPeriodStart: &curStart,
			LifetimeUpBytes: 3000, LifetimeDownBytes: 3000, LifetimeTotalBytes: 6000, PeriodBaselineBytes: 1200},
	}}
	rolled := &domain.PSPClient{ID: 1, UserID: 1, PanelID: 10, Email: "u1@psp.local",
		LifetimeUpBytes: 5000, LifetimeDownBytes: 7000, LifetimeTotalBytes: 12000,
		LastRawUpBytes: 100, LastRawDownBytes: 100, LastRawTotalBytes: 200,
		PeriodBaselineUpBytes: 1000, PeriodBaselineDownBytes: 1000, PeriodBaselineTotalBytes: 2000}
	metered := &domain.PSPClient{ID: 2, UserID: 2, PanelID: 10, Email: "u2@psp.local",
		LifetimeUpBytes: 2000, LifetimeDownBytes: 2000, LifetimeTotalBytes: 4000,
		LastRawUpBytes: 100, LastRawDownBytes: 100, LastRawTotalBytes: 200,
		PeriodBaselineUpBytes: 400, PeriodBaselineDownBytes: 600, PeriodBaselineTotalBytes: 1000}
	idle := &domain.PSPClient{ID: 3, UserID: 2, PanelID: 10, Email: "u2-k1@psp.local",
		LifetimeUpBytes: 1000, LifetimeDownBytes: 1000, LifetimeTotalBytes: 2000,
		LastRawUpBytes: 50, LastRawDownBytes: 50, LastRawTotalBytes: 100,
		PeriodBaselineUpBytes: 100, PeriodBaselineDownBytes: 100, PeriodBaselineTotalBytes: 200}
	psp := &fakePSPClientRepo{byUser: map[int64][]*domain.PSPClient{1: {rolled}, 2: {metered, idle}}}
	pool := &fakeXUIPool{clients: map[int64]ports.XUIClient{
		10: &fakeXUIClient{inbounds: []ports.Inbound{{ID: 20, ClientStats: []ports.ClientTraffic{
			{Email: "u1@psp.local", Up: 700, Down: 500},  // delta 600 / 400
			{Email: "u2@psp.local", Up: 400, Down: 300},  // delta 300 / 200
			{Email: "u2-k1@psp.local", Up: 50, Down: 50}, // idle
		}}}},
	}}
	svc := New(users, &fakeOwnershipRepo{byUser: map[int64][]*domain.XUIClientEntry{}}, &fakeTrafficRepo{}, nil, nil, pool, &fakeDisabler{})
	svc.SetPSPClientRepo(psp)

	if err := svc.PollOnce(context.Background()); err != nil {
		t.Fatalf("PollOnce: %v", err)
	}
	if u := users.users[1]; u.TrafficPeriodStart == nil || !u.TrafficPeriodStart.After(oldStart) {
		t.Fatalf("user 1 period start = %v, want advanced past %v (rollover)", u.TrafficPeriodStart, oldStart)
	}
	if u := users.users[2]; !u.TrafficPeriodStart.Equal(curStart) || u.PeriodBaselineBytes != 1200 {
		t.Fatalf("user 2 rolled (period start %v, baseline %d), want no rollover this cycle", u.TrafficPeriodStart, u.PeriodBaselineBytes)
	}
	for _, tc := range []struct {
		name string
		c    *domain.PSPClient
		want [3]int64
	}{
		{"rolled user's client is reseeded", rolled, [3]int64{5000, 7000, 12000}},
		{"other user's metered client keeps its baselines", metered, [3]int64{400, 600, 1000}},
		{"other user's idle client keeps its baselines", idle, [3]int64{100, 100, 200}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := periodBaselines(tc.c); got != tc.want {
				t.Errorf("period baselines = %v, want %v", got, tc.want)
			}
		})
	}
	for _, tc := range []struct {
		id   int64
		want int
	}{{1, 1}, {2, 1}, {3, 0}} {
		if n := countBatched(psp.batchUpdated, tc.id); n != tc.want {
			t.Errorf("client %d appears %d times in the counter flush, want %d", tc.id, n, tc.want)
		}
	}
}

// A user caught mid-migration holds BOTH ownership rows and shared clients,
// and both tiers carry bytes into the same user total. Each tier must reseed
// with its OWN client's delta so that, after the rollover, Σ ownership period
// + Σ shared-client period == the user's period exactly. The two tiers number
// their rows independently — ownership ID 1 and psp_client ID 1 below are
// different clients with different deltas — so the shared deltas must not be
// keyed into the ownership map.
func TestPollOnce_MixedTierRolloverKeepsPeriodSumExact(t *testing.T) {
	oldStart := time.Now().AddDate(-1, 0, 0)
	users := &fakeUserRepo{users: map[int64]*domain.User{
		1: {ID: 1, Enabled: true, TrafficResetPeriod: domain.ResetMonthly, TrafficPeriodStart: &oldStart,
			LifetimeUpBytes: 10000, LifetimeDownBytes: 20000, LifetimeTotalBytes: 30000, PeriodBaselineBytes: 25000,
			LifetimeBaselineAt: &oldStart}, // cutoff set → no bootstrap fold
	}}
	ownership := &fakeOwnershipRepo{byUser: map[int64][]*domain.XUIClientEntry{
		1: {
			{ID: 1, UserID: 1, PanelID: 10, InboundID: 20, ClientEmail: "u1-n1@x", CreatedAt: oldStart,
				LifetimeUpBytes: 1000, LifetimeDownBytes: 2000, LifetimeTotalBytes: 3000,
				LastRawUpBytes: 100, LastRawDownBytes: 200, LastRawTotalBytes: 300,
				PeriodBaselineUpBytes: 100, PeriodBaselineDownBytes: 100, PeriodBaselineTotalBytes: 200},
			{ID: 2, UserID: 1, PanelID: 10, InboundID: 20, ClientEmail: "u1-n2@x", CreatedAt: oldStart,
				LifetimeUpBytes: 500, LifetimeDownBytes: 500, LifetimeTotalBytes: 1000,
				LastRawUpBytes: 10, LastRawDownBytes: 10, LastRawTotalBytes: 20},
		},
	}}
	psp := &fakePSPClientRepo{byUser: map[int64][]*domain.PSPClient{
		1: {
			{ID: 1, UserID: 1, PanelID: 10, Email: "u1@psp.local",
				LifetimeUpBytes: 2000, LifetimeDownBytes: 2000, LifetimeTotalBytes: 4000,
				LastRawUpBytes: 100, LastRawDownBytes: 100, LastRawTotalBytes: 200,
				PeriodBaselineTotalBytes: 300},
			{ID: 2, UserID: 1, PanelID: 10, Email: "u1-k1@psp.local",
				LifetimeUpBytes: 800, LifetimeDownBytes: 200, LifetimeTotalBytes: 1000,
				LastRawUpBytes: 50, LastRawDownBytes: 50, LastRawTotalBytes: 100,
				PeriodBaselineTotalBytes: 100},
		},
	}}
	pool := &fakeXUIPool{clients: map[int64]ports.XUIClient{
		10: &fakeXUIClient{inbounds: []ports.Inbound{{ID: 20, ClientStats: []ports.ClientTraffic{
			{Email: "u1-n1@x", Up: 150, Down: 260},       // ownership delta 50 / 60 = 110
			{Email: "u1-n2@x", Up: 10, Down: 10},         // ownership idle
			{Email: "u1@psp.local", Up: 400, Down: 300},  // shared delta 300 / 200 = 500
			{Email: "u1-k1@psp.local", Up: 50, Down: 50}, // shared idle
		}}}},
	}}
	svc := New(users, ownership, &fakeTrafficRepo{}, nil, nil, pool, &fakeDisabler{})
	svc.SetPSPClientRepo(psp)

	if err := svc.PollOnce(context.Background()); err != nil {
		t.Fatalf("PollOnce: %v", err)
	}
	u := users.users[1]
	if got := u.PeriodUsed(); got != 610 {
		t.Fatalf("user PeriodUsed after rollover = %d, want 610 (110 ownership + 500 shared)", got)
	}

	nodeRows, err := svc.UserNodeUsage(context.Background(), 1)
	if err != nil {
		t.Fatalf("UserNodeUsage: %v", err)
	}
	var ownershipPeriod int64
	for _, r := range nodeRows {
		ownershipPeriod += r.PeriodTotalBytes
	}
	// UserServerUsage prefers the shared tier whenever the user has any
	// psp_client, so it reads the shared half of the split.
	serverRows, err := svc.UserServerUsage(context.Background(), 1)
	if err != nil {
		t.Fatalf("UserServerUsage: %v", err)
	}
	var sharedPeriod int64
	for _, r := range serverRows {
		sharedPeriod += r.PeriodTotalBytes
	}
	if ownershipPeriod != 110 || sharedPeriod != 500 {
		t.Errorf("period by tier = ownership %d + shared %d, want 110 + 500 (each tier reseeds with its own delta)",
			ownershipPeriod, sharedPeriod)
	}
	if ownershipPeriod+sharedPeriod != u.PeriodUsed() {
		t.Errorf("Σ ownership period %d + Σ shared period %d = %d, want the user's period %d",
			ownershipPeriod, sharedPeriod, ownershipPeriod+sharedPeriod, u.PeriodUsed())
	}
}

// A rollover cycle whose shared-client list fails has no shared clients to
// reseed. The poll must not trip over that: the user still rolls (quota
// enforcement depends on it) and the psp_client rows are left exactly as
// stored — no write at all, rather than a write built from nothing — and a
// Warn says why the per-server period stays on the previous period until
// the next cycle reseeds it (TestPollOnce_SharedClientListFailureReseedsSharedTierNextCycle).
func TestPollOnce_SharedClientListFailureStillRollsUser(t *testing.T) {
	oldStart := time.Now().AddDate(-1, 0, 0)
	users := &fakeUserRepo{users: map[int64]*domain.User{
		1: {ID: 1, Enabled: true, TrafficResetPeriod: domain.ResetMonthly, TrafficPeriodStart: &oldStart,
			LifetimeUpBytes: 1000, LifetimeDownBytes: 2000, LifetimeTotalBytes: 3000, PeriodBaselineBytes: 300},
	}}
	stored := &domain.PSPClient{ID: 1, UserID: 1, PanelID: 10, Email: "u1@psp.local",
		LifetimeUpBytes: 1000, LifetimeDownBytes: 2000, LifetimeTotalBytes: 3000,
		LastRawUpBytes: 100, LastRawDownBytes: 100, LastRawTotalBytes: 200,
		PeriodBaselineUpBytes: 100, PeriodBaselineDownBytes: 200, PeriodBaselineTotalBytes: 300}
	psp := &fakePSPClientRepo{byUser: map[int64][]*domain.PSPClient{1: {stored}}}
	pool := &fakeXUIPool{clients: map[int64]ports.XUIClient{
		10: &fakeXUIClient{inbounds: []ports.Inbound{
			{ID: 20, ClientStats: []ports.ClientTraffic{{Email: "u1@psp.local", Up: 700, Down: 500}}},
		}},
	}}
	svc := New(users, &fakeOwnershipRepo{byUser: map[int64][]*domain.XUIClientEntry{}}, &fakeTrafficRepo{}, nil, nil, pool, &fakeDisabler{})
	svc.SetPSPClientRepo(listAllFailing{fakePSPClientRepo: psp, err: errors.New("database is locked")})

	var pollErr error
	logged := captureLog(t, func() { pollErr = svc.PollOnce(context.Background()) })
	if pollErr != nil {
		t.Fatalf("PollOnce: %v", pollErr)
	}
	// The generic "list failed" Warn doesn't tie the failure to the stale
	// per-server period it leaves behind; this one does.
	if !strings.Contains(logged, reseedSkippedWarn) {
		t.Errorf("rollover cycle with a failed shared-client list did not log %q; got:\n%s", reseedSkippedWarn, logged)
	}
	u := users.users[1]
	if u.TrafficPeriodStart == nil || !u.TrafficPeriodStart.After(oldStart) {
		t.Fatalf("user period start = %v, want advanced past %v (the user must still roll)", u.TrafficPeriodStart, oldStart)
	}
	if u.PeriodBaselineBytes != 3000 {
		t.Errorf("user period baseline = %d, want 3000 (rolled at the unchanged lifetime)", u.PeriodBaselineBytes)
	}
	if got, want := periodBaselines(stored), [3]int64{100, 200, 300}; got != want {
		t.Errorf("shared client period baselines = %v, want untouched %v", got, want)
	}
	if psp.batchUpdated != nil {
		t.Errorf("shared-client counter flush = %d clients, want none (nothing was listed)", len(psp.batchUpdated))
	}
}

// The reseed is a rollover-only pass: in an ordinary cycle a shared client's
// period baselines stay put while its lifetime advances (that IS the period
// usage growing), and an idle client isn't dragged into the counter flush.
// The user's own split grows by the cycle's per-direction delta.
func TestPollOnce_SharedClientNonRolloverKeepsPeriodBaselines(t *testing.T) {
	start := time.Now() // this period → no rollover
	users := &fakeUserRepo{users: map[int64]*domain.User{
		1: {ID: 1, Enabled: true, TrafficResetPeriod: domain.ResetMonthly, TrafficPeriodStart: &start,
			LifetimeUpBytes: 3000, LifetimeDownBytes: 3000, LifetimeTotalBytes: 6000,
			PeriodBaselineBytes: 1200, PeriodBaselineUpBytes: 600, PeriodBaselineDownBytes: 600}, // split 2400 / 2400
	}}
	metered := &domain.PSPClient{ID: 1, UserID: 1, PanelID: 10, Email: "u1@psp.local",
		LifetimeUpBytes: 2000, LifetimeDownBytes: 2000, LifetimeTotalBytes: 4000,
		LastRawUpBytes: 100, LastRawDownBytes: 100, LastRawTotalBytes: 200,
		PeriodBaselineUpBytes: 400, PeriodBaselineDownBytes: 600, PeriodBaselineTotalBytes: 1000}
	idle := &domain.PSPClient{ID: 2, UserID: 1, PanelID: 10, Email: "u1-k1@psp.local",
		LifetimeUpBytes: 1000, LifetimeDownBytes: 1000, LifetimeTotalBytes: 2000,
		LastRawUpBytes: 50, LastRawDownBytes: 50, LastRawTotalBytes: 100,
		PeriodBaselineUpBytes: 100, PeriodBaselineDownBytes: 100, PeriodBaselineTotalBytes: 200}
	psp := &fakePSPClientRepo{byUser: map[int64][]*domain.PSPClient{1: {metered, idle}}}
	pool := &fakeXUIPool{clients: map[int64]ports.XUIClient{
		10: &fakeXUIClient{inbounds: []ports.Inbound{{ID: 20, ClientStats: []ports.ClientTraffic{
			{Email: "u1@psp.local", Up: 400, Down: 300},  // delta 300 / 200
			{Email: "u1-k1@psp.local", Up: 50, Down: 50}, // idle
		}}}},
	}}
	svc := New(users, &fakeOwnershipRepo{byUser: map[int64][]*domain.XUIClientEntry{}}, &fakeTrafficRepo{}, nil, nil, pool, &fakeDisabler{})
	svc.SetPSPClientRepo(psp)

	if err := svc.PollOnce(context.Background()); err != nil {
		t.Fatalf("PollOnce: %v", err)
	}
	if u := users.users[1]; !u.TrafficPeriodStart.Equal(start) || u.PeriodBaselineBytes != 1200 {
		t.Fatalf("user rolled (period start %v, baseline %d), want no rollover this cycle", u.TrafficPeriodStart, u.PeriodBaselineBytes)
	}
	if up, down := users.users[1].PeriodUsedSplit(); up != 2700 || down != 2600 {
		t.Fatalf("user PeriodUsedSplit = (%d, %d), want (2700, 2600) (2400 / 2400 plus this cycle's 300 / 200)", up, down)
	}
	if metered.LifetimeTotalBytes != 4500 {
		t.Fatalf("metered client lifetime = %d, want 4500 (the cycle's delta was metered)", metered.LifetimeTotalBytes)
	}
	for _, tc := range []struct {
		name string
		c    *domain.PSPClient
		want [3]int64
	}{
		{"metered client keeps its baselines", metered, [3]int64{400, 600, 1000}},
		{"idle client keeps its baselines", idle, [3]int64{100, 100, 200}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := periodBaselines(tc.c); got != tc.want {
				t.Errorf("period baselines = %v, want unchanged %v", got, tc.want)
			}
		})
	}
	if n := countBatched(psp.batchUpdated, 1); n != 1 {
		t.Errorf("metered client appears %d times in the counter flush, want 1", n)
	}
	if n := countBatched(psp.batchUpdated, 2); n != 0 {
		t.Errorf("idle client appears %d times in the counter flush, want 0 (no rollover, nothing to persist)", n)
	}
}
