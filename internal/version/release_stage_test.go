package version

import (
	"reflect"
	"testing"
)

func TestReleaseStagesAndAliases(t *testing.T) {
	for _, tc := range []struct {
		v       string
		pre     bool
		stage   string
		aliases []string
	}{
		{"4.1.0-beta.2", false, "beta", []string{"beta", "testing"}},
		{"4.1.0-rc.1", false, "rc", []string{"rc", "beta", "testing"}},
		{"4.1.0", true, "pending", nil},
		{"4.0.1.11", true, "beta", []string{"beta", "testing"}},
		{"4.1.0", false, "stable", []string{"stable", "latest", "rc", "beta", "testing"}},
	} {
		if got := ReleaseStage(tc.v, tc.pre); got != tc.stage {
			t.Errorf("%+v: %s", tc, got)
		}
		if got := ReleaseAliases(tc.v, !tc.pre); !reflect.DeepEqual(got, tc.aliases) {
			t.Errorf("%+v: %v", tc, got)
		}
	}
}
func TestAdvanceReleaseLocksTheBaseAndRefusesSupersededCandidates(t *testing.T) {
	for _, tc := range []struct {
		source, stage, want string
		tags                []string
	}{
		{"v4.1.0-beta.2", "rc", "4.1.0-rc.1", nil}, {"v4.1.0-beta.2", "stable", "4.1.0", nil},
		{"v4.1.0-rc.3", "stable", "4.1.0", nil}, {"v4.1.0-rc.3", "rc", "", nil},
		{"v4.1.0-beta.2", "stable", "", []string{"v4.1.0-beta.3"}},
		{"v4.1.0-beta.2", "rc", "", []string{"v4.1.0-rc.1"}},
		{"v4.1.0-beta.2", "rc", "", []string{"v4.1.0"}},
		{"v4.0.1.11", "stable", "", nil}, {"release/4.1.0-beta.1", "stable", "", nil},
		{"v4.1.0", "stable", "", nil}, {"v4.1.0-beta.1", "beta", "", nil},
	} {
		got, err := AdvanceRelease(tc.source, tc.stage, tc.tags)
		if (err == nil) != (tc.want != "") || got != tc.want {
			t.Errorf("%+v: %s %v", tc, got, err)
		}
	}
}
