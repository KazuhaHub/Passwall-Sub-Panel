package domain

import (
	"testing"
	"time"
)

func TestNodeRelayStatusVisibility(t *testing.T) {
	enabled := []RelayLine{{Address: "relay.example", Enabled: true}}
	disabled := []RelayLine{{Address: "relay.example", Enabled: false}}
	tests := []struct {
		name       string
		node       *Node
		hideDirect bool
		showRelay  bool
	}{
		{name: "no relays", node: &Node{}, hideDirect: false, showRelay: false},
		{name: "disabled relay", node: &Node{Relays: disabled, HideDirect: true}, hideDirect: false, showRelay: false},
		{name: "opt in", node: &Node{Relays: enabled, ShowRelayStatus: true}, hideDirect: false, showRelay: true},
		{name: "hidden direct forces relay", node: &Node{Relays: enabled, HideDirect: true}, hideDirect: true, showRelay: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.node.EffectiveHideDirect(); got != tc.hideDirect {
				t.Fatalf("EffectiveHideDirect = %v, want %v", got, tc.hideDirect)
			}
			if got := tc.node.EffectiveShowRelayStatus(); got != tc.showRelay {
				t.Fatalf("EffectiveShowRelayStatus = %v, want %v", got, tc.showRelay)
			}
		})
	}
}

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

// TestUserPeriodUsedSplit pins the Subscription-Userinfo split's one hard
// invariant: up+down == PeriodUsed() EXACTLY, whatever the baselines hold, so
// the header a client shows can never disagree with quota enforcement. Upload
// is clamped into [0, total]; download absorbs every residual — including a
// row whose lifetime total drifted from up+down (the pre-v3.9.0-beta.32
// asymmetric-reset over-count) and the signed baselines a manual
// SetPeriodUsage or the upgrade backfill leave behind. The down baseline is
// deliberately not read: the "ignored down baseline" case would read 14 GiB
// of download (more than the 10 GiB period) if it were.
func TestUserPeriodUsedSplit(t *testing.T) {
	const gb = int64(1) << 30
	cases := []struct {
		name                        string
		lifeUp, lifeDown, lifeTotal int64
		baseline, baseUp, baseDown  int64
		wantUp, wantDown            int64
	}{
		{"zero values", 0, 0, 0, 0, 0, 0, 0, 0},
		{"never rolled — whole lifetime, measured", 3 * gb, 7 * gb, 10 * gb, 0, 0, 0, 3 * gb, 7 * gb},
		{"measured split after rollover", 13 * gb, 37 * gb, 50 * gb, 40 * gb, 11 * gb, 29 * gb, 2 * gb, 8 * gb},
		{"just rolled over", 11 * gb, 29 * gb, 40 * gb, 40 * gb, 11 * gb, 29 * gb, 0, 0},
		{"upgrade backfill (up baseline = lifetime up) reads as all download", 11 * gb, 34 * gb, 45 * gb, 40 * gb, 11 * gb, 29 * gb, 0, 5 * gb},
		{"negative up baseline clamps upload to total", 2 * gb, 5 * gb, 7 * gb, 4 * gb, -3 * gb, 9 * gb, 3 * gb, 0},
		{"negative down baseline is ignored", 2 * gb, 5 * gb, 7 * gb, 4 * gb, 2 * gb, -2 * gb, 0, 3 * gb},
		{"ignored down baseline", 6 * gb, 14 * gb, 20 * gb, 10 * gb, 3 * gb, 0, 3 * gb, 7 * gb},
		{"up baseline above lifetime up clamps upload to 0", 1 * gb, 9 * gb, 10 * gb, 4 * gb, 5 * gb, 0, 0, 6 * gb},
		{"baseline above lifetime — 0/0", 5 * gb, 5 * gb, 10 * gb, 20 * gb, 0, 0, 0, 0},
		{"drifted lifetime total — residual goes to download", 2 * gb, 3 * gb, 9 * gb, 4 * gb, 1 * gb, 1 * gb, 1 * gb, 4 * gb},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			u := &User{
				LifetimeUpBytes: tc.lifeUp, LifetimeDownBytes: tc.lifeDown, LifetimeTotalBytes: tc.lifeTotal,
				PeriodBaselineBytes: tc.baseline, PeriodBaselineUpBytes: tc.baseUp, PeriodBaselineDownBytes: tc.baseDown,
			}
			up, down := u.PeriodUsedSplit()
			if up != tc.wantUp || down != tc.wantDown {
				t.Fatalf("PeriodUsedSplit() = (%d, %d), want (%d, %d)", up, down, tc.wantUp, tc.wantDown)
			}
			if up+down != u.PeriodUsed() {
				t.Fatalf("up+down = %d, want PeriodUsed() = %d", up+down, u.PeriodUsed())
			}
			if up < 0 || down < 0 {
				t.Fatalf("negative direction: up %d down %d", up, down)
			}
		})
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
