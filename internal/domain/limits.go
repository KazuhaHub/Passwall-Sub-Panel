package domain

// Entitlement limits and their inheritance from the owning group.
//
// Three numbers describe what a subscriber is entitled to: a traffic quota, a
// concurrent-IP cap and a device cap. Until v3.9.3 each lived only on the user
// row, which made a fleet-wide policy change an N-row edit. They now inherit
// from the user's group unless the user overrides them.
//
// # Why this needed a third state
//
// 0 already means "unlimited", on both sides of the wire: PSP encodes it that
// way and so does every panel. Adding a group layer without a third state
// leaves 0 ambiguous — "no limit" or "whatever the group says"? — and reading
// it the wrong way invents a quota for a user who has none, which is the
// traffic-floor defect (docs/traffic-floor-defect.md) in a new place.
//
// So: nil = inherit, 0 = explicitly unlimited, N = explicitly N. The user's
// value wins when set; otherwise the group's; otherwise unlimited.
//
// # Why the resolved value stays on User
//
// User keeps plain int fields carrying the RESOLVED value, because ~100 call
// sites want the number and nothing else — a metering loop asking "what is
// this user's quota" should not have to hold a group. The raw override lives
// beside it in the *Override fields, and only the admin write path and the UI
// touch those.
//
// That split is only safe because the write path is a sparse pointer DTO
// (UpdateProfileInput): a field absent from a request is not written, so an
// update that never mentions a limit cannot silently convert an inheriting
// user into an explicit one. LimitOverrides exists to keep that invariant
// visible; user_limits_test.go pins it.

// LimitOverrides is a user's raw, per-field entitlement overrides. A nil
// field means "inherit from the group".
type LimitOverrides struct {
	TrafficLimitBytes *int64
	IPLimit           *int
	DeviceLimit       *int
}

// GroupLimits is a group's entitlement policy. A nil field means the group
// states nothing, which resolves to unlimited.
type GroupLimits struct {
	TrafficLimitBytes *int64
	IPLimit           *int
	DeviceLimit       *int
}

// EffectiveLimits is the resolved answer for one user: what PSP will actually
// enforce. Zero means unlimited, matching the panel-side encoding.
type EffectiveLimits struct {
	TrafficLimitBytes int64
	IPLimit           int
	DeviceLimit       int
}

// ResolveLimits applies the inheritance rule: user override, else group, else
// unlimited. A nil group is a group that states nothing, not an error — a user
// whose group was deleted must keep working, and unlimited is the safe side to
// miss on (it lets a paying user through rather than cutting them off).
func ResolveLimits(o LimitOverrides, g GroupLimits) EffectiveLimits {
	return EffectiveLimits{
		TrafficLimitBytes: resolveInt64(o.TrafficLimitBytes, g.TrafficLimitBytes),
		IPLimit:           resolveInt(o.IPLimit, g.IPLimit),
		DeviceLimit:       resolveInt(o.DeviceLimit, g.DeviceLimit),
	}
}

func resolveInt64(user, group *int64) int64 {
	if user != nil {
		return *user
	}
	if group != nil {
		return *group
	}
	return 0
}

func resolveInt(user, group *int) int {
	if user != nil {
		return *user
	}
	if group != nil {
		return *group
	}
	return 0
}

// InheritsTrafficLimit reports whether this user takes their traffic quota
// from the group. Used by the admin API to render inherit-vs-override without
// the caller having to know the nil convention.
func (o LimitOverrides) InheritsTrafficLimit() bool { return o.TrafficLimitBytes == nil }

// InheritsIPLimit reports whether the concurrent-IP cap comes from the group.
func (o LimitOverrides) InheritsIPLimit() bool { return o.IPLimit == nil }

// InheritsDeviceLimit reports whether the device cap comes from the group.
func (o LimitOverrides) InheritsDeviceLimit() bool { return o.DeviceLimit == nil }

// LimitOverridesFromCreate maps a creation API that cannot express inheritance
// onto the tri-state.
//
// Every path that creates a user — the admin form, self-registration, SSO
// auto-provision — carries plain scalars where 0 has always meant "unlimited".
// None of them has a way to say "inherit", so a 0 arriving here is not a
// deliberate opt-out of the group's policy; it is the absence of an opinion.
// Reading it as inherit is the same judgement migrateLimitsToTriState makes
// about stored zeroes, and for the same reason: without a group layer to stand
// against, a 0 never carried override intent.
//
// A non-zero value is an explicit override, because the caller did choose it.
//
// The admin UI sends real tri-state on EDIT, so an operator who genuinely
// wants "unlimited regardless of the group" can still say so — just not while
// filling in a create form that has no such control.
func LimitOverridesFromCreate(trafficBytes int64, ipLimit, deviceLimit int) LimitOverrides {
	var o LimitOverrides
	if trafficBytes != 0 {
		v := trafficBytes
		o.TrafficLimitBytes = &v
	}
	if ipLimit != 0 {
		v := ipLimit
		o.IPLimit = &v
	}
	if deviceLimit != 0 {
		v := deviceLimit
		o.DeviceLimit = &v
	}
	return o
}
