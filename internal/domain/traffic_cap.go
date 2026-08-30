package domain

import "time"

// PanelQuotaCap converts PSP's period-relative quota headroom into the
// absolute counter value a panel actually enforces against.
//
// The two sides measure different things, and conflating them is a defect
// PSP shipped for a long time (docs/traffic-floor-defect.md):
//
//   - PSP thinks in PERIODS. TrafficFloorBytes returns "bytes left in the
//     current billing period" — a number that resets every period.
//   - 3X-UI thinks in LIFETIMES. Its depletion sweep is
//     `total > 0 AND up + down >= total`, where up/down accumulate through
//     `SET up = up + ?` and are never reset — PSP keeps its own period
//     baselines and pins the panel's own renewal cycle to never.
//
// Pushing the period number into the lifetime comparison disabled a user at
// half their quota in their first period, and earlier every period after.
// Adding the panel's own counter restores the intent: the panel now cuts the
// client off exactly when it burns `headroom` more bytes FROM NOW.
//
// headroom <= 0 means unlimited and must pass through as 0 — the panel reads
// total == 0 as "no cap", and adding an offset there would invent a quota for
// a user who has none.
//
// One consequence worth stating: TrafficFloorBytes encodes "already at or past
// the limit" as a headroom of 1, which rebases to panelLifetime+1. The panel
// therefore cuts an exhausted client as soon as it moves ANY further traffic,
// rather than while it sits idle at exactly its limit. That is the right shape
// for a net whose job is bounding abuse during a PSP outage — an idle exhausted
// user consumes nothing, and the moment they consume anything they are gone
// within one traffic tick. Special-casing the sentinel to cut an idle user too
// would couple this to another package's encoding to buy a few seconds.
//
// panelLifetime is the last raw cumulative counter PSP read from the panel
// (LastRawTotalBytes). It can be stale by up to one poll interval, and it goes
// backwards when the panel-side counter is reset (an xray restart, an admin
// pressing reset). Both make the resulting cap slightly generous until the
// next poll refreshes it — bounded, self-correcting, and erring toward letting
// a user through rather than cutting them off, which is the right side to miss
// on for a safety net whose whole job is to bound abuse during a PSP outage.
func PanelQuotaCap(headroom, panelLifetime int64) int64 {
	if headroom <= 0 {
		return 0
	}
	if panelLifetime < 0 {
		panelLifetime = 0
	}
	return panelLifetime + headroom
}

// PanelQuotaCap resolves the absolute cap to push for this shared client.
func (c *PSPClient) PanelQuotaCap(headroom int64) int64 {
	if c == nil {
		return PanelQuotaCap(headroom, 0)
	}
	return PanelQuotaCap(headroom, c.LastRawTotalBytes)
}

// UserLifecycle is the enforcement state every one of a user's panel-side
// clients must reflect. It exists as a type rather than a parameter list
// because it is a CONTRACT, not an argument bundle: PSP's own data plane is
// the design target and 3X-UI / S-UI are compatibility targets, so this set
// is defined by what PSP wants enforced — each adapter then translates as
// much of it as its panel can express and declares the rest as a capability
// gap. Adding a field here should not ripple through five signatures.
type UserLifecycle struct {
	// Enable is the user's effective service state (account enabled, not
	// expired, not suspended).
	Enable bool
	// ExpiryTime is the panel-side expiry in epoch milliseconds; 0 = never.
	ExpiryTime int64
	// QuotaHeadroom is bytes remaining IN THE CURRENT PERIOD, NOT the number
	// a panel is given. It stays period-relative everywhere; the rebase onto
	// a panel counter happens at the one point that knows WHICH client's
	// counter applies, via PanelQuota below. 0 = unlimited.
	QuotaHeadroom int64
	// IPLimit caps concurrent source IPs; 0 = unlimited.
	IPLimit int
	// DeviceLimit caps bound devices; 0 = unlimited.
	DeviceLimit int
}

// Lifecycle assembles the enforcement state to push for this user.
// quotaHeadroom comes from the caller because computing it needs a usage read
// the domain layer has no business doing.
func (u *User) Lifecycle(now time.Time, quotaHeadroom int64) UserLifecycle {
	if u == nil {
		return UserLifecycle{}
	}
	return UserLifecycle{
		Enable:        u.EffectiveEnabled(now),
		ExpiryTime:    u.PushExpireTime(),
		QuotaHeadroom: quotaHeadroom,
		IPLimit:       u.IPLimit,
		DeviceLimit:   u.DeviceLimit,
	}
}

