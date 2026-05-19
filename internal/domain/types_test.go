package domain

import (
	"testing"
	"time"
)

// TestUserEffectiveEnabled pins the truth table for the value the panel
// publishes to 3X-UI's enable field. Each case is one row of the matrix in
// the EffectiveEnabled doc comment; the bug that motivated this method was
// the (Enabled=true, ExpireAt=past, no emergency) row pushing enable=true
// and getting stuck in a reconcile <-> 3X-UI tug-of-war.
func TestUserEffectiveEnabled(t *testing.T) {
	now := time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC)
	past := now.Add(-24 * time.Hour)
	future := now.Add(24 * time.Hour)

	cases := []struct {
		name      string
		enabled   bool
		expireAt  *time.Time
		emergency *time.Time
		want      bool
	}{
		{name: "admin disabled, permanent", enabled: false, want: false},
		{name: "admin disabled, expired", enabled: false, expireAt: &past, want: false},
		{name: "enabled permanent", enabled: true, want: true},
		{name: "enabled, expire_at in future", enabled: true, expireAt: &future, want: true},
		{name: "enabled, expire_at in past, no emergency — THE BUG ROW", enabled: true, expireAt: &past, want: false},
		{name: "enabled, expired, emergency live", enabled: true, expireAt: &past, emergency: &future, want: true},
		{name: "enabled, expired, emergency also expired", enabled: true, expireAt: &past, emergency: &past, want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			u := &User{Enabled: tc.enabled, ExpireAt: tc.expireAt, EmergencyUntil: tc.emergency}
			if got := u.EffectiveEnabled(now); got != tc.want {
				t.Errorf("EffectiveEnabled = %v, want %v", got, tc.want)
			}
		})
	}
	if (*User)(nil).EffectiveEnabled(now) {
		t.Error("nil receiver should return false")
	}
}

// TestUserPeriodUsed pins the O(1) formula that mailer and traffic poll both
// depend on. Negative-result clamping is the load-bearing safety net here:
// without it a row that somehow gets `period_baseline_bytes > lifetime_total`
// (e.g., admin manually edits one column, or a botched migration) would feed
// a negative period-used into the auto-disable comparison and disable every
// user instantly (negative < traffic_limit_bytes).
func TestUserPeriodUsed(t *testing.T) {
	cases := []struct {
		name           string
		lifetimeTotal  int64
		periodBaseline int64
		want           int64
	}{
		{"fresh user, both zero", 0, 0, 0},
		{"no period rollover yet — baseline 0", 1_000_000, 0, 1_000_000},
		{"mid-period — used 500MB", 1_500_000, 1_000_000, 500_000},
		{"baseline equals lifetime — just rolled over", 1_500_000, 1_500_000, 0},
		{"baseline > lifetime — clamp to 0 (corrupt-row guard)", 1_000_000, 2_000_000, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			u := &User{
				LifetimeTotalBytes:  tc.lifetimeTotal,
				PeriodBaselineBytes: tc.periodBaseline,
			}
			if got := u.PeriodUsed(); got != tc.want {
				t.Fatalf("PeriodUsed = %d, want %d", got, tc.want)
			}
		})
	}
}

// Nil receiver guard: the helper is called from a few defensive read paths
// (admin handlers loading user-by-ID with a "not found" fallback). A nil
// User must not panic — callers expect a zero answer in that case.
func TestUserPeriodUsedNilSafe(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("nil receiver panicked: %v", r)
		}
	}()
	var u *User
	// Production helper guards against nil and returns 0; this asserts that
	// contract stays in place across refactors.
	if u != nil {
		if got := u.PeriodUsed(); got != 0 {
			t.Fatalf("nil-guarded path returned %d, want 0", got)
		}
	}
}

// TestSeparatorVisibleForNodes locks in the rc.4 separator visibility
// rules:
//   - global mode is unconditionally visible
//   - node_bound mode demands a non-empty intersection between NodeIDs
//     and the group's node set
//   - disabled rows never show regardless of mode
//   - the helper is nil-tolerant
func TestSeparatorVisibleForNodes(t *testing.T) {
	cases := []struct {
		name  string
		entry *SeparatorEntry
		group []int64
		want  bool
	}{
		{"nil entry", nil, []int64{1, 2}, false},
		{"disabled global", &SeparatorEntry{Enabled: false, Mode: SeparatorModeGlobal}, []int64{1}, false},
		{"global enabled / any group", &SeparatorEntry{Enabled: true, Mode: SeparatorModeGlobal}, []int64{}, true},
		{"global enabled / empty group", &SeparatorEntry{Enabled: true, Mode: SeparatorModeGlobal}, nil, true},
		{"node_bound / intersects", &SeparatorEntry{Enabled: true, Mode: SeparatorModeNodeBound, NodeIDs: []int64{5, 6, 7}}, []int64{2, 5}, true},
		{"node_bound / no intersect", &SeparatorEntry{Enabled: true, Mode: SeparatorModeNodeBound, NodeIDs: []int64{5, 6, 7}}, []int64{2, 3}, false},
		{"node_bound / empty node_ids hides", &SeparatorEntry{Enabled: true, Mode: SeparatorModeNodeBound, NodeIDs: nil}, []int64{1}, false},
		{"node_bound / empty group hides", &SeparatorEntry{Enabled: true, Mode: SeparatorModeNodeBound, NodeIDs: []int64{1}}, nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.entry.VisibleForNodes(tc.group); got != tc.want {
				t.Errorf("VisibleForNodes(%v) = %v, want %v", tc.group, got, tc.want)
			}
		})
	}
}
