package app

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/adapters/sqlstore"
	"github.com/KazuhaHub/passwall-sub-panel/internal/config"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// TestHourAlignedCutoff pins that the raw-snapshot prune cutoff lands on a UTC
// hour boundary and never later than (now - retentionDays). Whole-hour-aligned
// deletes are what keep rollup from re-aggregating a partially-pruned hour into
// a smaller (regressed) hourly bucket — see pruneTrafficSnapshots.
func TestHourAlignedCutoff(t *testing.T) {
	const days = 7
	cases := []time.Time{
		time.Date(2026, 5, 21, 14, 37, 12, 500, time.UTC),
		time.Date(2026, 5, 21, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 3, 1, 23, 59, 59, 0, time.UTC),
		// A non-UTC input must still produce a UTC hour floor.
		time.Date(2026, 5, 21, 14, 37, 0, 0, time.FixedZone("X", 5*3600)),
	}
	for _, now := range cases {
		got := hourAlignedCutoff(now, days)

		if got.Location() != time.UTC {
			t.Fatalf("now=%v: cutoff location = %v, want UTC", now, got.Location())
		}
		if got.Minute() != 0 || got.Second() != 0 || got.Nanosecond() != 0 {
			t.Fatalf("now=%v: cutoff %v is not hour-aligned", now, got)
		}
		// The cutoff is the hour floor of (now - days), so it must be <= that
		// instant and strictly within the same hour.
		want := now.AddDate(0, 0, -days).UTC()
		if got.After(want) {
			t.Fatalf("now=%v: cutoff %v is after now-%dd %v", now, got, days, want)
		}
		if want.Sub(got) >= time.Hour {
			t.Fatalf("now=%v: cutoff %v more than an hour before now-%dd %v", now, got, days, want)
		}
	}
}

// connHistoryStore records what the hourly prune asked the
// connection_history store to do.
type connHistoryStore struct {
	mu        sync.Mutex
	cutoffs   []time.Time
	purges    int
	deleteErr error
}

func (s *connHistoryStore) DeleteBefore(_ context.Context, cutoff time.Time) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cutoffs = append(s.cutoffs, cutoff)
	return 3, s.deleteErr
}

func (s *connHistoryStore) PurgeOrphans(context.Context) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.purges++
	return 1, nil
}

func (s *connHistoryStore) calls() ([]time.Time, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]time.Time(nil), s.cutoffs...), s.purges
}

// retentionSettings is a settings repo holding one stored value, or failing.
type retentionSettings struct {
	stored ports.UISettings
	err    error
}

func (s retentionSettings) Load(context.Context, ports.UISettings) (ports.UISettings, error) {
	return s.stored, s.err
}
func (retentionSettings) Save(context.Context, ports.UISettings) error { return nil }

