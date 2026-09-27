package ports

import (
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

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
		SubSpreadOff:      s.RiskSubSpreadOff,
		DevicesOff:        s.RiskDevicesOff,
		UsageShiftOff:     s.RiskUsageShiftOff,
		LoginCountryOff:   s.RiskLoginCountryOff,
		MinDays:           s.RiskMinDays,
		MaxDevices:        s.RiskMaxDevices,
		UsageRatio:        s.RiskUsageRatio,
		UsageFloorGB:      s.RiskUsageFloorGB,
		LoginWarmupLogins: s.RiskLoginWarmupLogins,
		LoginHoldDays:     s.RiskLoginHoldDays,
		UsageWarmupDays:   s.RiskUsageWarmupDays,
		UsageFlagDays:     s.RiskUsageFlagDays,
		UsageSuspectDays:  s.RiskUsageSuspectDays,
	}
}

// GeoRuntimeSettings is the ONE mapping from the fleet-wide geo_anomaly.*
// knobs — the detector's former constants — to the domain's flat form, read
// by the traffic poll (freshness, shared exits, the per-poll caps), the
// infrastructure refresh (its cadence and hostname TTL) and the risk worker
// (the shared-exit threshold of its fetch window).
// domain.GeoRuntimeFromSettings then decides what an unset or out-of-range
// value means.
//
// Global only, so always read from the global settings: none of these keys
// is group-overridable, and a group-resolved UISettings would only echo the
// global values.
func (s UISettings) GeoRuntimeSettings() domain.GeoRuntimeSettings {
	return domain.GeoRuntimeSettings{
		FreshWindowSeconds:  s.GeoAnomalyFreshWindowSeconds,
		SharedExitMinUsers:  s.GeoAnomalySharedExitMinUsers,
		BanMaxPerPoll:       s.GeoAnomalyBanMaxPerPoll,
		LiftMaxPerPoll:      s.GeoAnomalyLiftMaxPerPoll,
		InfraRefreshMinutes: s.GeoAnomalyInfraRefreshMinutes,
		InfraHostTTLMinutes: s.GeoAnomalyInfraHostTTLMinutes,
	}
}

// RiskRuntimeSettings is the ONE mapping from the fleet-wide risk.* knobs —
// the worker's and the bell's former constants — to the domain's flat form,
// read by the risk loop (its cadence and first delay), the risk worker (the
// fetch window, the login lookback and usage_shift's series), the alert
// feed (the bell's freshness), the hourly cleanup (the connection history's
// and the flag records' retentions) and the risk center's live view (its
// staleness, its refresh cooldown and its device window).
// domain.RiskRuntimeFromSettings then decides what an unset or out-of-range
// value means.
//
// Global only, so always read from the global settings: none of these keys
// is group-overridable, and a group-resolved UISettings would only echo the
// global values. The sub-log and auth-event retentions are deliberately not
// carried: the runtime holds what was CONFIGURED, and each retention
// shortens its log where that log is read.
func (s UISettings) RiskRuntimeSettings() domain.RiskRuntimeSettings {
	return domain.RiskRuntimeSettings{
		RefreshIntervalMinutes: s.RiskRefreshIntervalMinutes,
		FirstDelayMinutes:      s.RiskFirstDelayMinutes,
		AlertFreshnessHours:    s.RiskAlertFreshnessHours,
		WindowDays:             s.RiskWindowDays,
		LoginLookbackDays:      s.RiskLoginLookbackDays,
		UsageBaselineDays:      s.RiskUsageBaselineDays,
		UsageRecentDays:        s.RiskUsageRecentDays,

		ConnectionRetentionDays: s.RiskConnectionRetentionDays,
		FlagRecordRetentionDays: s.RiskFlagRecordRetentionDays,

		LiveSnapshotStaleMinutes:   s.RiskLiveSnapshotStaleMinutes,
		LiveRefreshCooldownSeconds: s.RiskLiveRefreshCooldownSeconds,
		DeviceInferHours:           s.RiskDeviceInferHours,
	}
}

// runtimeDefaultPollMinutes is the traffic poll's shipped cadence, what an
// unset cron_traffic_pull_minutes runs at. The settings repository fills it
// on Load, so a loaded UISettings always carries it; RuntimeEffective
// repeats it for a caller that built its UISettings by hand. The same five
// minutes as alert.defaultPollInterval and riskcenter.defaultPollInterval,
// the other readers that floor a window at two polls.
const runtimeDefaultPollMinutes = 5

