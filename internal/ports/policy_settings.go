package ports

import "github.com/KazuhaHub/passwall-sub-panel/internal/domain"

// GeoPolicySettings is the ONE mapping from the stored geo_anomaly.* values to
// the domain's flat form, shared by the traffic poll (the concurrent-location
// verdict) and the risk worker (sub_spread reuses the group's scope, region
// tolerance, allow_anywhere and placed ratio). domain.GeoPolicyFromSettings
// then decides what an unset or nonsense value means.
//
// It replaces the literal the poll used to carry: two hand-copied literals
// would drift, and a knob one of them forgot is saved by the form, shown by
// the group editor and judged with the default, silently.
//
// GeoAnomalyIgnoreAddresses is deliberately not carried: it is global only and
// not part of the judging policy — it decides which addresses are judged at
// all — so each reader parses it from the global settings.
func (s UISettings) GeoPolicySettings() domain.GeoPolicySettings {
	return domain.GeoPolicySettings{
		Scope:              s.GeoAnomalyScope,
		MaxPlaces:          s.GeoAnomalyMaxPlaces,
		MaxRegions:         s.GeoAnomalyMaxRegions,
		MaxCities:          s.GeoAnomalyMaxCities,
		FlagAfterPolls:     s.GeoAnomalyFlagAfterPolls,
		ClearAfterPolls:    s.GeoAnomalyClearAfterPolls,
		MinPlacedRatio:     s.GeoAnomalyMinPlacedRatio,
		CoTravel:           s.GeoAnomalyCoTravel,
		AllowAnywhere:      s.GeoAnomalyAllowAnywhere,
		BanEnabled:         s.GeoAnomalyBanEnabled,
		BanMaxCountries:    s.GeoAnomalyBanMaxCountries,
		BanMaxRegions:      s.GeoAnomalyBanMaxRegions,
		BanMaxCities:       s.GeoAnomalyBanMaxCities,
		BanAfterPolls:      s.GeoAnomalyBanAfterPolls,
		BanDurationMinutes: s.GeoAnomalyBanDurationMinutes,
	}
}

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
