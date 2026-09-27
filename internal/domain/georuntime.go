package domain

import "time"

// The concurrent-location detector's fleet-wide knobs: what used to be
// constants in the poll, the enforcement and the infrastructure refresh, and
// is now an admin setting with the constant as its shipped default.
//
// Fleet-wide, not per group, each for a reason that does not depend on who
// is looking: freshness is judged per NODE before any user is known; the
// shared-exit rule counts accounts across the whole fleet at once; the
// per-poll caps bound one poll; the infrastructure cadences bound one loop.
// A group value would be stored, shown and never read, so none of these is in
// ports.OverridableScopeKeys.
//
// The bounds below are not policy. They are what keeps a knob inside the
// range where it still means what its name says, and each one is argued at
// the constant. A stored value outside them is clamped when read, never
// rejected at the form: this file is the one place a stored value is
// interpreted, the same bargain GeoPolicyFromSettings makes.
const (
	// LiveIPFreshWindowMinSeconds is two of 3X-UI's 10-second scans. A
	// narrower window marks a stream that is still open as stale between two
	// scans, and the user flickers between judged and idle.
	LiveIPFreshWindowMinSeconds = 20
	// LiveIPFreshWindowMaxSeconds is half the upstream's 30-minute memory.
	// Wider, and the window starts reading "remembered" as "connected now" —
	// the one confusion freshness exists to prevent.
	LiveIPFreshWindowMaxSeconds = 900
	// SharedExitMinUsersFloor: at 1, every source any account holds would be
	// a "shared exit" and nothing would ever be placed.
	SharedExitMinUsersFloor = 2
	// SharedExitMinUsersMax: above it, campus and carrier NATs that dozens
	// of accounts sit behind start counting as places again, and accusing.
	SharedExitMinUsersMax = 20
	// GeoPerPollDefault is the per-poll cap on automatic suspensions and,
	// separately, on lifts. GeoPerPollMax bounds one poll's latency: every
	// transition is an inline push to each panel the account is on.
	GeoPerPollDefault = 20
	GeoPerPollMax     = 200
	// InfraRefreshDefaultMinutes is how often PSP's own node and relay
	// addresses are rebuilt. InfraRefreshMaxMinutes bounds how long a relay
	// an admin just added can go on being judged as a user's location.
	InfraRefreshDefaultMinutes = 5
	InfraRefreshMaxMinutes     = 60
	// InfraHostTTLDefaultMinutes is how long a relay hostname's DNS answer is
	// reused. InfraHostTTLMaxMinutes is a day: a relay on dynamic DNS that
	// moved is otherwise judged at its old address for longer than that.
	InfraHostTTLDefaultMinutes = 10
	InfraHostTTLMaxMinutes     = 1440
)

// GeoRuntimeSettings is the flat, storage-shaped form: what the admin form
// saves, with 0 (or a negative number) meaning "never configured".
// ports.UISettings.GeoRuntimeSettings is the one mapping into it.
type GeoRuntimeSettings struct {
	FreshWindowSeconds, SharedExitMinUsers, BanMaxPerPoll, LiftMaxPerPoll,
	InfraRefreshMinutes, InfraHostTTLMinutes int
}

// GeoRuntime is the sanitised form every reader uses. Each field is inside
// its bounds; nothing downstream asks whether a value was meant.
type GeoRuntime struct {
	// FreshWindowSeconds: how far behind its node's newest scan a sighting
	// may be and still count as live (FreshLiveIPsWithin). 20..900.
	FreshWindowSeconds int
	// SharedExitMinUsers: how many distinct accounts on one source at once
	// make it a shared exit rather than a place (AddressExclusions). 2..20.
	SharedExitMinUsers int
	// BanMaxPerPoll / LiftMaxPerPoll: the automatic suspensions applied, and
	// the due ones lifted, in one poll. 1..200.
	BanMaxPerPoll  int
	LiftMaxPerPoll int
	// InfraRefresh: the infrastructure-address loop's cadence. 1..60 min.
	InfraRefresh time.Duration
	// InfraHostTTL: how long a node or relay hostname's answer is reused.
	// 1..1440 min.
	InfraHostTTL time.Duration
}

// DefaultGeoRuntime is the runtime a fresh install runs with: exactly the
// constants these knobs replaced, so an upgrade changes nothing.
func DefaultGeoRuntime() GeoRuntime {
	return GeoRuntime{
		FreshWindowSeconds: LiveIPFreshWindowSeconds,
		SharedExitMinUsers: SharedExitMinUsers,
		BanMaxPerPoll:      GeoPerPollDefault,
		LiftMaxPerPoll:     GeoPerPollDefault,
		InfraRefresh:       InfraRefreshDefaultMinutes * time.Minute,
		InfraHostTTL:       InfraHostTTLDefaultMinutes * time.Minute,
	}
}

// GeoRuntimeFromSettings turns stored settings into a runtime that is safe to
// run with: unset is the default, anything else is clamped to its bounds.
//
// Unset has to be the default and not the zero, for the reason
// GeoPolicyFromSettings gives: a fresh install stores nothing, and every one
// of these read literally is broken — a freshness window that keeps nothing
// live, a shared-exit rule switched off, caps that apply nothing, and a
// zero-length ticker, which panics.
func GeoRuntimeFromSettings(s GeoRuntimeSettings) GeoRuntime {
	d := DefaultGeoRuntime()
	return GeoRuntime{
		FreshWindowSeconds: settingOr(s.FreshWindowSeconds, d.FreshWindowSeconds, LiveIPFreshWindowMinSeconds, LiveIPFreshWindowMaxSeconds),
		SharedExitMinUsers: settingOr(s.SharedExitMinUsers, d.SharedExitMinUsers, SharedExitMinUsersFloor, SharedExitMinUsersMax),
		BanMaxPerPoll:      settingOr(s.BanMaxPerPoll, d.BanMaxPerPoll, 1, GeoPerPollMax),
		LiftMaxPerPoll:     settingOr(s.LiftMaxPerPoll, d.LiftMaxPerPoll, 1, GeoPerPollMax),
		InfraRefresh:       time.Duration(settingOr(s.InfraRefreshMinutes, InfraRefreshDefaultMinutes, 1, InfraRefreshMaxMinutes)) * time.Minute,
		InfraHostTTL:       time.Duration(settingOr(s.InfraHostTTLMinutes, InfraHostTTLDefaultMinutes, 1, InfraHostTTLMaxMinutes)) * time.Minute,
	}
}

// settingOr is THE rule for a stored runtime knob: v <= 0 is "never
// configured" and gives def; anything else is clamped to [lo, hi]. One
// function so every knob of this kind reads 0 the same way.
func settingOr(v, def, lo, hi int) int {
	if v <= 0 {
		return def
	}
	return min(max(v, lo), hi)
}
