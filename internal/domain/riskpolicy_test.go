package domain

import (
	"encoding/json"
	"math"
	"reflect"
	"testing"
)

// A fresh install has no risk.* rows, so every knob reads as its zero. Read
// literally that is a device limit of 0 and a zero-byte usage floor — every
// account that fetches at all would be over. Zero means "never configured",
// and RiskPolicyFromSettings is the one place that is decided.
//
// The defaults are pinned as literals rather than through the constants: the
// admin form shows them as placeholders and the SPA mirrors this function, so
// a default that moves has to move on purpose, here, where it is visible.
// And every signal is ON by default — the switches are negative so that a
// never-saved form means "observe".
func TestRiskPolicyFromSettings_UnsetIsTheDefault(t *testing.T) {
	want := RiskPolicy{
		DestBlockThreshold: 20,
		MinDays:            3,
		MaxDevices:         3,
		UsageRatio:         3.0,
		UsageFloorBytes:    3 << 30,
		LoginWarmupLogins:  3,
		LoginHoldDays:      7,
		UsageWarmupDays:    14,
		UsageFlagDays:      4,
		UsageSuspectDays:   2,
	}
	if got := DefaultRiskPolicy(); !reflect.DeepEqual(got, want) {
		t.Fatalf("DefaultRiskPolicy() = %+v\nwant the shipped default %+v", got, want)
	}
	if got := RiskPolicyFromSettings(RiskPolicySettings{}); !reflect.DeepEqual(got, want) {
		t.Fatalf("empty settings = %+v\nwant the shipped default %+v", got, want)
	}
}

// MinDays is how many distinct days a pattern must recur on inside the
// window before it is flagged. The window is at most RiskWindowDays (7) long,
// so a larger value could never be met and would silently turn "flagged" off
// — it is clamped to 7, the strictest a week can express. An unset or
// negative value is "never configured", not "any single day": it takes the
// default rather than 1, which would flag on one day's evidence.
func TestRiskPolicyFromSettings_MinDaysIsClampedTo1Through7(t *testing.T) {
	for _, c := range []struct{ in, want int }{
		{0, 3},
		{-2, 3},
		{9, 7},
		{7, 7},
		{1, 1},
		{5, 5},
	} {
		if got := RiskPolicyFromSettings(RiskPolicySettings{MinDays: c.in}).MinDays; got != c.want {
			t.Errorf("MinDays %d resolved to %d, want %d", c.in, got, c.want)
		}
	}
}

// A lower usage ratio accuses MORE accounts, so a misconfigured one must be
// raised, never kept: 1.0 would call every account that used its own median
// "over". The floor is 1.5 — the least a sustained change can mean.
// Unset (and a non-number a group override could carry, since the stored
// value is parsed without validation) is the default, not the floor.
func TestRiskPolicyFromSettings_RatioBelowOneAndAHalfIsRaised(t *testing.T) {
	for _, c := range []struct {
		name     string
		in, want float64
	}{
		{"one", 1.0, 1.5},
		{"just under", 1.49, 1.5},
		{"unset", 0, 3.0},
		{"negative", -2, 3.0},
		{"NaN", math.NaN(), 3.0},
		{"at the minimum", 1.5, 1.5},
		{"configured", 2.25, 2.25},
	} {
		if got := RiskPolicyFromSettings(RiskPolicySettings{UsageRatio: c.in}).UsageRatio; got != c.want {
			t.Errorf("%s: UsageRatio %v resolved to %v, want %v", c.name, c.in, got, c.want)
		}
	}
}

// A group override is stored as a raw string and parsed without validation,
// and strconv.ParseFloat("Inf") succeeds. An infinite ratio is the literal
// reading of "never over" — silence, which is the safe direction — but the
// ratio is written into every usage_shift evidence, and encoding/json refuses
// ±Inf: the account's row would fail to serialize and never be written. So it
// is kept, as the largest finite ratio. -Inf is not > 0: unset, the default.
func TestRiskPolicyFromSettings_InfiniteRatioStaysFinite(t *testing.T) {
	got := RiskPolicyFromSettings(RiskPolicySettings{UsageRatio: math.Inf(1)}).UsageRatio
	if got != math.MaxFloat64 {
		t.Fatalf("UsageRatio +Inf resolved to %v, want the largest finite ratio %v", got, math.MaxFloat64)
	}
	if _, err := json.Marshal(got); err != nil {
		t.Fatalf("the resolved ratio does not serialize: %v", err)
	}
	if got := RiskPolicyFromSettings(RiskPolicySettings{UsageRatio: math.Inf(-1)}).UsageRatio; got != 3.0 {
		t.Fatalf("UsageRatio -Inf resolved to %v, want the default 3", got)
	}
}

