package domain

import (
	"fmt"
	"math"
)

// Per-server traffic multiplier.
//
// A server's factor says how many bytes a user is CHARGED for each byte they
// move through that server, in thousandths: 1000 is one-for-one, 1500 charges
// half again, 500 charges half, and 0 makes the server free. It belongs to the
// ENTRY server because that is the only granularity the data has: a user holds
// one client counter per server, so relay lines and nodes on the same server
// cannot be told apart.
//
// The factor is applied once, when a poll books a delta: the user's quota
// ledger (lifetime, period and emergency baselines) holds charged bytes, and
// every reader of it — enforcement, the subscription header, mails, usage
// pages — is consistent without knowing a factor exists. Raw bytes are kept
// beside it for display. Changing a factor therefore affects only traffic
// booked afterwards.
const (
	// TrafficFactorDefault is one-for-one. Every existing server reads as this.
	TrafficFactorDefault = 1000
	// TrafficFactorMax caps the factor at 100×.
	TrafficFactorMax = 100000
)

// ValidateTrafficFactor refuses a factor outside 0..TrafficFactorMax.
func ValidateTrafficFactor(milli int) error {
	if milli < 0 || milli > TrafficFactorMax {
		return fmt.Errorf("%w: traffic_factor_milli must be between 0 and %d", ErrValidation, TrafficFactorMax)
	}
	return nil
}

// EffectiveTrafficFactor is the factor this server charges at. A definition
// that never carried one (nil) is one-for-one, so a panel built in code or by a
// test double without the field behaves exactly as before the multiplier.
func (p *Panel) EffectiveTrafficFactor() int {
	if p == nil || p.TrafficFactorMilli == nil {
		return TrafficFactorDefault
	}
	return clampFactor(*p.TrafficFactorMilli)
}

func clampFactor(milli int) int {
	switch {
	case milli < 0:
		return TrafficFactorDefault
	case milli > TrafficFactorMax:
		return TrafficFactorMax
	}
	return milli
}

// TrafficFactorOf looks a server's factor up in a map read once per pass. A
// server missing from the map is one-for-one.
func TrafficFactorOf(factors map[int64]int, panelID int64) int {
	milli, ok := factors[panelID]
	if !ok {
		return TrafficFactorDefault
	}
	return clampFactor(milli)
}

// ChargeBytes is raw bytes weighted by a factor in thousandths, rounded half
// up. At TrafficFactorDefault it is the identity, which is what keeps a panel
// where no factor was ever changed byte-identical to one without the feature.
// Overflow-safe for any raw a counter can hold: the quotient is formed before
// the multiplication, and an out-of-range result saturates.
func ChargeBytes(raw int64, milli int) int64 {
	if raw <= 0 {
		return 0
	}
	if milli == TrafficFactorDefault {
		return raw
	}
	if milli <= 0 {
		return 0
	}
	f := int64(milli)
	q, r := raw/1000, raw%1000
	if q > math.MaxInt64/f {
		return math.MaxInt64
	}
	hi := q * f
	lo := (r*f + 500) / 1000
	if hi > math.MaxInt64-lo {
		return math.MaxInt64
	}
	return hi + lo
}

// RawBytesFor converts charged bytes back into the raw bytes that would cost
// that much on a server with this factor, rounded DOWN so a gate built from it
// closes no later than the charged ledger would. ok is false at factor 0: no
// amount of raw traffic costs anything there. Saturates instead of
// overflowing.
func RawBytesFor(charged int64, milli int) (raw int64, ok bool) {
	if milli <= 0 {
		return 0, false
	}
	if charged <= 0 {
		return 0, true
	}
	if milli == TrafficFactorDefault {
		return charged, true
	}
	f := int64(milli)
	q, r := charged/f, charged%f
	if q > math.MaxInt64/1000 {
		return math.MaxInt64, true
	}
	hi := q * 1000
	lo := r * 1000 / f // r < f <= 100000, so r*1000 cannot overflow
	if hi > math.MaxInt64-lo {
		return math.MaxInt64, true
	}
	return hi + lo, true
}

// RawHeadroom converts PSP's period-relative quota headroom, which is in
// CHARGED bytes, into the raw bytes a 3X-UI / S-UI server with this factor may
// pass before its own counter should stop the client. It must be applied
// before domain.PanelQuotaCap, which rebases onto the panel's RAW counter.
//
// The encodings of the headroom are kept:
//
//   - 0 (unlimited) stays 0.
//   - 1 is the exhausted sentinel (TrafficFloorBytes returns it both for
//     "exhausted" and for "one byte left") and stays 1 on every server, a free
//     one included: an exhausted user stays suspended everywhere.
//   - Any other N becomes floor(N × 1000 / f), never below 1 — rounding a
//     small headroom down to 0 would turn it into "unlimited".
//   - At factor 0 a remaining headroom becomes 0: the server is free, so its
//     own counter must not gate the client.
func RawHeadroom(headroom int64, factorMilli int) int64 {
	if headroom <= 0 {
		return 0
	}
	if headroom == 1 {
		return 1
	}
	raw, ok := RawBytesFor(headroom, factorMilli)
	if !ok {
		return 0
	}
	if raw < 1 {
		return 1
	}
	return raw
}
