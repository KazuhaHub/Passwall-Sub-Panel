package app

import (
	"context"
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/adapters/sqlstore"
	"github.com/KazuhaHub/passwall-sub-panel/internal/config"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/riskcenter"
)

// THE QUEUE, THE LEVELS, THE BELL AND THE DRAWER COUNT THE SAME ACCOUNTS.
//
// The fleet reads (Queue, Levels, CountUrgent) bound freshness in SQL
// (updated_at >= since, in unix ms); the one-account read (UserAttention,
// which the drawer and the review actions use) reads the account's rows and
// bounds them in Go. On fakes both agree by construction; only the real
// stores show the two comparisons are the same one — at the boundary, where
// an off-by-one would put an account in the bell and out of the drawer.
//
// Real SQLite through the stores Build opened, the service clock pinned: a
// latched geo row and a flagged risk row written exactly at since (in), and
// another of each a millisecond before it (out).
func TestRiskCenterAttentionAgreesAcrossReads(t *testing.T) {
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
	newUser := func(name, uuid string) *domain.User {
		t.Helper()
		u := &domain.User{
			UPN: name + "@example.test", Email: name + "@example.test", SSOProvider: domain.SSOProviderLocal,
			SSOSubject: name + "@example.test", Role: domain.RoleUser, Enabled: true, UUID: uuid,
			SubToken: "fixture-" + name + "-subscription-token", TrafficResetPeriod: domain.ResetMonthly,
		}
		if err := a.repos.User.Create(ctx, u); err != nil {
			t.Fatal(err)
		}
		return u
	}
	geoIn := newUser("agree-geo-in", "66666666-6666-4666-8666-000000000001")
	geoOut := newUser("agree-geo-out", "66666666-6666-4666-8666-000000000002")
	riskIn := newUser("agree-risk-in", "66666666-6666-4666-8666-000000000003")
	riskOut := newUser("agree-risk-out", "66666666-6666-4666-8666-000000000004")

	db, err := sqlstore.Open(cfg.DBKind(), cfg.DBDSN())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
	geo, signals := sqlstore.NewGeoStreakRepo(db), sqlstore.NewRiskSignalRepo(db)
	if err := geo.Save(ctx, map[int64]domain.GeoRecord{
		geoIn.ID:  {UserID: geoIn.ID, State: domain.GeoStateIdle, Streak: domain.GeoStreak{Over: 3, Flagged: true}},
		geoOut.ID: {UserID: geoOut.ID, State: domain.GeoStateIdle, Streak: domain.GeoStreak{Over: 3, Flagged: true}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := signals.Save(ctx, []domain.RiskSignal{
		{UserID: riskIn.ID, Kind: domain.RiskKindDevices, State: domain.GeoStateFlagged, Code: domain.RiskCodeOver, Evidence: json.RawMessage(`{"v":1}`)},
		{UserID: riskOut.ID, Kind: domain.RiskKindDevices, State: domain.GeoStateFlagged, Code: domain.RiskCodeOver, Evidence: json.RawMessage(`{"v":1}`)},
	}); err != nil {
		t.Fatal(err)
	}

	// Each window as the service resolves it from the stored settings: the
	// geo verdicts' (the alert freshness floored at two polls) and the risk
	// signals' (the alert freshness).
	set, err := a.repos.Settings.Load(ctx, ports.UISettings{})
	if err != nil {
		t.Fatal(err)
	}
	rt := domain.RiskRuntimeFromSettings(set.RiskRuntimeSettings())
	poll := 5 * time.Minute
	if set.CronTrafficPullMinutes > 0 {
		poll = time.Duration(set.CronTrafficPullMinutes) * time.Minute
	}
	now := time.Now().Truncate(time.Millisecond)
	geoSince, riskSince := now.Add(-rt.GeoBellFreshness(poll)), now.Add(-rt.AlertFreshness)
	for _, w := range []struct {
		table string
		uid   int64
		at    int64
	}{
		{"geo_streaks", geoIn.ID, geoSince.UnixMilli()},
		{"geo_streaks", geoOut.ID, geoSince.UnixMilli() - 1},
		{"risk_signals", riskIn.ID, riskSince.UnixMilli()},
		{"risk_signals", riskOut.ID, riskSince.UnixMilli() - 1},
	} {
		if err := db.Exec("UPDATE "+w.table+" SET updated_at = ? WHERE user_id = ?", w.at, w.uid).Error; err != nil {
			t.Fatal(err)
		}
	}

	svc := riskcenter.New(riskcenter.Deps{
		Live: a.traffic, Settings: a.repos.Settings, Users: a.repos.User, Panels: a.repos.XUIPanel,
		Fetches: a.repos.SubLog, History: sqlstore.NewConnectionHistoryRepo(db), Flags: sqlstore.NewFlagRecordRepo(db),
		Geo: geo, Signals: signals, Reviews: sqlstore.NewRiskReviewRepo(db), Holds: a.repos.User, Groups: a.repos.Group,
		Now: func() time.Time { return now },
	})

	view, err := svc.Queue(ctx, riskcenter.QueueQuery{Status: "all", PageSize: 100})
	if err != nil {
		t.Fatal(err)
	}
	queued := map[int64]domain.AccountAttention{}
	for _, r := range view.Rows {
		queued[r.User.ID] = r.Attention
	}
	levels, err := svc.Levels(ctx)
	if err != nil {
		t.Fatal(err)
	}
	urgent, err := svc.CountUrgent(ctx)
	if err != nil {
		t.Fatal(err)
	}

	for _, c := range []struct {
		u  *domain.User
		in bool
	}{{geoIn, true}, {geoOut, false}, {riskIn, true}, {riskOut, false}} {
		one, err := svc.UserAttention(ctx, c.u.ID)
		if err != nil {
			t.Fatalf("%s: %v", c.u.UPN, err)
		}
		q, inQueue := queued[c.u.ID]
		_, inLevels := levels[c.u.ID]
		if inQueue != c.in || inLevels != c.in || one.Urgent() != c.in {
			t.Fatalf("%s: queue %v, levels %v, drawer urgent %v; want all %v (updated_at at since is in, a millisecond before is out)",
				c.u.UPN, inQueue, inLevels, one.Urgent(), c.in)
		}
		if c.in && !reflect.DeepEqual(one, q) {
			t.Fatalf("%s: the drawer's attention %+v, the queue's %+v", c.u.UPN, one, q)
		}
		if c.in && levels[c.u.ID].Level != domain.FlagLevelFlagged {
			t.Fatalf("%s: level %q, want flagged", c.u.UPN, levels[c.u.ID].Level)
		}
	}
	if urgent != 2 || view.Counts.Urgent != 2 {
		t.Fatalf("urgent: bell %d, queue card %d; want 2 and 2", urgent, view.Counts.Urgent)
	}
}
