package app

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/adapters/sqlstore"
	"github.com/KazuhaHub/passwall-sub-panel/internal/config"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/metrics"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// THE HIT RECORDS AGE OUT. Build hands the hourly cleanup the real store: a hit
// older than dest.hit_retention_days is gone after one pass, an allowlist trial
// row goes at its own, shorter, dest.trial_retention_days, a batch marker at a
// day, and what a deleted account or panel left behind goes whatever its age.
// Without the wiring the prune returns at once on a nil store and a browsing
// record grows for ever.
func TestBuildPrunesDestRows(t *testing.T) {
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
	panel := &domain.Panel{Kind: domain.PanelKind3XUI, Name: "p", URL: "https://p.example"}
	if err := a.repos.XUIPanel.Save(ctx, panel); err != nil {
		t.Fatal(err)
	}
	newUser := func(n string) *domain.User {
		t.Helper()
		u := &domain.User{
			UPN: n + "@example.test", Email: n + "@example.test", SSOProvider: domain.SSOProviderLocal, SSOSubject: n + "@example.test",
			Role: domain.RoleUser, Enabled: true, UUID: "88888888-8888-4888-8888-" + strings.Repeat(n[:1], 12),
			SubToken: "fixture-dest-token-" + n, TrafficResetPeriod: domain.ResetMonthly,
		}
		if err := a.repos.User.Create(ctx, u); err != nil {
			t.Fatal(err)
		}
		return u
	}
	kept, gone := newUser("alice"), newUser("bob")

	hits := a.destRepos.Hits
	hour := func(ago time.Duration) int64 { return time.Now().Add(-ago).Truncate(time.Hour).UnixMilli() }
	row := func(ago time.Duration, user int64, source, action, dest string) domain.DestHit {
		h := hour(ago)
		return domain.DestHit{HourMS: h, PanelID: panel.ID, UserID: user, Source: source, Action: action, Dest: dest, Port: 443, Count: 1, FirstMS: h, LastMS: h}
	}
	if _, err := hits.StoreBatch(ctx, "agt_x", "seed", time.Now().Add(-96*time.Hour), []domain.DestHit{
		row(31*24*time.Hour, kept.ID, "p1", "block", "old.example"),      // past the 30 days
		row(time.Hour, kept.ID, "p1", "block", "fresh.example"),          // kept
		row(time.Hour, gone.ID, "p1", "block", "gone-user.example"),      // its account is deleted
		row(8*24*time.Hour, 0, "g1", "observe", "old-trial.example"),     // trial, past its 7 days
		row(24*time.Hour, 0, "g1", "observe", "fresh-trial.example"),     // trial, kept
		row(10*24*time.Hour, kept.ID, "p1", "observe", "report.example"), // a report-only row is a hit: 30 days
	}); err != nil {
		t.Fatal(err)
	}
	if err := a.repos.User.Delete(ctx, gone.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := hits.StoreBatch(ctx, "agt_x", "recent", time.Now(), nil); err != nil {
		t.Fatal(err)
	}
	// A marker 60 hours old still guards a re-send: the rows it covers are inside
	// the accepted 48-hour past window for a day more.
	if _, err := hits.StoreBatch(ctx, "agt_x", "in-window", time.Now().Add(-60*time.Hour), nil); err != nil {
		t.Fatal(err)
	}

	before := metrics.DestPrunedRowsTotal.With(metrics.DestPruneHits).Value()
	a.pruneDestRows(ctx)

	rows, total, err := hits.List(ctx, domain.DestHitFilter{PageSize: 100, IncludeTrial: true})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, r := range rows {
		got[r.Dest] = true
	}
	for _, want := range []string{"fresh.example", "fresh-trial.example", "report.example"} {
		if !got[want] {
			t.Errorf("%s was pruned but is within its retention (is the store wired into App?)", want)
		}
	}
	for _, unwanted := range []string{"old.example", "old-trial.example", "gone-user.example"} {
		if got[unwanted] {
			t.Errorf("%s survived the prune", unwanted)
		}
	}
	if total != 3 {
		t.Fatalf("%d rows left: %+v", total, rows)
	}
	if after := metrics.DestPrunedRowsTotal.With(metrics.DestPruneHits).Value(); after-before != 1 {
		t.Errorf("the pruned-rows counter moved by %d, want 1 (the 31-day-old hit)", after-before)
	}
	// A marker is kept past the accepted past window (48h) plus a margin (72h in
	// all): the seed marker (96h) goes; the recent and the 60h one stay.
	if res, _ := hits.StoreBatch(ctx, "agt_x", "in-window", time.Now(), nil); !res.Duplicate {
		t.Error("a batch marker inside the retained window was pruned")
	}
	if res, _ := hits.StoreBatch(ctx, "agt_x", "recent", time.Now(), nil); !res.Duplicate {
		t.Error("a current batch marker was pruned")
	}
	if res, _ := hits.StoreBatch(ctx, "agt_x", "seed", time.Now(), nil); res.Duplicate {
		t.Error("a four-day-old batch marker survived")
	}
}

// The retention is the admin's setting, read at each pass.
func TestPruneDestRows_ARetentionSettingChangesTheCutoff(t *testing.T) {
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
		_ = a.Shutdown(shutdownCtx)
		sqlstore.ConfigureSecretKey("")
	})
	panel := &domain.Panel{Kind: domain.PanelKind3XUI, Name: "p", URL: "https://p.example"}
	if err := a.repos.XUIPanel.Save(ctx, panel); err != nil {
		t.Fatal(err)
	}
	u := &domain.User{
		UPN: "c@example.test", Email: "c@example.test", SSOProvider: domain.SSOProviderLocal, SSOSubject: "c@example.test",
		Role: domain.RoleUser, Enabled: true, UUID: "99999999-9999-4999-8999-999999999999", SubToken: "fixture-dest-token-c", TrafficResetPeriod: domain.ResetMonthly,
	}
	if err := a.repos.User.Create(ctx, u); err != nil {
		t.Fatal(err)
	}
	h := time.Now().Add(-5 * 24 * time.Hour).Truncate(time.Hour).UnixMilli()
	if _, err := a.destRepos.Hits.StoreBatch(ctx, "agt_x", "b", time.Now(), []domain.DestHit{
		{HourMS: h, PanelID: panel.ID, UserID: u.ID, Source: "p1", Action: "block", Dest: "five-days.example", Port: 1, Count: 1, FirstMS: h, LastMS: h},
	}); err != nil {
		t.Fatal(err)
	}
	set, _ := a.settings.Load(ctx, ports.UISettings{})
	set.DestHitRetentionDays = 3
	if err := a.settings.Save(ctx, set); err != nil {
		t.Fatal(err)
	}
	a.pruneDestRows(ctx)
	if _, total, _ := a.destRepos.Hits.List(ctx, domain.DestHitFilter{IncludeTrial: true}); total != 0 {
		t.Fatalf("a 5-day-old hit survived a 3-day retention")
	}
}
