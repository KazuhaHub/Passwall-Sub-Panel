package traffic

import (
	"context"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// TestRecordSharedClientStats_AsymmetricResetNoOvercount pins the fix for a
// MEDIUM over-count: deltaTotal must be deltaUp + deltaDown, NOT an independent
// monotonicDelta on (up+down). Xray can reset the up and down counters
// independently; when only ONE resets, the synthetic total may not cross its own
// reset threshold, so an independent monotonicDelta on it folds the reset
// component straight into the quota-driving LifetimeTotalBytes.
//
// Prev raw up=1000 down=1000 total=2000. Now up RESETS to 50, down GROWS to 1100:
//   deltaUp   = monotonicDelta(50, 1000)   = 50   (reset → current)
//   deltaDown = monotonicDelta(1100, 1000) = 100
//   correct deltaTotal = 150
//   buggy   deltaTotal = monotonicDelta(1150, 2000) = 1150  (+1000 over-count)
func TestRecordSharedClientStats_AsymmetricResetNoOvercount(t *testing.T) {
	s := &Service{}
	sink := &pollSink{}
	c := &domain.PSPClient{
		ID:                 1,
		LifetimeUpBytes:    1000,
		LifetimeDownBytes:  1000,
		LifetimeTotalBytes: 2000,
		LastRawUpBytes:     1000,
		LastRawDownBytes:   1000,
		LastRawTotalBytes:  2000,
	}

	d := s.recordSharedClientStats(context.Background(), c, 50, 1100, sink)

	if d.up != 50 || d.down != 100 {
		t.Fatalf("component deltas = up %d down %d, want up 50 down 100", d.up, d.down)
	}
	if d.total != 150 {
		t.Fatalf("delta.total = %d, want 150 (deltaUp+deltaDown); ~1150 means the asymmetric reset was double-folded", d.total)
	}
	// Lifetime stays internally consistent: total == up + down.
	if c.LifetimeTotalBytes != 2150 || c.LifetimeTotalBytes != c.LifetimeUpBytes+c.LifetimeDownBytes {
		t.Fatalf("lifetime up=%d down=%d total=%d; want total 2150 == up+down",
			c.LifetimeUpBytes, c.LifetimeDownBytes, c.LifetimeTotalBytes)
	}
}
