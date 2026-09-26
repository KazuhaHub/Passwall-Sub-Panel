package domain

import "math"

// The risk signals' shipped defaults and bounds.
//
// Every signal is observe-only — nothing here suspends, blocks or notifies an
// account holder — so the defaults are tuned for an admin reading a table, not
// for enforcement: loose enough that a flag is worth opening, never so strict
// that the table is all red and gets ignored.
const (
	// RiskWindowDays is the fetch-log window the place and device signals
	// read, in panel-local calendar days. It is also MinDays' ceiling: a
	// pattern cannot recur on more days than the window holds.
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
}

// RiskPolicy is the sanitized policy the risk evaluators judge with. Every
// field is usable as it stands; the comments are the invariants
// RiskPolicyFromSettings guarantees.
type RiskPolicy struct {
	SubSpreadOff, DevicesOff, UsageShiftOff, LoginCountryOff bool
	MinDays                                                  int     // 1..RiskWindowDays
	MaxDevices                                               int     // >= 1
	UsageRatio                                               float64 // >= RiskUsageRatioMin
	UsageFloorBytes                                          int64   // >= RiskGiB
}

// DefaultRiskPolicy is the shipped policy: every signal on, each tolerance at
// its default.
func DefaultRiskPolicy() RiskPolicy {
	return RiskPolicy{
		MinDays:         RiskDefaultMinDays,
		MaxDevices:      RiskDefaultMaxDevices,
		UsageRatio:      RiskDefaultUsageRatio,
		UsageFloorBytes: RiskDefaultUsageFloorGB * RiskGiB,
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
//     without validation — is not > 0, so it takes the default.
//   - The floor is GB × RiskGiB, saturating rather than wrapping: a value too
//     large for int64 bytes must stay the highest floor, not turn negative
//     (no floor at all).
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
		p.UsageRatio = max(s.UsageRatio, RiskUsageRatioMin)
	}
	if s.UsageFloorGB > 0 {
		if int64(s.UsageFloorGB) > math.MaxInt64/RiskGiB {
			p.UsageFloorBytes = math.MaxInt64
		} else {
			p.UsageFloorBytes = int64(s.UsageFloorGB) * RiskGiB
		}
	}
	return p
}
