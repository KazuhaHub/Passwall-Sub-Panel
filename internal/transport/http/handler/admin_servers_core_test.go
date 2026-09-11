package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

type nativeCoreClientStub struct {
	ports.PanelClient
	installed       string
	installedEngine domain.NodeCoreEngine
	allowRestricted bool
}

func (c *nativeCoreClientStub) GetCoreVersionList(context.Context) ([]string, error) {
	return []string{"26.6.27", "26.7.28", "26.9.9"}, nil
}

func (c *nativeCoreClientStub) InstallCore(_ context.Context, version string) error {
	c.installed = version
	return nil
}

func (c *nativeCoreClientStub) GetCoreVersionListForEngine(_ context.Context, engine domain.NodeCoreEngine) ([]string, error) {
	switch engine {
	case domain.NodeCoreXray:
		return []string{"26.6.27", "26.7.28", "26.9.9"}, nil
	case domain.NodeCoreSingBox:
		return []string{"1.14.0"}, nil
	default:
		return nil, errors.New("unsupported engine")
	}
}

func (c *nativeCoreClientStub) InstallCoreEngine(_ context.Context, engine domain.NodeCoreEngine, version string, allow bool) error {
	c.installedEngine = engine
	c.installed = version
	c.allowRestricted = allow
	return nil
}

func (*nativeCoreClientStub) ApplyIsAsynchronous() bool { return true }

type nativeCorePanelRepo struct {
	ports.XUIPanelRepo
	panel *domain.XUIPanel
}

func (r nativeCorePanelRepo) GetByID(context.Context, int64) (*domain.XUIPanel, error) {
	return r.panel, nil
}

type nativeCoreListPanelRepo struct {
	ports.XUIPanelRepo
	panels []*domain.XUIPanel
}

func (r nativeCoreListPanelRepo) ListPaged(context.Context, ports.Pagination) ([]*domain.XUIPanel, int64, error) {
	return r.panels, int64(len(r.panels)), nil
}

type nativeCoreAgentRepo struct {
	ports.NodeAgentRepo
	agents []*domain.NodeAgent
	lists  int
	ids    []int64
}

func (r *nativeCoreAgentRepo) ListByPanelIDs(_ context.Context, panelIDs []int64) ([]*domain.NodeAgent, error) {
	r.lists++
	r.ids = append([]int64(nil), panelIDs...)
	return r.agents, nil
}

func TestNativeCoreUpgradeRequiresRestrictedConfirmationAndReturnsAccepted(t *testing.T) {
	gin.SetMode(gin.TestMode)
	client := &nativeCoreClientStub{}
	handler := &AdminServersHandler{
		repo: nativeCorePanelRepo{panel: &domain.XUIPanel{ID: 9, Kind: domain.PanelKindPSP}},
		pool: fakeWebCertPool{client: client},
	}

	request := func(body string) *httptest.ResponseRecorder {
		recorder := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(recorder)
		ctx.Params = gin.Params{{Key: "id", Value: "9"}}
		ctx.Request = httptest.NewRequest(http.MethodPost, "/admin/servers/9/upgrade-xray", strings.NewReader(body))
		ctx.Request.Header.Set("Content-Type", "application/json")
		handler.UpgradeXray(ctx)
		return recorder
	}

	blocked := request(`{"version":"26.9.9"}`)
	if blocked.Code != http.StatusConflict || client.installed != "" {
		t.Fatalf("unconfirmed response = %d %s, installed=%q", blocked.Code, blocked.Body.String(), client.installed)
	}
	var blockedBody struct {
		Reason string `json:"reason"`
	}
	if err := json.Unmarshal(blocked.Body.Bytes(), &blockedBody); err != nil || blockedBody.Reason != "restricted_core_confirmation_required" {
		t.Fatalf("blocked body = (%+v, %v)", blockedBody, err)
	}

	accepted := request(`{"version":"26.9.9","confirm_restricted":true}`)
	if accepted.Code != http.StatusAccepted || client.installed != "26.9.9" {
		t.Fatalf("confirmed response = %d %s, installed=%q", accepted.Code, accepted.Body.String(), client.installed)
	}
}

func TestNativeCoreUpgradeRejectsLatest(t *testing.T) {
	gin.SetMode(gin.TestMode)
	client := &nativeCoreClientStub{}
	handler := &AdminServersHandler{
		repo: nativeCorePanelRepo{panel: &domain.XUIPanel{ID: 9, Kind: domain.PanelKindPSP}},
		pool: fakeWebCertPool{client: client},
	}
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Params = gin.Params{{Key: "id", Value: "9"}}
	ctx.Request = httptest.NewRequest(http.MethodPost, "/admin/servers/9/upgrade-xray", strings.NewReader(`{"version":"latest"}`))
	ctx.Request.Header.Set("Content-Type", "application/json")
	handler.UpgradeXray(ctx)
	if recorder.Code != http.StatusBadRequest || client.installed != "" {
		t.Fatalf("latest response = %d %s, installed=%q", recorder.Code, recorder.Body.String(), client.installed)
	}
}

