package domain

import (
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
		MinDays:         3,
		MaxDevices:      3,
		UsageRatio:      3.0,
		UsageFloorBytes: 3 << 30,
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
