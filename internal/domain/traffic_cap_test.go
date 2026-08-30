package domain

import (
	"testing"
	"time"
)

func TestPanelQuotaCap(t *testing.T) {
	const GB = int64(1) << 30
	for _, tc := range []struct {
		name          string
		headroom      int64
		panelLifetime int64
		want          int64
	}{
		{"unlimited passes through as no cap", 0, 500 * GB, 0},
		{"a negative headroom is also no cap", -1, 500 * GB, 0},
		{
			// The case the defect got wrong: a client that has already moved
			// 60 GB in its life, with 40 GB left this period, must be cut at
			// 100 GB — not at 40, which it is already past.
			name:     "headroom is rebased onto the panel counter",
			headroom: 40 * GB, panelLifetime: 60 * GB, want: 100 * GB,
		},
		{
			// A brand-new client has no history, so the cap equals the
			// headroom — which is why the defect stayed invisible until a
			// client accumulated some traffic.
			name:     "a fresh client's cap equals its headroom",
			headroom: 40 * GB, panelLifetime: 0, want: 40 * GB,
		},
		{
			// TrafficFloorBytes returns 1 for an over-quota user. Rebased it
			// means "one more byte, then cut", which still trips the panel's
			// >= comparison on the very next tick.
			name:     "the over-quota sentinel survives rebasing",
			headroom: 1, panelLifetime: 200 * GB, want: 200*GB + 1,
		},
		{"a negative counter is clamped, not propagated", 40 * GB, -5, 40 * GB},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := PanelQuotaCap(tc.headroom, tc.panelLifetime); got != tc.want {
				t.Errorf("PanelQuotaCap(%d, %d) = %d, want %d", tc.headroom, tc.panelLifetime, got, tc.want)
			}
		})
	}
}

// The pushed cap must stay STABLE for an active user, which is the property
// that makes the panel-side value stop churning every poll: the panel counter
// grows by the same delta the period headroom shrinks by, so the sum is
// invariant. Before the fix the pushed value moved every single cycle.
func TestPanelQuotaCapIsInvariantAsTrafficAccrues(t *testing.T) {
	const GB = int64(1) << 30
	const limit = 100 * GB

	panelLifetime := int64(0)
	periodUsed := int64(0)
	first := PanelQuotaCap(limit-periodUsed, panelLifetime)

	for cycle := 0; cycle < 20; cycle++ {
		delta := int64(cycle+1) * 137 * (1 << 20) // uneven, so nothing cancels by luck
		panelLifetime += delta
		periodUsed += delta // PSP folds the same delta it read off the panel
		if got := PanelQuotaCap(limit-periodUsed, panelLifetime); got != first {
			t.Fatalf("cycle %d: cap = %d, want it to stay %d", cycle, got, first)
		}
	}
}

func TestPanelQuotaCapMethods(t *testing.T) {
	const GB = int64(1) << 30
	c := &PSPClient{LastRawTotalBytes: 60 * GB}
	if got := c.PanelQuotaCap(40 * GB); got != 100*GB {
		t.Errorf("PSPClient cap = %d, want %d", got, 100*GB)
	}
	// Nil-tolerant: the legacy push sites iterate rows that a concurrent
	// delete can empty, and a panic there would take down a poll cycle.
	var nilC *PSPClient
	if got := nilC.PanelQuotaCap(40 * GB); got != 40*GB {
		t.Errorf("nil PSPClient cap = %d, want %d", got, 40*GB)
	}
}

func TestUserLifecycleCarriesTheConnectionCaps(t *testing.T) {
	const GB = int64(1) << 30
	now := time.Now()
	u := &User{Enabled: true, IPLimit: 3, DeviceLimit: 2}

	got := u.Lifecycle(now, 40*GB)
	if got.IPLimit != 3 || got.DeviceLimit != 2 {
		t.Errorf("caps = %d/%d, want 3/2", got.IPLimit, got.DeviceLimit)
	}
	if got.QuotaHeadroom != 40*GB {
		t.Errorf("headroom = %d, want %d", got.QuotaHeadroom, 40*GB)
	}
	if !got.Enable {
		t.Error("an enabled, unexpired user must push enable=true")
	}
	// PanelQuota is the only place the headroom becomes a panel-facing number,
	// so the struct must still be carrying the period-relative value.
	if cap := got.PanelQuota(60 * GB); cap != 100*GB {
		t.Errorf("PanelQuota = %d, want %d", cap, 100*GB)
	}
}

// A nil user must not panic: the legacy push sites iterate rows a concurrent
// delete can empty out from under them.
func TestUserLifecycleNilSafe(t *testing.T) {
	var u *User
	if got := u.Lifecycle(time.Now(), 1); got != (UserLifecycle{}) {
		t.Errorf("nil user lifecycle = %+v, want zero", got)
	}
}

