package app

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/adapters/sqlstore"
	"github.com/KazuhaHub/passwall-sub-panel/internal/config"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/subdevice"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// THE DEVICE HASHER IS AN OPTIONAL DEP, WHICH MAKES FORGETTING IT SILENT.
//
// Deps.DeviceHasher is nil-tolerant by design (nil disables capture), so a
// Build that never constructs it, or a router that never hands it to the
// subscription handler, compiles, serves every fetch and leaves every unit
// test green — while no fetch ever records a device and the devices signal
// reads "no hwid" for the whole fleet forever. Only a fetch through the
// assembled application sees it.
//
// It also pins the three logged paths of GET /sub — a 200, a 304
// revalidation, and a whitelist refusal — because each calls the logger from
// its own site and a capture wired into one of them only would undercount
// exactly the clients that poll most (304) or the ones an admin most wants to
// see (blocked). And it pins what must NOT happen: the raw header reaching any
// column in any case, and anything in the response hinting that capture
// exists.
func TestBuildRecordsTheDeclaredDeviceOnEveryLoggedFetch(t *testing.T) {
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

	// The uri-list render needs the user's group row and no template, so no
	// seed files are needed (seed.Ensure runs in main, not in Build).
	g := &domain.Group{Slug: "hwid-capture", Name: "hwid capture"}
	if err := a.repos.Group.Create(ctx, g); err != nil {
		t.Fatal(err)
	}
	u := &domain.User{
		UPN: "hwid@example.test", Email: "hwid@example.test", SSOProvider: domain.SSOProviderLocal,
		SSOSubject: "hwid@example.test", Role: domain.RoleUser, Enabled: true, GroupID: g.ID,
		UUID: "66666666-6666-4666-8666-666666666666", SubToken: "fixture-hwid-subscription-token",
		TrafficResetPeriod: domain.ResetMonthly,
	}
	if err := a.repos.User.Create(ctx, u); err != nil {
		t.Fatal(err)
	}

	// Mixed case on purpose: the stored id is keyed on the lowercased value,
	// and the raw value must not survive in either case.
	const hwid = "h-1234567X"
	wantID := subdevice.NewHasher(cfg.SecretKeyMaterial()).ID(u.ID, "h-1234567x")
	if wantID == "" {
		t.Fatal("no device id for the fixture: is the config's secret material blank?")
	}
	const wantLabel = "iOS 17.5 · iPhone15,2"

	fetch := func(ua string, extra map[string]string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/sub/"+u.SubToken+"?client=uri-list", nil)
		req.Header.Set("User-Agent", ua)
		req.Header.Set("X-Hwid", hwid)
		req.Header.Set("X-Device-Os", "iOS")
		req.Header.Set("X-Ver-Os", "17.5")
		req.Header.Set("X-Device-Model", "iPhone15,2")
		for k, v := range extra {
			req.Header.Set(k, v)
		}
		rec := httptest.NewRecorder()
		a.server.Handler.ServeHTTP(rec, req)
		// Nothing in the answer may say whether the declaration was read:
		// Remnawave-style x-hwid-* response headers are exactly that tell.
		for name := range rec.Header() {
			if strings.HasPrefix(strings.ToLower(name), "x-hwid") {
				t.Errorf("response carries %s: capture must not be observable to the client", name)
			}
		}
		return rec
	}
	setSettings := func(edit func(*ports.UISettings)) {
		t.Helper()
		s, err := a.repos.Settings.Load(ctx, ports.UISettings{})
		if err != nil {
			t.Fatal(err)
		}
		edit(&s)
		if err := a.repos.Settings.Save(ctx, s); err != nil {
			t.Fatal(err)
		}
	}
	// The log insert is dispatched off the request thread, so wait for it.
	waitRows := func(n int) []domain.SubLog {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for {
			var rows []domain.SubLog
			if err := a.repos.SubLog.ScanSince(ctx, time.Now().Add(-time.Hour), 0, func(batch []domain.SubLog) error {
				rows = append(rows, batch...)
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			if len(rows) >= n || time.Now().After(deadline) {
				if len(rows) != n {
					t.Fatalf("sub_logs rows = %d, want %d", len(rows), n)
				}
				return rows
			}
			time.Sleep(20 * time.Millisecond)
		}
	}

	// (1) a normal fetch, (2) its revalidation, (3) a whitelist refusal.
	first := fetch("SomeClient/1.0", nil)
	if first.Code != http.StatusOK {
		t.Fatalf("fetch = %d, want 200: %s", first.Code, first.Body.String())
	}
	if rec := fetch("SomeClient/1.0", map[string]string{"If-None-Match": first.Header().Get("ETag")}); rec.Code != http.StatusNotModified {
		t.Fatalf("revalidation = %d, want 304", rec.Code)
	}
	setSettings(func(s *ports.UISettings) { s.SubClientFilterMode = "whitelist" })
	if rec := fetch("curl/8", nil); rec.Code != http.StatusForbidden {
		t.Fatalf("whitelist refusal = %d, want 403: %s", rec.Code, rec.Body.String())
	}

	for i, row := range waitRows(3) {
		if row.DeviceID != wantID {
			t.Errorf("row %d device_id = %q, want %q — is the hasher built in Build and handed to the /sub handler?", i, row.DeviceID, wantID)
		}
		if row.DeviceLabel != wantLabel {
			t.Errorf("row %d device_label = %q, want %q", i, row.DeviceLabel, wantLabel)
		}
	}

	// No column of any row holds the raw id, in either case. Read raw, so a
	// column the domain type does not map is checked too.
	db, err := sqlstore.Open(cfg.DBKind(), cfg.DBDSN())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
	var raw []map[string]any
	if err := db.Table("sub_logs").Find(&raw).Error; err != nil {
		t.Fatal(err)
	}
	if len(raw) != 3 {
		t.Fatalf("raw sub_logs rows = %d, want 3", len(raw))
	}
	for _, row := range raw {
		for col, v := range row {
			if strings.Contains(strings.ToLower(fmt.Sprint(v)), "h-1234567") {
				t.Errorf("sub_logs.%s holds the raw x-hwid: %v", col, v)
			}
		}
	}

	// Capture off: the next fetch is logged with no device at all.
	setSettings(func(s *ports.UISettings) {
		s.SubClientFilterMode = "blacklist"
		s.RiskHWIDCaptureOff = true
	})
	if rec := fetch("SomeClient/1.0", nil); rec.Code != http.StatusOK {
		t.Fatalf("fetch with capture off = %d, want 200", rec.Code)
	}
	rows := waitRows(4)
	last := rows[0]
	for _, r := range rows {
		if r.ID > last.ID {
			last = r
		}
	}
	if last.DeviceID != "" || last.DeviceLabel != "" {
		t.Errorf("capture off: logged device (%q, %q), want both empty", last.DeviceID, last.DeviceLabel)
	}
}
