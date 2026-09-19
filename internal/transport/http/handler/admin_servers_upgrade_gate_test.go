package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"github.com/KazuhaHub/passwall-sub-panel/internal/version"
)

// The 3X-UI upgrade gate answers two different questions and used to answer both
// with the same word. "PSP has not tested this release" is a risk the admin may
// accept; "this panel is older than the floor this build speaks" is not. The
// tests below hold the two apart, and hold the post-upgrade verdict to the
// version the upgrade actually asked for.

type upgradeGateClient struct {
	ports.PanelClient
	info     *ports.PanelUpdateInfo
	upgraded bool
	status   ports.ServerStatus
	inbounds []ports.Inbound
}

func (c *upgradeGateClient) GetPanelUpdateInfo(context.Context) (*ports.PanelUpdateInfo, error) {
	return c.info, nil
}
func (c *upgradeGateClient) UpdatePanel(context.Context) error { c.upgraded = true; return nil }
func (c *upgradeGateClient) GetServerStatus(context.Context) (*ports.ServerStatus, error) {
	return &c.status, nil
}
func (c *upgradeGateClient) ListInbounds(context.Context) ([]ports.Inbound, error) {
	return c.inbounds, nil
}

type upgradeGateRepo struct {
	ports.XUIPanelRepo
	panel          *domain.XUIPanel
	updatedVersion string
}

func (r *upgradeGateRepo) GetByID(context.Context, int64) (*domain.XUIPanel, error) {
	return r.panel, nil
}
func (r *upgradeGateRepo) UpdateVersion(_ context.Context, _ int64, panelVersion, _ string, _ *time.Time) error {
	r.updatedVersion = panelVersion
	return nil
}

type upgradeGateAudit struct {
	ports.AuditRepo
	actions []string
	details []string
}

func (a *upgradeGateAudit) Insert(_ context.Context, e *domain.AuditEntry) error {
	a.actions = append(a.actions, e.Action)
	a.details = append(a.details, e.AfterJSON)
	return nil
}

func (a *upgradeGateAudit) saw(action string) bool {
	for _, got := range a.actions {
		if got == action {
			return true
		}
	}
	return false
}

// withXUITestedRange installs a known tested range and restores whatever was
// there before, so the gate tests do not depend on (or leak into) the loaded
// compat state.
func withXUITestedRange(t *testing.T, min, max string) {
	t.Helper()
	oldMin, oldMax := version.ActiveMinXUI(), version.ActiveMaxTestedXUI()
	t.Cleanup(func() {
		version.SetActiveMinXUI(oldMin)
		version.SetActiveMaxTestedXUI(oldMax)
	})
	version.SetActiveMinXUI(min)
	version.SetActiveMaxTestedXUI(max)
}

func postUpgradePanel(t *testing.T, h *AdminServersHandler, body string) *httptest.ResponseRecorder {
	t.Helper()
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Params = gin.Params{{Key: "id", Value: "7"}}
	ctx.Request = httptest.NewRequest(http.MethodPost, "/admin/servers/7/upgrade-panel", strings.NewReader(body))
	ctx.Request.Header.Set("Content-Type", "application/json")
	h.UpgradePanel(ctx)
	return recorder
}

func newUpgradeGateHandler(client *upgradeGateClient, audit *upgradeGateAudit) *AdminServersHandler {
	return &AdminServersHandler{
		repo:  &upgradeGateRepo{panel: &domain.XUIPanel{ID: 7, Kind: domain.PanelKind3XUI}},
		pool:  fakeWebCertPool{client: client},
		audit: audit,
	}
}

