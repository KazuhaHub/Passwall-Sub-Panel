package domain

import (
	"testing"
	"time"
)

// TestUserEmergencyQuotaExhausted pins the /sub gate predicate: a user inside
// an active emergency window (granted after a traffic-exceeded auto-disable)
// is "exhausted" only once used (Lifetime - EmergencyBaseline) meets the
// per-window cap. The time-expired and wrong-reason rows must NOT report
// exhausted — those are handled by other gate branches.
func TestUserEmergencyQuotaExhausted(t *testing.T) {
	now := time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC)
	future := now.Add(time.Hour)
	past := now.Add(-time.Hour)
	const gb = int64(1) << 30
	quota := gb // 1 GiB per-window cap

	cases := []struct {
		name  string
		u     *User
		quota int64
		want  bool
	}{
		{"active, used == quota", &User{AutoDisabledReason: DisabledTrafficExceeded, EmergencyUntil: &future, LifetimeTotalBytes: 3 * gb, EmergencyBaselineBytes: 2 * gb}, quota, true},
		{"active service reason, used == quota", &User{ServiceDisabledReason: DisabledTrafficExceeded, EmergencyUntil: &future, LifetimeTotalBytes: 3 * gb, EmergencyBaselineBytes: 2 * gb}, quota, true},
		{"active, used > quota", &User{AutoDisabledReason: DisabledTrafficExceeded, EmergencyUntil: &future, LifetimeTotalBytes: 5 * gb, EmergencyBaselineBytes: 2 * gb}, quota, true},
		{"active, used < quota", &User{AutoDisabledReason: DisabledTrafficExceeded, EmergencyUntil: &future, LifetimeTotalBytes: 2*gb + 500, EmergencyBaselineBytes: 2 * gb}, quota, false},
		{"no cap (quota<=0)", &User{AutoDisabledReason: DisabledTrafficExceeded, EmergencyUntil: &future, LifetimeTotalBytes: 9 * gb, EmergencyBaselineBytes: 0}, 0, false},
		{"window expired by time", &User{AutoDisabledReason: DisabledTrafficExceeded, EmergencyUntil: &past, LifetimeTotalBytes: 9 * gb, EmergencyBaselineBytes: 0}, quota, false},
		{"wrong disable reason", &User{AutoDisabledReason: DisabledBlockedClient, EmergencyUntil: &future, LifetimeTotalBytes: 9 * gb, EmergencyBaselineBytes: 0}, quota, false},
		{"no emergency window", &User{AutoDisabledReason: DisabledTrafficExceeded, EmergencyUntil: nil, LifetimeTotalBytes: 9 * gb, EmergencyBaselineBytes: 0}, quota, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.u.EmergencyQuotaExhausted(tc.quota, now); got != tc.want {
				t.Fatalf("EmergencyQuotaExhausted = %v, want %v", got, tc.want)
			}
		})
	}
}
