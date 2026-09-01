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

// The create paths (admin form, self-registration, SSO JIT) carry SCALARS, so
// they cannot express the third state directly — LimitOverridesFromCreate is
// the single place that maps them onto it, and every provisioning path in the
// panel depends on it reading 0 as "no opinion" rather than "explicitly
// unlimited". Getting this backwards is silent and total: every user the panel
// ever creates would be pinned out of its group's policy, an operator would set
// a group quota and find it applies to nobody, and nothing would log.
func TestLimitOverridesFromCreate(t *testing.T) {
	t.Run("zero means no opinion, so the group decides", func(t *testing.T) {
		o := LimitOverridesFromCreate(0, 0, 0)
		if o.TrafficLimitBytes != nil || o.IPLimit != nil || o.DeviceLimit != nil {
			t.Fatalf("a zero-valued create must store nothing, got %+v", o)
		}
		// The consequence that actually matters, stated as the resolution it
		// produces rather than as the pointer being nil.
		got := ResolveLimits(o, GroupLimits{TrafficLimitBytes: i64(100), IPLimit: ip(3), DeviceLimit: ip(5)})
		if got.TrafficLimitBytes != 100 || got.IPLimit != 3 || got.DeviceLimit != 5 {
			t.Fatalf("a user created with zeros must inherit the group's policy, got %+v", got)
		}
	})

	t.Run("a stated value is an override and survives a group policy", func(t *testing.T) {
		o := LimitOverridesFromCreate(50, 1, 2)
		got := ResolveLimits(o, GroupLimits{TrafficLimitBytes: i64(100), IPLimit: ip(3), DeviceLimit: ip(5)})
		if got.TrafficLimitBytes != 50 || got.IPLimit != 1 || got.DeviceLimit != 2 {
			t.Fatalf("a stated create value must win over the group, got %+v", got)
		}
	})

	t.Run("the three fields are independent", func(t *testing.T) {
		o := LimitOverridesFromCreate(50, 0, 0)
		if o.TrafficLimitBytes == nil || *o.TrafficLimitBytes != 50 {
			t.Fatalf("traffic should be stated, got %+v", o.TrafficLimitBytes)
		}
		if o.IPLimit != nil || o.DeviceLimit != nil {
			t.Fatal("stating traffic must not pin the two connection caps")
		}
	})
}
