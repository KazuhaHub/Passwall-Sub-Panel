package ports

import (
	"reflect"
	"slices"
	"strings"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// RiskCenterPolicy is the risk center's policy page: EVERY geo_anomaly_* and
// risk_* field of UISettings — the concurrent-location detector's policy, its
// automatic suspension and fleet-wide runtime, and the risk signals' policy
// and runtime — with the same Go names, the same types and the same json tags
// (48). Each field means exactly what its UISettings namesake documents; read
// the comments there, not here, so there is one definition of each knob.
//
// The /risk-center/policy endpoint owns these keys. It is a separate type,
// not a view the handler assembles, so that "which keys belong to the policy"
// has one answer, held to the settings by
// TestRiskCenterPolicy_IsExactlyTheGeoAndRiskFields: a detector knob left out
// would have no place on the policy page, and a foreign key let in would let
// the risk center's save move something that is not its own.
type RiskCenterPolicy struct {
	// Concurrent locations: the judging policy.
	GeoAnomalyScope           string  `json:"geo_anomaly_scope"`
	GeoAnomalyMaxPlaces       int     `json:"geo_anomaly_max_places"`
	GeoAnomalyMaxRegions      int     `json:"geo_anomaly_max_regions"`
	GeoAnomalyMaxCities       int     `json:"geo_anomaly_max_cities"`
	GeoAnomalyFlagAfterPolls  int     `json:"geo_anomaly_flag_after_polls"`
	GeoAnomalyClearAfterPolls int     `json:"geo_anomaly_clear_after_polls"`
	GeoAnomalyMinPlacedRatio  float64 `json:"geo_anomaly_min_placed_ratio"`
	GeoAnomalyCoTravel        string  `json:"geo_anomaly_co_travel"`
	GeoAnomalyAllowAnywhere   bool    `json:"geo_anomaly_allow_anywhere"`
	GeoAnomalyIgnoreAddresses string  `json:"geo_anomaly_ignore_addresses"`
	// Concurrent locations: automatic temporary suspension.
	GeoAnomalyBanEnabled         bool `json:"geo_anomaly_ban_enabled"`
	GeoAnomalyBanMaxCountries    int  `json:"geo_anomaly_ban_max_countries"`
	GeoAnomalyBanMaxRegions      int  `json:"geo_anomaly_ban_max_regions"`
	GeoAnomalyBanMaxCities       int  `json:"geo_anomaly_ban_max_cities"`
	GeoAnomalyBanAfterPolls      int  `json:"geo_anomaly_ban_after_polls"`
	GeoAnomalyBanDurationMinutes int  `json:"geo_anomaly_ban_duration_minutes"`
	// Concurrent locations: the detector's fleet-wide runtime.
	GeoAnomalyFreshWindowSeconds  int `json:"geo_anomaly_fresh_window_seconds"`
	GeoAnomalySharedExitMinUsers  int `json:"geo_anomaly_shared_exit_min_users"`
	GeoAnomalyBanMaxPerPoll       int `json:"geo_anomaly_ban_max_per_poll"`
	GeoAnomalyLiftMaxPerPoll      int `json:"geo_anomaly_lift_max_per_poll"`
	GeoAnomalyInfraRefreshMinutes int `json:"geo_anomaly_infra_refresh_minutes"`
	GeoAnomalyInfraHostTTLMinutes int `json:"geo_anomaly_infra_host_ttl_minutes"`
	// Risk signals: device capture, the four switches and the thresholds.
	RiskHWIDCaptureOff    bool    `json:"risk_hwid_capture_off"`
	RiskSubSpreadOff      bool    `json:"risk_sub_spread_off"`
	RiskDevicesOff        bool    `json:"risk_devices_off"`
	RiskUsageShiftOff     bool    `json:"risk_usage_shift_off"`
	RiskLoginCountryOff   bool    `json:"risk_login_country_off"`
	RiskMinDays           int     `json:"risk_min_days"`
	RiskMaxDevices        int     `json:"risk_max_devices"`
	RiskUsageRatio        float64 `json:"risk_usage_ratio"`
	RiskUsageFloorGB      int     `json:"risk_usage_floor_gb"`
	RiskLoginWarmupLogins int     `json:"risk_login_warmup_logins"`
	RiskLoginHoldDays     int     `json:"risk_login_hold_days"`
	RiskUsageWarmupDays   int     `json:"risk_usage_warmup_days"`
	RiskUsageFlagDays     int     `json:"risk_usage_flag_days"`
	RiskUsageSuspectDays  int     `json:"risk_usage_suspect_days"`
	// Risk signals: the worker's fleet-wide runtime and the risk center's
	// retention and live view.
	RiskRefreshIntervalMinutes     int `json:"risk_refresh_interval_minutes"`
	RiskFirstDelayMinutes          int `json:"risk_first_delay_minutes"`
	RiskAlertFreshnessHours        int `json:"risk_alert_freshness_hours"`
	RiskWindowDays                 int `json:"risk_window_days"`
	RiskLoginLookbackDays          int `json:"risk_login_lookback_days"`
	RiskUsageBaselineDays          int `json:"risk_usage_baseline_days"`
	RiskUsageRecentDays            int `json:"risk_usage_recent_days"`
	RiskConnectionRetentionDays    int `json:"risk_connection_retention_days"`
	RiskFlagRecordRetentionDays    int `json:"risk_flag_record_retention_days"`
	RiskLiveSnapshotStaleMinutes   int `json:"risk_live_snapshot_stale_minutes"`
	RiskLiveRefreshCooldownSeconds int `json:"risk_live_refresh_cooldown_seconds"`
	RiskDeviceInferHours           int `json:"risk_device_infer_hours"`
}

// RiskCenterPolicy copies the policy out of the settings.
//
// Hand-written rather than reflected, like the other mappings in this
// package: a plain assignment list is greppable and fails to compile when a
// field is renamed, and TestRiskCenterPolicy_CopiesEveryPolicyFieldAndNothingElse
// holds it to every field with distinct values, so a forgotten or crossed
// line cannot pass.
func (s UISettings) RiskCenterPolicy() RiskCenterPolicy {
	return RiskCenterPolicy{
		GeoAnomalyScope:           s.GeoAnomalyScope,
		GeoAnomalyMaxPlaces:       s.GeoAnomalyMaxPlaces,
		GeoAnomalyMaxRegions:      s.GeoAnomalyMaxRegions,
		GeoAnomalyMaxCities:       s.GeoAnomalyMaxCities,
		GeoAnomalyFlagAfterPolls:  s.GeoAnomalyFlagAfterPolls,
		GeoAnomalyClearAfterPolls: s.GeoAnomalyClearAfterPolls,
		GeoAnomalyMinPlacedRatio:  s.GeoAnomalyMinPlacedRatio,
		GeoAnomalyCoTravel:        s.GeoAnomalyCoTravel,
		GeoAnomalyAllowAnywhere:   s.GeoAnomalyAllowAnywhere,
		GeoAnomalyIgnoreAddresses: s.GeoAnomalyIgnoreAddresses,

		GeoAnomalyBanEnabled:         s.GeoAnomalyBanEnabled,
		GeoAnomalyBanMaxCountries:    s.GeoAnomalyBanMaxCountries,
		GeoAnomalyBanMaxRegions:      s.GeoAnomalyBanMaxRegions,
		GeoAnomalyBanMaxCities:       s.GeoAnomalyBanMaxCities,
		GeoAnomalyBanAfterPolls:      s.GeoAnomalyBanAfterPolls,
		GeoAnomalyBanDurationMinutes: s.GeoAnomalyBanDurationMinutes,

		GeoAnomalyFreshWindowSeconds:  s.GeoAnomalyFreshWindowSeconds,
		GeoAnomalySharedExitMinUsers:  s.GeoAnomalySharedExitMinUsers,
		GeoAnomalyBanMaxPerPoll:       s.GeoAnomalyBanMaxPerPoll,
		GeoAnomalyLiftMaxPerPoll:      s.GeoAnomalyLiftMaxPerPoll,
		GeoAnomalyInfraRefreshMinutes: s.GeoAnomalyInfraRefreshMinutes,
		GeoAnomalyInfraHostTTLMinutes: s.GeoAnomalyInfraHostTTLMinutes,

		RiskHWIDCaptureOff:    s.RiskHWIDCaptureOff,
		RiskSubSpreadOff:      s.RiskSubSpreadOff,
		RiskDevicesOff:        s.RiskDevicesOff,
		RiskUsageShiftOff:     s.RiskUsageShiftOff,
		RiskLoginCountryOff:   s.RiskLoginCountryOff,
		RiskMinDays:           s.RiskMinDays,
		RiskMaxDevices:        s.RiskMaxDevices,
		RiskUsageRatio:        s.RiskUsageRatio,
		RiskUsageFloorGB:      s.RiskUsageFloorGB,
		RiskLoginWarmupLogins: s.RiskLoginWarmupLogins,
		RiskLoginHoldDays:     s.RiskLoginHoldDays,
		RiskUsageWarmupDays:   s.RiskUsageWarmupDays,
		RiskUsageFlagDays:     s.RiskUsageFlagDays,
		RiskUsageSuspectDays:  s.RiskUsageSuspectDays,

		RiskRefreshIntervalMinutes:     s.RiskRefreshIntervalMinutes,
		RiskFirstDelayMinutes:          s.RiskFirstDelayMinutes,
		RiskAlertFreshnessHours:        s.RiskAlertFreshnessHours,
		RiskWindowDays:                 s.RiskWindowDays,
		RiskLoginLookbackDays:          s.RiskLoginLookbackDays,
		RiskUsageBaselineDays:          s.RiskUsageBaselineDays,
		RiskUsageRecentDays:            s.RiskUsageRecentDays,
		RiskConnectionRetentionDays:    s.RiskConnectionRetentionDays,
		RiskFlagRecordRetentionDays:    s.RiskFlagRecordRetentionDays,
		RiskLiveSnapshotStaleMinutes:   s.RiskLiveSnapshotStaleMinutes,
		RiskLiveRefreshCooldownSeconds: s.RiskLiveRefreshCooldownSeconds,
		RiskDeviceInferHours:           s.RiskDeviceInferHours,
	}
}

// SetRiskCenterPolicy copies the policy into the settings and touches no
// other field: the policy endpoint saves the whole settings record (the
// store has no partial save), so this is what keeps its save from moving a
// setting it does not own.
func (s *UISettings) SetRiskCenterPolicy(p RiskCenterPolicy) {
	s.GeoAnomalyScope = p.GeoAnomalyScope
	s.GeoAnomalyMaxPlaces = p.GeoAnomalyMaxPlaces
	s.GeoAnomalyMaxRegions = p.GeoAnomalyMaxRegions
	s.GeoAnomalyMaxCities = p.GeoAnomalyMaxCities
	s.GeoAnomalyFlagAfterPolls = p.GeoAnomalyFlagAfterPolls
	s.GeoAnomalyClearAfterPolls = p.GeoAnomalyClearAfterPolls
	s.GeoAnomalyMinPlacedRatio = p.GeoAnomalyMinPlacedRatio
	s.GeoAnomalyCoTravel = p.GeoAnomalyCoTravel
	s.GeoAnomalyAllowAnywhere = p.GeoAnomalyAllowAnywhere
	s.GeoAnomalyIgnoreAddresses = p.GeoAnomalyIgnoreAddresses

	s.GeoAnomalyBanEnabled = p.GeoAnomalyBanEnabled
	s.GeoAnomalyBanMaxCountries = p.GeoAnomalyBanMaxCountries
	s.GeoAnomalyBanMaxRegions = p.GeoAnomalyBanMaxRegions
	s.GeoAnomalyBanMaxCities = p.GeoAnomalyBanMaxCities
	s.GeoAnomalyBanAfterPolls = p.GeoAnomalyBanAfterPolls
	s.GeoAnomalyBanDurationMinutes = p.GeoAnomalyBanDurationMinutes

	s.GeoAnomalyFreshWindowSeconds = p.GeoAnomalyFreshWindowSeconds
	s.GeoAnomalySharedExitMinUsers = p.GeoAnomalySharedExitMinUsers
	s.GeoAnomalyBanMaxPerPoll = p.GeoAnomalyBanMaxPerPoll
	s.GeoAnomalyLiftMaxPerPoll = p.GeoAnomalyLiftMaxPerPoll
	s.GeoAnomalyInfraRefreshMinutes = p.GeoAnomalyInfraRefreshMinutes
	s.GeoAnomalyInfraHostTTLMinutes = p.GeoAnomalyInfraHostTTLMinutes

	s.RiskHWIDCaptureOff = p.RiskHWIDCaptureOff
	s.RiskSubSpreadOff = p.RiskSubSpreadOff
	s.RiskDevicesOff = p.RiskDevicesOff
	s.RiskUsageShiftOff = p.RiskUsageShiftOff
	s.RiskLoginCountryOff = p.RiskLoginCountryOff
	s.RiskMinDays = p.RiskMinDays
	s.RiskMaxDevices = p.RiskMaxDevices
	s.RiskUsageRatio = p.RiskUsageRatio
	s.RiskUsageFloorGB = p.RiskUsageFloorGB
	s.RiskLoginWarmupLogins = p.RiskLoginWarmupLogins
	s.RiskLoginHoldDays = p.RiskLoginHoldDays
	s.RiskUsageWarmupDays = p.RiskUsageWarmupDays
	s.RiskUsageFlagDays = p.RiskUsageFlagDays
	s.RiskUsageSuspectDays = p.RiskUsageSuspectDays

	s.RiskRefreshIntervalMinutes = p.RiskRefreshIntervalMinutes
	s.RiskFirstDelayMinutes = p.RiskFirstDelayMinutes
	s.RiskAlertFreshnessHours = p.RiskAlertFreshnessHours
	s.RiskWindowDays = p.RiskWindowDays
	s.RiskLoginLookbackDays = p.RiskLoginLookbackDays
	s.RiskUsageBaselineDays = p.RiskUsageBaselineDays
	s.RiskUsageRecentDays = p.RiskUsageRecentDays
	s.RiskConnectionRetentionDays = p.RiskConnectionRetentionDays
	s.RiskFlagRecordRetentionDays = p.RiskFlagRecordRetentionDays
	s.RiskLiveSnapshotStaleMinutes = p.RiskLiveSnapshotStaleMinutes
	s.RiskLiveRefreshCooldownSeconds = p.RiskLiveRefreshCooldownSeconds
	s.RiskDeviceInferHours = p.RiskDeviceInferHours
}

// riskCenterPolicyKeys is read off the struct once. Reflection here, unlike
// the copies, because the list must BE the struct's tags in the struct's
// order — it is what the policy page's own key list has to match — and a
// hand-written copy would be one more thing to keep in step.
var riskCenterPolicyKeys = func() []string {
	t := reflect.TypeOf(RiskCenterPolicy{})
	keys := make([]string, 0, t.NumField())
	for i := 0; i < t.NumField(); i++ {
		keys = append(keys, strings.Split(t.Field(i).Tag.Get("json"), ",")[0])
	}
	return keys
}()

// RiskCenterPolicyKeys lists the policy's 48 json tags in struct order. The
// slice is the caller's own.
func RiskCenterPolicyKeys() []string { return slices.Clone(riskCenterPolicyKeys) }

// RiskCenterPolicyDefaults is the shipped value of every NUMERIC policy key
// (38), keyed by json tag, in the unit the setting is typed in — the number
// an unset (0) field falls back to. The policy page shows it as the empty
// field's placeholder and computes its presets and "in effect" lines from
// it, so the SPA holds no copy of any default: a copy is a second definition
// of each rule, and the first thing to drift.
//
// Every value comes from the domain value its reader falls back to: the geo
// policy's (DefaultGeoPolicy), the risk policy's four thresholds
// (DefaultRiskPolicy — the floor stated in GiB, as it is typed), and the 23
// runtime knobs exactly as RuntimeEffective states their defaults. The texts
// and switches have no default to show: an empty text means none, and a
// switch's zero value is its default by construction.
func RiskCenterPolicyDefaults() map[string]float64 {
	geo := domain.DefaultGeoPolicy()
	risk := domain.DefaultRiskPolicy()
	out := map[string]float64{
		"geo_anomaly_max_places":           float64(geo.MaxPlaces),
		"geo_anomaly_max_regions":          float64(geo.MaxRegions),
		"geo_anomaly_max_cities":           float64(geo.MaxCities),
		"geo_anomaly_flag_after_polls":     float64(geo.FlagAfterPolls),
		"geo_anomaly_clear_after_polls":    float64(geo.ClearAfterPolls),
		"geo_anomaly_min_placed_ratio":     geo.MinPlacedRatio,
		"geo_anomaly_ban_max_countries":    float64(geo.BanMaxCountries),
		"geo_anomaly_ban_max_regions":      float64(geo.BanMaxRegions),
		"geo_anomaly_ban_max_cities":       float64(geo.BanMaxCities),
		"geo_anomaly_ban_after_polls":      float64(geo.BanAfterPolls),
		"geo_anomaly_ban_duration_minutes": float64(geo.BanDurationMinutes),

		"risk_min_days":       float64(risk.MinDays),
		"risk_max_devices":    float64(risk.MaxDevices),
		"risk_usage_ratio":    risk.UsageRatio,
		"risk_usage_floor_gb": float64(risk.UsageFloorBytes / domain.RiskGiB),
	}
	_, runtime := RuntimeEffective(UISettings{})
	for key, v := range runtime {
		out[key] = float64(v)
	}
	return out
}
