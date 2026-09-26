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
		RiskSubSpreadOff:    true,
		RiskDevicesOff:      true,
		RiskUsageShiftOff:   true,
		RiskLoginCountryOff: true,
		RiskMinDays:         4,
		RiskMaxDevices:      5,
		RiskUsageRatio:      2.5,
		RiskUsageFloorGB:    6,
	}
	want := domain.RiskPolicySettings{
		SubSpreadOff:    true,
		DevicesOff:      true,
		UsageShiftOff:   true,
		LoginCountryOff: true,
		MinDays:         4,
		MaxDevices:      5,
		UsageRatio:      2.5,
		UsageFloorGB:    6,
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

	overridable := 0
	ut := reflect.TypeOf(UISettings{})
	for i := 0; i < ut.NumField(); i++ {
		tag := strings.Split(ut.Field(i).Tag.Get("json"), ",")[0]
		if rest, ok := strings.CutPrefix(tag, "risk_"); ok && OverridableScopeKeys["risk."+rest] {
			overridable++
		}
	}
	if n := reflect.TypeOf(domain.RiskPolicySettings{}).NumField(); overridable != n {
		t.Errorf("%d risk.* keys are group-overridable but domain.RiskPolicySettings has %d fields: a per-group knob the policy does not carry is editable and judged with nothing", overridable, n)
	}
}
