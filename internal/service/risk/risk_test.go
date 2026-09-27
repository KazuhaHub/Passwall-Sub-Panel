package risk

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"reflect"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/metrics"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// ---- fakes -------------------------------------------------------------

// fakeUsers pages like the user repository: 1-based pages, a page size
// capped at 200, ordered by id.
type fakeUsers struct {
	users []*domain.User
	err   error
}

func (f *fakeUsers) List(_ context.Context, filter ports.UserFilter) ([]*domain.User, int64, error) {
	if f.err != nil {
		return nil, 0, f.err
	}
	total := int64(len(f.users))
	size := filter.PageSize
	if size <= 0 {
		return f.users, total, nil
	}
	size = min(size, 200)
	page := max(filter.Page, 1)
	from := min((page-1)*size, len(f.users))
	to := min(from+size, len(f.users))
	return f.users[from:to], total, nil
}

type fakeStore struct {
	mu       sync.Mutex
	saves    [][]domain.RiskSignal
	purges   int
	saveErr  error
	purgeErr error
}

func (f *fakeStore) Save(_ context.Context, rows []domain.RiskSignal) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.saveErr != nil {
		return f.saveErr
	}
	f.saves = append(f.saves, append([]domain.RiskSignal(nil), rows...))
	return nil
}

func (f *fakeStore) PurgeOrphans(context.Context) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.purges++
	return 0, f.purgeErr
}

// saved is the last save as userID → kind → row, and fails on a (user, kind)
// written twice — the real store refuses such a batch outright.
func (f *fakeStore) saved(t *testing.T) map[int64]map[domain.RiskKind]domain.RiskSignal {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	out := map[int64]map[domain.RiskKind]domain.RiskSignal{}
	if len(f.saves) == 0 {
		return out
	}
	for _, r := range f.saves[len(f.saves)-1] {
		if out[r.UserID] == nil {
			out[r.UserID] = map[domain.RiskKind]domain.RiskSignal{}
		}
		if _, dup := out[r.UserID][r.Kind]; dup {
			t.Fatalf("user %d kind %s saved twice in one batch", r.UserID, r.Kind)
		}
		out[r.UserID][r.Kind] = r
	}
	return out
}

func (f *fakeStore) saveCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.saves)
}

// fakeSettings resolves a group to its own settings when it has an entry,
// else to the global ones, the way a group with no overrides inherits.
type fakeSettings struct {
	global     ports.UISettings
	loadErr    error
	groups     map[int64]ports.UISettings
	groupErr   map[int64]error
	groupReads map[int64]int
}

func (f *fakeSettings) Load(context.Context, ports.UISettings) (ports.UISettings, error) {
	return f.global, f.loadErr
}

func (f *fakeSettings) LoadForGroup(_ context.Context, gid int64, _ ports.UISettings) (ports.UISettings, error) {
	if f.groupReads == nil {
		f.groupReads = map[int64]int{}
	}
	f.groupReads[gid]++
	if err := f.groupErr[gid]; err != nil {
		return ports.UISettings{}, err
	}
	if s, ok := f.groups[gid]; ok {
		return s, nil
	}
	return f.global, nil
}

func (f *fakeSettings) LoadForUser(ctx context.Context, u *domain.User, d ports.UISettings) (ports.UISettings, error) {
	return f.LoadForGroup(ctx, u.GroupID, d)
}

type hourlyCall struct {
	userID       int64
	since, until time.Time
}

// fakeTraffic serves whatever buckets it holds, whatever the range: the
// service must place them by date itself, not trust the query's bounds.
type fakeTraffic struct {
	byUser     map[int64][]domain.HourlyTraffic
	fleet      []domain.HourlyTraffic
	userErr    map[int64]error
	fleetErr   error
	userCalls  []hourlyCall
	fleetCalls []hourlyCall
	onUser     func(userID int64)
}

func (f *fakeTraffic) ListHourlyByUser(_ context.Context, uid int64, since, until time.Time) ([]domain.HourlyTraffic, error) {
	f.userCalls = append(f.userCalls, hourlyCall{uid, since, until})
	if f.onUser != nil {
		f.onUser(uid)
	}
	if err := f.userErr[uid]; err != nil {
		return nil, err
	}
	return f.byUser[uid], nil
}

