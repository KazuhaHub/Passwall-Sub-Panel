package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"github.com/gin-gonic/gin"
)

type channelPanelRepo struct {
	ports.XUIPanelRepo
	panel                    *domain.Panel
	narrowWrites, fullWrites int
}

func (r *channelPanelRepo) GetByID(context.Context, int64) (*domain.Panel, error) {
	copy := *r.panel
	return &copy, nil
}
func (r *channelPanelRepo) GetByName(context.Context, string) (*domain.Panel, error) {
	return nil, domain.ErrNotFound
}
func (r *channelPanelRepo) Save(_ context.Context, panel *domain.Panel) error {
	r.fullWrites++
	copy := *panel
	r.panel = &copy
	return nil
}
func (r *channelPanelRepo) UpdateNativeMetadata(_ context.Context, _ int64, name, remark *string, channel *domain.PanelUpdateChannel) error {
	r.narrowWrites++
	if name != nil {
		r.panel.Name = *name
	}
	if remark != nil {
		r.panel.Remark = *remark
	}
	if channel != nil {
		r.panel.UpdateChannel = *channel
	}
	return nil
}

type channelPool struct {
	ports.PanelPool
	replaced int
	err      error
}

func (p *channelPool) Get(int64) (ports.PanelClient, error) { return nil, domain.ErrNotFound }
func (p *channelPool) SupportsKind(domain.PanelKind) bool   { return true }
func (p *channelPool) Replace(*domain.Panel) error          { p.replaced++; return p.err }

func updateChannelRequest(t *testing.T, h *AdminServersHandler, body string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = gin.Params{{Key: "id", Value: "41"}}
	c.Request = httptest.NewRequest(http.MethodPut, "https://panel.example/api/admin/servers/41", strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	h.Update(c)
	return w
}

func TestNativeServerUpdateChannelPersistsOnlyMetadataAndOmissionPreserves(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, initial := range []domain.PanelUpdateChannel{"", domain.PanelUpdateBeta, "future-custom"} {
		r := &channelPanelRepo{panel: &domain.Panel{ID: 41, Kind: domain.PanelKindPSP, Name: "original", URL: "psp://agt_original", Remark: "old", UpdateChannel: initial, PanelVersion: "observed-node", XrayVersion: "observed-core", APIToken: "untouched-token", Password: "untouched-secret"}}
		pool := &channelPool{}
		h := NewAdminServersHandler(r, pool, nil, nil, nil, nil)
		before := *r.panel
		w := updateChannelRequest(t, h, `{"name":"renamed"}`)
		if w.Code != 200 || r.panel.UpdateChannel != initial {
			t.Fatalf("omitted preference changed: %d", w.Code)
		}
		before.Name = "renamed"
		if !reflect.DeepEqual(before, *r.panel) || r.fullWrites != 0 || r.narrowWrites != 1 {
			t.Fatal("native metadata used a full-row save or changed non-metadata")
		}
		w = updateChannelRequest(t, h, `{"update_channel":"beta"}`)
		if w.Code != 200 || r.panel.UpdateChannel != domain.PanelUpdateBeta || r.fullWrites != 0 {
			t.Fatalf("preference update failed: %d", w.Code)
		}
		var dto serverDTO
		if err := json.Unmarshal(w.Body.Bytes(), &dto); err != nil {
			t.Fatal(err)
		}
		if dto.UpdateChannel != "beta" || strings.Contains(w.Body.String(), "untouched-secret") || strings.Contains(w.Body.String(), "untouched-token") {
			t.Fatal("wrong effective preference or secret-bearing response")
		}
		before.UpdateChannel = domain.PanelUpdateBeta
		if !reflect.DeepEqual(before, *r.panel) {
			t.Fatal("preference changed identity, credentials or runtime version")
		}
	}
}

func TestServerUpdateChannelRejectsInvalidAndExplicitThirdPartyInputBeforeWrites(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, test := range []struct {
		kind domain.PanelKind
		body string
	}{
		{domain.PanelKindPSP, `{"update_channel":""}`}, {domain.PanelKindPSP, `{"update_channel":"testing"}`},
		{domain.PanelKindPSP, `{"update_channel":"BETA"}`}, {domain.PanelKindPSP, `{"update_channel":1}`},
		{domain.PanelKind3XUI, `{"update_channel":"beta"}`}, {domain.PanelKindSUI, `{"update_channel":"stable"}`},
	} {
		r := &channelPanelRepo{panel: &domain.Panel{ID: 41, Kind: test.kind, Name: "original", URL: "original", APIToken: "token"}}
		pool := &channelPool{}
		before := *r.panel
		w := updateChannelRequest(t, NewAdminServersHandler(r, pool, nil, nil, nil, nil), test.body)
		if w.Code != 400 || r.narrowWrites != 0 || r.fullWrites != 0 || pool.replaced != 0 || !reflect.DeepEqual(before, *r.panel) {
			t.Fatalf("invalid update mutated state for %s: %d", test.kind, w.Code)
		}
	}
}

func TestNativeUpdateChannelPoolFailureRollsBackOnlySubmittedFields(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := &channelPanelRepo{panel: &domain.Panel{ID: 41, Kind: domain.PanelKindPSP, Name: "original", URL: "psp://agt_original", UpdateChannel: "future-custom", XrayVersion: "untouched-core"}}
	pool := &channelPool{err: errors.New("test pool failure")}
	before := *r.panel
	w := updateChannelRequest(t, NewAdminServersHandler(r, pool, nil, nil, nil, nil), `{"update_channel":"beta"}`)
	if w.Code != 500 || r.fullWrites != 0 || r.narrowWrites != 2 || !reflect.DeepEqual(before, *r.panel) {
		t.Fatal("preference rollback replaced unrelated fields or erased original raw preference")
	}
}

func TestNativeCreateUpdateChannelDefaultsStableOrAcceptsExplicitChoice(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, test := range []struct{ extra, want string }{{"", "stable"}, {`,"update_channel":"stable"`, "stable"}, {`,"update_channel":"beta"`, "beta"}} {
		provisioning := &nativeProvisioningRepoStub{}
		h := NewAdminServersHandler(nativeProvisioningPanelRepo{}, &nativeProvisioningPool{}, nil, nil, nil, nil).WithNativeAgentProvisioning(provisioning)
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodPost, "https://panel.example/api/admin/servers", strings.NewReader(`{"panel_type":"psp","name":"node"`+test.extra+`}`))
		c.Request.Header.Set("Content-Type", "application/json")
		h.Create(c)
		if w.Code != 201 || provisioning.panel == nil || string(provisioning.panel.UpdateChannel) != test.want {
			t.Fatalf("create preference: %d", w.Code)
		}
		var dto nativeServerCreateResponse
		if err := json.Unmarshal(w.Body.Bytes(), &dto); err != nil {
			t.Fatal(err)
		}
		if dto.Server.UpdateChannel != test.want {
			t.Fatal("create response omitted preference")
		}
	}
}