// The admin types gigabytes; the evaluator compares bytes. The unit is GiB
// (2^30), the unit the rest of the panel's traffic figures use. An unset or
// negative floor is the default 3 GiB, never zero: a zero floor would count a
// light account's first few megabytes as a surge.
//
// And a value too large for int64 bytes must not wrap negative — a negative
// floor is no floor at all, which is the accusing direction. Raising the
// floor may only ever raise it.
func TestRiskPolicyFromSettings_FloorIsGiB(t *testing.T) {
	for _, c := range []struct {
		in   int
		want int64
	}{
		{1, 1 << 30},
		{5, 5 << 30},
		{0, 3 << 30},
		{-4, 3 << 30},
	} {
		if got := RiskPolicyFromSettings(RiskPolicySettings{UsageFloorGB: c.in}).UsageFloorBytes; got != c.want {
			t.Errorf("UsageFloorGB %d resolved to %d bytes, want %d", c.in, got, c.want)
		}
	}
	large := RiskPolicyFromSettings(RiskPolicySettings{UsageFloorGB: 1 << 20}).UsageFloorBytes
	huge := RiskPolicyFromSettings(RiskPolicySettings{UsageFloorGB: math.MaxInt}).UsageFloorBytes
	if huge < RiskGiB || huge < large {
		t.Errorf("UsageFloorGB MaxInt resolved to %d bytes; want a floor at least as high as %d (1 Mi GiB), not a wrapped one", huge, large)
	}
}

// Each switch turns exactly its own signal off. Checked one at a time so a
// crossed wire — sub_spread's switch silencing devices — fails here instead
// of in a group that turned off the wrong signal.
func TestRiskPolicyFromSettings_OffFlagsPassThrough(t *testing.T) {
	for _, c := range []struct {
		name string
		in   RiskPolicySettings
		want func(RiskPolicy) bool
	}{
		{"sub_spread", RiskPolicySettings{SubSpreadOff: true}, func(p RiskPolicy) bool { return p.SubSpreadOff }},
		{"devices", RiskPolicySettings{DevicesOff: true}, func(p RiskPolicy) bool { return p.DevicesOff }},
		{"usage_shift", RiskPolicySettings{UsageShiftOff: true}, func(p RiskPolicy) bool { return p.UsageShiftOff }},
		{"login_country", RiskPolicySettings{LoginCountryOff: true}, func(p RiskPolicy) bool { return p.LoginCountryOff }},
	} {
		got := RiskPolicyFromSettings(c.in)
		if !c.want(got) {
			t.Errorf("%s: its switch did not turn it off: %+v", c.name, got)
		}
		off := 0
		for _, b := range []bool{got.SubSpreadOff, got.DevicesOff, got.UsageShiftOff, got.LoginCountryOff} {
			if b {
				off++
			}
		}
		if off != 1 {
			t.Errorf("%s: one switch turned %d signals off: %+v", c.name, off, got)
		}
		// The numeric knobs are independent of the switches.
		if got.MinDays != 3 || got.MaxDevices != 3 || got.UsageRatio != 3.0 || got.UsageFloorBytes != 3<<30 {
			t.Errorf("%s: a switch changed a numeric knob: %+v", c.name, got)
		}
	}
}

// The device limit is "more than this many devices". Unset or negative is
// the default, never zero: a limit of 0 would put every account that
// declared one device over it.
func TestRiskPolicyFromSettings_MaxDevicesUnsetIsTheDefault(t *testing.T) {
	for _, c := range []struct{ in, want int }{
		{0, 3},
		{-1, 3},
		{1, 1},
		{5, 5},
	} {
		if got := RiskPolicyFromSettings(RiskPolicySettings{MaxDevices: c.in}).MaxDevices; got != c.want {
			t.Errorf("MaxDevices %d resolved to %d, want %d", c.in, got, c.want)
		}
	}
}

