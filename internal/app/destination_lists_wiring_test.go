package app

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/adapters/sqlstore"
	"github.com/KazuhaHub/passwall-sub-panel/internal/config"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/jwtutil"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

func buildDestinationListsFixture(t *testing.T) *App {
	return buildDestinationListsFixtureWithCatalog(t, nil)
}

func buildDestinationListsFixtureWithCatalog(t *testing.T, catalog []byte) *App {
	t.Helper()
	directory := t.TempDir()
	if catalog != nil {
		path := filepath.Join(directory, "data", "destlists")
		if err := os.MkdirAll(path, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(path, "dlc_plain.yml"), catalog, 0600); err != nil {
			t.Fatal(err)
		}
	}
	a, err := Build(t.Context(), &config.Config{Listen: "127.0.0.1:0", JWTSecret: strings.Repeat("j", 48), EncryptionKey: strings.Repeat("e", 48), ConfigDir: filepath.Join(directory, "config"), DataDir: filepath.Join(directory, "data")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := a.Shutdown(ctx); err != nil {
			t.Error(err)
		}
		sqlstore.ConfigureSecretKey("")
	})
	return a
}

func TestBuildWiresDestinationListRefresh(t *testing.T) {
	a := buildDestinationListsFixture(t)
	if a.destLists == nil || a.destDefinitions == nil {
		t.Fatal("Build omitted the destination list service or definition store")
	}
}

func destinationRefreshAdminToken(t *testing.T, a *App) string {
	t.Helper()
	admin := &domain.User{UPN: "destination-admin@example.test", Email: "destination-admin@example.test", SSOProvider: domain.SSOProviderLocal, SSOSubject: "destination-admin@example.test", Role: domain.RoleAdmin, Enabled: true, UUID: "88888888-8888-4888-8888-888888888888", SubToken: "fixture-destination-admin-token", TrafficResetPeriod: domain.ResetMonthly}
	if err := a.repos.User.Create(t.Context(), admin); err != nil {
		t.Fatal(err)
	}
	settings, err := a.settings.Load(t.Context(), ports.UISettings{})
	if err != nil {
		t.Fatal(err)
	}
	token, err := jwtutil.NewIssuer(a.cfg.JWTSecret, func() jwtutil.Params {
		return jwtutil.Params{AccessTTL: time.Hour, RefreshTTL: time.Hour, Issuer: settings.JWTIssuer}
	}).IssueAccess(admin.ID, admin.UPN, admin.Role, admin.TokenVersion)
	if err != nil {
		t.Fatal(err)
	}
	return token
}

func destinationRefreshLegacySource(t *testing.T, a *App, hours int) domain.DestList {
	t.Helper()
	settings, err := a.settings.Load(t.Context(), ports.UISettings{})
	if err != nil {
		t.Fatal(err)
	}
	settings.DestListRefreshHours = hours
	if err := a.settings.Save(t.Context(), settings); err != nil {
		t.Fatal(err)
	}
	last := time.Now().UTC().Add(-12 * time.Hour)
	// Seed a legacy source rejected before network I/O. Its persisted refresh
	// error proves the actual worker selected and committed the due row.
	list := domain.DestList{Name: "legacy remote", Kind: domain.DestListRemote, SourceURL: "http://no-network.invalid/list", LastFetchedAt: &last}
	if err := a.destDefinitions.SaveList(t.Context(), &list, time.Time{}, time.Now()); err != nil {
		t.Fatal(err)
	}
	return list
}

func waitDestinationRefreshError(t *testing.T, a *App, id int64) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		list, err := a.destDefinitions.GetList(t.Context(), id)
		if err != nil {
			t.Fatal(err)
		}
		if list.LastError == "dest_list_insecure_url" {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("destination worker did not commit the due source's refresh result")
}

