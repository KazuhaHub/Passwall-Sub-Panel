package domain

import "testing"

func i64(v int64) *int64 { return &v }
func ip(v int) *int      { return &v }

// The whole point of the third state: 0 and "unset" are different answers, and
// reading one as the other either invents a quota for a user who has none or
// silently ignores a group policy.
func TestResolveLimits(t *testing.T) {
	for _, tc := range []struct {
		name string
		user LimitOverrides
		grp  GroupLimits
		want EffectiveLimits
	}{
		{
			name: "nothing set anywhere is unlimited",
			want: EffectiveLimits{},
		},
		{
			name: "the group supplies what the user leaves unset",
			grp:  GroupLimits{TrafficLimitBytes: i64(100), IPLimit: ip(3), DeviceLimit: ip(2)},
			want: EffectiveLimits{TrafficLimitBytes: 100, IPLimit: 3, DeviceLimit: 2},
		},
		{
			name: "an explicit user value wins over the group",
			user: LimitOverrides{TrafficLimitBytes: i64(50), IPLimit: ip(1), DeviceLimit: ip(9)},
			grp:  GroupLimits{TrafficLimitBytes: i64(100), IPLimit: ip(3), DeviceLimit: ip(2)},
			want: EffectiveLimits{TrafficLimitBytes: 50, IPLimit: 1, DeviceLimit: 9},
		},
		{
			// The case the third state exists for. Without it this user would
			// silently pick up the group's caps.
			name: "an explicit ZERO wins over the group, and means unlimited",
			user: LimitOverrides{TrafficLimitBytes: i64(0), IPLimit: ip(0), DeviceLimit: ip(0)},
			grp:  GroupLimits{TrafficLimitBytes: i64(100), IPLimit: ip(3), DeviceLimit: ip(2)},
			want: EffectiveLimits{},
		},
		{
			// A group value of 0 is equally explicit: "this plan is uncapped".
			name: "a group zero is also explicit",
			grp:  GroupLimits{TrafficLimitBytes: i64(0), IPLimit: ip(0)},
			want: EffectiveLimits{},
		},
		{
			name: "the three fields resolve independently",
			user: LimitOverrides{IPLimit: ip(5)},
			grp:  GroupLimits{TrafficLimitBytes: i64(100), IPLimit: ip(3), DeviceLimit: ip(2)},
			want: EffectiveLimits{TrafficLimitBytes: 100, IPLimit: 5, DeviceLimit: 2},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := ResolveLimits(tc.user, tc.grp); got != tc.want {
				t.Fatalf("ResolveLimits() = %+v, want %+v", got, tc.want)
			}
		})
	}
}

// A user whose group was deleted must keep working. Unlimited is the safe side
// to miss on: it lets a paying user through rather than cutting them off.
func TestResolveLimits_EmptyGroupIsUnlimitedNotAnError(t *testing.T) {
	got := ResolveLimits(LimitOverrides{}, GroupLimits{})
	if got != (EffectiveLimits{}) {
		t.Fatalf("ResolveLimits() = %+v, want all-unlimited", got)
	}
}

func TestLimitOverrides_InheritancePredicates(t *testing.T) {
	var none LimitOverrides
	if !none.InheritsTrafficLimit() || !none.InheritsIPLimit() || !none.InheritsDeviceLimit() {
		t.Fatal("an all-nil override set must report inheriting on every field")
	}
	// Zero is an override, not an absence — the distinction the whole type exists for.
	set := LimitOverrides{TrafficLimitBytes: i64(0), IPLimit: ip(0), DeviceLimit: ip(0)}
	if set.InheritsTrafficLimit() || set.InheritsIPLimit() || set.InheritsDeviceLimit() {
		t.Fatal("an explicit zero must NOT report as inheriting")
	}
}
