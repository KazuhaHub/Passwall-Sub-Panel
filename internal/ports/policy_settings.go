package ports

import "github.com/KazuhaHub/passwall-sub-panel/internal/domain"

// RiskPolicySettings is the ONE mapping from the stored risk.* values to the
// domain's flat form: the risk worker resolves a group's settings and hands
// this to domain.RiskPolicyFromSettings, which decides what an unset or
// nonsense value means.
//
// A method on UISettings rather than a literal at the call site (precedent:
// NodeTaskLifecyclePolicy) because a knob forgotten in a hand-copied literal
// fails silently — the form saves it, the group editor shows it, and the
// worker judges with the default. One mapping is one place for a test to
// hold to every field.
//
// RiskHWIDCaptureOff is deliberately not carried: it is global only (absent
// from OverridableScopeKeys), so a group-resolved copy of it would only ever
// echo the global value — whoever needs it reads it from the global settings.
func (s UISettings) RiskPolicySettings() domain.RiskPolicySettings {
	return domain.RiskPolicySettings{
		SubSpreadOff:    s.RiskSubSpreadOff,
		DevicesOff:      s.RiskDevicesOff,
		UsageShiftOff:   s.RiskUsageShiftOff,
		LoginCountryOff: s.RiskLoginCountryOff,
		MinDays:         s.RiskMinDays,
		MaxDevices:      s.RiskMaxDevices,
		UsageRatio:      s.RiskUsageRatio,
		UsageFloorGB:    s.RiskUsageFloorGB,
	}
}