func TestRunStartsDestinationListRefresh(t *testing.T) {
	a := buildDestinationListsFixture(t)
	token := destinationRefreshAdminToken(t, a) // prevent bootstrap-password output
	list := destinationRefreshLegacySource(t, a, 6)
	if before, err := a.destDefinitions.GetList(t.Context(), list.ID); err != nil || before.LastError != "" {
		t.Fatalf("Build performed a refresh before Run: %+v / %v", before, err)
	}
	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	a.server.Addr = probe.Addr().String()
	if err := probe.Close(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- a.Run() }()
	// A real HTTP response establishes that Run finished registering all of
	// its background workers before Shutdown starts draining the wait group.
	transport := &http.Transport{}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: time.Second}
	ready := false
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://"+a.server.Addr+"/api/admin/dest/settings", nil)
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Authorization", "Bearer "+token)
		response, err := client.Do(request)
		if err == nil {
			_, _ = io.Copy(io.Discard, response.Body)
			_ = response.Body.Close()
			if response.StatusCode == http.StatusOK {
				ready = true
				break
			}
		}
		select {
		case err := <-done:
			t.Fatalf("Run stopped before serving: %v", err)
		default:
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !ready {
		t.Fatal("Run did not serve destination settings")
	}
	waitDestinationRefreshError(t, a, list.ID)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := a.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if !errors.Is(err, http.ErrServerClosed) {
			t.Fatalf("Run stopped incorrectly: %v", err)
		}
	case <-ctx.Done():
		t.Fatal("Run did not stop after destination worker drained")
	}
}

type observedDestinationSettings struct {
	ports.SettingsRepo
	reads chan int
}

func (s *observedDestinationSettings) Load(ctx context.Context, defaults ports.UISettings) (ports.UISettings, error) {
	settings, err := s.SettingsRepo.Load(ctx, defaults)
	if err == nil {
		select {
		case s.reads <- settings.DestinationSettings().Effective().ListRefreshHours:
		default:
		}
	}
	return settings, err
}

func TestDestinationSettingsSaveWakesTheAssembledRefreshWorker(t *testing.T) {
	a := buildDestinationListsFixture(t)
	token := destinationRefreshAdminToken(t, a)
	list := destinationRefreshLegacySource(t, a, 168)
	observed := &observedDestinationSettings{SettingsRepo: a.settings, reads: make(chan int, 4)}
	a.settings = observed
	a.startDestinationListRefresh()
	select {
	case hours := <-observed.reads:
		if hours != 168 {
			t.Fatalf("initial refresh hours=%d", hours)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("worker did not read initial persisted settings")
	}
	request := httptest.NewRequest(http.MethodPut, "/api/admin/dest/settings", strings.NewReader(`{"settings":{"dest_list_refresh_hours":6}}`))
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	a.server.Handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("settings PUT=%d: %s", response.Code, response.Body.String())
	}
	select {
	case hours := <-observed.reads:
		if hours != 6 {
			t.Fatalf("worker kept stale refresh hours=%d", hours)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("assembled settings callback did not wake refresh worker")
	}
	waitDestinationRefreshError(t, a, list.ID)
}

type failedDestinationSettings struct {
	ports.SettingsRepo
	settings ports.UISettings
	err      error
}

func (s failedDestinationSettings) Load(context.Context, ports.UISettings) (ports.UISettings, error) {
	return s.settings, s.err
}

func TestDestinationRefreshHoursUsesBoundedLiveSettingsAndKeepsReadErrors(t *testing.T) {
	problem := errors.New("settings unavailable")
	for _, sample := range []struct {
		raw, want int
		err       error
	}{{0, 24, nil}, {6, 6, nil}, {999, 168, nil}, {-1, 24, nil}, {6, 0, problem}} {
		a := &App{settings: failedDestinationSettings{settings: ports.UISettings{DestListRefreshHours: sample.raw}, err: sample.err}}
		hours, err := a.destinationRefreshHours(t.Context())
		if hours != sample.want || !errors.Is(err, sample.err) {
			t.Fatalf("raw=%d gave hours=%d err=%v", sample.raw, hours, err)
		}
	}
	if _, err := (&App{}).destinationRefreshHours(t.Context()); !errors.Is(err, domain.ErrUnavailable) {
		t.Fatalf("missing settings became a refresh cadence: %v", err)
	}
}
