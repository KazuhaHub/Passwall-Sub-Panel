package risk

import (
	"context"
	"fmt"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/log"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/paneltz"
)

// usageShift judges every account's daily traffic against its own history
// (domain.EvaluateUsageShift) and appends one usage_shift row per account.
//
// Reads: the fleet's hourly sum once, then each judged account's hourly
// rows, all over the same 35 panel-local days. The fleet read feeds every
// account's fleet factor, so when it fails the kind is skipped — rows kept —
// for everyone whose verdict depends on data. An account whose signal is off
// needs no data and still reads "disabled", so the switch shows as taken
// even while the rollup is unreadable. One account's failed read costs only
// that account's row.
func (s *Service) usageShift(ctx context.Context, r *refresh) error {
	bounds := usageDayBounds(r.now, r.loc)
	since, until := bounds[0], bounds[domain.RiskUsageSeriesDays]
	endDate := paneltz.DateString(bounds[domain.RiskUsageSeriesDays-1], r.loc)

	var fleet [domain.RiskUsageSeriesDays]int64
	fleetOK := true
	if buckets, err := s.d.Traffic.SumHourlyAllUsers(ctx, since, until); err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("risk refresh: %w", ctx.Err())
		}
		fleetOK = false
		r.partial = true
		log.Warn("risk signals: fleet hourly traffic unreadable; usage_shift keeps its previous rows", "err", err)
	} else {
		fleet = dailyTotals(buckets, since, r.loc)
	}

	for _, u := range r.users {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("risk refresh: %w", err)
		}
		policy, ok := r.policies[u.GroupID]
		if !ok {
			continue // unreadable group: previous rows kept
		}
		p := usagePolicy(policy.risk)
		if p.Off {
			v, ev := domain.EvaluateUsageShift(p, domain.UsageShiftInput{})
			addVerdict(r, u.ID, domain.RiskKindUsageShift, v, ev)
			continue
		}
		if !fleetOK {
			continue
		}
		buckets, err := s.d.Traffic.ListHourlyByUser(ctx, u.ID, since, until)
		if err != nil {
			if ctx.Err() == nil {
				r.partial = true
				log.Warn("risk signals: hourly traffic unreadable; the account keeps its previous usage_shift row", "user_id", u.ID, "err", err)
			}
			continue
		}
		v, ev := domain.EvaluateUsageShift(p, domain.UsageShiftInput{
			EndDate:              endDate,
			User:                 dailyTotals(buckets, since, r.loc),
			Fleet:                fleet,
			HistoryRetentionDays: r.global.TrafficHistoryDays,
		})
		addVerdict(r, u.ID, domain.RiskKindUsageShift, v, ev)
	}
	return nil
}

// usagePolicy is the part of an account's risk policy usage_shift judges
// with.
func usagePolicy(p domain.RiskPolicy) domain.UsageShiftPolicy {
	return domain.UsageShiftPolicy{Off: p.UsageShiftOff, Ratio: p.UsageRatio, FloorBytes: p.UsageFloorBytes}
}

// usageDayBounds returns the local midnights b_0..b_35 of the 35 whole days
// before today: day k is [b_k, b_k+1), day 34 is yesterday, b_35 is the start
// of today. Today is left out because it is not over — a half day always
// reads low.
func usageDayBounds(now time.Time, loc *time.Location) [domain.RiskUsageSeriesDays + 1]time.Time {
	y, m, d := now.In(loc).Date()
	var b [domain.RiskUsageSeriesDays + 1]time.Time
	for k := range b {
		b[k] = time.Date(y, m, d-domain.RiskUsageSeriesDays+k, 0, 0, 0, 0, loc)
	}
	return b
}

// dailyTotals sums hourly buckets into the series' days by each bucket's
// START, read in the panel's zone. The rollup stores UTC bucket starts, so a
// UTC date would put a Shanghai evening on the wrong day. In a zone whose
// offset is not whole hours, a bucket straddling midnight counts to the day
// it starts in. Buckets outside the 35 days are dropped whatever the reader
// returned.
func dailyTotals(buckets []domain.HourlyTraffic, day0 time.Time, loc *time.Location) [domain.RiskUsageSeriesDays]int64 {
	var out [domain.RiskUsageSeriesDays]int64
	for _, b := range buckets {
		if k := domain.CivilDaysBetween(day0, b.BucketStart, loc); k >= 0 && k < domain.RiskUsageSeriesDays {
			out[k] += b.TotalBytes
		}
	}
	return out
}
