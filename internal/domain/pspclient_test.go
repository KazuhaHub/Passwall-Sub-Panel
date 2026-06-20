package domain

import "testing"

func TestPSPClientEmail(t *testing.T) {
	cases := []struct {
		userID    int64
		credClass int
		domain    string
		want      string
	}{
		{42, 0, "psp.local", "u42@psp.local"},
		{42, 0, "", "u42@psp.local"},          // empty domain → default
		{42, 1, "example.com", "u42-c1@example.com"},
		{7, 2, "x.test", "u7-c2@x.test"},
	}
	for _, tc := range cases {
		got := PSPClientEmail(tc.userID, tc.credClass, EmailRules{Domain: tc.domain})
		if got != tc.want {
			t.Errorf("PSPClientEmail(%d, %d, %q) = %q, want %q", tc.userID, tc.credClass, tc.domain, got, tc.want)
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