// The band is a policy number — "how much extra may a user get if PSP stops
// running" — so these cases pin the policy, not an implementation detail.
func TestPanelQuotaBand(t *testing.T) {
	for _, tc := range []struct {
		name     string
		headroom int64
		want     int64
	}{
		{"five percent of headroom", 10 << 30, 512 << 20},
		{"scales down with what is left", 1 << 30, 1 << 30 / 20},
		// A huge quota must not buy a proportionally huge overshoot.
		// The ceiling exists to bound absolute overshoot on very large plans,
		// and its size is the one thing here that data sets rather than
		// policy: the largest sibling drift seen in a 40.9h production window
		// was 607 MiB, so 1 GiB covers the worst observed cycle.
		{"capped in absolute terms", 1 << 50, 1 << 30},
		{"ceiling binds above 20 GiB of headroom", 21 << 30, 1 << 30},
		{"just below the ceiling stays proportional", 19 << 30, (19 << 30) / 20},
		// Both boundaries fall out of integer division, and both matter:
		// 0 is "unlimited" and 1 is TrafficFloorBytes' exhausted sentinel.
		// A band on either would be a hole in the safety net.
		{"unlimited gets no band", 0, 0},
		{"exhausted gets no band", 1, 0},
		{"negative gets no band", -5, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := PanelQuotaBand(tc.headroom); got != tc.want {
				t.Fatalf("PanelQuotaBand(%d) = %d, want %d", tc.headroom, got, tc.want)
			}
		})
	}
}

// The asymmetry is the whole point: a too-generous cap is bounded and only
// realisable during a PSP outage, while a too-strict one cuts a paying user
// off early. Only the first is tolerated.
func TestPanelQuotaWithinBand(t *testing.T) {
	// 10 GiB of headroom keeps this below the absolute ceiling, so the cases
	// below exercise the PROPORTIONAL arm; the ceiling has its own case in
	// TestPanelQuotaBand.
	const headroom = int64(10 << 30)
	const band = int64(512 << 20) // headroom / 20
	const want = int64(500 << 30)

	for _, tc := range []struct {
		name   string
		stored int64
		want   int64
		ok     bool
	}{
		{"exact match", want, want, true},
		{"generous, inside the band", want + band - 1, want, true},
		{"generous, exactly at the band", want + band, want, true},
		{"generous, past the band", want + band + 1, want, false},
		// Never tolerated at any magnitude: this is a user being cut off
		// before they should be, which happens when headroom GROWS (a new
		// period, an admin raising the quota).
		{"strict by one byte", want - 1, want, false},
		{"strict by a lot", want - (100 << 30), want, false},
		// Unlimited is exact in both directions: a leftover cap is a live
		// restriction, and inventing one for an uncapped user is the
		// traffic-floor defect in reverse.
		{"unlimited stays unlimited", 0, 0, true},
		{"leftover cap on an unlimited user", band, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := PanelQuotaWithinBand(tc.stored, tc.want, headroom); got != tc.ok {
				t.Fatalf("PanelQuotaWithinBand(%d, %d, %d) = %v, want %v",
					tc.stored, tc.want, headroom, got, tc.ok)
			}
		})
	}
}

// An exhausted client must be pushed exactly, or the band becomes the hole
// through which a user who has spent their quota keeps browsing.
func TestPanelQuotaWithinBand_ExhaustedIsExact(t *testing.T) {
	// TrafficFloorBytes encodes "at or past the limit" as a headroom of 1.
	const headroom = int64(1)
	want := PanelQuotaCap(headroom, 10<<30)
	if PanelQuotaWithinBand(want+1, want, headroom) {
		t.Fatal("an exhausted client tolerated a stale cap; the band must be 0 here")
	}
	if !PanelQuotaWithinBand(want, want, headroom) {
		t.Fatal("an exact match must still skip")
	}
}

// The steady state the band exists for: one user's quota is shared across
// their clients, so traffic on client B lowers client A's intended cap every
// cycle while A's own panel counter sits still. Before the band that was a
// guaranteed write per cycle per client.
func TestPanelQuotaWithinBand_AbsorbsSiblingDrift(t *testing.T) {
	const limit = int64(100 << 30)
	lastRawA := int64(20 << 30) // client A's panel counter, unmoving

	// A's cap was pushed when the user had burned nothing.
	stored := PanelQuotaCap(limit, lastRawA)

	// Client B now burns 512 MiB. A's own counter did not move, but the
	// user's headroom did, so A's intended cap drops by the same amount.
	headroom := limit - (512 << 20)
	want := PanelQuotaCap(headroom, lastRawA)
	if stored == want {
		t.Fatal("precondition: sibling traffic must move the intended cap")
	}
	if !PanelQuotaWithinBand(stored, want, headroom) {
		t.Fatalf("512 MiB of sibling drift should sit inside a %d-byte band",
			PanelQuotaBand(headroom))
	}

	// Keep burning. Once the accumulated drift passes the band, PSP pays for
	// one write and the staleness resets — it does not accumulate unbounded.
	headroom = limit - (16 << 30)
	want = PanelQuotaCap(headroom, lastRawA)
	if PanelQuotaWithinBand(stored, want, headroom) {
		t.Fatalf("16 GiB of drift must exceed the %d-byte band and force a write",
			PanelQuotaBand(headroom))
	}
}
