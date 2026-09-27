package domain

import "math"

// The risk signals' shipped defaults and bounds.
//
// Every signal is observe-only — nothing here suspends, blocks or notifies an
// account holder — so the defaults are tuned for an admin reading a table, not
// for enforcement: loose enough that a flag is worth opening, never so strict
// that the table is all red and gets ignored.
const (
	// RiskWindowDays is the longest fetch-log window the place and device
	// signals can read, in panel-local calendar days, and so MinDays'
	// ceiling: a pattern cannot recur on more days than the window holds.
	// Structural, not policy — every day mask (bit i = window day i) is a
	// uint8 — so risk.window_days is clamped to it, and it is also that
	// setting's default (RiskDefaultWindowDays).
	RiskWindowDays = 7
	// RiskDefaultMinDays: a place or device seen on 3 of 7 days is a habit,
	// not a trip or a borrowed phone.
	RiskDefaultMinDays = 3
	// RiskDefaultMaxDevices: a phone, a laptop and one more.
	RiskDefaultMaxDevices = 3
	// RiskDefaultUsageRatio: a day at three times the account's own median.
	RiskDefaultUsageRatio = 3.0
	// RiskUsageRatioMin is the lowest ratio a policy may judge with. Below
	// it an ordinary busy week would read as a sustained change.
	RiskUsageRatioMin = 1.5
	// RiskDefaultUsageFloorGB: a day under 3 GiB never counts as over.
	RiskDefaultUsageFloorGB = 3
	// RiskGiB is the unit an admin types the floor in.
	RiskGiB int64 = 1 << 30
)

// RiskPolicySettings is the flat, storage-shaped form of the risk policy —
// what the admin typed, already resolved group over global by the scoped
// settings layer (ports.UISettings.RiskPolicySettings builds it). A stored 0
// means "never configured"; RiskPolicyFromSettings turns this into a policy
// that is safe to judge with, the same split GeoPolicySettings makes for the
// concurrent-location policy.
type RiskPolicySettings struct {
	SubSpreadOff, DevicesOff, UsageShiftOff, LoginCountryOff bool
	MinDays, MaxDevices                                      int
	UsageRatio                                               float64
	UsageFloorGB                                             int
	// LoginWarmupLogins and LoginHoldDays are login_country's thresholds:
	// how many earlier placed logins a login needs before it is judged, and
	// how many days a login from a new country keeps the account flagged.
	LoginWarmupLogins, LoginHoldDays int
}

// RiskPolicy is the sanitized policy the risk evaluators judge with. Every
// field is usable as it stands; the comments are the invariants
// RiskPolicyFromSettings guarantees, and Bounded tightens two of them
// against the fleet's configured runtime.
type RiskPolicy struct {
	SubSpreadOff, DevicesOff, UsageShiftOff, LoginCountryOff bool
	MinDays                                                  int     // 1..RiskWindowDays; Bounded: <= the configured window
	MaxDevices                                               int     // >= 1
	UsageRatio                                               float64 // >= RiskUsageRatioMin, finite
	UsageFloorBytes                                          int64   // >= RiskGiB
	LoginWarmupLogins                                        int     // 1..RiskLoginWarmupMaxLogins
	LoginHoldDays                                            int     // 1..RiskLoginLookbackMaxDays; Bounded: <= the configured lookback
}

// DefaultRiskPolicy is the shipped policy: every signal on, each tolerance at
// its default.
func DefaultRiskPolicy() RiskPolicy {
	return RiskPolicy{
		MinDays:         RiskDefaultMinDays,
		MaxDevices:      RiskDefaultMaxDevices,
		UsageRatio:      RiskDefaultUsageRatio,
		UsageFloorBytes: RiskDefaultUsageFloorGB * RiskGiB,
		// login_country's thresholds, the constants they replaced.
		LoginWarmupLogins: RiskLoginWarmupLogins,
		LoginHoldDays:     RiskLoginHoldDays,
	}
}

