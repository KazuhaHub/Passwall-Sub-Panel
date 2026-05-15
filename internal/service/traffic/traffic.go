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
	SetEnabledAndSync(ctx context.Context, userID int64, enabled bool, reason domain.AutoDisabledReason, detail string) error
}

type Service struct {
	users     ports.UserRepo
	ownership ports.OwnershipRepo
	traffic   ports.TrafficRepo
	pool      ports.XUIPool
	disabler  UserDisabler
}

type inboundKey struct {
	panelID   int64
	inboundID int
}

type ownershipRef struct {
	userID int64
	email  string
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
	users, err := s.listAllUsers(ctx)
	if err != nil {
		return err
	}

	byInbound := make(map[inboundKey][]ownershipRef)
	totals := make(map[int64]trafficTotals, len(users))
	skipUsers := make(map[int64]bool)
	for _, u := range users {
		totals[u.ID] = trafficTotals{}
		entries, err := s.ownership.ListByUser(ctx, u.ID)
		if err != nil {
			log.Warn("traffic poll ownership", "user_id", u.ID, "err", err)
			continue
		}
		for _, e := range entries {
			key := inboundKey{panelID: e.PanelID, inboundID: e.InboundID}
			byInbound[key] = append(byInbound[key], ownershipRef{userID: u.ID, email: e.ClientEmail})
		}
	}

	for key, refs := range byInbound {
		c, err := s.pool.Get(key.panelID)
		if err != nil {
			log.Warn("traffic poll panel", "panel_id", key.panelID, "inbound_id", key.inboundID, "err", err)
			markSkippedUsers(skipUsers, refs)
			continue
		}
		traffics, err := c.GetInboundTraffics(ctx, key.inboundID)
		if err != nil {
			log.Warn("traffic poll inbound", "panel_id", key.panelID, "inbound_id", key.inboundID, "err", err)
			markSkippedUsers(skipUsers, refs)
			continue
		}
		trafficByEmail := make(map[string]ports.ClientTraffic, len(traffics))
		for _, t := range traffics {
			trafficByEmail[t.Email] = t
		}
		for _, ref := range refs {
			t, ok := trafficByEmail[ref.email]
			if !ok {
				continue
			}
			total := totals[ref.userID]
			total.up += t.Up
			total.down += t.Down
			total.hits++
			totals[ref.userID] = total
		}
	}

	for _, u := range users {
		if skipUsers[u.ID] {
			log.Warn("traffic poll user skipped due to inbound fetch failure", "user_id", u.ID)
			continue
		}
		if err := s.recordAndEnforce(ctx, u, totals[u.ID]); err != nil {
			log.Warn("traffic poll user", "user_id", u.ID, "err", err)
		}
	}
	return nil
}

func markSkippedUsers(skipUsers map[int64]bool, refs []ownershipRef) {
	for _, ref := range refs {
		skipUsers[ref.userID] = true
	}
}

func (s *Service) listAllUsers(ctx context.Context) ([]*domain.User, error) {
	out := []*domain.User{}
	page := 1
	const pageSize = 100
	for {
		users, total, err := s.users.List(ctx, ports.UserFilter{
			Pagination: ports.Pagination{Page: page, PageSize: pageSize},
		})
		if err != nil {
			return nil, fmt.Errorf("list users: %w", err)
		}
		out = append(out, users...)
		if int64(page*pageSize) >= total {
			break
		}
		page++
	}
	return out, nil
}

type trafficTotals struct {
	up   int64
	down int64
	hits int
}

