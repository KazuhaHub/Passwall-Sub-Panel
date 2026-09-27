package domain

import (
	"testing"
	"time"
)

// A fresh install has never saved these knobs, and every one of them reads
// as 0. Read literally, 0 would be a zero-length ticker (which panics), a
// first run at boot racing the probes, a bell that forgets a flag the moment
// it is written, a fetch window of no days and a login log read over no
// time. Unset means the shipped default, the value each knob had while it
// was a constant.
func TestRiskRuntimeFromSettings_UnsetIsTheDefault(t *testing.T) {
	want := RiskRuntime{
		RefreshInterval:   60 * time.Minute,
		FirstDelay:        2 * time.Minute,
		AlertFreshness:    24 * time.Hour,
		WindowDays:        7,
		LoginLookbackDays: 90,
	}
	if got := RiskRuntimeFromSettings(RiskRuntimeSettings{}); got != want {
		t.Fatalf("RiskRuntimeFromSettings(unset) = %+v\nwant %+v", got, want)
	}
}

// Each knob is clamped to its safety bound on read, and a negative value is
// unset like 0. The bounds are not policy: a refresh more often than every
// ten minutes re-reads a week of fetches and five weeks of hourly rows per
// account for nothing; a first delay past an hour leaves a fresh install's
// risk view empty; a bell freshness past a month keeps history on the bell;
// the fetch window cannot outgrow the day masks (a week); the login log is
// read at least a week (the hold) and at most a year (one run's memory).
func TestRiskRuntimeFromSettings_Clamps(t *testing.T) {
	for _, c := range []struct {
		name string
		in   RiskRuntimeSettings
		got  func(RiskRuntime) any
		want any
	}{
		{"refresh below ten minutes", RiskRuntimeSettings{RefreshIntervalMinutes: 5}, func(r RiskRuntime) any { return r.RefreshInterval }, 10 * time.Minute},
		{"refresh beyond a day", RiskRuntimeSettings{RefreshIntervalMinutes: 5000}, func(r RiskRuntime) any { return r.RefreshInterval }, 1440 * time.Minute},
		{"refresh in range", RiskRuntimeSettings{RefreshIntervalMinutes: 30}, func(r RiskRuntime) any { return r.RefreshInterval }, 30 * time.Minute},
		{"first delay beyond an hour", RiskRuntimeSettings{FirstDelayMinutes: 999}, func(r RiskRuntime) any { return r.FirstDelay }, 60 * time.Minute},
		{"first delay of one minute", RiskRuntimeSettings{FirstDelayMinutes: 1}, func(r RiskRuntime) any { return r.FirstDelay }, time.Minute},
		{"freshness beyond a month", RiskRuntimeSettings{AlertFreshnessHours: 9999}, func(r RiskRuntime) any { return r.AlertFreshness }, 720 * time.Hour},
		{"freshness in range", RiskRuntimeSettings{AlertFreshnessHours: 6}, func(r RiskRuntime) any { return r.AlertFreshness }, 6 * time.Hour},
		{"window beyond the day masks", RiskRuntimeSettings{WindowDays: 9}, func(r RiskRuntime) any { return r.WindowDays }, 7},
		{"window of three days", RiskRuntimeSettings{WindowDays: 3}, func(r RiskRuntime) any { return r.WindowDays }, 3},
		{"lookback shorter than the hold", RiskRuntimeSettings{LoginLookbackDays: 3}, func(r RiskRuntime) any { return r.LoginLookbackDays }, 7},
		{"lookback beyond a year", RiskRuntimeSettings{LoginLookbackDays: 999}, func(r RiskRuntime) any { return r.LoginLookbackDays }, 365},
		{"lookback beyond the old ninety", RiskRuntimeSettings{LoginLookbackDays: 200}, func(r RiskRuntime) any { return r.LoginLookbackDays }, 200},
		{"negative refresh is unset", RiskRuntimeSettings{RefreshIntervalMinutes: -1}, func(r RiskRuntime) any { return r.RefreshInterval }, 60 * time.Minute},
		{"negative first delay is unset", RiskRuntimeSettings{FirstDelayMinutes: -1}, func(r RiskRuntime) any { return r.FirstDelay }, 2 * time.Minute},
		{"negative freshness is unset", RiskRuntimeSettings{AlertFreshnessHours: -1}, func(r RiskRuntime) any { return r.AlertFreshness }, 24 * time.Hour},
		{"negative window is unset", RiskRuntimeSettings{WindowDays: -1}, func(r RiskRuntime) any { return r.WindowDays }, 7},
		{"negative lookback is unset", RiskRuntimeSettings{LoginLookbackDays: -1}, func(r RiskRuntime) any { return r.LoginLookbackDays }, 90},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := c.got(RiskRuntimeFromSettings(c.in)); got != c.want {
				t.Fatalf("got %v, want %v", got, c.want)
			}
		})
	}
}