func (f *fakeTraffic) SumHourlyAllUsers(_ context.Context, since, until time.Time) ([]domain.HourlyTraffic, error) {
	f.fleetCalls = append(f.fleetCalls, hourlyCall{0, since, until})
	if f.fleetErr != nil {
		return nil, f.fleetErr
	}
	return f.fleet, nil
}

// ---- helpers -----------------------------------------------------------

// refreshNow is 12:00 on 2026-09-25 in Shanghai. The usage series is then
// the 35 local days 2026-08-21 .. 2026-09-24.
var refreshNow = time.Date(2026, 9, 25, 4, 0, 0, 0, time.UTC)

// shippedSeriesDays is the usage series a fleet that never set
// risk.usage_baseline_days or risk.usage_recent_days reads: 28 + 7.
const shippedSeriesDays = domain.RiskUsageBaselineDays + domain.RiskUsageRecentDays

func shanghai(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	return loc
}

// midnightGap is a date whose 00:00 never happened: the zone springs forward
// AT midnight, from 00:00 straight to 01:00. time.Date resolves that missing
// midnight with the offset before the jump, to 23:00 of the day BEFORE.
type midnightGap struct {
	zone string
	y    int
	m    time.Month
	d    int
}

var midnightGaps = []midnightGap{
	{"America/Santiago", 2026, time.September, 6},
	{"America/Havana", 2026, time.March, 8},
}

// load returns the zone, after checking the premise: its midnight on the
// date really is missing. A tz database that moved the jump would otherwise
// turn every test built on it into a test of an ordinary day.
func (g midnightGap) load(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(g.zone)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, d := time.Date(g.y, g.m, g.d, 0, 0, 0, 0, loc).Date(); d == g.d {
		t.Fatalf("premise: %s has a midnight on %d-%02d-%02d; the tz database moved the jump", g.zone, g.y, g.m, g.d)
	}
	return loc
}

// at is hour:minute local on the day k days after the gap date (negative:
// before). Every wall time used with it exists: the gap is only 00:00–01:00.
func (g midnightGap) at(loc *time.Location, k, hour, minute int) time.Time {
	return time.Date(g.y, g.m, g.d+k, hour, minute, 0, 0, loc)
}

// date is the gap date plus k days, as the evidence writes it.
func (g midnightGap) date(k int) string {
	return time.Date(g.y, g.m, g.d+k, 12, 0, 0, 0, time.UTC).Format("2006-01-02")
}

func usersInGroups(groups ...int64) []*domain.User {
	out := make([]*domain.User, len(groups))
	for i, g := range groups {
		out[i] = &domain.User{ID: int64(i + 1), GroupID: g, UPN: fmt.Sprintf("u%d@example.test", i+1)}
	}
	return out
}

type harness struct {
	users    *fakeUsers
	store    *fakeStore
	settings *fakeSettings
	traffic  *fakeTraffic
	// The fetch-window sources. Each is wired only when set, so the tests
	// that predate them see exactly the usage_shift rows they always did.
	scanner     *fakeScanner
	geo         *fakeGeo
	isInfra     func(netip.Addr) bool
	infraLoaded func() bool
	// The login log and the landing addresses, wired only when set too.
	logins  *fakeLogins
	landing func() []netip.Addr
	// now is the run's clock; zero is refreshNow.
	now time.Time
}

func newHarness(users []*domain.User) *harness {
	return &harness{
		users:    &fakeUsers{users: users},
		store:    &fakeStore{},
		settings: &fakeSettings{global: ports.UISettings{Timezone: "Asia/Shanghai", TrafficHistoryDays: 730}},
		traffic:  &fakeTraffic{},
	}
}

func (h *harness) service() *Service {
	d := Deps{
		Users: h.users, Store: h.store, Settings: h.settings, Traffic: h.traffic,
		Now: func() time.Time {
			if !h.now.IsZero() {
				return h.now
			}
			return refreshNow
		},
		IsInfra: h.isInfra, InfraLoaded: h.infraLoaded,
	}
	// A nil *fakeScanner in the interface would be a non-nil dependency.
	if h.scanner != nil {
		d.SubLogs = h.scanner
	}
	if h.geo != nil {
		d.Geo = h.geo
	}
	if h.logins != nil {
		d.AuthEvents = h.logins
	}
	d.LandingAddrs = h.landing
	return New(d)
}

func outcomes() map[string]int64 {
	out := map[string]int64{}
	for _, o := range []string{"ok", "partial", "infra_pending", "error"} {
		out[o] = metrics.RiskRefreshTotal.With(o).Value()
	}
	return out
}

