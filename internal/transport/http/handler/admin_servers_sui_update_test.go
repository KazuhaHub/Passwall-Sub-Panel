package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/nodecompat"
	"github.com/KazuhaHub/passwall-sub-panel/internal/version"
)

type suiUpdateDispatcher struct {
	names []string
	work  []func(context.Context)
}

func (*suiUpdateDispatcher) Context() context.Context { return context.Background() }
func (d *suiUpdateDispatcher) Go(name string, fn func(context.Context)) {
	d.names = append(d.names, name)
	d.work = append(d.work, fn)
}

type suiUpdatePanelRepo struct {
	ports.XUIPanelRepo
	panels []*domain.Panel
	writes int
}

func (r *suiUpdatePanelRepo) ListPaged(context.Context, ports.Pagination) ([]*domain.Panel, int64, error) {
	return r.panels, int64(len(r.panels)), nil
}
func (r *suiUpdatePanelRepo) GetByID(context.Context, int64) (*domain.Panel, error) {
	return r.panels[0], nil
}
func (r *suiUpdatePanelRepo) UpdateVersion(context.Context, int64, string, string, *time.Time) error {
	r.writes++
	return nil
}

type suiUpdateClient struct {
	ports.PanelClient
	version string
}

func (*suiUpdateClient) Capabilities() []ports.PanelCapability {
	return []ports.PanelCapability{ports.CapabilityInboundRead, ports.CapabilityStatusRead}
}
func (*suiUpdateClient) ListInbounds(context.Context) ([]ports.Inbound, error) {
	return []ports.Inbound{{ID: 1}}, nil
}
func (c *suiUpdateClient) GetServerStatus(context.Context) (*ports.ServerStatus, error) {
	return &ports.ServerStatus{PanelVersion: c.version, XrayVersion: "1.14.0", CoreEngine: "sing-box"}, nil
}

func TestServerDTOSUIUpdateHintIsIndependentOfXUIAndUpgradeCapabilities(t *testing.T) {
	oldSUI, oldXUI := version.LatestSUI(), version.LatestXUI()
	t.Cleanup(func() { version.SetLatestSUI(oldSUI); version.SetLatestXUI(oldXUI) })
	version.SetLatestSUI("v1.6.2")
	version.SetLatestXUI("v3.7.0")
	for _, tc := range []struct {
		current string
		want    bool
	}{
		{"1.6.1", true}, {"v1.6.2", false}, {"1.7.0", false}, {"dev", false}, {"", false},
	} {
		h := &AdminServersHandler{pool: fakeWebCertPool{client: &suiUpdateClient{}}}
		dto := h.toServerDTOWithAgent(&domain.Panel{ID: 1, Kind: domain.PanelKindSUI, PanelVersion: tc.current}, nil, nodecompat.Policy(nodecompat.DefaultObservationAge()))
		if dto.UpdateAvailable != tc.want || dto.LatestXUIVersion != "" {
			t.Fatalf("current=%q DTO=%+v", tc.current, dto)
		}
		if tc.current != "" && dto.LatestSUIVersion != "v1.6.2" {
			t.Fatalf("S-UI tag missing: %+v", dto)
		}
		if slices.Contains(dto.Capabilities, ports.CapabilityPanelUpgrade) {
			t.Fatal("an availability hint must not invent remote-upgrade capability")
		}
	}
	for _, kind := range []domain.PanelKind{domain.PanelKind3XUI, domain.PanelKindPSP} {
		if dto := toServerDTO(&domain.Panel{Kind: kind, PanelVersion: "1.6.1"}); dto.LatestSUIVersion != "" {
			t.Fatalf("S-UI latest snapshot leaked into %q: %+v", kind, dto)
		}
	}
}

func TestAdminServersListQueuesOneSUIRefreshWithoutWaitingForGitHub(t *testing.T) {
	gin.SetMode(gin.TestMode)
	old := version.LatestSUI()
	t.Cleanup(func() { version.SetLatestSUI(old) })
	version.SetLatestSUI("v1.6.2")
	repo := &suiUpdatePanelRepo{panels: []*domain.Panel{
		{ID: 1, Kind: domain.PanelKindSUI, PanelVersion: "1.6.1"},
		{ID: 2, Kind: domain.PanelKindSUI, PanelVersion: "1.6.2"},
	}}
	dispatcher := &suiUpdateDispatcher{}
	h := NewAdminServersHandler(repo, fakeWebCertPool{client: &suiUpdateClient{}}, nil, nil, dispatcher, nil)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/admin/servers", nil)
	h.List(c)
	// The queued closure is intentionally not run: the HTTP result uses an
	// existing snapshot even if the app's background GitHub task is blocked.
	if w.Code != http.StatusOK || len(dispatcher.work) != 1 || dispatcher.names[0] != "admin-servers.latest-sui" {
		t.Fatalf("response=%d %s tasks=%v", w.Code, w.Body.String(), dispatcher.names)
	}
	var response struct {
		Items []serverDTO `json:"items"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil || len(response.Items) != 2 ||
		!response.Items[0].UpdateAvailable || response.Items[1].UpdateAvailable {
		t.Fatalf("response=%s error=%v", w.Body.String(), err)
	}
	for _, row := range response.Items {
		if row.LatestSUIVersion != "v1.6.2" || slices.Contains(row.Capabilities, ports.CapabilityPanelUpgrade) {
			t.Fatalf("row=%+v", row)
		}
	}
	// Other panel kinds must not trigger a needless S-UI GitHub check.
	repo.panels = []*domain.Panel{{ID: 3, Kind: domain.PanelKind3XUI}}
	w = httptest.NewRecorder()
	c, _ = gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/admin/servers", nil)
	h.List(c)
	if w.Code != http.StatusOK || len(dispatcher.work) != 1 {
		t.Fatal("non-S-UI fleet triggered a S-UI refresh")
	}
}

func TestAdminServersTestCarriesSUIHintForFreshObservationWithoutBlocking(t *testing.T) {
	gin.SetMode(gin.TestMode)
	old := version.LatestSUI()
	t.Cleanup(func() { version.SetLatestSUI(old) })
	version.SetLatestSUI("v1.6.2")
	for _, tc := range []struct {
		observed string
		want     bool
	}{{"1.6.1", true}, {"1.6.2", false}, {"1.7.0", false}, {"dev", false}} {
		repo := &suiUpdatePanelRepo{panels: []*domain.Panel{{ID: 1, Kind: domain.PanelKindSUI, PanelVersion: "stale"}}}
		dispatcher := &suiUpdateDispatcher{}
		h := NewAdminServersHandler(repo, fakeWebCertPool{client: &suiUpdateClient{version: tc.observed}}, nil, nil, dispatcher, nil)
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodPost, "/api/admin/servers/test", strings.NewReader(`{"id":1}`))
		c.Request.Header.Set("Content-Type", "application/json")
		h.Test(c)
		var response struct {
			OK               bool   `json:"ok"`
			PanelVersion     string `json:"panel_version"`
			LatestSUIVersion string `json:"latest_sui_version"`
			LatestXUIVersion string `json:"latest_xui_version"`
			UpdateAvailable  bool   `json:"update_available"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil || w.Code != http.StatusOK ||
			!response.OK || response.PanelVersion != tc.observed || response.LatestSUIVersion != "v1.6.2" ||
			response.LatestXUIVersion != "" || response.UpdateAvailable != tc.want || repo.writes != 1 || len(dispatcher.work) != 1 {
			t.Fatalf("response=%d %s writes=%d tasks=%d error=%v", w.Code, w.Body.String(), repo.writes, len(dispatcher.work), err)
		}
	}
}
