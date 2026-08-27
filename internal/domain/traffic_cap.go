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

// PanelQuotaCap resolves the absolute cap to push for this legacy per-node
// client.
func (e *XUIClientEntry) PanelQuotaCap(headroom int64) int64 {
	if e == nil {
		return PanelQuotaCap(headroom, 0)
	}
	return PanelQuotaCap(headroom, e.LastRawTotalBytes)
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
