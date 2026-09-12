package handler

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/jwtutil"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/panelpath"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"github.com/KazuhaHub/passwall-sub-panel/internal/transport/http/middleware"
)

type installationAgents struct {
	ports.NodeAgentRepo
	agent   *domain.NodeAgent
	streams []*domain.NodeAgentStream
}

func (r installationAgents) GetByPanelID(context.Context, int64) (*domain.NodeAgent, error) {
	return r.agent, nil
}
func (r installationAgents) ListStreams(context.Context, string) ([]*domain.NodeAgentStream, error) {
	return r.streams, nil
}

type installationNodes struct {
	ports.NodeRepo
	nodes []*domain.Node
}

func (r installationNodes) List(context.Context) ([]*domain.Node, error) { return r.nodes, nil }

type installationClient struct {
	ports.PanelClient
	status   ports.ServerStatus
	err      error
	inbounds []ports.Inbound
}

func (r installationClient) GetServerStatus(context.Context) (*ports.ServerStatus, error) {
	return &r.status, r.err
}
func (r installationClient) ListInbounds(context.Context) ([]ports.Inbound, error) {
	return r.inbounds, nil
}

type installationPool struct {
	ports.PanelPool
	client ports.PanelClient
}

func (r installationPool) Get(int64) (ports.PanelClient, error) { return r.client, nil }

type installationAudit struct {
	ports.AuditRepo
	entries []*domain.AuditEntry
	err     error
}

func (r *installationAudit) Insert(_ context.Context, entry *domain.AuditEntry) error {
	r.entries = append(r.entries, entry)
	return r.err
}

func installationFixture(t *testing.T) (*AdminServersHandler, *nativeProvisioningRepoStub) {
	t.Helper()
	raw := "pspn_" + strings.Repeat("a", 43)
	digest := sha256.Sum256([]byte(raw))
	agent := &domain.NodeAgent{ID: 42, PanelID: 41, AgentID: "agt_existing", Epoch: 7, CredentialSHA256: hex.EncodeToString(digest[:])}
	r := &nativeProvisioningRepoStub{agent: agent, credential: raw}
	h := NewAdminServersHandler(nativeProvisioningPanelRepo{}, &nativeProvisioningPool{}, nil, nil, nil, nil).
		WithNativeAgentProvisioning(r).WithNodeAgents(installationAgents{agent: agent})
	return h, r
}

func installationRequest(h *AdminServersHandler, method, action, body, prefix string, role domain.Role) *httptest.ResponseRecorder {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	group := router.Group("/api/admin", func(c *gin.Context) {
		if role != "" {
			c.Set(middleware.CtxClaims, &jwtutil.Claims{Role: role, UPN: "owner@example.test"})
		}
		c.Next()
	}, middleware.RequireRole(domain.RoleAdmin))
	group.GET("/servers/:id/node-installation", h.NodeInstallation)
	group.POST("/servers/:id/node-credential", h.StoreNodeCredential)
	group.POST("/servers/:id/node-install-script", h.NodeInstallScript)
	group.GET("/servers/:id/node-agent-status", h.NodeAgentStatus)
	req := httptest.NewRequest(method, "https://panel.example/api/admin/servers/41/"+action, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(panelpath.WithRequest(req.Context(), prefix))
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w
}

func TestNodeInstallationRetrievalIsPrivateStableAndAudited(t *testing.T) {
	h, r := installationFixture(t)
	audit := &installationAudit{}
	h.audit = audit
	before := *r.agent
	for i := 0; i < 2; i++ {
		w := installationRequest(h, http.MethodGet, "node-installation", "", "/panel", domain.RoleAdmin)
		var response nativeServerCreateResponse
		if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		if w.Code != http.StatusOK || response.Credential != r.credential || response.AgentID != before.AgentID || response.Endpoint != "https://panel.example/panel/v1/node/sync" || !strings.Contains(w.Header().Get("Cache-Control"), "no-store") {
			t.Fatalf("private stable retrieval failed: status=%d endpoint=%s", w.Code, response.Endpoint)
		}
	}
	if !reflect.DeepEqual(before, *r.agent) || len(audit.entries) != 2 {
		t.Fatal("retrieval mutated identity or lacked audit")
	}
	for _, entry := range audit.entries {
		encoded, _ := json.Marshal(entry)
		if entry.Action != "node_credential_read" || entry.Actor != "owner@example.test" || strings.Contains(string(encoded), r.credential) {
			t.Fatal("secret-read audit leaked credential or missed actor")
		}
	}
	audit.err = errors.New("database unavailable")
	w := installationRequest(h, http.MethodGet, "node-installation", "", "", domain.RoleAdmin)
	if w.Code != http.StatusServiceUnavailable || strings.Contains(w.Body.String(), r.credential) {
		t.Fatal("unaudited recovery read released a credential")
	}
}

func TestNodeInstallationLegacyBackfillDoesNotRotate(t *testing.T) {
	h, r := installationFixture(t)
	raw, before := r.credential, *r.agent
	r.credential = ""
	w := installationRequest(h, http.MethodGet, "node-installation", "", "", domain.RoleAdmin)
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "node_credential_unavailable") {
		t.Fatal("legacy digest-only record must require explicit input")
	}
	w = installationRequest(h, http.MethodPost, "node-credential", `{"credential":"`+raw+`x"}`, "", domain.RoleAdmin)
	if w.Code != http.StatusConflict || r.credential != "" {
		t.Fatal("mismatched legacy credential was saved")
	}
	w = installationRequest(h, http.MethodPost, "node-credential", `{"credential":"`+raw+`"}`, "", domain.RoleAdmin)
	if w.Code != http.StatusOK || r.credential != raw || !reflect.DeepEqual(before, *r.agent) {
		t.Fatal("backfill changed stable identity or credential verifier")
	}
}