func TestUpgradePanelRefusesATooOldTargetEvenWhenForced(t *testing.T) {
	gin.SetMode(gin.TestMode)
	withXUITestedRange(t, "3.4.2", "3.5.1")
	client := &upgradeGateClient{info: &ports.PanelUpdateInfo{
		CurrentVersion: "3.0.0", LatestVersion: "v3.0.0", UpdateAvailable: true,
	}}
	audit := &upgradeGateAudit{}
	h := newUpgradeGateHandler(client, audit)

	recorder := postUpgradePanel(t, h, `{"force":true}`)

	if recorder.Code != http.StatusConflict {
		t.Fatalf("forced too-old upgrade = %d %s, want a refusal", recorder.Code, recorder.Body.String())
	}
	if client.upgraded {
		t.Fatal("a panel below the compiled floor was upgraded anyway because the request said force")
	}
	var body struct {
		Reason   string `json:"reason"`
		CanForce bool   `json:"can_force"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode refusal: %v (%s)", err, recorder.Body.String())
	}
	if body.Reason != "hard_incompatible" {
		t.Errorf("reason = %q, want hard_incompatible", body.Reason)
	}
	if body.CanForce {
		t.Error("can_force is true for a hard rejection; the UI would offer an override that the server refuses")
	}
	if !audit.saw("panel_upgrade_blocked") {
		t.Errorf("no blocked audit row; actions = %v", audit.actions)
	}
	if audit.saw("panel_upgrade_forced") || audit.saw("panel_upgrade_initiated") {
		t.Errorf("an upgrade was recorded: %v", audit.actions)
	}
}

func TestUpgradePanelRefusesAnUnclassifiableTargetEvenWhenForced(t *testing.T) {
	gin.SetMode(gin.TestMode)
	// No tested range loaded at all: every version reads as unknown.
	withXUITestedRange(t, "3.4.2", "")
	client := &upgradeGateClient{info: &ports.PanelUpdateInfo{
		CurrentVersion: "3.4.2", LatestVersion: "v3.5.0", UpdateAvailable: true,
	}}
	h := newUpgradeGateHandler(client, &upgradeGateAudit{})

	recorder := postUpgradePanel(t, h, `{"force":true}`)

	if recorder.Code != http.StatusConflict || client.upgraded {
		t.Fatalf("forced unknown-target upgrade = %d %s, upgraded=%v", recorder.Code, recorder.Body.String(), client.upgraded)
	}
}

func TestUpgradePanelForcesAnUntestedTargetAndSaysSo(t *testing.T) {
	gin.SetMode(gin.TestMode)
	withXUITestedRange(t, "3.4.2", "3.5.1")
	client := &upgradeGateClient{info: &ports.PanelUpdateInfo{
		CurrentVersion: "3.5.1", LatestVersion: "v3.6.0", UpdateAvailable: true,
	}}
	audit := &upgradeGateAudit{}
	h := newUpgradeGateHandler(client, audit)

	refused := postUpgradePanel(t, h, `{}`)
	if refused.Code != http.StatusConflict {
		t.Fatalf("unforced untested upgrade = %d %s, want a refusal with a way forward", refused.Code, refused.Body.String())
	}
	var refusal struct {
		Reason   string `json:"reason"`
		CanForce bool   `json:"can_force"`
	}
	if err := json.Unmarshal(refused.Body.Bytes(), &refusal); err != nil {
		t.Fatalf("decode refusal: %v", err)
	}
	if refusal.Reason != "untested_target" || !refusal.CanForce {
		t.Fatalf("refusal = %+v, want untested_target with can_force", refusal)
	}

	accepted := postUpgradePanel(t, h, `{"force":true}`)
	if accepted.Code != http.StatusAccepted || !client.upgraded {
		t.Fatalf("forced untested upgrade = %d %s, upgraded=%v", accepted.Code, accepted.Body.String(), client.upgraded)
	}
	if !audit.saw("panel_upgrade_forced") {
		t.Errorf("forced upgrade left no forced audit row: %v", audit.actions)
	}
}

func TestUpgradePreviewOnlyOffersForceWhereItWouldBeAccepted(t *testing.T) {
	gin.SetMode(gin.TestMode)
	withXUITestedRange(t, "3.4.2", "3.5.1")
	for _, tc := range []struct {
		latest   string
		wantCode int
		canForce bool
	}{
		{"v3.6.0", http.StatusOK, true},  // untested: force is a real option
		{"v3.0.0", http.StatusOK, false}, // too old: force would be refused, so do not offer it
	} {
		client := &upgradeGateClient{info: &ports.PanelUpdateInfo{
			CurrentVersion: "3.5.1", LatestVersion: tc.latest, UpdateAvailable: true,
		}}
		h := newUpgradeGateHandler(client, &upgradeGateAudit{})
		recorder := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(recorder)
		ctx.Params = gin.Params{{Key: "id", Value: "7"}}
		ctx.Request = httptest.NewRequest(http.MethodGet, "/admin/servers/7/upgrade-preview", nil)
		h.UpgradePreview(ctx)

		if recorder.Code != tc.wantCode {
			t.Fatalf("preview %s = %d %s", tc.latest, recorder.Code, recorder.Body.String())
		}
		var body struct {
			CanForce bool `json:"can_force"`
		}
		if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode preview: %v", err)
		}
		if body.CanForce != tc.canForce {
			t.Errorf("preview %s can_force = %v, want %v", tc.latest, body.CanForce, tc.canForce)
		}
	}
}

func TestPostUpgradeSmokeRecordsSuccessOnlyAtThePlannedTarget(t *testing.T) {
	gin.SetMode(gin.TestMode)
	shrinkUpgradeSmoke(t)
	client := &upgradeGateClient{status: ports.ServerStatus{PanelVersion: "3.6.0", XrayVersion: "26.9.9"}}
	audit := &upgradeGateAudit{}
	repo := &upgradeGateRepo{panel: &domain.XUIPanel{ID: 7}}
	h := &AdminServersHandler{repo: repo, pool: fakeWebCertPool{client: client}, audit: audit}

	// getPanelUpdateInfo reports the target with a "v"; the panel reports it
	// without one. Both name the same release.
	h.runPostUpgradeSmoke(context.Background(), 7, "panel", "v3.6.0")

	if !audit.saw("panel_upgrade_succeeded") {
		t.Fatalf("actions = %v, want panel_upgrade_succeeded", audit.actions)
	}
	if repo.updatedVersion != "3.6.0" {
		t.Errorf("cached version = %q, want the observed 3.6.0", repo.updatedVersion)
	}
}

func TestPostUpgradeSmokeRefusesToCallAnotherVersionASuccess(t *testing.T) {
	gin.SetMode(gin.TestMode)
	shrinkUpgradeSmoke(t)
	// The panel answered and its inbounds decoded, but it is running the
	// version it had before — the upgrade did not land. Reporting success here
	// is the defect: an admin reads "succeeded" and stops looking.
	client := &upgradeGateClient{status: ports.ServerStatus{PanelVersion: "3.5.1", XrayVersion: "26.9.9"}}
	audit := &upgradeGateAudit{}
	h := &AdminServersHandler{repo: &upgradeGateRepo{panel: &domain.XUIPanel{ID: 7}}, pool: fakeWebCertPool{client: client}, audit: audit}

	h.runPostUpgradeSmoke(context.Background(), 7, "panel", "v3.6.0")

	if audit.saw("panel_upgrade_succeeded") {
		t.Fatalf("a panel at 3.5.1 was recorded as a successful upgrade to 3.6.0: %v", audit.actions)
	}
	if !audit.saw("panel_upgrade_target_mismatch") {
		t.Fatalf("actions = %v, want panel_upgrade_target_mismatch", audit.actions)
	}
}

// shrinkUpgradeSmoke collapses the probe's real timings so a test can drive the
// whole retry loop instead of waiting out a panel restart.
func shrinkUpgradeSmoke(t *testing.T) {
	t.Helper()
	grace, interval, attempts := upgradeSmokeGrace, upgradeSmokeInterval, upgradeSmokeAttempts
	t.Cleanup(func() {
		upgradeSmokeGrace, upgradeSmokeInterval, upgradeSmokeAttempts = grace, interval, attempts
	})
	upgradeSmokeGrace = 0
	upgradeSmokeInterval = time.Millisecond
	upgradeSmokeAttempts = 2
}