// RuntimeEffective reports, for each of the 23 geo and risk runtime knobs,
// the number the panel runs with (effective) and the shipped default an
// unset knob falls back to (defaults), both keyed by the knob's json tag and
// in the knob's own unit. The admin settings GET serves them read-only
// beside the stored values, so the settings page can say "in effect" and
// use the default as the empty field's placeholder without a copy of any
// default or clamp: a second copy in the SPA would be a second definition
// of each rule, and the first thing to drift (D18).
//
// Every number comes from the domain function that the reader of the knob
// uses, applied to the GLOBAL settings:
//   - the fleet-wide knobs through GeoRuntimeFromSettings and
//     RiskRuntimeFromSettings (so the alert freshness already carries its
//     floor of two risk refreshes);
//   - the five per-group thresholds through RiskPolicyFromSettings, then
//     Bounded against the configured runtime, the way the worker judges
//     with the global policy — a flag threshold above the judged days is
//     shown as the judged days;
//   - the live view's staleness through SnapshotStaleAfter, floored at two
//     traffic polls as the view floors it.
//
// Deliberately NOT applied: the sub-log and auth-event retentions, which
// shorten the fetch window, the login lookback and the device window where
// those logs are read (D15). They are separate settings on the same page
// and the knobs' hints say so; folding them in here would need a second
// copy of the read-time rule, which lives in the services.
//
// The bell freshness is reported for the risk entry. The concurrent-location
// entry is floored at two traffic polls on top (GeoBellFreshness), which
// only differs once a poll is longer than half the freshness, and the hint
// states that floor.
//
// A duration is converted to its unit rounding UP. Every conversion is exact
// except the bell freshness, whose floor of two refresh intervals need not
// be whole hours; rounding down would state a window the bell does not keep.
func RuntimeEffective(global UISettings) (effective, defaults map[string]int) {
	poll := time.Duration(runtimeDefaultPollMinutes) * time.Minute
	if global.CronTrafficPullMinutes > 0 {
		poll = time.Duration(global.CronTrafficPullMinutes) * time.Minute
	}
	rt := domain.RiskRuntimeFromSettings(global.RiskRuntimeSettings())
	effective = runtimeKnobValues(
		domain.GeoRuntimeFromSettings(global.GeoRuntimeSettings()),
		rt,
		domain.RiskPolicyFromSettings(global.RiskPolicySettings()).Bounded(rt),
		poll,
	)
	// The shipped values, whatever is stored: they are what an emptied field
	// falls back to. At the shipped poll interval the staleness floor is
	// below the shipped staleness, so every default is the domain's own.
	defaults = runtimeKnobValues(
		domain.DefaultGeoRuntime(),
		domain.DefaultRiskRuntime(),
		domain.DefaultRiskPolicy().Bounded(domain.DefaultRiskRuntime()),
		time.Duration(runtimeDefaultPollMinutes)*time.Minute,
	)
	return effective, defaults
}

// runtimeKnobValues lays the resolved runtimes out as the 23 knobs, each in
// the unit its setting is typed in.
func runtimeKnobValues(geo domain.GeoRuntime, rt domain.RiskRuntime, p domain.RiskPolicy, poll time.Duration) map[string]int {
	in := func(d, unit time.Duration) int { return int((d + unit - 1) / unit) }
	return map[string]int{
		"geo_anomaly_fresh_window_seconds":   geo.FreshWindowSeconds,
		"geo_anomaly_shared_exit_min_users":  geo.SharedExitMinUsers,
		"geo_anomaly_ban_max_per_poll":       geo.BanMaxPerPoll,
		"geo_anomaly_lift_max_per_poll":      geo.LiftMaxPerPoll,
		"geo_anomaly_infra_refresh_minutes":  in(geo.InfraRefresh, time.Minute),
		"geo_anomaly_infra_host_ttl_minutes": in(geo.InfraHostTTL, time.Minute),

		"risk_refresh_interval_minutes": in(rt.RefreshInterval, time.Minute),
		"risk_first_delay_minutes":      in(rt.FirstDelay, time.Minute),
		"risk_alert_freshness_hours":    in(rt.AlertFreshness, time.Hour),
		"risk_window_days":              rt.WindowDays,
		"risk_usage_baseline_days":      rt.UsageBaselineDays,
		"risk_usage_recent_days":        rt.UsageRecentDays,
		"risk_login_lookback_days":      rt.LoginLookbackDays,

		"risk_usage_warmup_days":   p.UsageWarmupDays,
		"risk_usage_flag_days":     p.UsageFlagDays,
		"risk_usage_suspect_days":  p.UsageSuspectDays,
		"risk_login_warmup_logins": p.LoginWarmupLogins,
		"risk_login_hold_days":     p.LoginHoldDays,

		"risk_connection_retention_days":     rt.ConnectionRetentionDays,
		"risk_flag_record_retention_days":    rt.FlagRecordRetentionDays,
		"risk_live_snapshot_stale_minutes":   in(rt.SnapshotStaleAfter(poll), time.Minute),
		"risk_live_refresh_cooldown_seconds": in(rt.LiveRefreshCooldown, time.Second),
		"risk_device_infer_hours":            in(rt.DeviceInferWindow, time.Hour),
	}
}