// wantOutcome checks that exactly one refresh was counted, under want.
func wantOutcome(t *testing.T, before map[string]int64, want string) {
	t.Helper()
	after := outcomes()
	for o, n := range after {
		delta := n - before[o]
		if o == want && delta != 1 {
			t.Fatalf("psp_risk_refresh_total{outcome=%s} moved by %d, want 1 (all deltas: before %v after %v)", o, delta, before, after)
		}
		if o != want && delta != 0 {
			t.Fatalf("psp_risk_refresh_total{outcome=%s} moved by %d, want 0 — this run is %s", o, delta, want)
		}
	}
}

// ---- tests -------------------------------------------------------------

// Every account gets its usage_shift row on every run — past the first page
// of the user list too, which is where a paging loop that stops early would
// silently leave the fleet's newest accounts unjudged forever.
func TestRefresh_WritesAUsageRowPerUser(t *testing.T) {
	groups := make([]int64, 250)
	h := newHarness(usersInGroups(groups...))
	before := outcomes()
	if err := h.service().RefreshOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	rows := h.store.saved(t)
	if len(rows) != 250 {
		t.Fatalf("saved rows for %d users, want 250", len(rows))
	}
	for uid, kinds := range rows {
		r, ok := kinds[domain.RiskKindUsageShift]
		if !ok || len(kinds) != 1 {
			t.Fatalf("user %d: rows %v, want exactly one usage_shift row", uid, kinds)
		}
		// No traffic at all: idle, and no evidence to outlive the week.
		if r.State != domain.GeoStateIdle || r.Code != domain.RiskCodeNoUsage || r.Evidence != nil {
			t.Fatalf("user %d: %s/%s evidence %s, want idle/no_usage with no evidence", uid, r.State, r.Code, r.Evidence)
		}
	}
	wantOutcome(t, before, "ok")
}

// Days are the panel's calendar days. The hourly rollup stores UTC bucket
// starts, and Shanghai's day begins at 16:00 UTC the day before: a bucket at
// 2026-09-23T16:00Z is the first hour of 09-24, the series' last day. Read
// as UTC dates, every evening in the east would land a day early. Buckets
// outside the 35 days — today's, still open, and anything older — are not
// counted, whatever the store hands back.
func TestRefresh_BucketsHourlyTrafficIntoPanelDays(t *testing.T) {
	h := newHarness(usersInGroups(0))
	h.traffic.byUser = map[int64][]domain.HourlyTraffic{1: {
		{BucketStart: time.Date(2026, 8, 20, 15, 0, 0, 0, time.UTC), TotalBytes: 888}, // 08-20 23:00 local: before the series
		{BucketStart: time.Date(2026, 8, 20, 16, 0, 0, 0, time.UTC), TotalBytes: 7},   // 08-21 00:00 local: day 0
		{BucketStart: time.Date(2026, 9, 23, 15, 0, 0, 0, time.UTC), TotalBytes: 100}, // 09-23 23:00 local: day 33
		{BucketStart: time.Date(2026, 9, 23, 16, 0, 0, 0, time.UTC), TotalBytes: 200}, // 09-24 00:00 local: day 34
		{BucketStart: time.Date(2026, 9, 24, 15, 0, 0, 0, time.UTC), TotalBytes: 50},  // 09-24 23:00 local: day 34
		{BucketStart: time.Date(2026, 9, 24, 16, 0, 0, 0, time.UTC), TotalBytes: 999}, // 09-25 00:00 local: today
	}}
	if err := h.service().RefreshOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	r := h.store.saved(t)[1][domain.RiskKindUsageShift]
	var ev domain.UsageShiftEvidence
	if err := json.Unmarshal(r.Evidence, &ev); err != nil {
		t.Fatalf("evidence %q: %v (verdict %s/%s)", r.Evidence, err, r.State, r.Code)
	}
	want := make([]int64, shippedSeriesDays)
	want[0], want[33], want[34] = 7, 100, 250
	if !reflect.DeepEqual(ev.Series, want) {
		t.Fatalf("series = %v\nwant     %v", ev.Series, want)
	}
	if ev.EndDate != "2026-09-24" {
		t.Fatalf("end date = %q, want 2026-09-24 (yesterday, in the panel's zone)", ev.EndDate)
	}
	if ev.HistoryRetentionDays != 730 {
		t.Fatalf("history retention = %d, want the global traffic_history_days 730", ev.HistoryRetentionDays)
	}
	sh := shanghai(t)
	wantSince := time.Date(2026, 8, 21, 0, 0, 0, 0, sh)
	wantUntil := time.Date(2026, 9, 25, 0, 0, 0, 0, sh)
	if len(h.traffic.userCalls) != 1 || !h.traffic.userCalls[0].since.Equal(wantSince) || !h.traffic.userCalls[0].until.Equal(wantUntil) {
		t.Fatalf("hourly reads %+v, want one over [%s, %s)", h.traffic.userCalls, wantSince, wantUntil)
	}
}

