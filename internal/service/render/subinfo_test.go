package render

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

type subInfoTrafficRepo struct {
	ports.TrafficRepo
	snapshot *domain.TrafficSnapshot
}

func (r subInfoTrafficRepo) LatestForUser(context.Context, int64) (*domain.TrafficSnapshot, error) {
	return r.snapshot, nil
}

// Historical snapshots stay cumulative after rollover. Subscription usage must
// follow the user's period baseline instead, including when snapshots are absent.
func TestBuildSubInfo_PeriodUsage(t *testing.T) {
	const gb = int64(1) << 30
	start := time.Date(2026, time.October, 1, 0, 0, 0, 0, time.UTC)
	cases := []struct {
		name     string
		lifetime int64
		baseline int64
		limit    int64
		snapshot *domain.TrafficSnapshot
		wantUsed int64
	}{
		{"monthly rollover", 40 * gb, 40 * gb, 50 * gb, &domain.TrafficSnapshot{UpBytes: 10 * gb, DownBytes: 30 * gb}, 0},
		{"new month usage", 45 * gb, 40 * gb, 50 * gb, &domain.TrafficSnapshot{UpBytes: 11 * gb, DownBytes: 34 * gb}, 5 * gb},
		{"first period", 5 * gb, 0, 50 * gb, &domain.TrafficSnapshot{UpBytes: gb, DownBytes: 4 * gb}, 5 * gb},
		{"missing snapshot", 45 * gb, 40 * gb, 50 * gb, nil, 5 * gb},
		{"manual usage override", 2 * gb, -3 * gb, 50 * gb, nil, 5 * gb},
		{"baseline above lifetime", 2 * gb, 3 * gb, 50 * gb, nil, 0},
		{"unlimited", 45 * gb, 40 * gb, 0, nil, 5 * gb},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := &Service{repos: ports.Repos{Traffic: subInfoTrafficRepo{snapshot: tc.snapshot}}}
			u := &domain.User{
				ID: 1, TrafficLimitBytes: tc.limit,
				LifetimeTotalBytes: tc.lifetime, PeriodBaselineBytes: tc.baseline,
				TrafficResetPeriod: domain.ResetMonthly, TrafficPeriodStart: &start,
			}
			want := fmt.Sprintf("upload=0; download=%d; total=%d", tc.wantUsed, tc.limit)
			if got := svc.buildSubInfo(context.Background(), u); got != want {
				t.Fatalf("Subscription-Userinfo = %q, want %q", got, want)
			}
		})
	}
}

func TestBuildSubInfo_Expiry(t *testing.T) {
	expiry := time.Date(2027, time.January, 1, 0, 0, 0, 0, time.UTC)
	zero := time.Time{}
	svc := &Service{repos: ports.Repos{Traffic: subInfoTrafficRepo{}}}
	for _, tc := range []struct {
		name   string
		expiry *time.Time
		suffix string
	}{
		{"no expiry", nil, ""},
		{"zero expiry", &zero, ""},
		{"real expiry", &expiry, fmt.Sprintf("; expire=%d", expiry.Unix())},
	} {
		t.Run(tc.name, func(t *testing.T) {
			u := &domain.User{ID: 1, TrafficLimitBytes: 50, LifetimeTotalBytes: 7, ExpireAt: tc.expiry}
			want := "upload=0; download=7; total=50" + tc.suffix
			if got := svc.buildSubInfo(context.Background(), u); got != want {
				t.Fatalf("Subscription-Userinfo = %q, want %q", got, want)
			}
		})
	}
}
