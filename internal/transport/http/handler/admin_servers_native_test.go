package handler

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

type nativeProvisioningPanelRepo struct{ ports.XUIPanelRepo }

func (nativeProvisioningPanelRepo) GetByName(context.Context, string) (*domain.XUIPanel, error) {
	return nil, domain.ErrNotFound
}

func (nativeProvisioningPanelRepo) GetByID(_ context.Context, id int64) (*domain.XUIPanel, error) {
	return &domain.XUIPanel{ID: id, Kind: domain.PanelKindPSP, Name: "native-west", URL: "psp://agt_existing"}, nil
}

type nativeProvisioningRepoStub struct {
	ports.NativeAgentProvisioningRepo
	panel *domain.XUIPanel
	agent *domain.NodeAgent
}

func (r *nativeProvisioningRepoStub) Create(_ context.Context, panel *domain.XUIPanel, agent *domain.NodeAgent) error {
	panel.ID = 41
	agent.ID = 42
	agent.PanelID = panel.ID
	panelCopy, agentCopy := *panel, *agent
	r.panel, r.agent = &panelCopy, &agentCopy
	return nil
}

func (r *nativeProvisioningRepoStub) RotateCredential(_ context.Context, panelID int64, digest string) (*domain.NodeAgent, error) {
	r.agent = &domain.NodeAgent{PanelID: panelID, AgentID: "agt_existing", CredentialSHA256: digest, Epoch: 7}
	return r.agent, nil
}

type nativeProvisioningPool struct {
	ports.XUIPool
	added *domain.XUIPanel
}

func (p *nativeProvisioningPool) SupportsKind(domain.PanelKind) bool { return true }
func (p *nativeProvisioningPool) Get(int64) (ports.PanelClient, error) {
	return nil, domain.ErrNotFound
}
func (p *nativeProvisioningPool) Add(panel *domain.Panel) error {
	copy := *panel
	p.added = &copy
	return nil
}

func TestAdminServersCreateNativeReturnsCredentialOnceAndStoresOnlyDigest(t *testing.T) {
	gin.SetMode(gin.TestMode)
	provisioning := &nativeProvisioningRepoStub{}
	pool := &nativeProvisioningPool{}
	handler := NewAdminServersHandler(nativeProvisioningPanelRepo{}, pool, nil, nil, nil, nil).
		WithNativeAgentProvisioning(provisioning)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "http://panel.example/api/admin/servers",
		strings.NewReader(`{"panel_type":"psp","name":"native-west","remark":"edge"}`))
	ctx.Request.Host = "panel.example"
	ctx.Request.Header.Set("Content-Type", "application/json")
	handler.Create(ctx)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("create status = %d: %s", recorder.Code, recorder.Body.String())
	}
	var response nativeServerCreateResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Server.Kind != string(domain.PanelKindPSP) || response.AgentID == "" ||
		!strings.HasPrefix(response.Credential, "pspn_") || response.Endpoint != "http://panel.example/v1/node/sync" {
		t.Fatalf("native response = %+v", response)
	}
	wantDigest := sha256.Sum256([]byte(response.Credential))
	if provisioning.agent == nil || provisioning.agent.CredentialSHA256 != hex.EncodeToString(wantDigest[:]) ||
		provisioning.agent.CredentialSHA256 == response.Credential {
		t.Fatalf("persisted agent = %+v", provisioning.agent)
	}
	if provisioning.panel == nil || provisioning.panel.URL != "psp://"+response.AgentID || pool.added == nil {
		t.Fatalf("provisioned panel = %+v, pool = %+v", provisioning.panel, pool.added)
	}
}

func TestAdminServersRotateNativeCredentialReturnsNewSecretAndPersistsOnlyDigest(t *testing.T) {
	gin.SetMode(gin.TestMode)
	provisioning := &nativeProvisioningRepoStub{}
	handler := NewAdminServersHandler(nativeProvisioningPanelRepo{}, &nativeProvisioningPool{}, nil, nil, nil, nil).
		WithNativeAgentProvisioning(provisioning)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Params = gin.Params{{Key: "id", Value: "41"}}
	ctx.Request = httptest.NewRequest(http.MethodPost,
		"https://panel.example/api/admin/servers/41/rotate-node-credential", nil)
	ctx.Request.Host = "panel.example"
	handler.RotateNativeCredential(ctx)
	if recorder.Code != http.StatusOK {
		t.Fatalf("rotate status = %d: %s", recorder.Code, recorder.Body.String())
	}
	var response nativeServerCreateResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.AgentID != "agt_existing" || !strings.HasPrefix(response.Credential, "pspn_") ||
		response.Endpoint != "https://panel.example/v1/node/sync" {
		t.Fatalf("rotation response = %+v", response)
	}
	wantDigest := sha256.Sum256([]byte(response.Credential))
	if provisioning.agent == nil || provisioning.agent.CredentialSHA256 != hex.EncodeToString(wantDigest[:]) ||
		provisioning.agent.CredentialSHA256 == response.Credential {
		t.Fatalf("persisted rotated agent = %+v", provisioning.agent)
	}
}
