package handler

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
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
		WithNativeAgentProvisioning(r).WithNodeAgents(installationAgents{agent: agent}).
		WithNodeInstallTemplate(fixtureInstallTemplate(t))
	// The constructor stamps startedAt from the wall clock, but these tests inject a
	// fixed `now`. Comparing the two would make the liveness verdict depend on the
	// real date, so pin it far enough back that "the panel has been listening" holds
	// for every case that does not deliberately say otherwise.
	h.startedAt = time.Unix(0, 0).UTC()
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
	group.POST("/servers/:id/node-installation-files", h.NodeInstallationFiles)
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

// THE SCRIPT DOWNLOAD USED TO BE THE ONE THING HERE THAT COULD NOT BE EXERCISED,
// and this case recorded that rather than hiding it: the endpoint rendered the
// version into a template compiled into the Node repository's module, whose own
// rule still read the legacy release shape and used the version as the download
// path — so a release this panel accepted was refused by the template, and PSP
// could not change that from here because the package was a pinned dependency.
//
// IT CAN BE EXERCISED NOW. The template is a signed asset the release publishes,
// so this endpoint renders a real published template and the case below asserts
// what the rendered script addresses. What it still owns besides that: the
// endpoint is private, and a refusal carries no credential.
func TestNodeInstallScriptPrivateDownloadAndAdministratorBoundary(t *testing.T) {
	h, r := installationFixture(t)
	w := installationRequest(h, http.MethodPost, "node-install-script", `{"version":"4.0.0"}`, "/panel", domain.RoleAdmin)
	if w.Code != http.StatusOK || !strings.HasPrefix(w.Body.String(), "#!/bin/sh") || !strings.Contains(w.Body.String(), r.credential) || !strings.Contains(w.Body.String(), "https://panel.example/panel/v1/node/sync") || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/plain") || !strings.Contains(w.Header().Get("Cache-Control"), "no-store") {
		t.Fatalf("private installation script download failed: status=%d", w.Code)
	}
	// THE SCRIPT CARRIES BOTH IDENTITIES, WHICH IS THE POINT. It builds the
	// download path from the TAG and the asset name from the VERSION, and they are
	// never the same string: the path segment a release lives at is
	// `release/4.0.0` while the archive it serves is `passwall-node_4.0.0_…`. A
	// renderer that had only one of them — which is what the template used to
	// take — could address no release this project publishes.
	for _, want := range []string{"version='4.0.0'", "tag='release/4.0.0'", "releases/download/${tag}", "passwall-node_${version}_linux_${arch}"} {
		if !strings.Contains(w.Body.String(), want) {
			t.Fatalf("the rendered script is missing %q, so it does not address the release by tag and name it by version", want)
		}
	}
	for _, version := range []string{"latest", "04.0.0", "4.0.0;id", ""} {
		body, _ := json.Marshal(map[string]string{"version": version})
		w := installationRequest(h, http.MethodPost, "node-install-script", string(body), "", domain.RoleAdmin)
		if w.Code != http.StatusBadRequest || strings.Contains(w.Body.String(), r.credential) {
			t.Fatalf("unsafe version accepted: %q", version)
		}
	}
	for _, role := range []domain.Role{"", domain.RoleUser, domain.RoleOperator} {
		for _, route := range []struct{ method, path, body string }{{http.MethodGet, "node-installation", ""}, {http.MethodPost, "node-credential", `{"credential":"` + r.credential + `"}`}, {http.MethodPost, "node-install-script", `{"version":"4.0.0"}`}, {http.MethodGet, "node-agent-status", ""}} {
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

// A restart of the PANEL is not evidence about the NODE. last_seen is durable but
// stops advancing while PSP is down, so an outage longer than the silence window
// leaves every healthy agent looking dead the moment PSP returns — an operator hit
// exactly this and reported the fleet as disconnected when nothing was wrong.
//
// The rule under test: "offline" may only be asserted once THIS process has been
// listening for a full silence window. Before that, not-heard-from is reported as
// its own state rather than as a failure PSP has not established.
//
// The three unknowns must stay three. "never connected", "panel has not listened
// long enough" and "agent has gone silent" have different causes and different
// operator actions; collapsing any pair of them is the defect this guards.
func TestNodeAgentStatusDoesNotBlameTheNodeForThePanelsOwnDowntime(t *testing.T) {
	now := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)
	stale := now.Add(-91 * time.Second) // beyond 3 x the 30s poll
	fresh := now.Add(-5 * time.Second)
	for _, tc := range []struct {
		name  string
		seen  *time.Time
		upFor time.Duration
		want  string
	}{
		{name: "silent agent, panel listening long enough", seen: &stale, upFor: 10 * time.Minute, want: "offline"},
		{name: "silent agent, panel only just restarted", seen: &stale, upFor: 3 * time.Second, want: "awaiting_checkin"},
		{name: "panel exactly one window old", seen: &stale, upFor: 90 * time.Second, want: "offline"},
		// The new branch must not swallow an agent that IS reporting.
		{name: "fresh agent, panel only just restarted", seen: &fresh, upFor: 3 * time.Second, want: "applying"},
		// Never-connected keeps its own answer: nothing was ever installed here,
		// which is a different problem with a different fix.
		{name: "never connected, panel only just restarted", seen: nil, upFor: 3 * time.Second, want: "waiting"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, r := installationFixture(t)
			r.agent.LastSeen = tc.seen
			h.startedAt = now.Add(-tc.upFor)
			h.agents = installationAgents{agent: r.agent}
			h.nodes = installationNodes{nodes: []*domain.Node{{PanelID: 41, Enabled: true}}}
			h.pool = installationPool{client: installationClient{err: domain.ErrNotFound}}
			got, err := h.nodeAgentStatus(t.Context(), 41, now)
			if err != nil {
				t.Fatalf("nodeAgentStatus: %v", err)
			}
			if got.State != tc.want {
				t.Fatalf("state=%q want %q (agent last seen %v, panel up for %v)", got.State, tc.want, tc.seen, tc.upFor)
			}
		})
	}
}

