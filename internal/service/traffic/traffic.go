// Package traffic implements the periodic traffic-collection job that
// powers the panel's usage dashboard and the auto-disable / auto-reenable
// behaviour around traffic quotas and reset periods.
package traffic

import (
	"context"
	"fmt"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/log"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// UserDisabler is the narrow subset of user.Service this package needs.
// Defined here to keep the import direction one-way.
type UserDisabler interface {
	SetEnabledAndSync(ctx context.Context, userID int64, enabled bool, reason domain.AutoDisabledReason) error
}

type Service struct {
	users    ports.UserRepo
	traffic  ports.TrafficRepo
	pool     ports.XUIPool
	disabler UserDisabler
}

func New(users ports.UserRepo, traffic ports.TrafficRepo, pool ports.XUIPool, disabler UserDisabler) *Service {
	return &Service{users: users, traffic: traffic, pool: pool, disabler: disabler}
}

// PollOnce walks every user, pulls aggregated traffic, writes a snapshot,
// and enforces quotas + period resets.
//
// Errors per user are logged; the overall pass keeps going so one bad user
// doesn't block the rest.
func (s *Service) PollOnce(ctx context.Context) error {
	page := 1
	const pageSize = 100
	for {
		users, total, err := s.users.List(ctx, ports.UserFilter{
			Pagination: ports.Pagination{Page: page, PageSize: pageSize},
		})
		if err != nil {
			return fmt.Errorf("list users: %w", err)
		}
		for _, u := range users {
			if err := s.pollUser(ctx, u); err != nil {
				log.Warn("traffic poll user", "user_id", u.ID, "err", err)
			}
		}
		if int64(page*pageSize) >= total {
			break
		}
		page++
	}
	return nil
}

func (s *Service) pollUser(ctx context.Context, u *domain.User) error {
	email := u.EmailForXUI()
	if email == "" {
		return nil
	}

	// Aggregate across panels. Most users live on one panel and the inner
	// loop runs once.
	var totalUp, totalDown int64
	hits := 0
	for _, panel := range s.pool.List() {
		c, err := s.pool.Get(panel)
		if err != nil {
			continue
		}
		traffics, err := c.GetClientTraffic(ctx, email)
		if err != nil {
			continue // email may not exist on this panel — try the next
		}
		for _, t := range traffics {
			totalUp += t.Up
			totalDown += t.Down
			hits++
		}
	}
	if hits == 0 {
		// No 3X-UI rows for this user; still record a zero snapshot so the
		// dashboard can show "0 used today" instead of "no data".
	}

	now := time.Now()
	snap := &domain.TrafficSnapshot{
		UserID:     u.ID,
		UpBytes:    totalUp,
		DownBytes:  totalDown,
		TotalBytes: totalUp + totalDown,
		CapturedAt: now,
	}
	if err := s.traffic.Insert(ctx, snap); err != nil {
		return fmt.Errorf("insert snapshot: %w", err)
	}

	// Roll the period if a boundary has been crossed.
	if u.TrafficPeriodStart != nil && shouldRollPeriod(now, *u.TrafficPeriodStart, u.TrafficResetPeriod) {
		u.TrafficPeriodStart = &now
		// If they were auto-disabled for traffic, the new period gives them
		// quota back — re-enable.
		if !u.Enabled && u.AutoDisabledReason == domain.DisabledTrafficExceeded {
			if err := s.disabler.SetEnabledAndSync(ctx, u.ID, true, domain.DisabledNone); err != nil {
				log.Warn("traffic re-enable", "user_id", u.ID, "err", err)
			}
		} else {
			if err := s.users.Update(ctx, u); err != nil {
				log.Warn("traffic period start update", "user_id", u.ID, "err", err)
			}
		}
		return nil
	}

	// Enforce limit
	if u.TrafficLimitBytes <= 0 || !u.Enabled {
		return nil
	}
	periodUsed, err := s.periodUsage(ctx, u, snap)
	if err != nil {
		return err
	}
	if periodUsed >= u.TrafficLimitBytes {
		if err := s.disabler.SetEnabledAndSync(ctx, u.ID, false, domain.DisabledTrafficExceeded); err != nil {
			return fmt.Errorf("auto-disable: %w", err)
		}
		log.Info("auto-disabled user (traffic exceeded)",
			"user_id", u.ID, "period_used", periodUsed, "limit", u.TrafficLimitBytes)
	}
	return nil
}

// periodUsage returns bytes used since the user's current period start.
// Falls back to the latest snapshot's total if no earlier snapshot exists
// (treats "no history" as "all usage is in this period").
func (s *Service) periodUsage(ctx context.Context, u *domain.User, latest *domain.TrafficSnapshot) (int64, error) {
	if u.TrafficPeriodStart == nil {
		return latest.TotalBytes, nil
	}
	baseSnap, err := s.traffic.LastBefore(ctx, u.ID, *u.TrafficPeriodStart)
	if err != nil || baseSnap == nil {
		return latest.TotalBytes, nil
	}
	used := latest.TotalBytes - baseSnap.TotalBytes
	if used < 0 {
		used = latest.TotalBytes
	}
	return used, nil
}

func shouldRollPeriod(now, periodStart time.Time, period domain.ResetPeriod) bool {
	switch period {
	case domain.ResetMonthly:
		return now.Year() != periodStart.Year() || now.Month() != periodStart.Month()
	case domain.ResetQuarterly:
		nowQ := (int(now.Month()) - 1) / 3
		psQ := (int(periodStart.Month()) - 1) / 3
		return now.Year() != periodStart.Year() || nowQ != psQ
	}
	return false
}

// UsageReport summarises a single user's traffic for the dashboard.
type UsageReport struct {
	UserID              int64
	PermanentTotalBytes int64
	PeriodUsedBytes     int64
	TodayUsedBytes      int64
}

// ReportFor returns the lifetime / current-period / today usage for one user.
func (s *Service) ReportFor(ctx context.Context, userID int64) (*UsageReport, error) {
	report := &UsageReport{UserID: userID}
	latest, err := s.traffic.LatestForUser(ctx, userID)
	if err != nil || latest == nil {
		return report, nil
	}
	report.PermanentTotalBytes = latest.TotalBytes

	now := time.Now()
	todayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	if base, err := s.traffic.LastBefore(ctx, userID, todayStart); err == nil && base != nil {
		report.TodayUsedBytes = latest.TotalBytes - base.TotalBytes
	} else {
		report.TodayUsedBytes = latest.TotalBytes
	}

	u, err := s.users.GetByID(ctx, userID)
	if err == nil && u.TrafficPeriodStart != nil {
		if base, err := s.traffic.LastBefore(ctx, userID, *u.TrafficPeriodStart); err == nil && base != nil {
			report.PeriodUsedBytes = latest.TotalBytes - base.TotalBytes
		} else {
			report.PeriodUsedBytes = latest.TotalBytes
		}
	}
	return report, nil
}
