package ports

import "github.com/KazuhaHub/passwall-sub-panel/internal/domain"

// AccessControlSettings is exactly the dest_* portion of UISettings. The
// destination endpoint owns these fleet controls; zero selects the default.
type AccessControlSettings struct {
	DestHitRetentionDays      int `json:"dest_hit_retention_days"`
	DestTrialRetentionDays    int `json:"dest_trial_retention_days"`
	DestUsageRetentionDays    int `json:"dest_usage_retention_days"`
	DestListRefreshHours      int `json:"dest_list_refresh_hours"`
	DestPolicyApplyMinSeconds int `json:"dest_policy_apply_min_seconds"`
}

func (s UISettings) AccessControlSettings() AccessControlSettings {
	return AccessControlSettings{DestHitRetentionDays: s.DestHitRetentionDays, DestTrialRetentionDays: s.DestTrialRetentionDays, DestUsageRetentionDays: s.DestUsageRetentionDays, DestListRefreshHours: s.DestListRefreshHours, DestPolicyApplyMinSeconds: s.DestPolicyApplyMinSeconds}
}

func (s UISettings) DestinationSettings() domain.DestinationSettings {
	return domain.DestinationSettings{HitRetentionDays: s.DestHitRetentionDays, TrialRetentionDays: s.DestTrialRetentionDays, UsageRetentionDays: s.DestUsageRetentionDays, ListRefreshHours: s.DestListRefreshHours, PolicyApplyMinSeconds: s.DestPolicyApplyMinSeconds}
}

func (s *UISettings) SetDestinationSettings(value domain.DestinationSettings) {
	s.DestHitRetentionDays, s.DestTrialRetentionDays, s.DestUsageRetentionDays = value.HitRetentionDays, value.TrialRetentionDays, value.UsageRetentionDays
	s.DestListRefreshHours, s.DestPolicyApplyMinSeconds = value.ListRefreshHours, value.PolicyApplyMinSeconds
}