// connection_history holds IP addresses, so its retention is a promise, not a
// preference: risk.connection_retention_days, a week unless set, at most 90
// days — and never "keep forever", which is what 0 means for the sub-log
// retention and must not mean here. A stored 0 or a negative number prunes
// at the default week; a value past the privacy bound prunes at 90 days.
//
// A settings outage skips the retention pass (the next hour retries) rather
// than pruning at the default: an admin who keeps 90 days would otherwise
// lose 83 of them to one failed read. The orphan purge needs no setting and
// runs regardless, so a deleted account's addresses are gone within the hour
// whatever else fails.
func TestPruneConnectionHistory_UsesTheSettingAndNeverKeepsForever(t *testing.T) {
	day := 24 * time.Hour
	for _, c := range []struct {
		name     string
		settings ports.SettingsRepo
		want     time.Duration // 0: no retention pass
	}{
		{"unset is a week", retentionSettings{}, 7 * day},
		{"zero is a week, not forever", retentionSettings{stored: ports.UISettings{RiskConnectionRetentionDays: 0, SubLogRetentionDays: 0}}, 7 * day},
		{"negative is a week", retentionSettings{stored: ports.UISettings{RiskConnectionRetentionDays: -3}}, 7 * day},
		{"a configured month", retentionSettings{stored: ports.UISettings{RiskConnectionRetentionDays: 30}}, 30 * day},
		{"past the privacy bound is 90 days", retentionSettings{stored: ports.UISettings{RiskConnectionRetentionDays: 500}}, 90 * day},
		{"a settings outage skips the retention pass", retentionSettings{err: errors.New("db down")}, 0},
		{"no settings repo is the default", nil, 7 * day},
	} {
		t.Run(c.name, func(t *testing.T) {
			store := &connHistoryStore{}
			a := &App{connHistory: store}
			if c.settings != nil {
				a.settings = c.settings
			}
			before := time.Now()
			a.pruneConnectionHistory(context.Background())
			after := time.Now()

			cutoffs, purges := store.calls()
			if purges != 1 {
				t.Errorf("orphan purge ran %d times, want 1", purges)
			}
			if c.want == 0 {
				if len(cutoffs) != 0 {
					t.Fatalf("pruned at %v on a settings outage; want the pass skipped", cutoffs)
				}
				return
			}
			if len(cutoffs) != 1 {
				t.Fatalf("DeleteBefore called %d times, want 1", len(cutoffs))
			}
			if lo, hi := before.Add(-c.want), after.Add(-c.want); cutoffs[0].Before(lo) || cutoffs[0].After(hi) {
				t.Fatalf("cutoff %v, want now - %v (between %v and %v)", cutoffs[0], c.want, lo, hi)
			}
		})
	}
}

// A failed prune still purges orphans: the two passes protect different
// promises, and one failing must not cost the other.
func TestPruneConnectionHistory_PurgesOrphansWhenThePruneFails(t *testing.T) {
	store := &connHistoryStore{deleteErr: errors.New("db down")}
	a := &App{connHistory: store, settings: retentionSettings{}}
	a.pruneConnectionHistory(context.Background())
	if cutoffs, purges := store.calls(); len(cutoffs) != 1 || purges != 1 {
		t.Fatalf("prune %d, purge %d; want both attempted once", len(cutoffs), purges)
	}
}

