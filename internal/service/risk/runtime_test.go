package risk

import (
	"encoding/json"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// The worker's fleet-wide knobs (risk.window_days, risk.login_lookback_days,
// risk.usage_baseline_days, risk.usage_recent_days) and the per-group
// thresholds of login_country and usage_shift, as a run reads them.

// A shortened log must still say so. The configured window is a week and a
// group's min_days is 5, but the sub-log retention keeps only three days: no
// device or place can recur on five days of three, and the verdict is
// unknown/retention_short — the one code that tells the admin the logs are
// too short for the policy. min_days is bounded by the CONFIGURED window
// (7), never by the window the retention left (3); bounding it by the latter
// would lower min_days to 3 and silently judge instead.
//
// Mutation: bounding the policy by the retention-limited window
// (Bounded(rt) with rt.WindowDays = windowDays(retention, configured)) turns
// this red.
func TestRefreshOnce_RetentionShortStillSurfaces(t *testing.T) {
	h := newSpreadHarness(usersInGroups(0), rowsOf(
		everyDay(t, device(1, ipHomeGD, "0a1b2c3d4e5f6071", "iOS 17.5 · iPhone15,2")),
		everyDay(t, client(1, ipHunan, "ClashX Pro/1.118.0")),
	))
	h.settings.global.RiskWindowDays = 7
	h.settings.global.RiskMinDays = 5
	h.settings.global.SubLogRetentionDays = 3
	if err := h.service().RefreshOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	r, ev := devicesRow(t, h, 1)
	wantDevicesRow(t, r, domain.GeoStateUnknown, domain.RiskCodeRetentionShort)
	if ev.WindowDays != 3 || ev.RetentionDays != 3 || ev.MinDays != 5 {
		t.Fatalf("devices evidence window %d, retention %d, min_days %d; want 3, 3 and the policy's 5", ev.WindowDays, ev.RetentionDays, ev.MinDays)
	}
	sr, sev := spreadRow(t, h, 1)
	wantSpreadRow(t, sr, domain.GeoStateUnknown, domain.RiskCodeRetentionShort)
	if sev.WindowDays != 3 || sev.MinDays != 5 {
		t.Fatalf("sub_spread evidence window %d, min_days %d; want 3 and 5", sev.WindowDays, sev.MinDays)
	}
}

// The fetch window is risk.window_days: three days reads three days of
// fetches, from the first instant of the third-last local date, and the
// evidence says so. A group's min_days above the window is bounded to it —
// otherwise "flagged" could never be reached — so the default 3 stays 3 and
// a 5 reads as 3.
func TestRefreshOnce_FetchWindowComesFromSettings(t *testing.T) {
	h := newSpreadHarness(usersInGroups(0), rowsOf(
		everyDay(t, device(1, ipHomeGD, "0a1b2c3d4e5f6071", "iOS 17.5 · iPhone15,2")),
	))
	h.settings.global.RiskWindowDays = 3
	h.settings.global.RiskMinDays = 5
	if err := h.service().RefreshOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	_, ev := devicesRow(t, h, 1)
	if ev.WindowDays != 3 || ev.WindowStart != "2026-09-23" || ev.MinDays != 3 {
		t.Fatalf("devices evidence window %d from %s, min_days %d; want 3 days from 2026-09-23 and min_days bounded to 3", ev.WindowDays, ev.WindowStart, ev.MinDays)
	}
	if want := time.Date(2026, 9, 23, 0, 0, 0, 0, shanghai(t)); len(h.scanner.calls) != 1 || !h.scanner.calls[0].since.Equal(want) {
		t.Fatalf("scans %+v, want one from %s", h.scanner.calls, want)
	}

	// A retention shorter than the configured window still wins: the rows
	// before it are gone.
	h = newSpreadHarness(usersInGroups(0), rowsOf(
		everyDay(t, device(1, ipHomeGD, "0a1b2c3d4e5f6071", "iOS 17.5 · iPhone15,2")),
	))
	h.settings.global.RiskWindowDays = 5
	h.settings.global.SubLogRetentionDays = 2
	if err := h.service().RefreshOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, ev := devicesRow(t, h, 1); ev.WindowDays != 2 {
		t.Fatalf("window %d under retention 2 and a configured 5, want 2", ev.WindowDays)
	}
}

// The login log is read over risk.login_lookback_days: 30 reads 30 days, and
// 200 — past the old fixed 90 — reads 200 when the auth events are kept that
// long (0, "keep forever", here). The store's bound is taken a day early, in
// UTC, as every read of it is; the evaluator cuts the lookback exactly.
func TestRefreshOnce_LoginLookbackComesFromSettings(t *testing.T) {
	for _, lookback := range []int{30, 200} {
		h := newLoginHarness(t, usersInGroups(0), append(settledAt(1, ipHomeGD), signIn(1, ipTokyo, day))...)
		h.settings.global.RiskLoginLookbackDays = lookback
		h.settings.global.AuthEventRetentionDays = 0
		if err := h.service().RefreshOnce(t.Context()); err != nil {
			t.Fatal(err)
		}
		_, ev := loginRow(t, h, 1)
		if ev.LookbackDays != lookback {
			t.Fatalf("lookback %d: evidence says %d", lookback, ev.LookbackDays)
		}
		want := refreshNow.Add(-time.Duration(lookback) * day).UTC().Add(-day)
		if since := h.logins.calls[0].Since; since == nil || !since.Equal(want) {
			t.Fatalf("lookback %d: read since %v, want %v (now − %d days, a day early)", lookback, since, want, lookback)
		}
	}

	// The auth-event retention still shortens it when shorter.
	h := newLoginHarness(t, usersInGroups(0), append(settledAt(1, ipHomeGD), signIn(1, ipTokyo, day))...)
	h.settings.global.RiskLoginLookbackDays = 200
	h.settings.global.AuthEventRetentionDays = 60
	if err := h.service().RefreshOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, ev := loginRow(t, h, 1); ev.LookbackDays != 60 {
		t.Fatalf("lookback %d under retention 60 and a configured 200, want 60", ev.LookbackDays)
	}
}

// login_country's warm-up and hold are per group. Two accounts with the same
// history — one login from home 40 days ago, one from Tokyo 20 days ago —
// read differently by group: the shipped policy (a warm-up of three, a
// seven-day hold) has nothing recent to judge; a group with a warm-up of one
// and a 30-day hold judges the Tokyo login and flags it. The evidence
// carries the group's numbers.
func TestRefreshOnce_GroupLoginKnobsReachTheEvaluator(t *testing.T) {
	var events []*domain.AuthEvent
	for uid := int64(1); uid <= 2; uid++ {
		events = append(events, signIn(uid, ipHomeGD, 40*day), signIn(uid, ipTokyo, 20*day))
	}
	h := newLoginHarness(t, usersInGroups(0, 5), events...)
	g := h.settings.global
	g.RiskLoginWarmupLogins = 1
	g.RiskLoginHoldDays = 30
	h.settings.groups = map[int64]ports.UISettings{5: g}
	if err := h.service().RefreshOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	shipped, _ := loginRow(t, h, 1)
	wantLoginRow(t, shipped, domain.GeoStateIdle, domain.RiskCodeNoRecentLogins)
	tuned, ev := loginRow(t, h, 2)
	wantLoginRow(t, tuned, domain.GeoStateFlagged, domain.RiskCodeNewCountry)
	if ev.Warmup != 1 || ev.HoldDays != 30 || ev.Judged != 1 {
		t.Fatalf("warm-up %d, hold %d, judged %d; want the group's 1 and 30, and the Tokyo login judged", ev.Warmup, ev.HoldDays, ev.Judged)
	}
}

// dailyUsage is one hourly bucket at local noon on each of the n Shanghai
// days before refreshNow's, oldest first, carrying bytes(k).
func dailyUsage(t *testing.T, n int, bytes func(k int) int64) []domain.HourlyTraffic {
	t.Helper()
	sh := shanghai(t)
	out := make([]domain.HourlyTraffic, 0, n)
	for k := range n {
		out = append(out, domain.HourlyTraffic{BucketStart: time.Date(2026, 9, 25-n+k, 12, 0, 0, 0, sh).UTC(), TotalBytes: bytes(k)})
	}
	return out
}

// usageRow is user uid's usage_shift row and its decoded evidence.
func usageRow(t *testing.T, h *harness, uid int64) (domain.RiskSignal, domain.UsageShiftEvidence) {
	t.Helper()
	r, ok := h.store.saved(t)[uid][domain.RiskKindUsageShift]
	if !ok {
		t.Fatalf("user %d has no usage_shift row", uid)
	}
	var ev domain.UsageShiftEvidence
	if err := json.Unmarshal(r.Evidence, &ev); err != nil {
		t.Fatalf("user %d evidence %q: %v (verdict %s/%s)", uid, r.Evidence, err, r.State, r.Code)
	}
	return r, ev
}

// usage_shift's series is risk.usage_baseline_days + risk.usage_recent_days
// long. With 14 and 3 the worker reads the 17 local days before today —
// 2026-09-08 .. 2026-09-24, from the first instant of the first to today's
// — for the fleet and for the account alike; places buckets on those 17
// columns, day 0 being 09-08 and nothing from the evening before; and the
// evidence says which lengths it was judged with.
func TestUsageShift_ReadsTheConfiguredSeriesLength(t *testing.T) {
	h := newHarness(usersInGroups(0))
	h.settings.global.RiskUsageBaselineDays = 14
	h.settings.global.RiskUsageRecentDays = 3
	h.traffic.byUser = map[int64][]domain.HourlyTraffic{1: {
		{BucketStart: time.Date(2026, 9, 7, 15, 0, 0, 0, time.UTC), TotalBytes: 888},  // 09-07 23:00 local: before the series
		{BucketStart: time.Date(2026, 9, 7, 16, 0, 0, 0, time.UTC), TotalBytes: 7},    // 09-08 00:00 local: day 0
		{BucketStart: time.Date(2026, 9, 24, 15, 0, 0, 0, time.UTC), TotalBytes: 50},  // 09-24 23:00 local: day 16
		{BucketStart: time.Date(2026, 9, 24, 16, 0, 0, 0, time.UTC), TotalBytes: 999}, // 09-25 00:00 local: today
	}}
	if err := h.service().RefreshOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	_, ev := usageRow(t, h, 1)
	want := make([]int64, 17)
	want[0], want[16] = 7, 50
	if !reflect.DeepEqual(ev.Series, want) {
		t.Fatalf("series = %v\nwant     %v", ev.Series, want)
	}
	if ev.EndDate != "2026-09-24" || ev.BaselineDays != 14 || ev.RecentDays != 3 {
		t.Fatalf("end %s, baseline %d, judged days %d; want 2026-09-24, 14 and 3", ev.EndDate, ev.BaselineDays, ev.RecentDays)
	}
	sh := shanghai(t)
	since, until := time.Date(2026, 9, 8, 0, 0, 0, 0, sh), time.Date(2026, 9, 25, 0, 0, 0, 0, sh)
	calls := slices.Concat(h.traffic.fleetCalls, h.traffic.userCalls)
	if len(h.traffic.fleetCalls) != 1 || len(h.traffic.userCalls) != 1 {
		t.Fatalf("hourly reads fleet %d, user %d; want one each", len(h.traffic.fleetCalls), len(h.traffic.userCalls))
	}
	for _, c := range calls {
		if !c.since.Equal(since) || !c.until.Equal(until) {
			t.Fatalf("hourly read over [%s, %s), want [%s, %s)", c.since.In(sh), c.until.In(sh), since, until)
		}
	}
}

// usage_shift's three thresholds are per group, bounded by the fleet's
// configured series. Three accounts with the same month — 1 GiB a day, the
// first two judged days at 10 GiB — read three ways:
//   - the shipped policy (flag at 4, suspect at 2): suspect;
//   - a group flagging at 2: flagged;
//   - a group asking for a 40-day warm-up, flag at 10 and suspect at 12:
//     bounded to the 28-day baseline and the 7 judged days (suspect then
//     held to flag), so the account is judged — two over-days of seven,
//     within — and the evidence carries the bounded numbers.
func TestRefreshOnce_GroupUsageKnobsReachTheEvaluator(t *testing.T) {
	month := dailyUsage(t, shippedSeriesDays, func(k int) int64 {
		if k == 28 || k == 29 {
			return 10 << 30
		}
		return 1 << 30
	})
	h := newHarness(usersInGroups(0, 5, 6))
	h.traffic.byUser = map[int64][]domain.HourlyTraffic{1: month, 2: month, 3: month}
	flagAtTwo, bounded := h.settings.global, h.settings.global
	flagAtTwo.RiskUsageFlagDays = 2
	bounded.RiskUsageWarmupDays, bounded.RiskUsageFlagDays, bounded.RiskUsageSuspectDays = 40, 10, 12
	h.settings.groups = map[int64]ports.UISettings{5: flagAtTwo, 6: bounded}
	if err := h.service().RefreshOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		uid                   int64
		state                 domain.GeoState
		code                  domain.RiskCode
		warmup, flag, suspect int
	}{
		{1, domain.GeoStateSuspect, domain.RiskCodeBuilding, 14, 4, 2},
		{2, domain.GeoStateFlagged, domain.RiskCodeSustained, 14, 2, 2},
		{3, domain.GeoStateClean, domain.RiskCodeWithin, 28, 7, 7},
	} {
		r, ev := usageRow(t, h, c.uid)
		if r.State != c.state || r.Code != c.code {
			t.Errorf("user %d: %s/%s, want %s/%s", c.uid, r.State, r.Code, c.state, c.code)
		}
		if ev.WarmupDays != c.warmup || ev.FlagDays != c.flag || ev.SuspectDays != c.suspect || ev.OverDays != 2 {
			t.Errorf("user %d: judged with warm-up %d, flag %d, suspect %d (%d over); want %d, %d, %d (2 over)",
				c.uid, ev.WarmupDays, ev.FlagDays, ev.SuspectDays, ev.OverDays, c.warmup, c.flag, c.suspect)
		}
	}
}