// failingInstallTemplate is a source that refuses everything the way one of the
// two failure KINDS does — the distinction the endpoints have to preserve.
type failingInstallTemplate struct{ err error }

func (f failingInstallTemplate) Validate(ports.InstallTemplateRequest) error { return f.err }
func (f failingInstallTemplate) Render(context.Context, ports.InstallTemplateRequest) (string, error) {
	return "", f.err
}

// A PUBLICATION THAT COULD NOT BE READ IS NOT A BAD REQUEST.
//
// The endpoint used to have one failure mode, because rendering was a pure
// function over a compiled-in template: every refusal was something the caller had
// done. It now fetches a signed asset, so a refusal can also mean the release could
// not be obtained or was not signed by this project — and answering "a canonical
// HTTPS PSP endpoint and exact published Node version are required" to that sends
// an operator to re-check an endpoint and a version that are both fine, while the
// publication is what is wrong. The two are told apart, and neither leaks the
// credential.
func TestInstallationEndpointsSeparateABadRequestFromAnUnreadableRelease(t *testing.T) {
	h, r := installationFixture(t)
	for _, tc := range []struct {
		name       string
		err        error
		action     string
		body       string
		wantStatus int
	}{
		{"an unreadable release", ports.ErrInstallTemplateSource, "node-install-script", `{"version":"4.0.0"}`, http.StatusBadGateway},
		{"a release that failed verification", fmt.Errorf("%w: bad signature", ports.ErrInstallTemplateSource), "node-install-script", `{"version":"4.0.0"}`, http.StatusBadGateway},
		{"the caller's own request", ports.ErrInstallTemplateRequest, "node-install-script", `{"version":"4.0.0"}`, http.StatusBadRequest},
		{"an unreadable release for the Docker bundle", ports.ErrInstallTemplateSource, "node-installation-files", `{"method":"docker","version":"latest"}`, http.StatusBadGateway},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h.WithNodeInstallTemplate(failingInstallTemplate{err: tc.err})
			w := installationRequest(h, http.MethodPost, tc.action, tc.body, "", domain.RoleAdmin)
			if w.Code != tc.wantStatus {
				t.Fatalf("status=%d, want %d; body=%s", w.Code, tc.wantStatus, w.Body.String())
			}
			if strings.Contains(w.Body.String(), r.credential) {
				t.Fatal("a refusal carried the node credential")
			}
		})
	}
}

// AND A HANDLER WITH NO SOURCE AT ALL REFUSES rather than panicking. The field is
// optional in the same way every other dependency on this handler is, and its
// degraded behaviour is a refusal: there is no script to hand over, and inventing
// one is not an option.
func TestInstallationEndpointsRefuseWithoutATemplateSource(t *testing.T) {
	h, _ := installationFixture(t)
	h.WithNodeInstallTemplate(nil)
	w := installationRequest(h, http.MethodPost, "node-install-script", `{"version":"4.0.0"}`, "", domain.RoleAdmin)
	if w.Code != http.StatusBadGateway {
		t.Fatalf("a handler with no template source answered %d", w.Code)
	}
}
