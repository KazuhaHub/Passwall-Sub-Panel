package ports

import (
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// runtimeKnobTags cover the geo, risk and destination runtime controls.
// The geo and risk tunables used to be
// constants (or are new with the risk center): the six fleet-wide geo
// knobs, the twelve fleet-wide risk knobs and the five per-group risk
// thresholds. The settings page shows the value in effect for each of them,
// and nothing else is in the maps.
var runtimeKnobTags = []string{
	"dest_hit_retention_days",
	"dest_trial_retention_days",
	"dest_usage_retention_days",
	"dest_list_refresh_hours",
	"dest_policy_apply_min_seconds",
	"geo_anomaly_fresh_window_seconds",
	"geo_anomaly_shared_exit_min_users",
	"geo_anomaly_ban_max_per_poll",
	"geo_anomaly_lift_max_per_poll",
	"geo_anomaly_infra_refresh_minutes",
	"geo_anomaly_infra_host_ttl_minutes",
	"risk_refresh_interval_minutes",
	"risk_first_delay_minutes",
	"risk_alert_freshness_hours",
	"risk_window_days",
	"risk_usage_baseline_days",
	"risk_usage_recent_days",
	"risk_usage_warmup_days",
	"risk_usage_flag_days",
	"risk_usage_suspect_days",
	"risk_login_warmup_logins",
	"risk_login_hold_days",
	"risk_login_lookback_days",
	"risk_connection_retention_days",
	"risk_flag_record_retention_days",
	"risk_live_snapshot_stale_minutes",
	"risk_live_refresh_cooldown_seconds",
	"risk_device_infer_hours",
}

func sortedKeys(m map[string]int) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// The settings page shows, beside each runtime knob, the number the panel
// actually runs with, and uses the shipped default as the empty field's
// placeholder. Both come from here and nowhere else: the SPA carries no copy
// of any default or clamp (D18), so this is the one place a wrong number
// could come from, and the checks below hold it to the domain.
//
//   - the keys are exactly the 28 knobs, in both maps, so the page never
//     looks up a knob that is missing and never shows one it has no field for;
//   - every fleet-wide geo_anomaly_/risk_ integer setting is among them (a
//     knob added later without a value in effect fails here, whatever else
//     it forgot);
//   - the defaults are the domain's shipped values, and an install that
//     stored nothing runs with exactly those;
//   - stored values go through the domain's own clamps and floors.
func TestRuntimeEffective_CoversEveryRuntimeKey(t *testing.T) {
	eff, def := RuntimeEffective(UISettings{})
	want := slices.Sorted(slices.Values(runtimeKnobTags))
	if len(want) != 28 {
		t.Fatalf("the knob list has %d entries, want 28", len(want))
	}
	if got := sortedKeys(eff); !slices.Equal(got, want) {
		t.Errorf("runtime_effective keys = %v\nwant %v", got, want)
	}
	if got := sortedKeys(def); !slices.Equal(got, want) {
		t.Errorf("runtime_defaults keys = %v\nwant %v", got, want)
	}

	ut := reflect.TypeOf(UISettings{})
	for i := 0; i < ut.NumField(); i++ {
		f := ut.Field(i)
		tag := strings.Split(f.Tag.Get("json"), ",")[0]
		var key string
		switch {
		case strings.HasPrefix(tag, "dest_"):
			key = "dest." + strings.TrimPrefix(tag, "dest_")
		case strings.HasPrefix(tag, "geo_anomaly_"):
			key = "geo_anomaly." + strings.TrimPrefix(tag, "geo_anomaly_")
		case strings.HasPrefix(tag, "risk_"):
			key = "risk." + strings.TrimPrefix(tag, "risk_")
		default:
			continue
		}
		// Integers only: the ignore list and the capture switch are not
		// knobs with a number in effect. Group-overridable ones are the
		// judging policy, most of which predates these maps; the five
		// that joined it are in the list above.
		if f.Type.Kind() != reflect.Int || OverridableScopeKeys[key] {
			continue
		}
		if _, ok := def[tag]; !ok {
			t.Errorf("%s is a fleet-wide integer knob with no value in effect: the settings page cannot say what it runs with", tag)
		}
	}

	shipped := map[string]int{
		"dest_hit_retention_days":            30,
		"dest_trial_retention_days":          7,
		"dest_usage_retention_days":          7,
		"dest_list_refresh_hours":            24,
		"dest_policy_apply_min_seconds":      60,
		"geo_anomaly_fresh_window_seconds":   domain.LiveIPFreshWindowSeconds,
		"geo_anomaly_shared_exit_min_users":  domain.SharedExitMinUsers,
		"geo_anomaly_ban_max_per_poll":       domain.GeoPerPollDefault,
		"geo_anomaly_lift_max_per_poll":      domain.GeoPerPollDefault,
		"geo_anomaly_infra_refresh_minutes":  domain.InfraRefreshDefaultMinutes,
		"geo_anomaly_infra_host_ttl_minutes": domain.InfraHostTTLDefaultMinutes,
		"risk_refresh_interval_minutes":      domain.RiskDefaultRefreshMinutes,
		"risk_first_delay_minutes":           domain.RiskDefaultFirstDelayMinutes,
		"risk_alert_freshness_hours":         domain.RiskDefaultAlertFreshnessHours,
		"risk_window_days":                   domain.RiskDefaultWindowDays,
		"risk_usage_baseline_days":           domain.RiskUsageBaselineDays,
		"risk_usage_recent_days":             domain.RiskUsageRecentDays,
		"risk_usage_warmup_days":             domain.RiskUsageWarmupDays,
		"risk_usage_flag_days":               domain.RiskUsageFlagDays,
		"risk_usage_suspect_days":            domain.RiskUsageSuspectDays,
		"risk_login_warmup_logins":           domain.RiskLoginWarmupLogins,
		"risk_login_hold_days":               domain.RiskLoginHoldDays,
		"risk_login_lookback_days":           domain.RiskLoginLookbackDays,
		"risk_connection_retention_days":     domain.RiskDefaultConnectionRetentionDays,
		"risk_flag_record_retention_days":    domain.RiskDefaultFlagRecordRetentionDays,
		"risk_live_snapshot_stale_minutes":   domain.RiskDefaultLiveSnapshotStaleMinutes,
		"risk_live_refresh_cooldown_seconds": domain.RiskDefaultLiveRefreshCooldownSeconds,
		"risk_device_infer_hours":            domain.RiskDefaultDeviceInferHours,
	}
	if !reflect.DeepEqual(def, shipped) {
		t.Errorf("runtime_defaults = %v\nwant the domain's shipped values %v", def, shipped)
	}
	// Nothing stored is the shipped behaviour, the upgrade promise (I8).
	if !reflect.DeepEqual(eff, shipped) {
		t.Errorf("runtime_effective of an install that stored nothing = %v\nwant the shipped values %v", eff, shipped)
	}
	// The defaults do not depend on what is stored: they are what an
	// emptied field falls back to, the placeholder the page shows.
	if _, def2 := RuntimeEffective(UISettings{RiskUsageFlagDays: 3, GeoAnomalyFreshWindowSeconds: 300, CronTrafficPullMinutes: 600}); !reflect.DeepEqual(def2, shipped) {
		t.Errorf("runtime_defaults moved with the stored settings: %v", def2)
	}

	set := UISettings{
		GeoAnomalyFreshWindowSeconds:   5,     // below 20
		GeoAnomalySharedExitMinUsers:   99,    // above 20
		GeoAnomalyBanMaxPerPoll:        1000,  // above 200
		GeoAnomalyLiftMaxPerPoll:       7,     // as stored
		GeoAnomalyInfraRefreshMinutes:  999,   // above 60
		GeoAnomalyInfraHostTTLMinutes:  99999, // above 1440
		RiskRefreshIntervalMinutes:     1440,
		RiskFirstDelayMinutes:          999,  // above 60
		RiskAlertFreshnessHours:        1,    // under two refreshes
		RiskWindowDays:                 9,    // above the uint8 ceiling of 7
		RiskUsageBaselineDays:          3,    // below 14
		RiskUsageRecentDays:            99,   // above 14
		RiskUsageWarmupDays:            3,    // below 7
		RiskUsageFlagDays:              1,    // below 2: one day is a download
		RiskUsageSuspectDays:           -1,   // unset
		RiskLoginWarmupLogins:          60,   // above 50
		RiskLoginHoldDays:              30,   // as stored
		RiskLoginLookbackDays:          3,    // below 7
		RiskConnectionRetentionDays:    500,  // above the privacy bound of 90
		RiskFlagRecordRetentionDays:    9999, // above 3650
		RiskLiveSnapshotStaleMinutes:   1,    // under two polls
		RiskLiveRefreshCooldownSeconds: 1,    // below 5
		RiskDeviceInferHours:           999,  // above 168
		CronTrafficPullMinutes:         5,
	}
	eff, _ = RuntimeEffective(set)
	for tag, want := range map[string]int{
		"geo_anomaly_fresh_window_seconds":   domain.LiveIPFreshWindowMinSeconds,
		"geo_anomaly_shared_exit_min_users":  domain.SharedExitMinUsersMax,
		"geo_anomaly_ban_max_per_poll":       domain.GeoPerPollMax,
		"geo_anomaly_lift_max_per_poll":      7,
		"geo_anomaly_infra_refresh_minutes":  domain.InfraRefreshMaxMinutes,
		"geo_anomaly_infra_host_ttl_minutes": domain.InfraHostTTLMaxMinutes,
		"risk_refresh_interval_minutes":      1440,
		"risk_first_delay_minutes":           domain.RiskFirstDelayMaxMinutes,
		// Raised to two refresh intervals: 2 × 1440 min.
		"risk_alert_freshness_hours": 48,
		"risk_window_days":           domain.RiskWindowDays,
		"risk_usage_baseline_days":   domain.RiskUsageBaselineMinDays,
		"risk_usage_recent_days":     domain.RiskUsageRecentMaxDays,
		// warm-up 3 is raised to its floor of 7, inside the 14-day baseline.
		"risk_usage_warmup_days":   domain.RiskUsageWarmupMinDays,
		"risk_usage_flag_days":     domain.RiskUsageOverDaysMin,
		"risk_usage_suspect_days":  domain.RiskUsageOverDaysMin,
		"risk_login_warmup_logins": domain.RiskLoginWarmupMaxLogins,
		// The hold of 30 is held to the lookback, which is raised to 7.
		"risk_login_hold_days":               domain.RiskLoginLookbackMinDays,
		"risk_login_lookback_days":           domain.RiskLoginLookbackMinDays,
		"risk_connection_retention_days":     domain.RiskConnectionRetentionMaxDays,
		"risk_flag_record_retention_days":    domain.RiskFlagRecordRetentionMaxDays,
		"risk_live_snapshot_stale_minutes":   10, // two five-minute polls
		"risk_live_refresh_cooldown_seconds": domain.RiskLiveRefreshCooldownMinSeconds,
		"risk_device_infer_hours":            domain.RiskDeviceInferMaxHours,
	} {
		if got := eff[tag]; got != want {
			t.Errorf("stored %s: in effect %d, want %d", tag, got, want)
		}
	}
}

// The five per-group thresholds are shown for the GLOBAL policy as the
// worker judges with it: after RiskPolicy.Bounded against the configured
// fleet runtime, so a flag threshold above the judged days reads as the
// judged days — what the worker uses — and never as the stored number.
func TestRuntimeEffective_GroupKnobsAreBoundedByTheConfiguredRuntime(t *testing.T) {
	eff, _ := RuntimeEffective(UISettings{
		RiskUsageBaselineDays: 14,
		RiskUsageWarmupDays:   30,
		RiskUsageRecentDays:   3,
		RiskUsageFlagDays:     10,
		RiskUsageSuspectDays:  5,
		RiskLoginLookbackDays: 14,
		RiskLoginHoldDays:     30,
	})
	for tag, want := range map[string]int{
		"risk_usage_warmup_days":  14,
		"risk_usage_flag_days":    3,
		"risk_usage_suspect_days": 3,
		"risk_login_hold_days":    14,
	} {
		if got := eff[tag]; got != want {
			t.Errorf("%s in effect = %d, want %d", tag, got, want)
		}
	}
}

// The staleness warning is floored at two traffic polls where it is read
// (RiskRuntime.SnapshotStaleAfter), and the number shown must be that
// floored one: at a 30-minute poll the view warns after an hour, whatever
// the field says. An unset poll interval is the shipped five minutes, as
// the settings repository resolves it on load.
func TestRuntimeEffective_StalenessCoversTwoPolls(t *testing.T) {
	for _, c := range []struct {
		poll, stale, want int
	}{
		{poll: 30, stale: 15, want: 60},
		{poll: 30, stale: 90, want: 90},
		{poll: 0, stale: 0, want: 15},
		{poll: 0, stale: 8, want: 10},
	} {
		eff, _ := RuntimeEffective(UISettings{CronTrafficPullMinutes: c.poll, RiskLiveSnapshotStaleMinutes: c.stale})
		if got := eff["risk_live_snapshot_stale_minutes"]; got != c.want {
			t.Errorf("poll %d min, stale %d min: in effect %d, want %d", c.poll, c.stale, got, c.want)
		}
	}
}

// The bell freshness shown "in effect" is the window the bell applies, not
// a rounding of it: the risk entry's window is exactly
// RiskRuntimeFromSettings(...).AlertFreshness (alert.bellFreshness), and the
// page shows the number times an hour. Two refreshes need not be whole hours
// (a 100-minute cadence gives 3h20m); shown rounded up as 4, the page
// promised a flag still on the bell at 3h30m that was already off it, and
// rounded down as 3 it would call a flag at 3h10m gone that is still lit.
// Every cadence and a spread of stored freshnesses, so no refresh interval
// can bring the two apart again.
func TestRuntimeEffective_BellFreshnessIsTheWindowTheBellKeeps(t *testing.T) {
	for refresh := domain.RiskRefreshMinMinutes; refresh <= domain.RiskRefreshMaxMinutes; refresh++ {
		for _, fresh := range []int{0, 1, 3, 24, domain.RiskAlertFreshnessMaxHours} {
			set := UISettings{RiskRefreshIntervalMinutes: refresh, RiskAlertFreshnessHours: fresh}
			eff, _ := RuntimeEffective(set)
			shown := time.Duration(eff["risk_alert_freshness_hours"]) * time.Hour
			if kept := domain.RiskRuntimeFromSettings(set.RiskRuntimeSettings()).AlertFreshness; shown != kept {
				t.Fatalf("refresh %d min, freshness %d h: in effect %v, but the bell keeps a flag %v", refresh, fresh, shown, kept)
			}
		}
	}
	// The case the rounding got wrong, by name: two 100-minute refreshes
	// completed to four whole hours, on the page and on the bell alike.
	if eff, _ := RuntimeEffective(UISettings{RiskRefreshIntervalMinutes: 100, RiskAlertFreshnessHours: 1}); eff["risk_alert_freshness_hours"] != 4 {
		t.Errorf("refresh 100 min, freshness 1 h: in effect %d h, want 4", eff["risk_alert_freshness_hours"])
	}
}
