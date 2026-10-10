package ports

import (
	"reflect"
	"strings"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

func TestAccessControlSettings_IsExactlyTheDestFields(t *testing.T) {
	type field struct {
		Name, Tag string
		Type      reflect.Type
	}
	fields := func(typ reflect.Type, destOnly bool) map[string]field {
		result := map[string]field{}
		for i := 0; i < typ.NumField(); i++ {
			f := typ.Field(i)
			tag := strings.Split(f.Tag.Get("json"), ",")[0]
			if !destOnly || strings.HasPrefix(tag, "dest_") {
				result[f.Name] = field{f.Name, tag, f.Type}
			}
		}
		return result
	}
	want, got := fields(reflect.TypeOf(UISettings{}), true), fields(reflect.TypeOf(AccessControlSettings{}), false)
	if len(want) != 5 || !reflect.DeepEqual(want, got) {
		t.Fatalf("destination DTO fields drifted: want=%v got=%v", want, got)
	}
	var settings UISettings
	fillDistinct(reflect.ValueOf(&settings))
	value := reflect.ValueOf(settings.AccessControlSettings())
	for name := range want {
		if !reflect.DeepEqual(value.FieldByName(name).Interface(), reflect.ValueOf(settings).FieldByName(name).Interface()) {
			t.Fatalf("destination DTO mapping crossed field %s", name)
		}
	}
}

func TestDestinationRuntimeSettingsRemainGlobalAndBoundTrialWindow(t *testing.T) {
	s := UISettings{}
	s.SetDestinationSettings(domain.DestinationSettings{HitRetentionDays: 1, TrialRetentionDays: 30, UsageRetentionDays: 3, ListRefreshHours: 6, PolicyApplyMinSeconds: 30})
	effective, defaults := RuntimeEffective(s)
	if effective["dest_trial_retention_days"] != 1 || s.DestinationSettings().TrialRetentionDays != 30 || defaults["dest_trial_retention_days"] != 7 {
		t.Fatalf("trial window/default drift: %+v / %+v", effective, defaults)
	}
	for _, key := range []string{"dest.hit_retention_days", "dest.trial_retention_days", "dest.usage_retention_days", "dest.list_refresh_hours", "dest.policy_apply_min_seconds"} {
		if OverridableScopeKeys[key] {
			t.Fatalf("fleet control became a group override: %s", key)
		}
	}
}