func TestNativeCoreSelectionListsAndAcceptsAuditedSingBox(t *testing.T) {
	gin.SetMode(gin.TestMode)
	client := &nativeCoreClientStub{}
	handler := &AdminServersHandler{
		repo: nativeCorePanelRepo{panel: &domain.XUIPanel{ID: 9, Kind: domain.PanelKindPSP}},
		pool: fakeWebCertPool{client: client},
	}

	listRecorder := httptest.NewRecorder()
	listContext, _ := gin.CreateTestContext(listRecorder)
	listContext.Params = gin.Params{{Key: "id", Value: "9"}}
	listContext.Request = httptest.NewRequest(http.MethodGet, "/admin/servers/9/core-releases", nil)
	handler.ListCoreReleases(listContext)
	if listRecorder.Code != http.StatusOK || !strings.Contains(listRecorder.Body.String(), `"engine":"sing-box"`) ||
		!strings.Contains(listRecorder.Body.String(), `"version":"1.14.0"`) {
		t.Fatalf("core catalog response = %d %s", listRecorder.Code, listRecorder.Body.String())
	}

	selectRecorder := httptest.NewRecorder()
	selectContext, _ := gin.CreateTestContext(selectRecorder)
	selectContext.Params = gin.Params{{Key: "id", Value: "9"}}
	selectContext.Request = httptest.NewRequest(http.MethodPost, "/admin/servers/9/select-core",
		strings.NewReader(`{"engine":"sing-box","version":"1.14.0"}`))
	selectContext.Request.Header.Set("Content-Type", "application/json")
	handler.SelectCore(selectContext)
	if selectRecorder.Code != http.StatusAccepted || client.installedEngine != domain.NodeCoreSingBox ||
		client.installed != "1.14.0" || client.allowRestricted {
		t.Fatalf("select response = %d %s, selected=%s/%s allow=%v", selectRecorder.Code,
			selectRecorder.Body.String(), client.installedEngine, client.installed, client.allowRestricted)
	}
}

func TestNativeCoreSelectionRequiresExactRestrictionAcknowledgement(t *testing.T) {
	gin.SetMode(gin.TestMode)
	client := &nativeCoreClientStub{}
	handler := &AdminServersHandler{
		repo: nativeCorePanelRepo{panel: &domain.XUIPanel{ID: 9, Kind: domain.PanelKindPSP}},
		pool: fakeWebCertPool{client: client},
	}
	request := func(body string) *httptest.ResponseRecorder {
		recorder := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(recorder)
		ctx.Params = gin.Params{{Key: "id", Value: "9"}}
		ctx.Request = httptest.NewRequest(http.MethodPost, "/admin/servers/9/select-core", strings.NewReader(body))
		ctx.Request.Header.Set("Content-Type", "application/json")
		handler.SelectCore(ctx)
		return recorder
	}

	unconfirmed := request(`{"engine":"xray","version":"26.9.9"}`)
	if unconfirmed.Code != http.StatusConflict || client.installed != "" ||
		!strings.Contains(unconfirmed.Body.String(), `"reason":"restricted_core_confirmation_required"`) {
		t.Fatalf("unconfirmed response = %d %s, selected=%s/%s", unconfirmed.Code,
			unconfirmed.Body.String(), client.installedEngine, client.installed)
	}

	confirmed := request(`{"engine":"xray","version":"26.9.9","confirm_restricted":true}`)
	if confirmed.Code != http.StatusAccepted || client.installedEngine != domain.NodeCoreXray ||
		client.installed != "26.9.9" || !client.allowRestricted {
		t.Fatalf("confirmed response = %d %s, selected=%s/%s allow=%v", confirmed.Code,
			confirmed.Body.String(), client.installedEngine, client.installed, client.allowRestricted)
	}

	unexpected := request(`{"engine":"sing-box","version":"1.14.0","confirm_restricted":true}`)
	if unexpected.Code != http.StatusBadRequest ||
		!strings.Contains(unexpected.Body.String(), `"reason":"unexpected_restricted_confirmation"`) {
		t.Fatalf("unexpected acknowledgement response = %d %s", unexpected.Code, unexpected.Body.String())
	}

	unaudited := request(`{"engine":"sing-box","version":"latest"}`)
	if unaudited.Code != http.StatusBadRequest ||
		!strings.Contains(unaudited.Body.String(), `"reason":"core_not_audited"`) {
		t.Fatalf("unaudited response = %d %s", unaudited.Code, unaudited.Body.String())
	}
}

func TestServerListSeparatesDesiredAndObservedNativeCoreWithoutPerRowQueries(t *testing.T) {
	gin.SetMode(gin.TestMode)
	panel := &domain.XUIPanel{
		ID: 9, Kind: domain.PanelKindPSP, Name: "native", PanelVersion: "v0.2.0", XrayVersion: "26.6.27",
	}
	agents := &nativeCoreAgentRepo{agents: []*domain.NodeAgent{{
		PanelID: 9, DesiredCoreEngine: domain.NodeCoreSingBox, DesiredCoreVersion: "1.14.0",
		ObservedCoreEngine: domain.NodeCoreXray,
	}}}
	handler := &AdminServersHandler{
		repo: nativeCoreListPanelRepo{panels: []*domain.XUIPanel{panel}},
		pool: fakeWebCertPool{client: &nativeCoreClientStub{}}, agents: agents,
	}
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/admin/servers", nil)
	handler.List(ctx)
	if recorder.Code != http.StatusOK {
		t.Fatalf("list response = %d %s", recorder.Code, recorder.Body.String())
	}
	if agents.lists != 1 || len(agents.ids) != 1 || agents.ids[0] != panel.ID {
		t.Fatalf("node agent batch reads = %d ids=%v, want one page-scoped read", agents.lists, agents.ids)
	}
	var envelope struct {
		Items []serverDTO `json:"items"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if len(envelope.Items) != 1 {
		t.Fatalf("server items = %+v", envelope.Items)
	}
	got := envelope.Items[0]
	if got.CoreEngine != "xray" || got.CoreVersion != "26.6.27" ||
		got.DesiredCoreEngine != "sing-box" || got.DesiredCoreVersion != "1.14.0" {
		t.Fatalf("native core identity = %+v", got)
	}
}
