package middleware

import (
	"testing"
	"time"
)

func TestPerIPLimiterAllowsUpToLimit(t *testing.T) {
	l := NewPerIPLimiter(3, time.Minute)
	for i := 0; i < 3; i++ {
		if !l.Allow("1.2.3.4") {
			t.Fatalf("request %d should be allowed (limit=3)", i+1)
		}
	}
	if l.Allow("1.2.3.4") {
		t.Fatal("4th request should be blocked")
	}
}

func TestPerIPLimiterIndependentBuckets(t *testing.T) {
	l := NewPerIPLimiter(2, time.Minute)
	if !l.Allow("1.1.1.1") || !l.Allow("1.1.1.1") {
		t.Fatal("first IP should burn through its allowance cleanly")
	}
	if l.Allow("1.1.1.1") {
		t.Fatal("first IP should now be limited")
	}
	// Different IP must NOT share the bucket; otherwise one noisy IP
	// could rate-limit every other client behind a CDN.
	if !l.Allow("2.2.2.2") {
		t.Fatal("second IP should have its own allowance")
	}
}

func TestPerIPLimiterResetsAfterWindow(t *testing.T) {
	l := NewPerIPLimiter(1, 50*time.Millisecond)
	if !l.Allow("3.3.3.3") {
		t.Fatal("first request should pass")
	}
	if l.Allow("3.3.3.3") {
		t.Fatal("second request inside window should be blocked")
	}
	time.Sleep(80 * time.Millisecond)
	if !l.Allow("3.3.3.3") {
		t.Fatal("after window expiry the bucket should reset")
	}
}

func TestPerIPLimiterZeroLimitFallsBackToOne(t *testing.T) {
	// limit=0 would be a footgun (every request blocked); the
	// constructor clamps to 1.
	l := NewPerIPLimiter(0, time.Minute)
	if !l.Allow("4.4.4.4") {
		t.Fatal("limit=0 should be normalised to 1 — first request must pass")
	}
	if l.Allow("4.4.4.4") {
		t.Fatal("second request with effective limit=1 should be blocked")
	}
}
