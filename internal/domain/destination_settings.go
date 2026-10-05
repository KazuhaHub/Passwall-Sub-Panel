package domain

import "fmt"

type DestinationSettings struct {
	HitRetentionDays, TrialRetentionDays, UsageRetentionDays int
	ListRefreshHours, PolicyApplyMinSeconds                  int
}

func DefaultDestinationSettings() DestinationSettings {
	return DestinationSettings{HitRetentionDays: 30, TrialRetentionDays: 7, UsageRetentionDays: 7, ListRefreshHours: 24, PolicyApplyMinSeconds: 60}
}

func (s DestinationSettings) Validate() error {
	for _, field := range []struct {
		name            string
		value, min, max int
	}{
		{"dest_hit_retention_days", s.HitRetentionDays, 1, 365},
		{"dest_trial_retention_days", s.TrialRetentionDays, 1, 30},
		{"dest_usage_retention_days", s.UsageRetentionDays, 1, 30},
		{"dest_list_refresh_hours", s.ListRefreshHours, 6, 168},
		{"dest_policy_apply_min_seconds", s.PolicyApplyMinSeconds, 30, 3600},
	} {
		if field.value != 0 && (field.value < field.min || field.value > field.max) {
			return fmt.Errorf("%w: %s must be 0 (default) or %d..%d", ErrValidation, field.name, field.min, field.max)
		}
	}
	resolved := s.withDefaultsAndBounds()
	if resolved.TrialRetentionDays > resolved.HitRetentionDays {
		return fmt.Errorf("%w: dest_trial_retention_days must not exceed dest_hit_retention_days", ErrValidation)
	}
	return nil
}

func (s DestinationSettings) EffectiveTrialRetentionDays() int {
	return s.Effective().TrialRetentionDays
}

// Zero means the product default, never permanent retention. Bound values read
// from storage before converting them to durations or using them for cleanup.
func (s DestinationSettings) withDefaultsAndBounds() DestinationSettings {
	defaults := DefaultDestinationSettings()
	return DestinationSettings{
		HitRetentionDays:      settingOr(s.HitRetentionDays, defaults.HitRetentionDays, 1, 365),
		TrialRetentionDays:    settingOr(s.TrialRetentionDays, defaults.TrialRetentionDays, 1, 30),
		UsageRetentionDays:    settingOr(s.UsageRetentionDays, defaults.UsageRetentionDays, 1, 30),
		ListRefreshHours:      settingOr(s.ListRefreshHours, defaults.ListRefreshHours, 6, 168),
		PolicyApplyMinSeconds: settingOr(s.PolicyApplyMinSeconds, defaults.PolicyApplyMinSeconds, 30, 3600),
	}
}

func (s DestinationSettings) Effective() DestinationSettings {
	value := s.withDefaultsAndBounds()
	value.TrialRetentionDays = min(value.TrialRetentionDays, value.HitRetentionDays)
	return value
}