// login_country's two thresholds are per-group knobs now: how many earlier
// placed logins a login needs before it is judged, and how many days one
// login from a new country keeps the account flagged. Unset or negative is
// the default (3 and 7), never the literal: a warm-up of 0 would judge an
// account's very first login, which is always "new". The warm-up is capped
// at 50 — past it a signal that expects rare logins never finishes
// learning — and the hold at a year, the longest the login log is read.
func TestRiskPolicyFromSettings_LoginKnobs(t *testing.T) {
	def := RiskPolicyFromSettings(RiskPolicySettings{})
	if def.LoginWarmupLogins != 3 || def.LoginHoldDays != 7 {
		t.Fatalf("unset login knobs = warm-up %d, hold %d; want 3 and 7", def.LoginWarmupLogins, def.LoginHoldDays)
	}
	for _, c := range []struct {
		name         string
		in           RiskPolicySettings
		warmup, hold int
	}{
		{"warm-up beyond fifty", RiskPolicySettings{LoginWarmupLogins: 60}, 50, 7},
		{"warm-up of one", RiskPolicySettings{LoginWarmupLogins: 1}, 1, 7},
		{"negative warm-up is unset", RiskPolicySettings{LoginWarmupLogins: -2}, 3, 7},
		{"hold of a month", RiskPolicySettings{LoginHoldDays: 30}, 3, 30},
		{"hold beyond a year", RiskPolicySettings{LoginHoldDays: 400}, 3, 365},
		{"negative hold is unset", RiskPolicySettings{LoginHoldDays: -1}, 3, 7},
	} {
		got := RiskPolicyFromSettings(c.in)
		if got.LoginWarmupLogins != c.warmup || got.LoginHoldDays != c.hold {
			t.Errorf("%s: warm-up %d, hold %d; want %d and %d", c.name, got.LoginWarmupLogins, got.LoginHoldDays, c.warmup, c.hold)
		}
	}
}

// A group's knobs are bounded by the fleet's runtime they are measured
// against: a place cannot recur on more days than the fetch window holds,
// and a login cannot stay recent longer than the log is read. Without the
// bound a group min_days of 7 under a 3-day window would make "flagged"
// silently impossible, and a hold of 30 days under a 14-day lookback would
// describe a month the worker never reads.
//
// The bound is the CONFIGURED window and lookback, never one a retention
// shortened: retention_short exists to say the logs are too short for
// min_days, and bounding min_days by them would silence it (see the
// worker's TestRefreshOnce_RetentionShortStillSurfaces).
func TestRiskPolicy_BoundedByRuntime(t *testing.T) {
	rt := RiskRuntimeFromSettings(RiskRuntimeSettings{WindowDays: 3, LoginLookbackDays: 14, UsageBaselineDays: 14, UsageRecentDays: 3})
	got := RiskPolicyFromSettings(RiskPolicySettings{MinDays: 7, LoginHoldDays: 30, UsageWarmupDays: 20, UsageFlagDays: 5, UsageSuspectDays: 4}).Bounded(rt)
	if got.MinDays != 3 || got.LoginHoldDays != 14 {
		t.Fatalf("bounded min_days %d, hold %d; want 3 (the window) and 14 (the lookback)", got.MinDays, got.LoginHoldDays)
	}
	// usage_shift's thresholds meet the series they are judged on: a
	// warm-up cannot need more history than the baseline holds (it would
	// never finish), an account cannot be over on more days than are judged
	// (flagged would be unreachable), and suspect is never above flag.
	if got.UsageWarmupDays != 14 || got.UsageFlagDays != 3 || got.UsageSuspectDays != 3 {
		t.Fatalf("bounded usage warm-up %d, flag %d, suspect %d; want 14 (the baseline), 3 (the judged days) and 3 (the flag)", got.UsageWarmupDays, got.UsageFlagDays, got.UsageSuspectDays)
	}
	within := RiskPolicyFromSettings(RiskPolicySettings{MinDays: 2, LoginHoldDays: 10, UsageWarmupDays: 10, UsageFlagDays: 3, UsageSuspectDays: 2}).Bounded(rt)
	if within.MinDays != 2 || within.LoginHoldDays != 10 || within.UsageWarmupDays != 10 || within.UsageFlagDays != 3 || within.UsageSuspectDays != 2 {
		t.Fatalf("knobs inside the runtime moved: %+v", within)
	}
	// Suspect above flag is held to flag whatever the runtime: under the
	// shipped week a group flagging at 3 and calling 5 suspect has no
	// suspect stage, and says so.
	if s := RiskPolicyFromSettings(RiskPolicySettings{UsageFlagDays: 3, UsageSuspectDays: 5}).Bounded(DefaultRiskRuntime()); s.UsageFlagDays != 3 || s.UsageSuspectDays != 3 {
		t.Fatalf("flag 3, suspect 5 bounded to flag %d, suspect %d; want 3 and 3", s.UsageFlagDays, s.UsageSuspectDays)
	}
	// At the defaults the bound is the identity: an upgrade changes nothing.
	if def := DefaultRiskPolicy(); !reflect.DeepEqual(def.Bounded(DefaultRiskRuntime()), def) {
		t.Fatalf("the default policy bounded by the default runtime = %+v, want it unchanged", def.Bounded(DefaultRiskRuntime()))
	}
	// A zero runtime (never sanitised) bounds nothing rather than driving
	// min_days to 0, which the evaluators would read as "any one day".
	if z := DefaultRiskPolicy().Bounded(RiskRuntime{}); z.MinDays != 3 || z.LoginHoldDays != 7 || z.UsageWarmupDays != 14 || z.UsageFlagDays != 4 || z.UsageSuspectDays != 2 {
		t.Fatalf("bounded by a zero runtime: %+v; want the defaults 3/7 and 14/4/2", z)
	}
}

