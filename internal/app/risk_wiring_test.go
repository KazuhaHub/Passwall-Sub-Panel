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
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/operationgate"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/risk"
)

// THE RISK WORKER IS BUILT, NOT INJECTED BY ANYONE ELSE, SO FORGETTING IT IS
// SILENT.
//
// Nothing fails if Build never constructs the worker or hands it the wrong
// store: the loop returns at once on a nil service, every unit test builds
// the service directly, and the risk table simply stays empty — which the
// admin view would show as "never computed" forever. Only a run through the
// assembled application sees it: the users, the settings, the hourly rollup
// and the risk_signals table Build opened.
func TestBuildWiresTheRiskSignals(t *testing.T) {
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
		UPN: "risk@example.test", Email: "risk@example.test", SSOProvider: domain.SSOProviderLocal,
		SSOSubject: "risk@example.test", Role: domain.RoleUser, Enabled: true,
		UUID: "77777777-7777-4777-8777-777777777777", SubToken: "fixture-risk-subscription-token",
		TrafficResetPeriod: domain.ResetMonthly,
	}
	if err := a.repos.User.Create(ctx, u); err != nil {
		t.Fatal(err)
	}
	if a.risk == nil {
		t.Fatal("Build did not construct the risk worker: the loop would return at once and no signal would ever be computed")
	}
	if err := a.risk.RefreshOnce(ctx); err != nil {
		t.Fatal(err)
	}

	// The store is not on App, so this opens the same database file Build
	// opened, the way the admin view will read it.
	db, err := sqlstore.Open(cfg.DBKind(), cfg.DBDSN())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
	rows, err := sqlstore.NewRiskSignalRepo(db).List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var got *domain.RiskSignal
	for i := range rows {
		if rows[i].UserID == u.ID && rows[i].Kind == domain.RiskKindUsageShift {
			got = &rows[i]
		}
	}
	if got == nil {
		t.Fatalf("no usage_shift row for the account after a refresh (rows: %+v)", rows)
	}
	// No traffic yet: idle, not unknown and not clean.
	if got.State != domain.GeoStateIdle || got.Code != domain.RiskCodeNoUsage || got.UPN != u.UPN {
		t.Fatalf("row = %+v, want idle/no_usage for %s", *got, u.UPN)
	}
}

// ---- loop fakes: one account, no traffic, a store that reports saves ----

type loopUsers struct{}

func (loopUsers) List(context.Context, ports.UserFilter) ([]*domain.User, int64, error) {
	return []*domain.User{{ID: 1}}, 1, nil
}

type loopSettings struct{}

func (loopSettings) Load(context.Context, ports.UISettings) (ports.UISettings, error) {
	return ports.UISettings{}, nil
}
func (loopSettings) LoadForGroup(context.Context, int64, ports.UISettings) (ports.UISettings, error) {
	return ports.UISettings{}, nil
}
func (loopSettings) LoadForUser(context.Context, *domain.User, ports.UISettings) (ports.UISettings, error) {
	return ports.UISettings{}, nil
}

type loopTraffic struct{}

func (loopTraffic) ListHourlyByUser(context.Context, int64, time.Time, time.Time) ([]domain.HourlyTraffic, error) {
	return nil, nil
}
func (loopTraffic) SumHourlyAllUsers(context.Context, time.Time, time.Time) ([]domain.HourlyTraffic, error) {
	return nil, nil
}

type loopStore struct{ saved chan struct{} }

func (s loopStore) Save(context.Context, []domain.RiskSignal) error {
	select {
	case s.saved <- struct{}{}:
	default:
	}
	return nil
}
func (loopStore) PurgeOrphans(context.Context) (int64, error) { return 0, nil }

func newLoopApp() (*App, loopStore) {
	store := loopStore{saved: make(chan struct{}, 1)}
	a := &App{
		operationGate: operationgate.New(),
		risk:          risk.New(risk.Deps{Users: loopUsers{}, Store: store, Settings: loopSettings{}, Traffic: loopTraffic{}}),
	}
	return a, store
}

// The loop refreshes once its first delay has passed — without waiting out
// a whole hourly tick first, which would leave a fresh install's risk view
// empty for an hour — and exits on cancel.
func TestRiskLoopRefreshesAfterItsFirstDelay(t *testing.T) {
	a, store := newLoopApp()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { defer close(done); a.runRiskLoop(ctx, 0) }()

	select {
	case <-store.saved:
	case <-time.After(2 * time.Second):
		t.Fatal("no refresh saved within 2s of a zero first delay")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("risk loop did not exit on context cancel")
	}
}

// Shutdown must not wait out the first delay: a cancel during it ends the
// loop at once, with nothing computed.
func TestRiskLoopExitsDuringItsFirstDelay(t *testing.T) {
	a, store := newLoopApp()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); a.runRiskLoop(ctx, time.Hour) }()
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("risk loop did not exit on a cancel during its first delay")
	}
	select {
	case <-store.saved:
		t.Fatal("refreshed although the loop was cancelled before its first delay passed")
	default:
	}
}
