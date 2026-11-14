package ports

import (
	"fmt"
	"reflect"
	"slices"
	"strings"
	"time"
)

// AccessControlSettings is the access-control page's settings: EVERY dest_*
// field of UISettings, with the same Go names, types and json tags. Each field
// means exactly what its UISettings namesake documents.
//
// The /api/admin/dest/settings endpoint owns these keys, the same arrangement
// as the risk center's policy (RiskCenterPolicy) and for the same reason: the
// system settings page loads the whole record and a tab opened before another
// admin's save would write it back, so that page keeps these keys exactly as
// stored (TestSettingsPut_PreservesEveryDestKey) and the access-control page is
// their one writer. The set is held to the settings by
// TestAccessControlSettings_IsExactlyTheDestFields, which takes it by prefix.
type AccessControlSettings struct {
	DestPolicyApplyMinSeconds int    `json:"dest_policy_apply_min_seconds"`
	DestListRefreshHours      int    `json:"dest_list_refresh_hours"`
	DestRulesUpdateMode       string `json:"dest_rules_update_mode"`
	DestHitRetentionDays      int    `json:"dest_hit_retention_days"`
	DestTrialRetentionDays    int    `json:"dest_trial_retention_days"`
}

// AccessControlSettings copies the settings out of the record. Hand-written,
// like RiskCenterPolicy: a plain assignment list is greppable and stops
// compiling when a field is renamed.
func (s UISettings) AccessControlSettings() AccessControlSettings {
	return AccessControlSettings{
		DestPolicyApplyMinSeconds: s.DestPolicyApplyMinSeconds,
		DestListRefreshHours:      s.DestListRefreshHours,
		DestRulesUpdateMode:       s.DestRulesUpdateMode,
		DestHitRetentionDays:      s.DestHitRetentionDays,
		DestTrialRetentionDays:    s.DestTrialRetentionDays,
	}
}

// SetAccessControlSettings copies the settings into the record and touches no
// other field: the settings store saves whole records only.
func (s *UISettings) SetAccessControlSettings(p AccessControlSettings) {
	s.DestPolicyApplyMinSeconds = p.DestPolicyApplyMinSeconds
	s.DestListRefreshHours = p.DestListRefreshHours
	s.DestRulesUpdateMode = p.DestRulesUpdateMode
	s.DestHitRetentionDays = p.DestHitRetentionDays
	s.DestTrialRetentionDays = p.DestTrialRetentionDays
}

var accessControlSettingsKeys = func() []string {
	t := reflect.TypeOf(AccessControlSettings{})
	keys := make([]string, 0, t.NumField())
	for i := 0; i < t.NumField(); i++ {
		keys = append(keys, strings.Split(t.Field(i).Tag.Get("json"), ",")[0])
	}
	return keys
}()

// AccessControlSettingsKeys lists the json tags in struct order. The slice is
// the caller's own.
func AccessControlSettingsKeys() []string { return slices.Clone(accessControlSettingsKeys) }

// The rules-update modes of dest_rules_update_mode.
const (
	// DestRulesUpdateManual shows that a newer geosite release exists and waits
	// for an admin to apply it. The default.
	DestRulesUpdateManual = "manual"
	// DestRulesUpdateAuto downloads, verifies and applies a newer release.
	DestRulesUpdateAuto = "auto"
)

// Shipped defaults and bounds of the numeric settings.
const (
	DestPolicyApplyMinSecondsDefault = 60
	DestPolicyApplyMinSecondsMin     = 30
	DestPolicyApplyMinSecondsMax     = 3600
	DestListRefreshHoursDefault      = 24
	DestListRefreshHoursMin          = 6
	DestListRefreshHoursMax          = 168
	DestHitRetentionDaysDefault      = 30
	DestHitRetentionDaysMin          = 1
	DestHitRetentionDaysMax          = 365
	DestTrialRetentionDaysDefault    = 7
	DestTrialRetentionDaysMin        = 1
	DestTrialRetentionDaysMax        = 30
)

// AccessControlEffective is what the panel runs with for the stored settings:
// 0 (never configured) is the default, anything out of range is clamped.
type AccessControlEffective struct {
	PolicyApplyMinSeconds int    `json:"dest_policy_apply_min_seconds"`
	ListRefreshHours      int    `json:"dest_list_refresh_hours"`
	RulesUpdateMode       string `json:"dest_rules_update_mode"`
	// HitRetentionDays is how long hit records are kept. TrialRetentionDays is
	// how long the allowlist trial rows are, and is never longer than it.
	HitRetentionDays   int `json:"dest_hit_retention_days"`
	TrialRetentionDays int `json:"dest_trial_retention_days"`
}

