package sendthrottle

import (
	"testing"
	"time"
)

const cd = 60 * time.Second

func TestThrottle_PerKeyCooldown(t *testing.T) {
	now := time.Date(2026, 6, 16, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	th := New(0, 0, clock) // no global cap; cooldown is per-call

	if !th.Allow(1, cd) {
		t.Fatal("first send to key 1 must be allowed")
	}
	if th.Allow(1, cd) {
		t.Fatal("second send to key 1 within cooldown must be blocked")
	}
	// A different recipient is independent.
	if !th.Allow(2, cd) {
		t.Fatal("first send to key 2 must be allowed (per-key, not global)")
	}
	// After the cooldown elapses, key 1 is allowed again.
	now = now.Add(61 * time.Second)
	if !th.Allow(1, cd) {
		t.Fatal("send after cooldown elapsed must be allowed")
	}
}

// TestThrottle_CooldownPerCall: the cooldown is a per-call argument, so the same
// throttle can apply a live (changing) setting value.
func TestThrottle_CooldownPerCall(t *testing.T) {
	now := time.Date(2026, 6, 16, 12, 0, 0, 0, time.UTC)
	th := New(0, 0, func() time.Time { return now })
	if !th.Allow(1, 0) {
		t.Fatal("cooldown 0 disables the per-key gate — first send allowed")
	}
	if !th.Allow(1, 0) {
		t.Fatal("cooldown 0 disables the per-key gate — repeat send still allowed")
	}
}

func TestThrottle_GlobalCap(t *testing.T) {
	now := time.Date(2026, 6, 16, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	// Global cap of 3 per minute, no per-key cooldown — distinct recipients.
	th := New(3, time.Minute, clock)

	for i := int64(1); i <= 3; i++ {
		if !th.Allow(i, 0) {
			t.Fatalf("send %d within the global cap must be allowed", i)
		}
	}
	if th.Allow(4, 0) {
		t.Fatal("4th distinct send must be blocked by the global cap")
	}
	// Window slides → budget frees up.
	now = now.Add(61 * time.Second)
	if !th.Allow(5, 0) {
		t.Fatal("after the window slides, the global budget must refill")
	}
}

func TestThrottle_BlockedSendConsumesNoBudget(t *testing.T) {
	now := time.Date(2026, 6, 16, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	// cooldown blocks key 1's 2nd send; that block must NOT consume a global slot.
	th := New(5, time.Minute, clock)
	if !th.Allow(1, cd) {
		t.Fatal("first send allowed")
	}
	if th.Allow(1, cd) {
		t.Fatal("second send to key 1 blocked by cooldown")
	}
	// Global budget should still be 4 remaining (only one send recorded).
	allowed := 0
	for i := int64(10); i < 100; i++ {
		if th.Allow(i, cd) {
			allowed++
		}
	}
	if allowed != 4 {
		t.Fatalf("global cap should have 4 slots left after one recorded send, got %d", allowed)
	}
}

// TestThrottle_Disabled: a 0 cooldown and a 0 global cap disable both gates.
func TestThrottle_Disabled(t *testing.T) {
	now := time.Date(2026, 6, 16, 12, 0, 0, 0, time.UTC)
	th := New(0, 0, func() time.Time { return now }) // global cap off
	for i := 0; i < 100; i++ {
		if !th.Allow(1, 0) { // cooldown off
			t.Fatal("with both gates disabled every send must be allowed")
		}
	}
}