// The usage series survives a date without a midnight wherever it falls in
// it. Anchored at resolved midnights, the date read as the day before:
//   - as the series' first day, every bucket was counted from the evening
//     before — that evening became day 0, every day moved one column late,
//     and yesterday fell off the end;
//   - as its last day, the columns were labelled as ending the day before;
//   - as today, the read stopped at 23:00 the evening before, and
//     yesterday's last hour was never read. The fake hands every bucket out
//     whatever the range, so the range itself is checked: the store cuts
//     exactly there.
func TestRefresh_UsageDaysSurviveADateWithoutAMidnight(t *testing.T) {
	type bucket struct {
		day, hour int // relative to the gap date, local
		bytes     int64
	}
	for _, g := range midnightGaps {
		for _, c := range []struct {
			name    string
			today   int // today, relative to the gap date
			buckets []bucket
			series  map[int]int64 // the nonzero series days
			endDate string
		}{
			{"first day", 35, []bucket{{-1, 23, 888}, {0, 1, 7}, {34, 12, 250}}, map[int]int64{0: 7, 34: 250}, g.date(34)},
			{"last day", 1, []bucket{{0, 12, 5}}, map[int]int64{34: 5}, g.date(0)},
			{"today", 0, []bucket{{-1, 23, 9}}, map[int]int64{34: 9}, g.date(-1)},
		} {
			t.Run(g.zone+"/"+c.name, func(t *testing.T) {
				loc := g.load(t)
				h := newHarness(usersInGroups(0))
				h.settings.global.Timezone = g.zone
				h.now = g.at(loc, c.today, 12, 0)
				var rows []domain.HourlyTraffic
				for _, b := range c.buckets {
					rows = append(rows, domain.HourlyTraffic{BucketStart: g.at(loc, b.day, b.hour, 0).UTC(), TotalBytes: b.bytes})
				}
				h.traffic.byUser = map[int64][]domain.HourlyTraffic{1: rows}
				if err := h.service().RefreshOnce(t.Context()); err != nil {
					t.Fatal(err)
				}
				r := h.store.saved(t)[1][domain.RiskKindUsageShift]
				var ev domain.UsageShiftEvidence
				if err := json.Unmarshal(r.Evidence, &ev); err != nil {
					t.Fatalf("evidence %q: %v (verdict %s/%s)", r.Evidence, err, r.State, r.Code)
				}
				want := make([]int64, shippedSeriesDays)
				for k, b := range c.series {
					want[k] = b
				}
				if !reflect.DeepEqual(ev.Series, want) {
					t.Fatalf("series = %v\nwant     %v", ev.Series, want)
				}
				if ev.EndDate != c.endDate {
					t.Fatalf("end date = %q, want %q (yesterday)", ev.EndDate, c.endDate)
				}
				// Both reads cover the 35 days as the store cuts them: from no
				// later than day 0's first instant, up to no earlier than
				// today's.
				from := dayStartOf(t, g.at(loc, c.today-shippedSeriesDays, 12, 0), loc)
				to := dayStartOf(t, g.at(loc, c.today, 12, 0), loc)
				for _, call := range slices.Concat(h.traffic.fleetCalls, h.traffic.userCalls) {
					if call.since.After(from) || call.until.Before(to) {
						t.Fatalf("hourly read [%s, %s) does not cover the series [%s, %s)",
							call.since.In(loc), call.until.In(loc), from, to)
					}
				}
			})
		}
	}
}