// PolicyApplyMin is the trailing debounce window of policy publication.
func (e AccessControlEffective) PolicyApplyMin() time.Duration {
	return time.Duration(e.PolicyApplyMinSeconds) * time.Second
}

// ListRefresh is the interval of the list refresh loop.
func (e AccessControlEffective) ListRefresh() time.Duration {
	return time.Duration(e.ListRefreshHours) * time.Hour
}

// HitRetention is how long hit records are kept.
func (e AccessControlEffective) HitRetention() time.Duration {
	return time.Duration(e.HitRetentionDays) * 24 * time.Hour
}

// TrialRetention is how long the allowlist trial rows are kept.
func (e AccessControlEffective) TrialRetention() time.Duration {
	return time.Duration(e.TrialRetentionDays) * 24 * time.Hour
}

// AccessControlEffectiveOf applies the defaults and clamps. A negative number
// is the default, not the minimum: it is not a value anybody meant.
func AccessControlEffectiveOf(s UISettings) AccessControlEffective {
	clamp := func(v, def, lo, hi int) int {
		switch {
		case v <= 0:
			return def
		case v < lo:
			return lo
		case v > hi:
			return hi
		}
		return v
	}
	mode := DestRulesUpdateManual
	if strings.EqualFold(strings.TrimSpace(s.DestRulesUpdateMode), DestRulesUpdateAuto) {
		mode = DestRulesUpdateAuto
	}
	hit := clamp(s.DestHitRetentionDays, DestHitRetentionDaysDefault, DestHitRetentionDaysMin, DestHitRetentionDaysMax)
	// The trial rows are shorter-lived than the hits, never longer: a retention
	// of a few days for hits must not leave the more sensitive-looking fold of a
	// whole group's browsing around for a week.
	trial := min(clamp(s.DestTrialRetentionDays, DestTrialRetentionDaysDefault, DestTrialRetentionDaysMin, DestTrialRetentionDaysMax), hit)
	return AccessControlEffective{
		PolicyApplyMinSeconds: clamp(s.DestPolicyApplyMinSeconds, DestPolicyApplyMinSecondsDefault, DestPolicyApplyMinSecondsMin, DestPolicyApplyMinSecondsMax),
		ListRefreshHours:      clamp(s.DestListRefreshHours, DestListRefreshHoursDefault, DestListRefreshHoursMin, DestListRefreshHoursMax),
		RulesUpdateMode:       mode,
		HitRetentionDays:      hit,
		TrialRetentionDays:    trial,
	}
}

// AccessControlSettingsDefaults is the shipped value of every numeric key, in
// the unit it is typed in, for the page's placeholders: the SPA holds no copy
// of a default.
func AccessControlSettingsDefaults() map[string]float64 {
	return map[string]float64{
		"dest_policy_apply_min_seconds": DestPolicyApplyMinSecondsDefault,
		"dest_list_refresh_hours":       DestListRefreshHoursDefault,
		"dest_hit_retention_days":       DestHitRetentionDaysDefault,
		"dest_trial_retention_days":     DestTrialRetentionDaysDefault,
	}
}

// ValidateAccessControlSettings checks a settings update. Unlike the risk
// policy, whose numbers are clamped where they are read, these are refused
// when typed out of range: a debounce of 5 seconds that silently runs as 30
// is a setting that lies. 0 and "" stay valid (the default). On refusal it
// names the json key at fault.
func ValidateAccessControlSettings(s AccessControlSettings) (field string, err error) {
	check := func(key string, v, lo, hi int) (string, error) {
		if v != 0 && (v < lo || v > hi) {
			return key, fmt.Errorf("%s must be 0 (the default) or between %d and %d", key, lo, hi)
		}
		return "", nil
	}
	if f, err := check("dest_policy_apply_min_seconds", s.DestPolicyApplyMinSeconds, DestPolicyApplyMinSecondsMin, DestPolicyApplyMinSecondsMax); err != nil {
		return f, err
	}
	if f, err := check("dest_list_refresh_hours", s.DestListRefreshHours, DestListRefreshHoursMin, DestListRefreshHoursMax); err != nil {
		return f, err
	}
	if f, err := check("dest_hit_retention_days", s.DestHitRetentionDays, DestHitRetentionDaysMin, DestHitRetentionDaysMax); err != nil {
		return f, err
	}
	if f, err := check("dest_trial_retention_days", s.DestTrialRetentionDays, DestTrialRetentionDaysMin, DestTrialRetentionDaysMax); err != nil {
		return f, err
	}
	switch s.DestRulesUpdateMode {
	case "", DestRulesUpdateManual, DestRulesUpdateAuto:
	default:
		return "dest_rules_update_mode", fmt.Errorf("dest_rules_update_mode must be %q or %q", DestRulesUpdateManual, DestRulesUpdateAuto)
	}
	return "", nil
}
