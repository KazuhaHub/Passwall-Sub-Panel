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
// follow the user's period baselines instead, including when snapshots are
// absent. upload/download come from User.PeriodUsedSplit, so their sum is
// always PeriodUsed() — the figure quota enforcement trusts. A just-backfilled
// row (up baseline = lifetime up) reports exactly the pre-split #279 output
// (upload=0, download=period usage); after that only its pre-upgrade usage
// stays attributed to download until it rolls, and later upload is reported
// as upload.
func TestBuildSubInfo_PeriodUsage(t *testing.T) {
	const gb = int64(1) << 30
	start := time.Date(2026, time.October, 1, 0, 0, 0, 0, time.UTC)
	cases := []struct {
		name                       string
		lifeUp, lifeDown, lifetime int64
		baseline, baseUp, baseDown int64
		limit                      int64
		snapshot                   *domain.TrafficSnapshot
		wantUp, wantDown           int64
	}{
		{"monthly rollover", 10 * gb, 30 * gb, 40 * gb, 40 * gb, 10 * gb, 30 * gb, 50 * gb, &domain.TrafficSnapshot{UpBytes: 10 * gb, DownBytes: 30 * gb}, 0, 0},
		{"measured split after rollover", 11 * gb, 34 * gb, 45 * gb, 40 * gb, 10 * gb, 30 * gb, 50 * gb, &domain.TrafficSnapshot{UpBytes: 11 * gb, DownBytes: 34 * gb}, gb, 4 * gb},
		{"first period", gb, 4 * gb, 5 * gb, 0, 0, 0, 50 * gb, &domain.TrafficSnapshot{UpBytes: gb, DownBytes: 4 * gb}, gb, 4 * gb},
		{"missing snapshot", 11 * gb, 34 * gb, 45 * gb, 40 * gb, 10 * gb, 30 * gb, 50 * gb, nil, gb, 4 * gb},
		{"pre-upgrade backfilled row reads as all download", 11 * gb, 34 * gb, 45 * gb, 40 * gb, 11 * gb, 29 * gb, 50 * gb, nil, 0, 5 * gb},
		{"backfilled row with post-upgrade upload", 12 * gb, 34 * gb, 46 * gb, 40 * gb, 11 * gb, 29 * gb, 50 * gb, nil, gb, 5 * gb},
		{"manual usage override", gb, gb, 2 * gb, -3 * gb, -gb, -2 * gb, 50 * gb, nil, 2 * gb, 3 * gb},
		{"residual absorbed by download", 2 * gb, 3 * gb, 9 * gb, 4 * gb, gb, gb, 50 * gb, nil, gb, 4 * gb},
		{"upload clamped to period total", 2 * gb, 5 * gb, 7 * gb, 4 * gb, -3 * gb, 9 * gb, 50 * gb, nil, 3 * gb, 0},
		{"baseline above lifetime", gb, gb, 2 * gb, 3 * gb, 0, 0, 50 * gb, nil, 0, 0},
		{"unlimited", 11 * gb, 34 * gb, 45 * gb, 40 * gb, 10 * gb, 30 * gb, 0, nil, gb, 4 * gb},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := &Service{repos: ports.Repos{Traffic: subInfoTrafficRepo{snapshot: tc.snapshot}}}
			u := &domain.User{
				ID: 1, TrafficLimitBytes: tc.limit,
				LifetimeUpBytes: tc.lifeUp, LifetimeDownBytes: tc.lifeDown, LifetimeTotalBytes: tc.lifetime,
				PeriodBaselineBytes: tc.baseline, PeriodBaselineUpBytes: tc.baseUp, PeriodBaselineDownBytes: tc.baseDown,
				TrafficResetPeriod: domain.ResetMonthly, TrafficPeriodStart: &start,
			}
			if tc.wantUp+tc.wantDown != u.PeriodUsed() {
				t.Fatalf("bad case: want %d+%d != PeriodUsed() %d", tc.wantUp, tc.wantDown, u.PeriodUsed())
			}
			want := fmt.Sprintf("upload=%d; download=%d; total=%d", tc.wantUp, tc.wantDown, tc.limit)
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
			u := &domain.User{ID: 1, TrafficLimitBytes: 50, LifetimeUpBytes: 3, LifetimeDownBytes: 4, LifetimeTotalBytes: 7, ExpireAt: tc.expiry}
			want := "upload=3; download=4; total=50" + tc.suffix
			if got := svc.buildSubInfo(context.Background(), u); got != want {
				t.Fatalf("Subscription-Userinfo = %q, want %q", got, want)
			}
		})
	}
}
