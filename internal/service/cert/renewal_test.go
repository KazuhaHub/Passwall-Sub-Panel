package cert

import (
	"testing"
	"time"
)

func TestRenewDueHybridThreshold(t *testing.T) {
	now := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	day := 24 * time.Hour

	cases := []struct {
		name            string
		lifetimeDays    int
		remainingDays   int
		renewBeforeDays int
		want            bool
	}{
		// 90-day cert, renew-before 30d: threshold stays 30d (30 ≤ 2/3·90=60).
		{"90d not yet due", 90, 31, 30, false},
		{"90d due at 30d left", 90, 30, 30, true},
		// 45-day cert, renew-before 30d: 30 ≤ 2/3·45=30 → threshold stays 30d.
		{"45d due at 30d left", 45, 30, 30, true},
		{"45d not due at 31d left", 45, 31, 30, false},
		// 6-day cert, renew-before 30d: 30 > 2/3·6=4 → fallback threshold = 6/3 = 2d.
		// Without the fallback a fixed 30d rule would renew immediately (thrash).
		{"6d short-cert not due at 3d left", 6, 3, 30, false},
		{"6d short-cert due at 2d left", 6, 2, 30, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			notAfter := now.Add(time.Duration(tc.remainingDays) * day)
			notBefore := notAfter.Add(-time.Duration(tc.lifetimeDays) * day)
			got := renewDue(notBefore, notAfter, now, tc.renewBeforeDays)
			if got != tc.want {
				t.Fatalf("renewDue(lifetime=%dd, remaining=%dd, before=%dd) = %v, want %v",
					tc.lifetimeDays, tc.remainingDays, tc.renewBeforeDays, got, tc.want)
			}
		})
	}
}

// An unknown expiry (zero NotAfter) must never trigger a blind renewal.
func TestRenewDueUnknownExpiry(t *testing.T) {
	now := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	if renewDue(time.Time{}, time.Time{}, now, 30) {
		t.Fatal("zero NotAfter must not be due")
	}
}

// With no NotBefore (lifetime unknown) the plain N-day rule applies — no
// fallback, since we can't compute the lifetime fraction.
func TestRenewDueNoNotBeforeUsesPlainDays(t *testing.T) {
	now := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	day := 24 * time.Hour
	notAfter := now.Add(20 * day)
	if !renewDue(time.Time{}, notAfter, now, 30) {
		t.Fatal("20d left with 30d threshold and no NotBefore should be due")
	}
	notAfter = now.Add(40 * day)
	if renewDue(time.Time{}, notAfter, now, 30) {
		t.Fatal("40d left with 30d threshold should not be due")
	}
}
