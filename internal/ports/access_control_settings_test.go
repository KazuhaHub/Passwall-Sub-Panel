package ports

import (
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

// The access-control page owns EVERY dest_* setting and nothing else, the same
// way the risk center's policy owns every geo_anomaly_* and risk_* one. The set
// is defined by prefix, so a knob added to UISettings later either joins the
// settings or fails here; a dest_ knob the page does not carry would be
// editable nowhere once the system settings page hands them over.
func isDestTag(tag string) bool { return strings.HasPrefix(tag, "dest_") }

func TestAccessControlSettings_IsExactlyTheDestFields(t *testing.T) {
	type field struct {
		name, tag string
		typ       reflect.Type
	}
	ut := reflect.TypeOf(UISettings{})
	want := map[string]field{}
	for i := 0; i < ut.NumField(); i++ {
		f := ut.Field(i)
		if isDestTag(jsonTag(f)) {
			want[f.Name] = field{f.Name, jsonTag(f), f.Type}
		}
	}
	pt := reflect.TypeOf(AccessControlSettings{})
	got := map[string]field{}
	for i := 0; i < pt.NumField(); i++ {
		f := pt.Field(i)
		got[f.Name] = field{f.Name, jsonTag(f), f.Type}
	}
	if len(want) != 5 {
		t.Errorf("UISettings has %d dest_ fields, specified 5 (three of batch 1, two retention keys): update this count only together with the page", len(want))
	}
	for name, w := range want {
		g, ok := got[name]
		switch {
		case !ok:
			t.Errorf("UISettings.%s (%s) is a dest setting the access-control settings do not carry", name, w.tag)
		case g != w:
			t.Errorf("AccessControlSettings.%s = %s %q, UISettings has %s %q", name, g.typ, g.tag, w.typ, w.tag)
		}
	}
	for name, g := range got {
		if _, ok := want[name]; !ok {
			t.Errorf("AccessControlSettings.%s (%s) is not a dest_ setting of UISettings", name, g.tag)
		}
	}
}

// The copies are hand-written, so each is held to every field, in both
// directions of "nothing else".
func TestAccessControlSettings_CopiesEveryFieldAndNothingElse(t *testing.T) {
	var full UISettings
	fillDistinct(reflect.ValueOf(&full))

	got := full.AccessControlSettings()
	gv, uv := reflect.ValueOf(got), reflect.ValueOf(full)
	for i := 0; i < gv.NumField(); i++ {
		name := gv.Type().Field(i).Name
		if g, w := gv.Field(i).Interface(), uv.FieldByName(name).Interface(); g != w {
			t.Errorf("AccessControlSettings().%s = %v, want UISettings.%s = %v", name, g, name, w)
		}
	}

	var into UISettings
	into.SetAccessControlSettings(got)
	iv := reflect.ValueOf(into)
	for i := 0; i < iv.NumField(); i++ {
		f := iv.Type().Field(i)
		switch {
		case isDestTag(jsonTag(f)):
			if g, w := iv.Field(i).Interface(), uv.Field(i).Interface(); g != w {
				t.Errorf("SetAccessControlSettings left %s = %v, want %v", f.Name, g, w)
			}
		case !iv.Field(i).IsZero():
			t.Errorf("SetAccessControlSettings wrote %s, which is not a dest setting", f.Name)
		}
	}

	kept := full
	kept.SetAccessControlSettings(AccessControlSettings{})
	kv := reflect.ValueOf(kept)
	for i := 0; i < kv.NumField(); i++ {
		f := kv.Type().Field(i)
		if isDestTag(jsonTag(f)) {
			if !kv.Field(i).IsZero() {
				t.Errorf("SetAccessControlSettings(zero) kept %s = %v", f.Name, kv.Field(i).Interface())
			}
		} else if !reflect.DeepEqual(kv.Field(i).Interface(), uv.Field(i).Interface()) {
			t.Errorf("SetAccessControlSettings changed %s, which is not a dest setting", f.Name)
		}
	}
}

func TestAccessControlSettingsKeys_AreTheTagsInOrder(t *testing.T) {
	pt := reflect.TypeOf(AccessControlSettings{})
	var want []string
	for i := 0; i < pt.NumField(); i++ {
		want = append(want, jsonTag(pt.Field(i)))
	}
	got := AccessControlSettingsKeys()
	if !slices.Equal(got, want) {
		t.Fatalf("AccessControlSettingsKeys() = %v\nwant %v", got, want)
	}
	got[0] = "tampered"
	if AccessControlSettingsKeys()[0] == "tampered" {
		t.Error("AccessControlSettingsKeys hands out its own backing array")
	}
}

// 0 and "" are "never configured": every reader falls to the shipped default,
// and anything out of range is clamped toward the SAFER end (a slower refresh,
// a longer debounce) rather than refused where it is read.
func TestAccessControlEffective_DefaultsAndClamps(t *testing.T) {
	d := AccessControlEffectiveOf(UISettings{})
	if d.PolicyApplyMinSeconds != 60 || d.ListRefreshHours != 24 || d.RulesUpdateMode != DestRulesUpdateManual ||
		d.HitRetentionDays != 30 || d.TrialRetentionDays != 7 {
		t.Fatalf("defaults = %+v, want 60 s / 24 h / manual / 30 d / 7 d", d)
	}
	if d.PolicyApplyMin() != time.Minute || d.ListRefresh() != 24*time.Hour {
		t.Fatalf("durations = %v / %v", d.PolicyApplyMin(), d.ListRefresh())
	}
	type in = UISettings
	for _, c := range []struct {
		name string
		in   UISettings
		want AccessControlEffective
	}{
		{"low", in{DestPolicyApplyMinSeconds: 5, DestListRefreshHours: 1}, AccessControlEffective{30, 6, DestRulesUpdateManual, 30, 7}},
		{"high", in{DestPolicyApplyMinSeconds: 99999, DestListRefreshHours: 9999}, AccessControlEffective{3600, 168, DestRulesUpdateManual, 30, 7}},
		{"negative is the default", in{DestPolicyApplyMinSeconds: -4, DestListRefreshHours: -1, DestHitRetentionDays: -3, DestTrialRetentionDays: -2}, AccessControlEffective{60, 24, DestRulesUpdateManual, 30, 7}},
		{"in range", in{DestPolicyApplyMinSeconds: 120, DestListRefreshHours: 12, DestRulesUpdateMode: "auto"}, AccessControlEffective{120, 12, DestRulesUpdateAuto, 30, 7}},
		{"unknown mode is manual", in{DestRulesUpdateMode: "yolo"}, AccessControlEffective{60, 24, DestRulesUpdateManual, 30, 7}},
		{"mode is case and space tolerant", in{DestRulesUpdateMode: " AUTO "}, AccessControlEffective{60, 24, DestRulesUpdateAuto, 30, 7}},
		// RETENTION. 0 is the default and NEVER "keep for ever": a browsing record
		// that cannot expire is the thing these keys exist to prevent.
		{"retention in range", in{DestHitRetentionDays: 90, DestTrialRetentionDays: 14}, AccessControlEffective{60, 24, DestRulesUpdateManual, 90, 14}},
		{"hit retention clamps to 1..365", in{DestHitRetentionDays: 9999}, AccessControlEffective{60, 24, DestRulesUpdateManual, 365, 7}},
		{"trial retention clamps to 1..30", in{DestHitRetentionDays: 365, DestTrialRetentionDays: 400}, AccessControlEffective{60, 24, DestRulesUpdateManual, 365, 30}},
		// The trial rows are shorter-lived than the hits, never longer.
		{"trial never outlives hits", in{DestHitRetentionDays: 3, DestTrialRetentionDays: 20}, AccessControlEffective{60, 24, DestRulesUpdateManual, 3, 3}},
		{"trial default is cut to a short hit retention", in{DestHitRetentionDays: 2}, AccessControlEffective{60, 24, DestRulesUpdateManual, 2, 2}},
	} {
		if got := AccessControlEffectiveOf(c.in); got != c.want {
			t.Errorf("%s: %+v, want %+v", c.name, got, c.want)
		}
	}
	if d.HitRetention() != 30*24*time.Hour || d.TrialRetention() != 7*24*time.Hour {
		t.Fatalf("retention durations = %v / %v", d.HitRetention(), d.TrialRetention())
	}
}

func TestValidateAccessControlSettings(t *testing.T) {
	ok := []AccessControlSettings{
		{},
		{DestPolicyApplyMinSeconds: 30, DestListRefreshHours: 6, DestRulesUpdateMode: "manual"},
		{DestPolicyApplyMinSeconds: 3600, DestListRefreshHours: 168, DestRulesUpdateMode: "auto"},
		{DestHitRetentionDays: 1, DestTrialRetentionDays: 1},
		{DestHitRetentionDays: 365, DestTrialRetentionDays: 30},
	}
	for _, s := range ok {
		if field, err := ValidateAccessControlSettings(s); err != nil {
			t.Errorf("%+v refused (%s): %v", s, field, err)
		}
	}
	bad := map[string]AccessControlSettings{
		"dest_policy_apply_min_seconds": {DestPolicyApplyMinSeconds: 29},
		"dest_list_refresh_hours":       {DestListRefreshHours: 169},
		"dest_rules_update_mode":        {DestRulesUpdateMode: "sometimes"},
		"dest_hit_retention_days":       {DestHitRetentionDays: 366},
		"dest_trial_retention_days":     {DestTrialRetentionDays: 31},
	}
	for want, s := range bad {
		field, err := ValidateAccessControlSettings(s)
		if err == nil || field != want {
			t.Errorf("%+v: field %q err %v, want %q refused", s, field, err, want)
		}
	}
	for _, s := range []AccessControlSettings{{DestPolicyApplyMinSeconds: -1}, {DestHitRetentionDays: -1}, {DestTrialRetentionDays: -1}} {
		if _, err := ValidateAccessControlSettings(s); err == nil {
			t.Errorf("%+v: a negative number was accepted", s)
		}
	}
}

func TestAccessControlSettingsDefaults_CarriesTheRetentionDefaults(t *testing.T) {
	d := AccessControlSettingsDefaults()
	if d["dest_hit_retention_days"] != 30 || d["dest_trial_retention_days"] != 7 {
		t.Fatalf("defaults = %v", d)
	}
}