// dayStartOf is the first instant of noon's local date, found by walking back
// minute by minute: slow and obviously right, as a test's own answer should be.
func dayStartOf(t *testing.T, noon time.Time, loc *time.Location) time.Time {
	t.Helper()
	y, m, d := noon.In(loc).Date()
	at := noon
	for range 36 * 60 {
		prev := at.Add(-time.Minute)
		if py, pm, pd := prev.In(loc).Date(); py != y || pm != m || pd != d {
			return at
		}
		at = prev
	}
	t.Fatalf("no start found for %s", noon)
	return time.Time{}
}

// A group that switched the signal off gets "disabled" with no evidence —
// the admin sees the switch took, and nothing from before it outlives it —
// while the rest of the fleet is still judged. Nothing is read for an
// account whose verdict cannot depend on it.
func TestRefresh_OffGroupWritesDisabledWithNullEvidence(t *testing.T) {
	h := newHarness(usersInGroups(7, 0))
	off := h.settings.global
	off.RiskUsageShiftOff = true
	h.settings.groups = map[int64]ports.UISettings{7: off}
	if err := h.service().RefreshOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	rows := h.store.saved(t)
	r := rows[1][domain.RiskKindUsageShift]
	if r.State != domain.GeoStateDisabled || r.Code != domain.RiskCodeSignalOff || r.Evidence != nil {
		t.Fatalf("off group: %s/%s evidence %s, want disabled/signal_off with NULL evidence", r.State, r.Code, r.Evidence)
	}
	if other := rows[2][domain.RiskKindUsageShift]; other.State == domain.GeoStateDisabled || other.State == "" {
		t.Fatalf("the group without the switch was %q, want it judged", other.State)
	}
	for _, c := range h.traffic.userCalls {
		if c.userID == 1 {
			t.Fatal("read the hourly traffic of an account whose signal is off")
		}
	}
}

// A group whose settings cannot be read is not judged with somebody else's
// policy — not the global one, not the default. Its accounts get no rows this
// run, so the store keeps what it had, and the run says it was partial. The
// failing group is read once, not once per member.
func TestRefresh_UnreadableGroupKeepsItsRows(t *testing.T) {
	h := newHarness(usersInGroups(1, 2, 2))
	h.settings.groupErr = map[int64]error{2: errors.New("scope table locked")}
	before := outcomes()
	if err := h.service().RefreshOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	rows := h.store.saved(t)
	if len(rows) != 1 || rows[1] == nil {
		t.Fatalf("saved rows for users %v, want only user 1 (group 2 unreadable)", keys(rows))
	}
	if n := h.settings.groupReads[2]; n != 1 {
		t.Fatalf("group 2's settings read %d times, want once", n)
	}
	wantOutcome(t, before, "partial")
}

// Without the global settings there is no panel timezone to cut days in and
// no retention to judge against. Nothing is written — not even a purge —
// and the error goes back to the loop.
func TestRefresh_SettingsReadFailureWritesNothing(t *testing.T) {
	h := newHarness(usersInGroups(0))
	boom := errors.New("settings table gone")
	h.settings.loadErr = boom
	before := outcomes()
	err := h.service().RefreshOnce(t.Context())
	if !errors.Is(err, boom) {
		t.Fatalf("RefreshOnce = %v, want the settings error", err)
	}
	if h.store.saveCount() != 0 || h.store.purges != 0 {
		t.Fatalf("saves %d, purges %d; want nothing written", h.store.saveCount(), h.store.purges)
	}
	wantOutcome(t, before, "error")
}

