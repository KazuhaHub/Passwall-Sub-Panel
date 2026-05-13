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
	users     ports.UserRepo
	ownership ports.OwnershipRepo
	traffic   ports.TrafficRepo
	pool      ports.XUIPool
	disabler  UserDisabler
}

func New(users ports.UserRepo, ownership ports.OwnershipRepo, traffic ports.TrafficRepo, pool ports.XUIPool, disabler UserDisabler) *Service {
	return &Service{users: users, ownership: ownership, traffic: traffic, pool: pool, disabler: disabler}
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
	// Walk the ownership table for this user: each entry has the (panel,
	// inbound, email) tuple actually stored in 3X-UI. Querying ownership
	// (rather than re-deriving the email) keeps traffic polling correct
	// even when the email-format setting changes between client creations.
	entries, err := s.ownership.ListByUser(ctx, u.ID)
	if err != nil {
		return fmt.Errorf("list ownership: %w", err)
	}
	var totalUp, totalDown int64
	hits := 0
	for _, e := range entries {
		c, err := s.pool.Get(e.PanelID)
		if err != nil {
			continue
		}
		traffics, err := c.GetClientTraffic(ctx, e.ClientEmail)
		if err != nil {
			continue
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

// SetPeriodUsage adjusts the current billing-period usage by moving the
// user's period baseline to "now". This keeps future 3X-UI poll results
// additive from the admin-selected value instead of being overwritten by the
// next cumulative snapshot.
func (s *Service) SetPeriodUsage(ctx context.Context, userID int64, usedBytes int64) error {
	if usedBytes < 0 {
		return fmt.Errorf("%w: traffic usage must be >= 0", domain.ErrValidation)
	}
	u, err := s.users.GetByID(ctx, userID)
	if err != nil {
		return err
	}

	var latestTotal int64
	if latest, err := s.traffic.LatestForUser(ctx, userID); err == nil && latest != nil {
		latestTotal = latest.TotalBytes
	}
	baseTotal := latestTotal - usedBytes
	if baseTotal < 0 {
		baseTotal = 0
	}
	currentTotal := baseTotal + usedBytes
	now := time.Now()
	periodStart := now
	baseAt := now.Add(-time.Millisecond)

	if err := s.traffic.Insert(ctx, &domain.TrafficSnapshot{
		UserID:     userID,
		DownBytes:  baseTotal,
		TotalBytes: baseTotal,
		CapturedAt: baseAt,
	}); err != nil {
		return err
	}
	if err := s.traffic.Insert(ctx, &domain.TrafficSnapshot{
		UserID:     userID,
		DownBytes:  currentTotal,
		TotalBytes: currentTotal,
		CapturedAt: now,
	}); err != nil {
		return err
	}

	u.TrafficPeriodStart = &periodStart
	if err := s.users.Update(ctx, u); err != nil {
		return err
	}
	if u.TrafficLimitBytes <= 0 {
		return nil
	}
	if usedBytes >= u.TrafficLimitBytes && u.Enabled {
		return s.disabler.SetEnabledAndSync(ctx, u.ID, false, domain.DisabledTrafficExceeded)
	}
	if usedBytes < u.TrafficLimitBytes && !u.Enabled && u.AutoDisabledReason == domain.DisabledTrafficExceeded {
		return s.disabler.SetEnabledAndSync(ctx, u.ID, true, domain.DisabledNone)
	}
	return nil
}
