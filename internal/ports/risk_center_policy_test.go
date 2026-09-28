package ports

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// isPolicyTag is the rule the risk center's policy is defined by: every
// stored setting of the concurrent-location detector and of the risk
// signals, and nothing else.
func isPolicyTag(tag string) bool {
	return strings.HasPrefix(tag, "geo_anomaly_") || strings.HasPrefix(tag, "risk_")
}

func jsonTag(f reflect.StructField) string {
	return strings.Split(f.Tag.Get("json"), ",")[0]
}

// fillDistinct sets every settable string, bool, int and float field of the
// struct v points at to a value no other field of the same kind holds, so a
// copy that crosses two fields cannot pass for a copy that keeps them.
// Fields of other kinds are left alone; the policy has none.
func fillDistinct(v reflect.Value) {
	v = v.Elem()
	for i := 0; i < v.NumField(); i++ {
		f := v.Field(i)
		n := i + 1
		switch f.Kind() {
		case reflect.String:
			f.SetString("v" + strings.Repeat("x", n))
		case reflect.Bool:
			f.SetBool(true)
		case reflect.Int, reflect.Int64:
			f.SetInt(int64(100 + n))
		case reflect.Float64:
			f.SetFloat(float64(n) + 0.25)
		}
	}
}

// The policy page owns EVERY geo_anomaly_* and risk_* setting and nothing
// else. The set is defined by prefix, so a knob added to UISettings later
// either joins the policy or fails here: a detector knob the risk center
// does not carry would be editable nowhere once the system settings page
// hands the policy over, and a non-detector field in the policy would let
// the risk center's save move something that is not its own. Name, type and
// tag all match, so the hand-written copies can be plain assignments.
func TestRiskCenterPolicy_IsExactlyTheGeoAndRiskFields(t *testing.T) {
	type field struct {
		name, tag string
		typ       reflect.Type
	}
	ut := reflect.TypeOf(UISettings{})
	want := map[string]field{}
	for i := 0; i < ut.NumField(); i++ {
		f := ut.Field(i)
		if isPolicyTag(jsonTag(f)) {
			want[f.Name] = field{f.Name, jsonTag(f), f.Type}
		}
	}
	pt := reflect.TypeOf(RiskCenterPolicy{})
	got := map[string]field{}
	for i := 0; i < pt.NumField(); i++ {
		f := pt.Field(i)
		got[f.Name] = field{f.Name, jsonTag(f), f.Type}
	}
	if len(want) != 48 {
		t.Errorf("UISettings has %d geo_anomaly_/risk_ fields, the policy was specified at 48: update this count only together with the policy page", len(want))
	}
	for name, w := range want {
		g, ok := got[name]
		switch {
		case !ok:
			t.Errorf("UISettings.%s (%s) is a detector setting the risk center policy does not carry", name, w.tag)
		case g != w:
			t.Errorf("RiskCenterPolicy.%s = %s %q, UISettings has %s %q", name, g.typ, g.tag, w.typ, w.tag)
		}
	}
	for name, g := range got {
		if _, ok := want[name]; !ok {
			t.Errorf("RiskCenterPolicy.%s (%s) is not a geo_anomaly_/risk_ setting of UISettings", name, g.tag)
		}
	}
}

// The two copies are hand-written, so each is held to every field: out of
// a UISettings whose every field is distinct, the policy must read each
// field from its namesake; into one, the policy must write exactly its own
// fields — every other setting keeps its value, in both directions of
// "nothing else" (a filled policy into zero settings, a zero policy into
// filled settings).
func TestRiskCenterPolicy_CopiesEveryPolicyFieldAndNothingElse(t *testing.T) {
	var full UISettings
	fillDistinct(reflect.ValueOf(&full))

	pol := full.RiskCenterPolicy()
	pv, uv := reflect.ValueOf(pol), reflect.ValueOf(full)
	for i := 0; i < pv.NumField(); i++ {
		name := pv.Type().Field(i).Name
		if got, want := pv.Field(i).Interface(), uv.FieldByName(name).Interface(); got != want {
			t.Errorf("RiskCenterPolicy().%s = %v, want UISettings.%s = %v", name, got, name, want)
		}
	}

	var into UISettings
	into.SetRiskCenterPolicy(pol)
	iv := reflect.ValueOf(into)
	for i := 0; i < iv.NumField(); i++ {
		f := iv.Type().Field(i)
		switch {
		case isPolicyTag(jsonTag(f)):
			if got, want := iv.Field(i).Interface(), uv.Field(i).Interface(); got != want {
				t.Errorf("SetRiskCenterPolicy left %s = %v, want %v", f.Name, got, want)
			}
		case !iv.Field(i).IsZero():
			t.Errorf("SetRiskCenterPolicy wrote %s, which is not a policy setting", f.Name)
		}
	}

	// Every switch reads true above, so two crossed switches would pass;
	// each is therefore set alone, both ways.
	for i := 0; i < uv.NumField(); i++ {
		f := uv.Type().Field(i)
		if f.Type.Kind() != reflect.Bool || !isPolicyTag(jsonTag(f)) {
			continue
		}
		var one UISettings
		reflect.ValueOf(&one).Elem().Field(i).SetBool(true)
		var want RiskCenterPolicy
		reflect.ValueOf(&want).Elem().FieldByName(f.Name).SetBool(true)
		if got := one.RiskCenterPolicy(); got != want {
			t.Errorf("%s alone copies out as %+v", f.Name, got)
		}
		var back UISettings
		back.SetRiskCenterPolicy(want)
		if !reflect.DeepEqual(back, one) {
			t.Errorf("%s alone does not copy back in as itself alone", f.Name)
		}
	}

	kept := full
	kept.SetRiskCenterPolicy(RiskCenterPolicy{})
	kv := reflect.ValueOf(kept)
	for i := 0; i < kv.NumField(); i++ {
		f := kv.Type().Field(i)
		if isPolicyTag(jsonTag(f)) {
			if !kv.Field(i).IsZero() {
				t.Errorf("SetRiskCenterPolicy(zero) kept %s = %v", f.Name, kv.Field(i).Interface())
			}
		} else if !reflect.DeepEqual(kv.Field(i).Interface(), uv.Field(i).Interface()) {
			t.Errorf("SetRiskCenterPolicy changed %s, which is not a policy setting", f.Name)
		}
	}
}

