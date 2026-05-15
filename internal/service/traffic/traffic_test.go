package traffic

import (
	"context"
	"sort"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

type fakeTrafficRepo struct {
	snapshots []*domain.TrafficSnapshot
}

func (r *fakeTrafficRepo) Insert(ctx context.Context, s *domain.TrafficSnapshot) error {
	r.snapshots = append(r.snapshots, s)
	return nil
}

func (r *fakeTrafficRepo) LatestForUser(ctx context.Context, userID int64) (*domain.TrafficSnapshot, error) {
	var latest *domain.TrafficSnapshot
	for _, s := range r.snapshots {
		if s.UserID != userID {
			continue
		}
		if latest == nil || s.CapturedAt.After(latest.CapturedAt) {
			latest = s
		}
	}
	if latest == nil {
		return nil, domain.ErrNotFound
	}
	return latest, nil
}

func (r *fakeTrafficRepo) LastBefore(ctx context.Context, userID int64, before time.Time) (*domain.TrafficSnapshot, error) {
	var latest *domain.TrafficSnapshot
	for _, s := range r.snapshots {
		if s.UserID != userID || !s.CapturedAt.Before(before) {
			continue
		}
		if latest == nil || s.CapturedAt.After(latest.CapturedAt) {
			latest = s
		}
	}
	if latest == nil {
		return nil, domain.ErrNotFound
	}
	return latest, nil
}

func (r *fakeTrafficRepo) ListByUser(ctx context.Context, userID int64, since, until time.Time) ([]*domain.TrafficSnapshot, error) {
	out := []*domain.TrafficSnapshot{}
	for _, s := range r.snapshots {
		if s.UserID == userID && !s.CapturedAt.Before(since) && s.CapturedAt.Before(until) {
			out = append(out, s)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].CapturedAt.Before(out[j].CapturedAt)
	})
	return out, nil
}

func snap(userID int64, at string, up, down int64) *domain.TrafficSnapshot {
	t, err := time.ParseInLocation("2006-01-02 15:04", at, time.Local)
	if err != nil {
		panic(err)
	}
	return &domain.TrafficSnapshot{
		UserID:     userID,
		UpBytes:    up,
		DownBytes:  down,
		TotalBytes: up + down,
		CapturedAt: t,
	}
}

func day(date string) time.Time {
	t, err := time.ParseInLocation("2006-01-02", date, time.Local)
	if err != nil {
		panic(err)
	}
	return t
}

func TestHistoryForUsesBaselineBeforeSince(t *testing.T) {
	repo := &fakeTrafficRepo{snapshots: []*domain.TrafficSnapshot{
		snap(1, "2026-04-30 23:55", 40, 60),
		snap(1, "2026-05-01 12:00", 70, 100),
		snap(1, "2026-05-02 12:00", 90, 130),
	}}
	svc := New(nil, nil, repo, nil, nil)

	report, err := svc.HistoryFor(context.Background(), 1, HistoryDay, day("2026-05-01"), day("2026-05-02"))
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Items) != 2 {
		t.Fatalf("items len = %d, want 2", len(report.Items))
	}
	if got := report.Items[0].TotalBytes; got != 70 {
		t.Fatalf("first day total = %d, want 70", got)
	}
	if got := report.Items[1].TotalBytes; got != 50 {
		t.Fatalf("second day total = %d, want 50", got)
	}
}

func TestHistoryForFillsEmptyBuckets(t *testing.T) {
	repo := &fakeTrafficRepo{snapshots: []*domain.TrafficSnapshot{
		snap(1, "2026-05-01 12:00", 10, 20),
		snap(1, "2026-05-03 12:00", 30, 60),
	}}
	svc := New(nil, nil, repo, nil, nil)

	report, err := svc.HistoryFor(context.Background(), 1, HistoryDay, day("2026-05-01"), day("2026-05-03"))
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Items) != 3 {
		t.Fatalf("items len = %d, want 3", len(report.Items))
	}
	if got := report.Items[1].TotalBytes; got != 0 {
		t.Fatalf("empty day total = %d, want 0", got)
	}
	if got := report.Items[2].TotalBytes; got != 60 {
		t.Fatalf("third day total = %d, want 60", got)
	}
}

func TestHistoryForHandlesCounterReset(t *testing.T) {
	repo := &fakeTrafficRepo{snapshots: []*domain.TrafficSnapshot{
		snap(1, "2026-04-30 23:55", 200, 300),
		snap(1, "2026-05-01 12:00", 20, 30),
	}}
	svc := New(nil, nil, repo, nil, nil)

	report, err := svc.HistoryFor(context.Background(), 1, HistoryDay, day("2026-05-01"), day("2026-05-01"))
	if err != nil {
		t.Fatal(err)
	}
	if got := report.Items[0].TotalBytes; got != 50 {
		t.Fatalf("reset day total = %d, want 50", got)
	}
}

func TestHistoryForWeekAndMonthLabels(t *testing.T) {
	repo := &fakeTrafficRepo{snapshots: []*domain.TrafficSnapshot{
		snap(1, "2026-05-15 12:00", 10, 20),
	}}
	svc := New(nil, nil, repo, nil, nil)

	weekly, err := svc.HistoryFor(context.Background(), 1, HistoryWeek, day("2026-05-15"), day("2026-05-15"))
	if err != nil {
		t.Fatal(err)
	}
	if got := weekly.Items[0].Date; got != "2026-05-11" {
		t.Fatalf("week label = %s, want 2026-05-11", got)
	}

	monthly, err := svc.HistoryFor(context.Background(), 1, HistoryMonth, day("2026-05-15"), day("2026-05-15"))
	if err != nil {
		t.Fatal(err)
	}
	if got := monthly.Items[0].Date; got != "2026-05" {
		t.Fatalf("month label = %s, want 2026-05", got)
	}
}