// RiskPolicyFromSettings turns stored settings into a policy that is safe to
// judge with.
//
// A value that is zero or negative means "never configured" and takes the
// default, never its literal reading: a device limit of 0 or a zero-byte floor
// would put every account that fetches at all over it. Every other repair
// errs toward NOT accusing, because a misconfiguration must degrade into
// silence rather than into a table of false flags:
//   - MinDays is clamped to 1..RiskWindowDays. Above the window it could never
//     be met, and "flagged" would silently stop existing.
//   - UsageRatio is raised to RiskUsageRatioMin. A lower ratio accuses more.
//     NaN — which a group override can carry, since stored values are parsed
//     without validation — is not > 0, so it takes the default. +Inf ("Inf"
//     parses) means "never over" and is kept as the largest finite ratio:
//     the ratio is written into usage_shift's evidence, and encoding/json
//     refuses an infinity, so the account's row could never be saved.
//   - The floor is GB × RiskGiB, saturating rather than wrapping: a value too
//     large for int64 bytes must stay the highest floor, not turn negative
//     (no floor at all).
//   - login_country's warm-up is clamped to 1..RiskLoginWarmupMaxLogins:
//     above it the signal never finishes learning, which is "off" by
//     another name. The hold is clamped to 1..RiskLoginLookbackMaxDays, the
//     longest the log can be read; Bounded then holds it to the lookback
//     actually configured.
//
// The four switches have no "unset" — false IS the default (signal on) — and
// are copied through.
//
// This is the single place these rules live; the admin form does not
// validate, so there is no second definition of "valid" to drift from it.
func RiskPolicyFromSettings(s RiskPolicySettings) RiskPolicy {
	p := DefaultRiskPolicy()
	p.SubSpreadOff = s.SubSpreadOff
	p.DevicesOff = s.DevicesOff
	p.UsageShiftOff = s.UsageShiftOff
	p.LoginCountryOff = s.LoginCountryOff
	if s.MinDays > 0 {
		p.MinDays = min(s.MinDays, RiskWindowDays)
	}
	if s.MaxDevices > 0 {
		p.MaxDevices = s.MaxDevices
	}
	if s.UsageRatio > 0 {
		p.UsageRatio = min(max(s.UsageRatio, RiskUsageRatioMin), math.MaxFloat64)
	}
	if s.UsageFloorGB > 0 {
		if int64(s.UsageFloorGB) > math.MaxInt64/RiskGiB {
			p.UsageFloorBytes = math.MaxInt64
		} else {
			p.UsageFloorBytes = int64(s.UsageFloorGB) * RiskGiB
		}
	}
	if s.LoginWarmupLogins > 0 {
		p.LoginWarmupLogins = min(s.LoginWarmupLogins, RiskLoginWarmupMaxLogins)
	}
	if s.LoginHoldDays > 0 {
		p.LoginHoldDays = min(s.LoginHoldDays, RiskLoginLookbackMaxDays)
	}
	return p
}

// Bounded holds a group's policy to the fleet runtime it is measured
// against, the configured values only:
//   - MinDays <= rt.WindowDays: a place or device cannot recur on more days
//     than the fetch window holds, and a min_days above it would make
//     "flagged" silently unreachable.
//   - LoginHoldDays <= rt.LoginLookbackDays: a login cannot stay recent
//     longer than the log is read.
//
// The CONFIGURED window, never the one a short sub-log retention leaves:
// with a week configured, min_days 5 and three days of logs kept, the place
// and device signals must read unknown/retention_short — the code that
// tells the admin the logs are too short for the policy — and bounding
// min_days to three would judge them instead. The retention is applied
// where the logs are read.
//
// A zero field in rt (a runtime that did not come from
// RiskRuntimeFromSettings) bounds nothing: min_days 0 would read as "any
// one day", the accusing direction. The group's own knobs are what
// RiskPolicyFromSettings made of them; this is the single place they meet
// the fleet's.
func (p RiskPolicy) Bounded(rt RiskRuntime) RiskPolicy {
	if rt.WindowDays > 0 {
		p.MinDays = min(p.MinDays, rt.WindowDays)
	}
	if rt.LoginLookbackDays > 0 {
		p.LoginHoldDays = min(p.LoginHoldDays, rt.LoginLookbackDays)
	}
	return p
}