func (s *Service) recordAndEnforce(ctx context.Context, u *domain.User, totals trafficTotals) error {
	if totals.hits == 0 {
		// No 3X-UI rows for this user; still record a zero snapshot so the
		// dashboard can show "0 used today" instead of "no data".
	}

	now := time.Now()
	snap := &domain.TrafficSnapshot{
		UserID:     u.ID,
		UpBytes:    totals.up,
		DownBytes:  totals.down,
		TotalBytes: totals.up + totals.down,
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
			if err := s.disabler.SetEnabledAndSync(ctx, u.ID, true, domain.DisabledNone, ""); err != nil {
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
		if err := s.disabler.SetEnabledAndSync(ctx, u.ID, false, domain.DisabledTrafficExceeded, "traffic limit exceeded"); err != nil {
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

type HistoryPeriod string

const (
	HistoryDay   HistoryPeriod = "day"
	HistoryWeek  HistoryPeriod = "week"
	HistoryMonth HistoryPeriod = "month"
)

type HistoryItem struct {
	Date       string
	UpBytes    int64
	DownBytes  int64
	TotalBytes int64
}

type HistoryReport struct {
	UserID int64
	Period HistoryPeriod
	Since  string
	Until  string
	Items  []HistoryItem
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

func (s *Service) HistoryFor(ctx context.Context, userID int64, period HistoryPeriod, since, until time.Time) (*HistoryReport, error) {
	period, err := normalizeHistoryPeriod(period)
	if err != nil {
		return nil, err
	}
	since = startOfDay(since)
	until = startOfDay(until)
	if until.Before(since) {
		return nil, fmt.Errorf("%w: until must be on or after since", domain.ErrValidation)
	}
	untilExclusive := until.AddDate(0, 0, 1)

	snapshots, err := s.traffic.ListByUser(ctx, userID, since, untilExclusive)
	if err != nil {
		return nil, err
	}

	var prev *domain.TrafficSnapshot
	if base, err := s.traffic.LastBefore(ctx, userID, since); err == nil && base != nil {
		prev = base
	}
	prevUp, prevDown, prevTotal := snapshotCounters(prev)

	items := make([]HistoryItem, 0)
	idx := 0
	for bucketStart := bucketStartFor(since, period); bucketStart.Before(untilExclusive); bucketStart = nextBucketStart(bucketStart, period) {
		bucketEnd := nextBucketStart(bucketStart, period)
		if bucketEnd.After(untilExclusive) {
			bucketEnd = untilExclusive
		}

		var lastInBucket *domain.TrafficSnapshot
		for idx < len(snapshots) && snapshots[idx].CapturedAt.Before(bucketEnd) {
			if !snapshots[idx].CapturedAt.Before(since) {
				lastInBucket = snapshots[idx]
			}
			idx++
		}

		item := HistoryItem{Date: bucketLabel(bucketStart, period)}
		if lastInBucket != nil {
			item.UpBytes = deltaCounter(lastInBucket.UpBytes, prevUp)
			item.DownBytes = deltaCounter(lastInBucket.DownBytes, prevDown)
			item.TotalBytes = deltaCounter(lastInBucket.TotalBytes, prevTotal)
			prevUp = lastInBucket.UpBytes
			prevDown = lastInBucket.DownBytes
			prevTotal = lastInBucket.TotalBytes
		}
		items = append(items, item)
	}

	return &HistoryReport{
		UserID: userID,
		Period: period,
		Since:  since.Format("2006-01-02"),
		Until:  until.Format("2006-01-02"),
		Items:  items,
	}, nil
}

func normalizeHistoryPeriod(period HistoryPeriod) (HistoryPeriod, error) {
	switch period {
	case "", HistoryDay:
		return HistoryDay, nil
	case HistoryWeek, HistoryMonth:
		return period, nil
	default:
		return "", fmt.Errorf("%w: invalid history period", domain.ErrValidation)
	}
}

func startOfDay(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
}

func bucketStartFor(t time.Time, period HistoryPeriod) time.Time {
	t = startOfDay(t)
	switch period {
	case HistoryWeek:
		offset := (int(t.Weekday()) + 6) % 7
		return t.AddDate(0, 0, -offset)
	case HistoryMonth:
		return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, t.Location())
	default:
		return t
	}
}

func nextBucketStart(t time.Time, period HistoryPeriod) time.Time {
	switch period {
	case HistoryWeek:
		return t.AddDate(0, 0, 7)
	case HistoryMonth:
		return t.AddDate(0, 1, 0)
	default:
		return t.AddDate(0, 0, 1)
	}
}

func bucketLabel(t time.Time, period HistoryPeriod) string {
	if period == HistoryMonth {
		return t.Format("2006-01")
	}
	return t.Format("2006-01-02")
}

func snapshotCounters(s *domain.TrafficSnapshot) (up, down, total int64) {
	if s == nil {
		return 0, 0, 0
	}
	return s.UpBytes, s.DownBytes, s.TotalBytes
}

func deltaCounter(current, previous int64) int64 {
	delta := current - previous
	if delta < 0 {
		delta = current
	}
	if delta < 0 {
		return 0
	}
	return delta
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
		return s.disabler.SetEnabledAndSync(ctx, u.ID, false, domain.DisabledTrafficExceeded, "traffic limit exceeded")
	}
	if usedBytes < u.TrafficLimitBytes && !u.Enabled && u.AutoDisabledReason == domain.DisabledTrafficExceeded {
		return s.disabler.SetEnabledAndSync(ctx, u.ID, true, domain.DisabledNone, "")
	}
	return nil
}
