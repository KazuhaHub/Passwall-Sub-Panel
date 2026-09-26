package app

import (
	"context"
	"encoding/json"
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
// assembled application sees it: the users, the settings, the hourly rollup,
// the fetch log, the infrastructure set and the risk_signals table Build
// opened.
//
// Every optional source is nil-tolerant, so each is checked by what it
// changes: without the fetch log there is no sub_spread or devices row at
// all; without InfraLoaded the place signals would run before the set was
// ever built; without IsInfra the node's own address would count as a
// place; without the login log there is no login_country row. The geo
// resolver cannot be told apart here — no database is installed, so a wired
// one and a missing one both read geo_unavailable — and neither can the
// landing addresses, which matter only through the countries it would name.
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
	// One node, whose own address the account also fetched from (a client
	// whose fetch went out through the node), and one fetch from home.
	node := &domain.Node{PanelID: 1, InboundID: 1, DisplayName: "risk-node", ServerAddress: "203.0.113.9", Enabled: true}
	if err := a.repos.Node.Create(ctx, node); err != nil {
		t.Fatal(err)
	}
	for _, ip := range []string{"203.0.113.9", "198.51.100.20"} {
		if err := a.repos.SubLog.Insert(ctx, &domain.SubLog{UserID: u.ID, IP: ip, UA: "clash.meta/1.19.2", ClientType: "mihomo", AccessedAt: time.Now()}); err != nil {
			t.Fatal(err)
		}
	}
	// A panel login from home, a day ago.
	if err := a.repos.AuthEvent.Insert(ctx, &domain.AuthEvent{
		UserID: u.ID, UPN: u.UPN, Method: domain.AuthMethodLocal, Outcome: domain.AuthOutcomeSuccess,
		IP: "198.51.100.20", UA: "Mozilla/5.0", At: time.Now().Add(-24 * time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	if a.risk == nil {
		t.Fatal("Build did not construct the risk worker: the loop would return at once and no signal would ever be computed")
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
	rowOf := func(kind domain.RiskKind) *domain.RiskSignal {
		t.Helper()
		rows, err := sqlstore.NewRiskSignalRepo(db).List(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for i := range rows {
			if rows[i].UserID == u.ID && rows[i].Kind == kind {
				return &rows[i]
			}
		}
		return nil
	}

	// Build starts no loop, so the infrastructure set has never been built:
	// the place signals wait, and the rest is written.
	if err := a.risk.RefreshOnce(ctx); err != nil {
		t.Fatal(err)
	}
	got := rowOf(domain.RiskKindUsageShift)
	if got == nil {
		t.Fatal("no usage_shift row for the account after a refresh")
	}
	// No traffic yet: idle, not unknown and not clean.
	if got.State != domain.GeoStateIdle || got.Code != domain.RiskCodeNoUsage || got.UPN != u.UPN {
		t.Fatalf("row = %+v, want idle/no_usage for %s", *got, u.UPN)
	}
	if spread := rowOf(domain.RiskKindSubSpread); spread != nil {
		t.Fatalf("sub_spread row %+v before the infrastructure set was ever built: InfraLoaded is not wired", *spread)
	}
	if login := rowOf(domain.RiskKindLoginCountry); login != nil {
		t.Fatalf("login_country row %+v before the infrastructure set was ever built", *login)
	}
	// The device count places nothing and does not wait for the set. Both
	// fetches declared no device: unknown, not clean.
	if dev := rowOf(domain.RiskKindDevices); dev == nil || dev.State != domain.GeoStateUnknown || dev.Code != domain.RiskCodeNoHWID {
		t.Fatalf("devices row %+v before the infrastructure set was built, want unknown/no_hwid from the fetch log", dev)
	}

	if err := a.traffic.RefreshInfraAddresses(ctx); err != nil {
		t.Fatal(err)
	}
	if err := a.risk.RefreshOnce(ctx); err != nil {
		t.Fatal(err)
	}
	spread := rowOf(domain.RiskKindSubSpread)
	if spread == nil {
		t.Fatal("no sub_spread row after the infrastructure set was built: the fetch log is not wired")
	}
	var ev domain.SubSpreadEvidence
	if err := json.Unmarshal(spread.Evidence, &ev); err != nil {
		t.Fatalf("evidence %s: %v", spread.Evidence, err)
	}
	// Two fetches: the node's own address set aside as infrastructure, the
	// other kept — and nothing placed, because no geo database is installed.
	if spread.State != domain.GeoStateUnknown || spread.Code != domain.RiskCodeGeoUnavailable ||
		ev.Excluded.Infra != 1 || ev.Coverage.Sources != 1 {
		t.Fatalf("sub_spread = %s/%s, excluded %+v, coverage %+v; want unknown/geo_unavailable with the node's address set aside and one source kept",
			spread.State, spread.Code, ev.Excluded, ev.Coverage)
	}
	// The login is recent and from an address no rule sets aside, so it is
	// judged — and cannot be placed without a database: unknown, not idle.
	login := rowOf(domain.RiskKindLoginCountry)
	if login == nil {
		t.Fatal("no login_country row after the infrastructure set was built: the login log is not wired")
	}
	var lev domain.LoginCountryEvidence
	if err := json.Unmarshal(login.Evidence, &lev); err != nil {
		t.Fatalf("evidence %s: %v", login.Evidence, err)
	}
	if login.State != domain.GeoStateUnknown || login.Code != domain.RiskCodeGeoUnavailable || lev.Recent != 1 {
		t.Fatalf("login_country = %s/%s with %d recent logins, want unknown/geo_unavailable with the one login read",
			login.State, login.Code, lev.Recent)
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
