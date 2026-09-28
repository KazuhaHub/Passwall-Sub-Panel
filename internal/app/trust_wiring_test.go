package app

import (
	"context"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/adapters/sqlstore"
	"github.com/KazuhaHub/passwall-sub-panel/internal/config"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// THE TRUSTED ACCOUNTS ARE LATE-BOUND, WHICH MAKES FORGETTING THEM SILENT.
//
// Both consumers are nil-tolerant: SetTrustedLister left out of Build, or
// risk.Deps.Trust left empty, compiles, every unit test wires a fake, and the
// detectors simply go on judging a trusted account's location. The SQL guard
// still stops a geo_auto suspension (TestBuildNeverGeoAutoSuspendsATrustedAccount),
// so nothing would look broken — the account would just stay flagged in the
// risk center after the admin trusted it.
//
// The poll's lister is read by reflection, as the connection recorder's is:
// a poll judges only connections a panel reported live, and the only panel
// this test could stand up is a loopback server the adapter's SSRF-refusing
// dialer turns away. It must be the very store behind a.riskReviews — the
// one the review actions write — not merely something non-nil. The risk
// worker is driven for real: its sub_spread and login_country rows for a
// trusted account read exempt / trusted, and the untrusted control's do not.
func TestBuildWiresTheTrustedAccounts(t *testing.T) {
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

	field := reflect.ValueOf(a.traffic).Elem().FieldByName("trusted")
	if !field.IsValid() {
		t.Fatal("traffic.Service has no trusted field; update this test with the lister's new home")
	}
	if field.IsNil() {
		t.Fatal("the traffic poll has no trusted-account lister: Build does not call SetTrustedLister")
	}
	store, ok := a.riskReviews.(*sqlstore.RiskReviewRepo)
	if !ok || store == nil {
		t.Fatalf("a.riskReviews = %T, want the *sqlstore.RiskReviewRepo the review actions write", a.riskReviews)
	}
	if got := field.Elem(); got.Type() != reflect.TypeOf(store) || got.Pointer() != reflect.ValueOf(store).Pointer() {
		t.Fatalf("the poll reads its trusted accounts from a %v other than the risk_reviews store", got.Type())
	}

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
	trusted := newUser("trusted", "88888888-8888-4888-8888-000000000001")
	control := newUser("control", "88888888-8888-4888-8888-000000000002")
	for _, u := range []*domain.User{trusted, control} {
		if err := a.repos.SubLog.Insert(ctx, &domain.SubLog{UserID: u.ID, IP: "198.51.100.20", UA: "clash.meta/1.19.2",
			ClientType: "mihomo", AccessedAt: time.Now()}); err != nil {
			t.Fatal(err)
		}
	}
	rev := domain.RiskReview{UserID: trusted.ID, Trusted: true, TrustedAtMS: 1, TrustedBy: 1, UpdatedAtMS: 1}
	if err := store.Save(ctx, rev,
		domain.ReviewFlag(trusted.ID, domain.FlagReviewTrusted, domain.ReviewFlagParams{By: 1}, time.Now())); err != nil {
		t.Fatal(err)
	}

	// The place signals wait for the infrastructure set; Build starts no loop.
	if err := a.traffic.RefreshInfraAddresses(ctx); err != nil {
		t.Fatal(err)
	}
	if err := a.risk.RefreshOnce(ctx); err != nil {
		t.Fatal(err)
	}
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
	got := map[int64]map[domain.RiskKind]domain.RiskSignal{}
	for _, r := range rows {
		if got[r.UserID] == nil {
			got[r.UserID] = map[domain.RiskKind]domain.RiskSignal{}
		}
		got[r.UserID][r.Kind] = r
	}
	for _, kind := range []domain.RiskKind{domain.RiskKindSubSpread, domain.RiskKindLoginCountry} {
		r, ok := got[trusted.ID][kind]
		if !ok || r.State != domain.GeoStateExempt || r.Code != domain.RiskCodeTrusted {
			t.Fatalf("trusted account's %s row = %+v (present %v), want exempt/trusted: risk.Deps.Trust is not wired", kind, r, ok)
		}
		if c, ok := got[control.ID][kind]; !ok || c.Code == domain.RiskCodeTrusted {
			t.Fatalf("control's %s row = %+v (present %v), want a row judged on its own", kind, c, ok)
		}
	}
}