// The prune is only as good as the loop that runs it: the hourly cleanup
// loop runs it on its first pass, at start, like every other retention.
func TestAuditCleanupLoopPrunesConnectionHistory(t *testing.T) {
	store := &connHistoryStore{}
	a := &App{connHistory: store, settings: retentionSettings{}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { defer close(done); a.runAuditCleanupLoop(ctx) }()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cutoffs, purges := store.calls(); len(cutoffs) == 1 && purges == 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if cutoffs, purges := store.calls(); len(cutoffs) != 1 || purges != 1 {
		t.Fatalf("after the first pass: prune %d, purge %d; want 1 and 1", len(cutoffs), purges)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("cleanup loop did not exit on context cancel")
	}
}

// Build hands the loop the real store: a row last seen past the retention is
// gone after one pass, and a current one stays. Without the wiring the prune
// returns at once on a nil store and the table grows for ever — silently,
// since nothing reads its size.
func TestBuildPrunesConnectionHistory(t *testing.T) {
	ctx := t.Context()
	directory := t.TempDir()
	cfg := &config.Config{
		Listen: "127.0.0.1:0", JWTSecret: strings.Repeat("j", 48), EncryptionKey: strings.Repeat("e", 48),
		ConfigDir: filepath.Join(directory, "config"), DataDir: filepath.Join(directory, "data"),
	}
	a, err := Build(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := a.Shutdown(shutdownCtx); err != nil {
			t.Error(err)
		}
		sqlstore.ConfigureSecretKey("")
	})
	u := &domain.User{
		UPN: "history@example.test", Email: "history@example.test", SSOProvider: domain.SSOProviderLocal,
		SSOSubject: "history@example.test", Role: domain.RoleUser, Enabled: true,
		UUID: "66666666-6666-4666-8666-666666666666", SubToken: "fixture-history-subscription-token",
		TrafficResetPeriod: domain.ResetMonthly,
	}
	if err := a.repos.User.Create(ctx, u); err != nil {
		t.Fatal(err)
	}

	// The store is not reachable from outside App, so this opens the same
	// database file Build opened.
	db, err := sqlstore.Open(cfg.DBKind(), cfg.DBDSN())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
	repo := sqlstore.NewConnectionHistoryRepo(db)
	now := time.Now()
	for _, c := range []struct {
		ip string
		at time.Time
	}{
		{"198.51.100.1", now.Add(-8 * 24 * time.Hour)},
		{"198.51.100.2", now.Add(-time.Hour)},
	} {
		if err := repo.Record(ctx, []domain.LiveConnection{{UserID: u.ID, PanelID: 1, SourceKey: c.ip, IP: c.ip}}, c.at); err != nil {
			t.Fatal(err)
		}
	}

	a.pruneConnectionHistory(ctx)

	rows, total, err := repo.List(ctx, ports.ConnectionHistoryFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(rows) != 1 || rows[0].IP != "198.51.100.2" {
		t.Fatalf("after the prune: %+v (total %d); want only the source seen an hour ago — is the store wired into App?", rows, total)
	}
}

// flag_records is the history of attention changes, kept by
// risk.flag_record_retention_days: 90 days unless set, at most ten years,
// and — like the connection history — never "keep forever": 0 or a negative
// number prunes at the default. A settings outage skips the retention pass
// rather than pruning a longer configured history at the default; the
// orphan purge runs regardless, so a deleted account's records are gone
// within the hour.
func TestPruneFlagRecords_UsesTheSetting(t *testing.T) {
	day := 24 * time.Hour
	for _, c := range []struct {
		name     string
		settings ports.SettingsRepo
		want     time.Duration // 0: no retention pass
	}{
		{"unset is 90 days", retentionSettings{}, 90 * day},
		{"zero is 90 days, not forever", retentionSettings{stored: ports.UISettings{RiskFlagRecordRetentionDays: 0}}, 90 * day},
		{"negative is 90 days", retentionSettings{stored: ports.UISettings{RiskFlagRecordRetentionDays: -3}}, 90 * day},
		{"a configured year", retentionSettings{stored: ports.UISettings{RiskFlagRecordRetentionDays: 365}}, 365 * day},
		{"past ten years is ten years", retentionSettings{stored: ports.UISettings{RiskFlagRecordRetentionDays: 5000}}, 3650 * day},
		{"the connection history's setting is not this one", retentionSettings{stored: ports.UISettings{RiskConnectionRetentionDays: 3}}, 90 * day},
		{"a settings outage skips the retention pass", retentionSettings{err: errors.New("db down")}, 0},
		{"no settings repo is the default", nil, 90 * day},
	} {
		t.Run(c.name, func(t *testing.T) {
			store := &connHistoryStore{}
			a := &App{flagRecords: store}
			if c.settings != nil {
				a.settings = c.settings
			}
			before := time.Now()
			a.pruneFlagRecords(context.Background())
			after := time.Now()

			cutoffs, purges := store.calls()
			if purges != 1 {
				t.Errorf("orphan purge ran %d times, want 1", purges)
			}
			if c.want == 0 {
				if len(cutoffs) != 0 {
					t.Fatalf("pruned at %v on a settings outage; want the pass skipped", cutoffs)
				}
				return
			}
			if len(cutoffs) != 1 {
				t.Fatalf("DeleteBefore called %d times, want 1", len(cutoffs))
			}
			if lo, hi := before.Add(-c.want), after.Add(-c.want); cutoffs[0].Before(lo) || cutoffs[0].After(hi) {
				t.Fatalf("cutoff %v, want now - %v (between %v and %v)", cutoffs[0], c.want, lo, hi)
			}
		})
	}
}

// A failed prune still purges orphans, as for the connection history.
func TestPruneFlagRecords_PurgesOrphansWhenThePruneFails(t *testing.T) {
	store := &connHistoryStore{deleteErr: errors.New("db down")}
	a := &App{flagRecords: store, settings: retentionSettings{}}
	a.pruneFlagRecords(context.Background())
	if cutoffs, purges := store.calls(); len(cutoffs) != 1 || purges != 1 {
		t.Fatalf("prune %d, purge %d; want both attempted once", len(cutoffs), purges)
	}
}

// The hourly cleanup loop runs the flag-record prune on its first pass.
func TestAuditCleanupLoopPrunesFlagRecords(t *testing.T) {
	store := &connHistoryStore{}
	a := &App{flagRecords: store, settings: retentionSettings{}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { defer close(done); a.runAuditCleanupLoop(ctx) }()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cutoffs, purges := store.calls(); len(cutoffs) == 1 && purges == 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if cutoffs, purges := store.calls(); len(cutoffs) != 1 || purges != 1 {
		t.Fatalf("after the first pass: prune %d, purge %d; want 1 and 1", len(cutoffs), purges)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("cleanup loop did not exit on context cancel")
	}
}

// Build hands the loop the real store: a record older than the retention is
// gone after one pass, a current one stays, and a deleted account's record
// goes whatever its age. Without the wiring the prune returns at once on a
// nil store and the history grows for ever.
func TestBuildPrunesFlagRecords(t *testing.T) {
	ctx := t.Context()
	directory := t.TempDir()
	cfg := &config.Config{
		Listen: "127.0.0.1:0", JWTSecret: strings.Repeat("j", 48), EncryptionKey: strings.Repeat("e", 48),
		ConfigDir: filepath.Join(directory, "config"), DataDir: filepath.Join(directory, "data"),
	}
	a, err := Build(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := a.Shutdown(shutdownCtx); err != nil {
			t.Error(err)
		}
		sqlstore.ConfigureSecretKey("")
	})
	newUser := func(n int) *domain.User {
		t.Helper()
		u := &domain.User{
			UPN: fmt.Sprintf("flags-%d@example.test", n), Email: fmt.Sprintf("flags-%d@example.test", n),
			SSOProvider: domain.SSOProviderLocal, SSOSubject: fmt.Sprintf("flags-%d@example.test", n),
			Role: domain.RoleUser, Enabled: true, UUID: fmt.Sprintf("77777777-7777-4777-8777-%012d", n),
			SubToken: fmt.Sprintf("fixture-flags-subscription-token-%d", n), TrafficResetPeriod: domain.ResetMonthly,
		}
		if err := a.repos.User.Create(ctx, u); err != nil {
			t.Fatal(err)
		}
		return u
	}
	kept, gone := newUser(1), newUser(2)

	db, err := sqlstore.Open(cfg.DBKind(), cfg.DBDSN())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
	repo := sqlstore.NewFlagRecordRepo(db)
	now := time.Now()
	flag := func(uid int64, ev domain.FlagEvent, at time.Time) domain.FlagRecord {
		return domain.FlagRecord{UserID: uid, Source: domain.FlagSourceGeo, Event: ev, Level: domain.FlagLevelSuspect, AtMS: at.UnixMilli()}
	}
	if err := repo.Append(ctx, []domain.FlagRecord{
		flag(kept.ID, domain.FlagEnterSuspect, now.Add(-91*24*time.Hour)),
		flag(kept.ID, domain.FlagEnterFlagged, now.Add(-time.Hour)),
		flag(gone.ID, domain.FlagEnterSuspect, now.Add(-time.Hour)),
	}); err != nil {
		t.Fatal(err)
	}
	if err := a.repos.User.Delete(ctx, gone.ID); err != nil {
		t.Fatal(err)
	}

	a.pruneFlagRecords(ctx)

	var n int64
	if err := db.Raw("SELECT COUNT(*) FROM flag_records").Scan(&n).Error; err != nil {
		t.Fatal(err)
	}
	rows, total, err := repo.List(ctx, ports.FlagRecordFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 || total != 1 || len(rows) != 1 || rows[0].Event != domain.FlagEnterFlagged {
		t.Fatalf("after the prune: %d rows stored, listed %+v; want only the current record of the existing account — is the store wired into App?", n, rows)
	}
}

// risk_reviews has no foreign key to users: a deleted account's review row
// would outlive it, holding an admin's note about somebody nobody can open.
// The hourly cleanup loop purges those on its first pass — no retention, a
// review row in force is current state, not history.
func TestAuditCleanupLoopPurgesRiskReviewOrphans(t *testing.T) {
	store := &connHistoryStore{}
	a := &App{riskReviews: store}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { defer close(done); a.runAuditCleanupLoop(ctx) }()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, purges := store.calls(); purges == 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if cutoffs, purges := store.calls(); purges != 1 || len(cutoffs) != 0 {
		t.Fatalf("after the first pass: purge %d, prune %d; want one purge and no retention pass", purges, len(cutoffs))
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("cleanup loop did not exit on context cancel")
	}
}

// Build hands the loop the real store: after one pass the review row of a
// deleted account is gone and the existing account's stays. Without the
// wiring the purge returns at once on a nil store.
func TestBuildPrunesRiskReviewOrphans(t *testing.T) {
	ctx := t.Context()
	directory := t.TempDir()
	cfg := &config.Config{
		Listen: "127.0.0.1:0", JWTSecret: strings.Repeat("j", 48), EncryptionKey: strings.Repeat("e", 48),
		ConfigDir: filepath.Join(directory, "config"), DataDir: filepath.Join(directory, "data"),
	}
	a, err := Build(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := a.Shutdown(shutdownCtx); err != nil {
			t.Error(err)
		}
		sqlstore.ConfigureSecretKey("")
	})
	newUser := func(n int) *domain.User {
		t.Helper()
		u := &domain.User{
			UPN: fmt.Sprintf("reviews-%d@example.test", n), Email: fmt.Sprintf("reviews-%d@example.test", n),
			SSOProvider: domain.SSOProviderLocal, SSOSubject: fmt.Sprintf("reviews-%d@example.test", n),
			Role: domain.RoleUser, Enabled: true, UUID: fmt.Sprintf("88888888-8888-4888-8888-%012d", n),
			SubToken: fmt.Sprintf("fixture-reviews-subscription-token-%d", n), TrafficResetPeriod: domain.ResetMonthly,
		}
		if err := a.repos.User.Create(ctx, u); err != nil {
			t.Fatal(err)
		}
		return u
	}
	kept, gone := newUser(1), newUser(2)

	db, err := sqlstore.Open(cfg.DBKind(), cfg.DBDSN())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
	repo := sqlstore.NewRiskReviewRepo(db)
	for _, u := range []*domain.User{kept, gone} {
		rev := domain.RiskReview{UserID: u.ID, Trusted: true, TrustedAtMS: 1, TrustedBy: 1, UpdatedAtMS: 1}
		if err := repo.Save(ctx, rev, domain.ReviewFlag(u.ID, domain.FlagReviewTrusted, domain.ReviewFlagParams{By: 1}, time.Now())); err != nil {
			t.Fatal(err)
		}
	}
	if err := a.repos.User.Delete(ctx, gone.ID); err != nil {
		t.Fatal(err)
	}

	a.pruneRiskReviews(ctx)

	var n int64
	if err := db.Raw("SELECT COUNT(*) FROM risk_reviews").Scan(&n).Error; err != nil {
		t.Fatal(err)
	}
	if _, ok, err := repo.Get(ctx, kept.ID); n != 1 || !ok || err != nil {
		t.Fatalf("after the purge: %d rows stored, the existing account's present = %v (%v); want only that one — is the store wired into App?", n, ok, err)
	}
}
