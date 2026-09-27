package risk

import (
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// The worker's fleet-wide knobs (risk.window_days, risk.login_lookback_days)
// and login_country's per-group thresholds, as a run reads them.

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