func TestNodeInstallScriptPrivateDownloadAndAdministratorBoundary(t *testing.T) {
	h, r := installationFixture(t)
	w := installationRequest(h, http.MethodPost, "node-install-script", `{"version":"v0.1.0"}`, "/panel", domain.RoleAdmin)
	if w.Code != http.StatusOK || !strings.HasPrefix(w.Body.String(), "#!/bin/sh") || !strings.Contains(w.Body.String(), r.credential) || !strings.Contains(w.Body.String(), "https://panel.example/panel/v1/node/sync") || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/plain") || !strings.Contains(w.Header().Get("Cache-Control"), "no-store") {
		t.Fatal("private installation script download failed")
	}
	for _, version := range []string{"latest", "v0.01.0", "v0.1.0;id", ""} {
		body, _ := json.Marshal(map[string]string{"version": version})
		w := installationRequest(h, http.MethodPost, "node-install-script", string(body), "", domain.RoleAdmin)
		if w.Code != http.StatusBadRequest || strings.Contains(w.Body.String(), r.credential) {
			t.Fatalf("unsafe version accepted: %q", version)
		}
	}
	for _, role := range []domain.Role{"", domain.RoleUser, domain.RoleOperator} {
		for _, route := range []struct{ method, path, body string }{{http.MethodGet, "node-installation", ""}, {http.MethodPost, "node-credential", `{"credential":"` + r.credential + `"}`}, {http.MethodPost, "node-install-script", `{"version":"v0.1.0"}`}, {http.MethodGet, "node-agent-status", ""}} {
			w := installationRequest(h, route.method, route.path, route.body, "", role)
			if (w.Code != http.StatusUnauthorized && w.Code != http.StatusForbidden) || strings.Contains(w.Body.String(), r.credential) {
				t.Fatalf("non-administrator accessed %s", route.path)
			}
		}
	}
}

func TestNodeAgentStatusRequiresFreshRuntimeAndConvergence(t *testing.T) {
	now := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	old := now.Add(-91 * time.Second)
	for _, tc := range []struct {
		name, want, core string
		seen             *time.Time
		nodes, inbounds  int
		converged        bool
		appliedEpoch     uint64
		err              error
	}{
		{name: "no heartbeat", want: "waiting"},
		{name: "stale heartbeat", seen: &old, want: "offline", nodes: 1},
		{name: "heartbeat only", seen: &now, want: "applying", err: domain.ErrNotFound},
		{name: "ready no nodes", seen: &now, want: "unconfigured", converged: true, appliedEpoch: 7},
		{name: "pending config", seen: &now, want: "applying", nodes: 1, core: "running"},
		{name: "old epoch acknowledgement", seen: &now, want: "applying", nodes: 1, core: "running", converged: true, appliedEpoch: 6, inbounds: 1},
		{name: "core stopped", seen: &now, want: "applying", nodes: 1, core: "stopped", converged: true, appliedEpoch: 7},
		{name: "listener unapplied", seen: &now, want: "applying", nodes: 1, core: "running", converged: true, appliedEpoch: 7},
		{name: "core failure", seen: &now, want: "error", nodes: 1, core: "degraded"},
		{name: "fully applied", seen: &now, want: "running", nodes: 1, inbounds: 1, core: "running", converged: true, appliedEpoch: 7},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, r := installationFixture(t)
			r.agent.LastSeen = tc.seen
			streams := []*domain.NodeAgentStream{}
			if tc.converged {
				for _, stream := range []domain.NodeAgentStreamName{domain.NodeAgentStreamConfig, domain.NodeAgentStreamRoster, domain.NodeAgentStreamDirectives} {
					streams = append(streams, &domain.NodeAgentStream{Stream: stream, DesiredETag: "A", AppliedETag: "A", AppliedEpoch: tc.appliedEpoch})
				}
			}
			h.agents = installationAgents{agent: r.agent, streams: streams}
			nodes := []*domain.Node{}
			for i := 0; i < tc.nodes; i++ {
				nodes = append(nodes, &domain.Node{PanelID: 41, Enabled: true})
			}
			h.nodes = installationNodes{nodes: nodes}
			h.pool = installationPool{client: installationClient{status: ports.ServerStatus{XrayState: tc.core}, err: tc.err, inbounds: make([]ports.Inbound, tc.inbounds)}}
			got, err := h.nodeAgentStatus(t.Context(), 41, now)
			if err != nil || got.State != tc.want || got.ConfiguredNodes != tc.nodes {
				t.Fatalf("state=%s err=%v want=%s", got.State, err, tc.want)
			}
		})
	}
}
