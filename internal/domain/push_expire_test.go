package domain

import (
	"testing"
	"time"
)

// TestPushExpireTime pins MAX(ExpireAt, EmergencyUntil) — the contract that lets
// UseEmergencyAccess extend a past-expiry user WITHOUT mutating their stored
// ExpireAt (see the L23 fix). A future EmergencyUntil must win over a past
// ExpireAt, and once the window is cleared the real (past) ExpireAt governs.
func TestPushExpireTime(t *testing.T) {
	past := time.Now().Add(-48 * time.Hour)
	future := time.Now().Add(2 * time.Hour)

	if (&User{}).PushExpireTime() != 0 {
		t.Error("no expiry / no window → 0 (unlimited)")
	}
	if got := (&User{ExpireAt: &past}).PushExpireTime(); got != past.UnixMilli() {
		t.Errorf("past expiry, no window → %d, want %d", got, past.UnixMilli())
	}
	// Active emergency window beyond a past expiry → the window wins, and the
	// stored ExpireAt is untouched (the caller never had to overwrite it).
	u := &User{ExpireAt: &past, EmergencyUntil: &future}
	if got := u.PushExpireTime(); got != future.UnixMilli() {
		t.Errorf("emergency beyond past expiry → %d, want %d (window wins)", got, future.UnixMilli())
	}
	if !u.ExpireAt.Equal(past) {
		t.Error("PushExpireTime must not mutate ExpireAt")
	}
}