func TestServerCreateUpdateChannelRejectsInvalidOrThirdPartyPreference(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, body := range []string{
		`{"panel_type":"psp","name":"node","update_channel":""}`,
		`{"panel_type":"psp","name":"node","update_channel":"testing"}`,
		`{"panel_type":"3xui","name":"node","url":"https://original.invalid","update_channel":"stable"}`,
		`{"panel_type":"sui","name":"node","url":"https://original.invalid","api_token":"token","update_channel":"beta"}`,
	} {
		provisioning := &nativeProvisioningRepoStub{}
		h := NewAdminServersHandler(nativeProvisioningPanelRepo{}, &nativeProvisioningPool{}, nil, nil, nil, nil).WithNativeAgentProvisioning(provisioning)
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodPost, "https://panel.example/api/admin/servers", strings.NewReader(body))
		c.Request.Header.Set("Content-Type", "application/json")
		h.Create(c)
		if w.Code != 400 || provisioning.panel != nil {
			t.Fatalf("invalid create accepted: %d", w.Code)
		}
	}
}

func TestServerDTOUpdateChannelOnlyNativeAndLegacyDefaultsStable(t *testing.T) {
	for _, test := range []struct {
		kind domain.PanelKind
		raw  domain.PanelUpdateChannel
		want string
	}{
		{domain.PanelKindPSP, "", "stable"}, {domain.PanelKindPSP, "future-custom", "stable"},
		{domain.PanelKindPSP, domain.PanelUpdateBeta, "beta"}, {domain.PanelKind3XUI, domain.PanelUpdateBeta, ""}, {domain.PanelKindSUI, domain.PanelUpdateStable, ""},
	} {
		if got := toServerDTO(&domain.Panel{Kind: test.kind, UpdateChannel: test.raw}).UpdateChannel; got != test.want {
			t.Fatalf("effective DTO preference = %q, want %q", got, test.want)
		}
	}
}