// usage_shift's three per-group thresholds. Unset or negative is the
// shipped default (a 14-day warm-up, flagged at 4 over-days, suspect at 2),
// and each has a floor that errs toward NOT accusing, the precedent
// RiskUsageRatioMin set: a warm-up under 7 days takes the median over a
// first week, which is a median over setup (profiles imported, a first big
// download); flag or suspect at 1 over-day calls a single download a change
// of habit. The ceilings are the longest series a fleet may configure —
// Bounded then holds each to the series actually configured.
func TestRiskPolicyFromSettings_UsageFloors(t *testing.T) {
	def := RiskPolicyFromSettings(RiskPolicySettings{})
	if def.UsageWarmupDays != 14 || def.UsageFlagDays != 4 || def.UsageSuspectDays != 2 {
		t.Fatalf("unset usage days = warm-up %d, flag %d, suspect %d; want 14, 4 and 2", def.UsageWarmupDays, def.UsageFlagDays, def.UsageSuspectDays)
	}
	for _, c := range []struct {
		name                  string
		in                    RiskPolicySettings
		warmup, flag, suspect int
	}{
		{"flag at one day", RiskPolicySettings{UsageFlagDays: 1}, 14, 2, 2},
		{"suspect at one day", RiskPolicySettings{UsageSuspectDays: 1}, 14, 4, 2},
		{"warm-up of three days", RiskPolicySettings{UsageWarmupDays: 3}, 7, 4, 2},
		{"warm-up beyond the longest baseline", RiskPolicySettings{UsageWarmupDays: 99}, 56, 4, 2},
		{"flag beyond the longest judged days", RiskPolicySettings{UsageFlagDays: 99}, 14, 14, 2},
		{"suspect beyond the longest judged days", RiskPolicySettings{UsageSuspectDays: 99}, 14, 4, 14},
		{"in range", RiskPolicySettings{UsageWarmupDays: 10, UsageFlagDays: 3, UsageSuspectDays: 3}, 10, 3, 3},
		{"negative is unset", RiskPolicySettings{UsageWarmupDays: -1, UsageFlagDays: -1, UsageSuspectDays: -1}, 14, 4, 2},
	} {
		got := RiskPolicyFromSettings(c.in)
		if got.UsageWarmupDays != c.warmup || got.UsageFlagDays != c.flag || got.UsageSuspectDays != c.suspect {
			t.Errorf("%s: warm-up %d, flag %d, suspect %d; want %d, %d and %d", c.name, got.UsageWarmupDays, got.UsageFlagDays, got.UsageSuspectDays, c.warmup, c.flag, c.suspect)
		}
	}
}
