package ports

import (
	"reflect"
	"strings"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// RiskPolicySettings is the ONE place the stored risk.* values become the
// domain's flat form. A knob it forgets is the worst kind of bug: the admin
// form and the group editor both show it, saves succeed, and the worker
// judges with the default forever.
//
// Four checks, each catching what the others cannot:
//   - every knob is set to a distinct non-zero value and the exact result is
//     compared, so two crossed numeric fields fail;
//   - each switch is set alone, so two crossed switches fail (four "true"s
//     cannot tell them apart);
//   - every field of the result is non-zero under reflection, so a field
//     added to domain.RiskPolicySettings later without a mapping fails;
//   - the number of group-overridable risk.* keys equals the number of result
//     fields, so a new per-group knob that never reaches the policy fails.
func TestUISettings_RiskPolicySettingsCarriesEveryRiskKnob(t *testing.T) {
	s := UISettings{
		RiskSubSpreadOff:      true,
		RiskDevicesOff:        true,
		RiskUsageShiftOff:     true,
		RiskLoginCountryOff:   true,
		RiskMinDays:           4,
		RiskMaxDevices:        5,
		RiskUsageRatio:        2.5,
		RiskUsageFloorGB:      6,
		RiskLoginWarmupLogins: 7,
		RiskLoginHoldDays:     8,
		RiskUsageWarmupDays:   9,
		RiskUsageFlagDays:     10,
		RiskUsageSuspectDays:  11,
	}
	want := domain.RiskPolicySettings{
		SubSpreadOff:      true,
		DevicesOff:        true,
		UsageShiftOff:     true,
		LoginCountryOff:   true,
		MinDays:           4,
		MaxDevices:        5,
		UsageRatio:        2.5,
		UsageFloorGB:      6,
		LoginWarmupLogins: 7,
		LoginHoldDays:     8,
		UsageWarmupDays:   9,
		UsageFlagDays:     10,
		UsageSuspectDays:  11,
	}
	got := s.RiskPolicySettings()
	if got != want {
		t.Fatalf("RiskPolicySettings() = %+v\nwant %+v", got, want)
	}
	v := reflect.ValueOf(got)
	for i := 0; i < v.NumField(); i++ {
		if v.Field(i).IsZero() {
			t.Errorf("domain.RiskPolicySettings.%s is never filled from UISettings", v.Type().Field(i).Name)
		}
	}

	for _, c := range []struct {
		name string
		in   UISettings
		want domain.RiskPolicySettings
	}{
		{"sub_spread", UISettings{RiskSubSpreadOff: true}, domain.RiskPolicySettings{SubSpreadOff: true}},
		{"devices", UISettings{RiskDevicesOff: true}, domain.RiskPolicySettings{DevicesOff: true}},
		{"usage_shift", UISettings{RiskUsageShiftOff: true}, domain.RiskPolicySettings{UsageShiftOff: true}},
		{"login_country", UISettings{RiskLoginCountryOff: true}, domain.RiskPolicySettings{LoginCountryOff: true}},
	} {
		if got := c.in.RiskPolicySettings(); got != c.want {
			t.Errorf("%s switch alone = %+v, want %+v", c.name, got, c.want)
		}
	}

	// Split by scope, as the geo count is: the per-group risk_ settings are
	// the judging policy, the global ones (less the capture switch, which
	// /sub reads and no evaluator judges with) the worker's fleet-wide
	// runtime. Each side must count exactly its mapping's fields, so a
	// stored knob that reaches neither fails here whichever side it was
	// meant for.
	overridable, global := 0, 0
	ut := reflect.TypeOf(UISettings{})
	for i := 0; i < ut.NumField(); i++ {
		tag := strings.Split(ut.Field(i).Tag.Get("json"), ",")[0]
		rest, ok := strings.CutPrefix(tag, "risk_")
		if !ok || tag == "risk_hwid_capture_off" {
			continue
		}
		if OverridableScopeKeys["risk."+rest] {
			overridable++
		} else {
			global++
		}
	}
	if n := reflect.TypeOf(domain.RiskPolicySettings{}).NumField(); overridable != n {
		t.Errorf("%d risk.* keys are group-overridable but domain.RiskPolicySettings has %d fields: a per-group knob the policy does not carry is editable and judged with nothing", overridable, n)
	}
	if n := reflect.TypeOf(domain.RiskRuntimeSettings{}).NumField(); global != n {
		t.Errorf("%d risk_ settings are global only (less hwid_capture_off) but domain.RiskRuntimeSettings has %d fields: a fleet-wide knob the runtime does not carry is saved and run with the default", global, n)
	}
}

// RiskRuntimeSettings is the ONE mapping from the fleet-wide risk.* knobs —
// the worker's and the bell's former constants — to the domain's flat form.
// The same checks as the policy mappings: exact distinct values (two crossed
// fields fail), and every result field non-zero (a domain field added
// without a mapping fails). The count against the stored settings is in the
// test above, split from the policy's.
func TestUISettings_RiskRuntimeSettingsCarriesEveryKnob(t *testing.T) {
	s := UISettings{
		RiskRefreshIntervalMinutes:  30,
		RiskFirstDelayMinutes:       4,
		RiskAlertFreshnessHours:     5,
		RiskWindowDays:              6,
		RiskLoginLookbackDays:       120,
		RiskUsageBaselineDays:       21,
		RiskUsageRecentDays:         5,
		RiskConnectionRetentionDays: 14,
		RiskFlagRecordRetentionDays: 400,

		RiskLiveSnapshotStaleMinutes:   45,
		RiskLiveRefreshCooldownSeconds: 90,
		RiskDeviceInferHours:           36,
	}
	want := domain.RiskRuntimeSettings{
		RefreshIntervalMinutes:  30,
		FirstDelayMinutes:       4,
		AlertFreshnessHours:     5,
		WindowDays:              6,
		LoginLookbackDays:       120,
		UsageBaselineDays:       21,
		UsageRecentDays:         5,
		ConnectionRetentionDays: 14,
		FlagRecordRetentionDays: 400,

		LiveSnapshotStaleMinutes:   45,
		LiveRefreshCooldownSeconds: 90,
		DeviceInferHours:           36,
	}
	got := s.RiskRuntimeSettings()
	if got != want {
		t.Fatalf("RiskRuntimeSettings() = %+v\nwant %+v", got, want)
	}
	v := reflect.ValueOf(got)
	for i := 0; i < v.NumField(); i++ {
		if v.Field(i).IsZero() {
			t.Errorf("domain.RiskRuntimeSettings.%s is never filled from UISettings", v.Type().Field(i).Name)
		}
	}
}

// The runtime carries what the admin CONFIGURED: the fetch window and the
// login lookback are never shortened by the sub-log or auth-event retention
// here. The retention is applied where the logs are read, because that is
// the one place that knows the rows before it are gone — and because a
// group's min_days is bounded by this window (RiskPolicy.Bounded), where a
// retention-shortened window would hide retention_short, the code that
// exists to say the logs are too short for min_days.
func TestRiskRuntime_CarriesConfiguredWindowNotRetention(t *testing.T) {
	for _, s := range []UISettings{
		{RiskWindowDays: 7, RiskLoginLookbackDays: 90, SubLogRetentionDays: 3, AuthEventRetentionDays: 14},
		{SubLogRetentionDays: 3, AuthEventRetentionDays: 14},
	} {
		rt := domain.RiskRuntimeFromSettings(s.RiskRuntimeSettings())
		if rt.WindowDays != 7 || rt.LoginLookbackDays != 90 {
			t.Fatalf("settings %+v: window %d, lookback %d; want the configured 7 and 90 whatever the retention", s.RiskRuntimeSettings(), rt.WindowDays, rt.LoginLookbackDays)
		}
	}
}

// GeoPolicySettings is the ONE mapping from the stored geo_anomaly.* values
// to the domain's flat form, shared by the traffic poll and the risk worker.
// Before it existed the poll carried a fifteen-field literal; a second
// hand-copied literal in the risk worker could drift from it, and a knob
// either one forgot would be saved by the form, shown by the group editor
// and judged with the default.
//
// The same four checks as the risk mapping: exact values (crossed numeric
// fields), each switch alone (crossed switches), every result field
// non-zero (a domain field added without a mapping), and the number of
// geo_anomaly_ settings — less the ignore list, which is global, decides
// which addresses are judged at all and is not part of the judging policy —
// equal to the number of result fields (a stored knob that never reaches
// the policy).
func TestUISettings_GeoPolicySettingsCarriesEveryGeoKnob(t *testing.T) {
	s := UISettings{
		GeoAnomalyScope:              "region",
		GeoAnomalyMaxPlaces:          2,
		GeoAnomalyMaxRegions:         3,
		GeoAnomalyMaxCities:          4,
		GeoAnomalyFlagAfterPolls:     5,
		GeoAnomalyClearAfterPolls:    6,
		GeoAnomalyMinPlacedRatio:     0.75,
		GeoAnomalyCoTravel:           "CN,HK",
		GeoAnomalyAllowAnywhere:      true,
		GeoAnomalyIgnoreAddresses:    "203.0.113.0/24",
		GeoAnomalyBanEnabled:         true,
		GeoAnomalyBanMaxCountries:    7,
		GeoAnomalyBanMaxRegions:      8,
		GeoAnomalyBanMaxCities:       9,
		GeoAnomalyBanAfterPolls:      10,
		GeoAnomalyBanDurationMinutes: 11,
	}
	want := domain.GeoPolicySettings{
		Scope:              "region",
		MaxPlaces:          2,
		MaxRegions:         3,
		MaxCities:          4,
		FlagAfterPolls:     5,
		ClearAfterPolls:    6,
		MinPlacedRatio:     0.75,
		CoTravel:           "CN,HK",
		AllowAnywhere:      true,
		BanEnabled:         true,
		BanMaxCountries:    7,
		BanMaxRegions:      8,
		BanMaxCities:       9,
		BanAfterPolls:      10,
		BanDurationMinutes: 11,
	}
	got := s.GeoPolicySettings()
	if got != want {
		t.Fatalf("GeoPolicySettings() = %+v\nwant %+v", got, want)
	}
	v := reflect.ValueOf(got)
	for i := 0; i < v.NumField(); i++ {
		if v.Field(i).IsZero() {
			t.Errorf("domain.GeoPolicySettings.%s is never filled from UISettings", v.Type().Field(i).Name)
		}
	}

	for _, c := range []struct {
		name string
		in   UISettings
		want domain.GeoPolicySettings
	}{
		{"allow_anywhere", UISettings{GeoAnomalyAllowAnywhere: true}, domain.GeoPolicySettings{AllowAnywhere: true}},
		{"ban_enabled", UISettings{GeoAnomalyBanEnabled: true}, domain.GeoPolicySettings{BanEnabled: true}},
	} {
		if got := c.in.GeoPolicySettings(); got != c.want {
			t.Errorf("%s switch alone = %+v, want %+v", c.name, got, c.want)
		}
	}

	// Split by scope, because the geo_anomaly_ settings now feed two
	// mappings: the per-group ones are the judging policy, the global ones
	// (less the ignore list) the fleet-wide detector runtime. Each side must
	// count exactly its mapping's fields, so a stored knob that reaches
	// neither fails here whichever side it was meant for.
	overridable, global := 0, 0
	ut := reflect.TypeOf(UISettings{})
	for i := 0; i < ut.NumField(); i++ {
		tag := strings.Split(ut.Field(i).Tag.Get("json"), ",")[0]
		rest, ok := strings.CutPrefix(tag, "geo_anomaly_")
		if !ok || tag == "geo_anomaly_ignore_addresses" {
			continue
		}
		if OverridableScopeKeys["geo_anomaly."+rest] {
			overridable++
		} else {
			global++
		}
	}
	if n := reflect.TypeOf(domain.GeoPolicySettings{}).NumField(); overridable != n {
		t.Errorf("%d geo_anomaly_ settings are group-overridable but domain.GeoPolicySettings has %d fields: a stored knob the policy does not carry is saved and judged with the default", overridable, n)
	}
	if n := reflect.TypeOf(domain.GeoRuntimeSettings{}).NumField(); global != n {
		t.Errorf("%d geo_anomaly_ settings are global only (less the ignore list) but domain.GeoRuntimeSettings has %d fields: a fleet-wide knob the runtime does not carry is saved and run with the default", global, n)
	}
}

// GeoRuntimeSettings is the ONE mapping from the fleet-wide geo_anomaly.*
// knobs — the detector's former constants — to the domain's flat form, read
// by the traffic poll, the infrastructure refresh and the risk worker. The
// same checks as the policy mappings: exact distinct values (two crossed
// fields fail), and every result field non-zero (a domain field added
// without a mapping fails). The count against the stored settings is in the
// test above, split from the policy's.
func TestUISettings_GeoRuntimeSettingsCarriesEveryKnob(t *testing.T) {
	s := UISettings{
		GeoAnomalyFreshWindowSeconds:  300,
		GeoAnomalySharedExitMinUsers:  4,
		GeoAnomalyBanMaxPerPoll:       5,
		GeoAnomalyLiftMaxPerPoll:      6,
		GeoAnomalyInfraRefreshMinutes: 7,
		GeoAnomalyInfraHostTTLMinutes: 8,
	}
	want := domain.GeoRuntimeSettings{
		FreshWindowSeconds:  300,
		SharedExitMinUsers:  4,
		BanMaxPerPoll:       5,
		LiftMaxPerPoll:      6,
		InfraRefreshMinutes: 7,
		InfraHostTTLMinutes: 8,
	}
	got := s.GeoRuntimeSettings()
	if got != want {
		t.Fatalf("GeoRuntimeSettings() = %+v\nwant %+v", got, want)
	}
	v := reflect.ValueOf(got)
	for i := 0; i < v.NumField(); i++ {
		if v.Field(i).IsZero() {
			t.Errorf("domain.GeoRuntimeSettings.%s is never filled from UISettings", v.Type().Field(i).Name)
		}
	}
}