// PanelQuota resolves this lifecycle's headroom into the absolute cap to push
// for a client whose panel-side counter currently reads panelLifetime.
//
// One user's lifecycle fans out to several clients, each with its OWN counter,
// so this cannot be folded into the struct — it is a per-client resolution of a
// per-user intent.
func (l UserLifecycle) PanelQuota(panelLifetime int64) int64 {
	return PanelQuotaCap(l.QuotaHeadroom, panelLifetime)
}

// Quota deadband. See docs/traffic-quota-deadband.md.
const (
	// quotaBandDivisor makes the band 1/20th — 5% — of the headroom it
	// protects. Expressed as a fraction of what is LEFT rather than of the
	// total limit, it carries a policy statement that does not depend on the
	// user's speed, plan size or usage pattern: IF PSP STOPS RUNNING, A USER
	// GETS AT MOST 5% LONGER THAN THEY SHOULD HAVE. Their remaining headroom
	// is the time they have left, so a fixed fraction of it is a fixed
	// fraction of that time.
	//
	// Anything much tighter is below the noise floor and buys nothing: the
	// panel's cap is already stale by one poll interval of the user's traffic
	// no matter what this is set to, so a band smaller than that adds no risk
	// and suppresses no writes either.
	quotaBandDivisor = 20
	// maxQuotaBandBytes is an absolute ceiling, and the one place the
	// proportional argument above is deliberately abandoned. 5% stays 5%
	// however large the quota, but on a multi-terabyte plan that percentage
	// is a bandwidth bill rather than a rounding error, and no operator
	// reading "5%" pictures 500 GB. It binds above 20 GiB of headroom.
	//
	// Unlike the divisor, this number IS one data should set: its whole job
	// is to bound the absolute drift a band may swallow, and how large that
	// has to be is an empirical question. A 40.9-hour production window
	// (25 users, 9 nodes, P=4.0) put the largest sibling drift ever observed
	// at 607 MiB, with p95 at 14.9 MiB. 1 GiB covers the worst observed cycle
	// with room to spare; the 8 GiB this started as was picked before that
	// window existed and left an order of magnitude of unnecessary overshoot
	// on the table.
	maxQuotaBandBytes = 1 << 30
)

// PanelQuotaBand is how far a panel's stored cap may lag the intended cap
// before PSP spends a write to correct it.
//
// The band is NOT a tuning knob derived from observed traffic. It is a policy
// number with one meaning: HOW MUCH EXTRA TRAFFIC A USER MAY GET IF PSP STOPS
// RUNNING. PSP itself still cuts a user off at the exact right point on its
// next poll, so a stale panel cap only ever materialises as real overshoot
// during a PSP outage — which is the one scenario the panel-side cap exists
// for. Sizing it from a measured delta distribution would optimise write
// volume and let the safety margin fall out as a by-product; this is the other
// way round, which is the correct one.
//
// Anchoring on the remaining headroom rather than the user's total limit is
// what keeps the band safe as a user approaches exhaustion: the tolerance
// shrinks with what is left to protect, so it can never exceed the headroom
// itself. A limit-anchored band would do the opposite — 1% of a 1 TB quota is
// 10 GB, which for a user with 1 GB left would authorise a tenfold overshoot.
//
// Integer division gives the boundary cases for free: headroom 0 (unlimited)
// and headroom 1 (TrafficFloorBytes' "at or past the limit" sentinel) both
// yield a band of 0, so an unlimited client and an exhausted one are always
// pushed exactly.
func PanelQuotaBand(headroom int64) int64 {
	if headroom <= 0 {
		return 0
	}
	band := headroom / quotaBandDivisor
	if band > maxQuotaBandBytes {
		return maxQuotaBandBytes
	}
	return band
}

// PanelQuotaWithinBand reports whether a panel's stored cap is close enough to
// the cap PSP intends that correcting it is not worth a write.
//
// The band is deliberately ASYMMETRIC, because the two directions of drift are
// not equally acceptable:
//
//   - stored > want — the panel holds a cap that is too GENEROUS. Bounded by
//     the band, only realisable during a PSP outage, and the direction the
//     steady state actually drifts in: a user's quota is shared across their
//     clients, so traffic on client j lowers every OTHER client's intended cap
//     while their own panel counters sit still. This is what the band is for.
//
//   - stored < want — the panel holds a cap that is too STRICT, and would cut
//     a paying user off EARLY. This happens when headroom grows: a new billing
//     period, or an admin raising the quota. Tolerating it would delay someone
//     getting service they are owed, so it is never tolerated — one write, now.
//
// want <= 0 means unlimited, which a panel encodes as 0 and must be exact: a
// leftover non-zero cap is a live restriction, not a rounding error.
func PanelQuotaWithinBand(stored, want, headroom int64) bool {
	if want <= 0 {
		return stored == want
	}
	if stored < want {
		return false
	}
	return stored-want <= PanelQuotaBand(headroom)
}
