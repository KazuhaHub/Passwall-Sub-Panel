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