// The key list is what the SPA's policy page is checked against, so it is
// the struct's own tags in the struct's own order, each once.
func TestRiskCenterPolicyKeys_AreTheTagsInOrder(t *testing.T) {
	pt := reflect.TypeOf(RiskCenterPolicy{})
	var want []string
	for i := 0; i < pt.NumField(); i++ {
		want = append(want, jsonTag(pt.Field(i)))
	}
	got := RiskCenterPolicyKeys()
	if !slices.Equal(got, want) {
		t.Fatalf("RiskCenterPolicyKeys() = %v\nwant %v", got, want)
	}
	if len(got) != 48 {
		t.Errorf("%d keys, want 48", len(got))
	}
	if len(slices.Compact(slices.Sorted(slices.Values(got)))) != len(got) {
		t.Errorf("a key is listed twice: %v", got)
	}
	// The slice is the caller's: changing it must not change the next call.
	got[0] = "tampered"
	if RiskCenterPolicyKeys()[0] == "tampered" {
		t.Error("RiskCenterPolicyKeys hands out its own backing array")
	}
}

// The policy page shows the shipped default in every empty numeric field
// and holds no copy of any: it reads them from here. So the keys are
// exactly the policy's 38 numbers (the three texts and seven switches have
// no "default" to show), and each value is the domain's own shipped value
// in the setting's unit — the geo policy's, the risk policy's (the floor in
// GiB, as it is typed) and the 23 runtime knobs' as RuntimeEffective states
// them.
func TestRiskCenterPolicyDefaults_CoverEveryNumericKeyExactly(t *testing.T) {
	got := RiskCenterPolicyDefaults()

	pt := reflect.TypeOf(RiskCenterPolicy{})
	var numeric []string
	for i := 0; i < pt.NumField(); i++ {
		switch pt.Field(i).Type.Kind() {
		case reflect.Int, reflect.Float64:
			numeric = append(numeric, jsonTag(pt.Field(i)))
		}
	}
	if len(numeric) != 38 {
		t.Errorf("the policy has %d numeric keys, want 38", len(numeric))
	}
	keys := make([]string, 0, len(got))
	for k := range got {
		keys = append(keys, k)
	}
	if slices.Sort(keys); !slices.Equal(keys, slices.Sorted(slices.Values(numeric))) {
		t.Fatalf("defaults keys = %v\nwant the numeric policy keys %v", keys, slices.Sorted(slices.Values(numeric)))
	}

	geo, risk := domain.DefaultGeoPolicy(), domain.DefaultRiskPolicy()
	want := map[string]float64{
		"geo_anomaly_max_places":           1,
		"geo_anomaly_max_regions":          1,
		"geo_anomaly_max_cities":           2,
		"geo_anomaly_flag_after_polls":     3,
		"geo_anomaly_clear_after_polls":    6,
		"geo_anomaly_min_placed_ratio":     0.5,
		"geo_anomaly_ban_max_countries":    1,
		"geo_anomaly_ban_max_regions":      2,
		"geo_anomaly_ban_max_cities":       3,
		"geo_anomaly_ban_after_polls":      6,
		"geo_anomaly_ban_duration_minutes": 60,
		"risk_min_days":                    3,
		"risk_max_devices":                 3,
		"risk_usage_ratio":                 3,
		"risk_usage_floor_gb":              3,
	}
	// The literals above are the shipped values the page was designed
	// around; they must also BE the domain's, or the page would show a
	// default the detectors do not use.
	fromDomain := map[string]float64{
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
		"risk_min_days":                    float64(risk.MinDays),
		"risk_max_devices":                 float64(risk.MaxDevices),
		"risk_usage_ratio":                 risk.UsageRatio,
		"risk_usage_floor_gb":              float64(risk.UsageFloorBytes / domain.RiskGiB),
	}
	for k, v := range fromDomain {
		if want[k] != v {
			t.Errorf("the domain ships %s = %v, the page was designed around %v", k, v, want[k])
		}
	}
	_, runtimeDefaults := RuntimeEffective(UISettings{})
	for k, v := range runtimeDefaults {
		want[k] = float64(v)
	}
	for k, w := range want {
		if g, ok := got[k]; !ok || g != w {
			t.Errorf("defaults[%s] = %v (present %v), want %v", k, g, ok, w)
		}
	}
}
