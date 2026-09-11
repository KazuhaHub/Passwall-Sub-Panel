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

type nativeCoreClientStub struct {
	ports.PanelClient
	installed string
}

func (c *nativeCoreClientStub) GetCoreVersionList(context.Context) ([]string, error) {
	return []string{"26.6.27", "26.7.28", "26.9.9"}, nil
}

func (c *nativeCoreClientStub) InstallCore(_ context.Context, version string) error {
	c.installed = version
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