// Deleting an account cascades nothing, so its rows would stay forever; each
// run purges them. A purge that fails costs nothing but tidiness — reads
// JOIN users and never show an orphan — so it does not stop the run.
func TestRefresh_PurgesOrphansEachRun(t *testing.T) {
	h := newHarness(usersInGroups(0))
	svc := h.service()
	for range 2 {
		if err := svc.RefreshOnce(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	if h.store.purges != 2 {
		t.Fatalf("purged %d times over two runs, want 2", h.store.purges)
	}

	h = newHarness(usersInGroups(0))
	h.store.purgeErr = errors.New("purge failed")
	before := outcomes()
	if err := h.service().RefreshOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(h.store.saved(t)) != 1 {
		t.Fatal("a failed purge stopped the run from saving")
	}
	wantOutcome(t, before, "ok")
}

// usage_shift reads the hourly rollup only — the fleet sum once, then each
// account — and every read covers exactly the 35 local days. Raw snapshots
// are kept only a week; the rollup is the one source that reaches back.
func TestRefresh_UsageReadsOnlyTheHourlyRollup(t *testing.T) {
	h := newHarness(usersInGroups(0, 0, 3))
	if err := h.service().RefreshOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	sh := shanghai(t)
	since, until := time.Date(2026, 8, 21, 0, 0, 0, 0, sh), time.Date(2026, 9, 25, 0, 0, 0, 0, sh)
	if len(h.traffic.fleetCalls) != 1 || !h.traffic.fleetCalls[0].since.Equal(since) || !h.traffic.fleetCalls[0].until.Equal(until) {
		t.Fatalf("fleet reads %+v, want one over [%s, %s)", h.traffic.fleetCalls, since, until)
	}
	seen := map[int64]int{}
	for _, c := range h.traffic.userCalls {
		seen[c.userID]++
		if !c.since.Equal(since) || !c.until.Equal(until) {
			t.Fatalf("user %d read over [%s, %s), want [%s, %s)", c.userID, c.since, c.until, since, until)
		}
	}
	if !reflect.DeepEqual(seen, map[int64]int{1: 1, 2: 1, 3: 1}) {
		t.Fatalf("per-user reads %v, want one each for users 1..3", seen)
	}
}

// The fleet sum feeds every account's factor. Without it no account can be
// judged fairly, so the kind is skipped — its rows kept — except where the
// verdict needs no data: a switched-off group still reads "disabled".
func TestRefresh_FleetReadFailureSkipsTheKind(t *testing.T) {
	h := newHarness(usersInGroups(0, 5))
	off := h.settings.global
	off.RiskUsageShiftOff = true
	h.settings.groups = map[int64]ports.UISettings{5: off}
	h.traffic.fleetErr = errors.New("rollup unreadable")
	before := outcomes()
	if err := h.service().RefreshOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	rows := h.store.saved(t)
	if _, judged := rows[1]; judged {
		t.Fatalf("user 1 was judged without the fleet: %v", rows[1])
	}
	if r := rows[2][domain.RiskKindUsageShift]; r.State != domain.GeoStateDisabled {
		t.Fatalf("the switched-off group read %q, want disabled", r.State)
	}
	if len(h.traffic.userCalls) != 0 {
		t.Fatalf("read %d accounts after the fleet read failed", len(h.traffic.userCalls))
	}
	wantOutcome(t, before, "partial")
}

// One account's unreadable traffic costs that account's row, not the run's.
func TestRefresh_UserReadFailureSkipsOnlyThatUser(t *testing.T) {
	h := newHarness(usersInGroups(0, 0))
	h.traffic.userErr = map[int64]error{1: errors.New("row locked")}
	before := outcomes()
	if err := h.service().RefreshOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	rows := h.store.saved(t)
	if _, ok := rows[1]; ok || rows[2] == nil {
		t.Fatalf("saved rows for users %v, want only user 2", keys(rows))
	}
	wantOutcome(t, before, "partial")
}

// Shutdown cancels the run between accounts: no further reads, nothing
// saved, and the cancellation is what comes back.
func TestRefresh_StopsOnCancel(t *testing.T) {
	h := newHarness(usersInGroups(0, 0, 0, 0, 0))
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	h.traffic.onUser = func(int64) { cancel() }
	err := h.service().RefreshOnce(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("RefreshOnce = %v, want context.Canceled", err)
	}
	if n := len(h.traffic.userCalls); n != 1 {
		t.Fatalf("read %d accounts after the cancel, want to stop after the first", n)
	}
	if h.store.saveCount() != 0 {
		t.Fatal("saved after the run was cancelled")
	}
}

// A service built with nothing must fail its run, not panic the loop: the
// loop runs under a panic shield, but a panic every hour would be a worker
// that never judges anything and says so only in a stack trace. The
// optional sources are optional: without Traffic there is simply no
// usage_shift row.
func TestRefresh_ZeroValueDepsDoNotPanic(t *testing.T) {
	if err := New(Deps{}).RefreshOnce(t.Context()); err == nil {
		t.Fatal("a service with no users, store or settings reported success")
	}
	h := newHarness(usersInGroups(0))
	svc := New(Deps{Users: h.users, Store: h.store, Settings: h.settings})
	if err := svc.RefreshOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	if rows := h.store.saved(t); len(rows) != 0 {
		t.Fatalf("saved %v with no traffic source, want nothing", rows)
	}
}

func keys[V any](m map[int64]V) []int64 {
	out := make([]int64, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
