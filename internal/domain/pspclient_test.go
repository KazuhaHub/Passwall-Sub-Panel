package domain

import "testing"

func TestPSPClientEmail(t *testing.T) {
	cases := []struct {
		userID int64
		suffix string
		domain string
		want   string
	}{
		{42, "", "psp.local", "u42@psp.local"},
		{42, "", "", "u42@psp.local"},                      // empty domain → default
		{42, "-c1", "example.com", "u42-c1@example.com"},   // SS-2022-128
		{7, "-k1a2b3c4d", "x.test", "u7-k1a2b3c4d@x.test"}, // flow-split hash suffix
	}
	for _, tc := range cases {
		got := PSPClientEmail(tc.userID, tc.suffix, EmailRules{Domain: tc.domain})
		if got != tc.want {
			t.Errorf("PSPClientEmail(%d, %q, %q) = %q, want %q", tc.userID, tc.suffix, tc.domain, got, tc.want)
		}
	}
}

func TestPSPClientPeriodUsedTotal(t *testing.T) {
	cases := []struct {
		lifetime, baseline, want int64
	}{
		{1000, 400, 600},
		{1000, 1000, 0},
		{500, 800, 0}, // baseline > lifetime (shouldn't happen) → floored at 0
		{0, 0, 0},
	}
	for _, tc := range cases {
		c := &PSPClient{LifetimeTotalBytes: tc.lifetime, PeriodBaselineTotalBytes: tc.baseline}
		if got := c.PeriodUsedTotal(); got != tc.want {
			t.Errorf("PeriodUsedTotal(lifetime=%d, baseline=%d) = %d, want %d", tc.lifetime, tc.baseline, got, tc.want)
		}
	}
}