// The worker rewrites every row once per refresh, so a bell that forgets a
// flag sooner than two refreshes would flicker it off between runs — the
// latched account lit, dark, lit. The freshness in effect is therefore at
// least two refresh intervals, whatever was stored: a daily refresh with a
// one-hour freshness keeps a flag on the bell for 48 hours.
func TestRiskRuntimeFromSettings_AlertFreshnessCoversTwoRefreshes(t *testing.T) {
	for _, c := range []struct {
		name              string
		refresh, freshest int
		want              time.Duration
	}{
		{"a daily refresh raises one hour to two days", 1440, 1, 48 * time.Hour},
		{"a half-hourly refresh leaves one hour alone", 30, 1, time.Hour},
		{"the defaults are the former 24 hours", 0, 0, 24 * time.Hour},
		{"a stored freshness above the floor is kept", 60, 6, 6 * time.Hour},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := RiskRuntimeFromSettings(RiskRuntimeSettings{RefreshIntervalMinutes: c.refresh, AlertFreshnessHours: c.freshest}).AlertFreshness
			if got != c.want {
				t.Fatalf("AlertFreshness = %v, want %v", got, c.want)
			}
		})
	}
}

// The geo bell counts latches the traffic poll re-judges every poll, so its
// floor is two POLL intervals, not two risk refreshes. The poll interval has
// no ceiling (cron_traffic_pull_minutes is only checked for < 0), so an admin
// who polls every 30 hours must not see a latched flag drop off the bell 24
// hours after the last poll and come back after the next.
func TestRiskRuntime_GeoBellFreshnessCoversTwoPolls(t *testing.T) {
	rt := RiskRuntimeFromSettings(RiskRuntimeSettings{AlertFreshnessHours: 24})
	if got := rt.GeoBellFreshness(30 * time.Hour); got != 60*time.Hour {
		t.Fatalf("GeoBellFreshness(30h) = %v, want 60h", got)
	}
	if got := rt.GeoBellFreshness(5 * time.Minute); got != 24*time.Hour {
		t.Fatalf("GeoBellFreshness(5m) = %v, want the configured 24h", got)
	}
	if got := rt.GeoBellFreshness(0); got != 24*time.Hour {
		t.Fatalf("GeoBellFreshness(0) = %v, want the configured 24h", got)
	}
}

// (guard) Upgrading must change nothing: the shipped defaults are exactly the
// constants these knobs replaced — an hourly refresh, a two-minute first
// delay, a 24-hour bell, a seven-day fetch window and a 90-day login
// lookback. Frozen here as literals, so moving a default is a deliberate
// edit of this test and not a side effect of editing a constant.
//
// Mutation: a default lookback of 91 turns this red.
func TestDefaultRiskRuntimeEqualsTheFormerConstants(t *testing.T) {
	want := RiskRuntime{
		RefreshInterval:   time.Hour,
		FirstDelay:        2 * time.Minute,
		AlertFreshness:    24 * time.Hour,
		WindowDays:        7,
		LoginLookbackDays: 90,
	}
	if got := DefaultRiskRuntime(); got != want {
		t.Fatalf("DefaultRiskRuntime() = %+v\nwant the former constants %+v", got, want)
	}
	if RiskLoginLookbackDays != want.LoginLookbackDays || RiskWindowDays != want.WindowDays {
		t.Fatalf("the named defaults moved: lookback %d, window %d", RiskLoginLookbackDays, RiskWindowDays)
	}
}
