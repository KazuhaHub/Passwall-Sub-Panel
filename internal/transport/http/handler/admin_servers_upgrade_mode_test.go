package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// A panel that can be told to upgrade, but not to upgrade TO anything.
type unpinnableUpgradeClient struct {
	ports.PanelClient
	info *ports.PanelUpdateInfo
}

func (c *unpinnableUpgradeClient) GetPanelUpdateInfo(_ context.Context) (*ports.PanelUpdateInfo, error) {
	return c.info, nil
}
func (c *unpinnableUpgradeClient) UpdatePanel(_ context.Context) error { return nil }

// A panel that cannot be upgraded at all: no PanelUpdater.
type noUpgradeClient struct{ ports.PanelClient }

type upgradeModeRepo struct {
	ports.XUIPanelRepo
	panel *domain.XUIPanel
}

func (r upgradeModeRepo) GetByID(_ context.Context, _ int64) (*domain.XUIPanel, error) {
	return r.panel, nil
}

func upgradePreflight(t *testing.T, client ports.XUIClient) map[string]any {
	t.Helper()
	gin.SetMode(gin.TestMode)
	h := &AdminServersHandler{
		repo: upgradeModeRepo{panel: &domain.XUIPanel{ID: 7, Kind: domain.PanelKind3XUI}},
		pool: fakeWebCertPool{client: client},
	}
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Params = gin.Params{{Key: "id", Value: "7"}}
	c.Request = httptest.NewRequest(http.MethodGet, "/admin/servers/7/upgrade-preview", nil)
	h.UpgradePreview(c)

	if recorder.Code != http.StatusOK {
		t.Fatalf("preview = %d %s", recorder.Code, recorder.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	return body
}

// The disclosure that keeps a caller honest. 3X-UI's /updatePanel takes no
// version argument, so the target shown is what PSP read from upstream a moment
// ago — not something the panel will be held to. Treating target_version as a
// promise is the mistake this field exists to prevent, and the plan's
// consequence follows from it: a strict managed upgrade needs a pinnable
// executor, and this one is not.
func TestTheThreeXUIUpgradeTargetIsReportedAsUnpinnable(t *testing.T) {
	body := upgradePreflight(t, &unpinnableUpgradeClient{info: &ports.PanelUpdateInfo{
		CurrentVersion: "3.5.1", LatestVersion: "v3.6.0", UpdateAvailable: true,
	}})
	if body["target_pinnable"] != false {
		t.Errorf("target_pinnable = %v, want false", body["target_pinnable"])
	}
	if body["upgrade_mode"] != "latest_only" {
		t.Errorf("upgrade_mode = %v, want latest_only", body["upgrade_mode"])
	}
	// The UI still needs the version it is talking about; being honest about the
	// caveat must not mean withholding the fact.
	if body["target_version"] != "v3.6.0" {
		t.Errorf("target_version = %v", body["target_version"])
	}
}

// A panel with no PanelUpdater is refused by the API, not merely hidden in the
// UI. Hiding a button is a presentation decision; the refusal has to be one a
// caller cannot walk around, which is why it is tested at the endpoint.
func TestAPanelThatCannotUpgradeIsRefusedByTheAPI(t *testing.T) {
	for _, tc := range []struct {
		name   string
		method string
		call   func(*AdminServersHandler, *gin.Context)
	}{
		{"preview", http.MethodGet, (*AdminServersHandler).UpgradePreview},
		{"upgrade", http.MethodPost, (*AdminServersHandler).UpgradePanel},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			h := &AdminServersHandler{
				repo: upgradeModeRepo{panel: &domain.XUIPanel{ID: 7, Kind: domain.PanelKindSUI}},
				pool: fakeWebCertPool{client: &noUpgradeClient{}},
			}
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Params = gin.Params{{Key: "id", Value: "7"}}
			c.Request = httptest.NewRequest(tc.method, "/admin/servers/7/upgrade-preview", strings.NewReader("{}"))
			c.Request.Header.Set("Content-Type", "application/json")
			tc.call(h, c)

			if recorder.Code != http.StatusNotImplemented {
				t.Fatalf("%s = %d %s, want 501", tc.name, recorder.Code, recorder.Body.String())
			}
			if !strings.Contains(recorder.Body.String(), ports.ErrPanelCapabilityUnsupported.Error()) {
				t.Fatalf("%s body does not name the unsupported capability: %s", tc.name, recorder.Body.String())
			}
		})
	}
}
