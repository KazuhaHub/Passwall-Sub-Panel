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
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// THE FLAG RECORDERS ARE LATE-BOUND, WHICH MAKES FORGETTING THEM SILENT.
//
// traffic.SetFlagRecorder and user.SetFlagRecorder are nil-tolerant, the
// service tests wire fakes, and with either line gone from Build the
// transitions still happen and simply leave no record: the risk center's
// flag history shows the risk signals and nothing of the location detector.
//
// So this drives the application Build assembled, one transition through
// each recorder, and reads the records back from the store the hourly
// cleanup prunes (a.flagRecords): a geo_auto suspension two hours into the
// shipped 60 minutes is lifted by the poll (the traffic service's recorder),
// and a fresh one is resumed by staff (the user service's). The verdict's own
// transitions go through the traffic recorder too, but a poll judges only
// connections a panel reported live, and the only panel a test can stand up
// is on loopback, which the 3X-UI adapter's safehttp dialer refuses.
func TestBuildWiresTheFlagRecorders(t *testing.T) {
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
	store, ok := a.flagRecords.(*sqlstore.FlagRecordRepo)
	if !ok || store == nil {
		t.Fatalf("a.flagRecords = %T, want the *sqlstore.FlagRecordRepo the cleanup prunes", a.flagRecords)
	}

	newUser := func(name, uuid string) *domain.User {
		u := &domain.User{
			UPN: name + "@example.test", Email: name + "@example.test", SSOProvider: domain.SSOProviderLocal,
			SSOSubject: name + "@example.test", Role: domain.RoleUser, Enabled: true,
			UUID: uuid, SubToken: "fixture-" + name + "-subscription-token", TrafficResetPeriod: domain.ResetMonthly,
		}
		if err := a.repos.User.Create(ctx, u); err != nil {
			t.Fatal(err)
		}
		return u
	}
	recordsOf := func(uid int64) []domain.FlagRecord {
		t.Helper()
		recs, _, err := store.List(ctx, ports.FlagRecordFilter{UserID: &uid, Pagination: ports.Pagination{Page: 1, PageSize: 10}})
		if err != nil {
			t.Fatal(err)
		}
		return recs
	}

	expired := newUser("flag-expiry", "55555555-5555-4555-8555-555555555555")
	began := time.Now().Add(-2 * time.Hour)
	if err := a.repos.User.UpdateServiceState(ctx, expired.ID, domain.DisabledGeoAutoSuspend, "detail", &began); err != nil {
		t.Fatal(err)
	}
	if err := a.traffic.PollOnce(ctx); err != nil {
		t.Fatalf("PollOnce: %v", err)
	}
	if recs := recordsOf(expired.ID); len(recs) != 1 || recs[0].Source != domain.FlagSourceGeoAuto || recs[0].Event != domain.FlagAutoLiftedExpiry {
		t.Fatalf("records after the poll's lift = %+v, want one geo_auto auto_lifted_expiry — is the traffic service's flag recorder wired?", recs)
	}

	resumed := newUser("flag-resume", "66666666-6666-4666-8666-666666666666")
	if applied, err := a.repos.User.SetServiceStateIfClear(ctx, resumed.ID, domain.DisabledGeoAutoSuspend, "detail", time.Now()); err != nil || !applied {
		t.Fatalf("SetServiceStateIfClear: applied=%v err=%v", applied, err)
	}
	if err := a.user.ResumeServiceAndSync(ctx, resumed.ID); err != nil {
		t.Fatalf("ResumeServiceAndSync: %v", err)
	}
	if recs := recordsOf(resumed.ID); len(recs) != 1 || recs[0].Source != domain.FlagSourceGeoAuto || recs[0].Event != domain.FlagAutoLiftedAdmin {
		t.Fatalf("records after a staff resume = %+v, want one geo_auto auto_lifted_admin — is the user service's flag recorder wired?", recs)
	}
}
