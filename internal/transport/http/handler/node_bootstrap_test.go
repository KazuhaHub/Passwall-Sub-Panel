package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/operationgate"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/panelpath"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"github.com/KazuhaHub/passwall-sub-panel/internal/transport/http/middleware"
	"github.com/gin-gonic/gin"
)

type bootstrapPanelRepo struct {
	ports.XUIPanelRepo
	panel *domain.Panel
}

func (r *bootstrapPanelRepo) GetByID(_ context.Context, id int64) (*domain.Panel, error) {
	if r.panel == nil || r.panel.ID != id {
		return nil, domain.ErrNotFound
	}
	p := *r.panel
	return &p, nil
}

type bootstrapAgentRepo struct {
	ports.NodeAgentRepo
	provisioning *nativeProvisioningRepoStub
}

func (r bootstrapAgentRepo) GetByPanelID(context.Context, int64) (*domain.NodeAgent, error) {
	if r.provisioning.agent == nil {
		return nil, domain.ErrNotFound
	}
	a := *r.provisioning.agent
	return &a, nil
}

type bootstrapCatalog struct{ err error }

func (r bootstrapCatalog) List(context.Context) (ports.NodeReleaseList, error) {
	return ports.NodeReleaseList{Releases: []ports.NodeReleaseCatalogEntry{{Version: "v0.0.1-beta3", Methods: []string{"linux"}, Platforms: []ports.NodeReleasePlatform{{OS: "linux", Arch: "amd64"}, {OS: "linux", Arch: "arm64"}}}}}, r.err
}

type bootstrapPreview struct{ blocked, stale bool }

func (r *bootstrapPreview) Preview(_ context.Context, id int64, core string, allow bool) (*domain.ServerMigrationPreview, error) {
	fp := strings.Repeat("a", 64)
	if r.stale {
		fp = strings.Repeat("b", 64)
	}
	p := &domain.ServerMigrationPreview{ServerID: id, CoreVersion: core, Fingerprint: fp, AllowRestrictedReality: allow, CanMigrate: !r.blocked, Blockers: []domain.MigrationIssue{}}
	if r.blocked {
		p.Blockers = []domain.MigrationIssue{{Code: "external_files"}}
	}
	return p, nil
}

type bootstrapPool struct {
	ports.PanelPool
	replaced, removed int
	replaceErr        error
	panel             *domain.Panel
}

func (p *bootstrapPool) Get(int64) (ports.PanelClient, error) { return nil, domain.ErrNotFound }

func (p *bootstrapPool) Replace(panel *domain.Panel) error {
	p.replaced++
	p.panel = panel
	return p.replaceErr
}
func (p *bootstrapPool) Remove(int64) error { p.removed++; return nil }

type bootstrapMigration struct {
	ports.ServerMigrationRepo
	panels             *bootstrapPanelRepo
	provisioning       *nativeProvisioningRepoStub
	calls              int
	err                error
	commitDespiteError bool
	applyHook          func()
}

func (r *bootstrapMigration) Apply(_ context.Context, id int64, fp string, agent *domain.NodeAgent, raw string) error {
	r.calls++
	if r.applyHook != nil {
		r.applyHook()
	}
	if fp != strings.Repeat("a", 64) {
		return domain.ErrConflict
	}
	if r.err != nil && !r.commitDespiteError {
		return r.err
	}
	r.panels.panel.Kind = domain.PanelKindPSP
	r.panels.panel.URL = "psp://" + agent.AgentID
	agentCopy := *agent
	agentCopy.ID = 42
	agentCopy.Epoch = 1
	r.provisioning.agent = &agentCopy
	r.provisioning.credential = raw
	return r.err
}

type bootstrapFixture struct {
	h            *NodeBootstrapHandler
	r            *gin.Engine
	panels       *bootstrapPanelRepo
	provisioning *nativeProvisioningRepoStub
	preview      *bootstrapPreview
	pool         *bootstrapPool
	migration    *bootstrapMigration
	audit        *installationAudit
}

func newBootstrapFixture(t *testing.T, migration bool) *bootstrapFixture {
	t.Helper()
	gin.SetMode(gin.TestMode)
	servers, provisioning := installationFixture(t)
	panels := &bootstrapPanelRepo{panel: &domain.Panel{ID: 41, Kind: domain.PanelKindPSP, Name: "existing", URL: "psp://agt_existing"}}
	if migration {
		panels.panel.Kind = domain.PanelKind3XUI
		panels.panel.URL = "https://old.example"
		provisioning.agent = nil
		provisioning.credential = ""
	}
	pool := &bootstrapPool{}
	audit := &installationAudit{}
	preview := &bootstrapPreview{}
	servers.repo = panels
	servers.pool = pool
	servers.audit = audit
	servers.agents = bootstrapAgentRepo{provisioning: provisioning}
	servers.nodeReleases = bootstrapCatalog{}
	servers.serverMigration = preview
	migrations := &bootstrapMigration{panels: panels, provisioning: provisioning}
	h := NewNodeBootstrapHandler(servers, migrations, operationgate.New())
	r := gin.New()
	r.Use(middleware.BackendOperationGate(h.gate))
	r.POST("/api/admin/servers/:id/node-install-command", h.MintInstall)
	r.POST("/api/admin/servers/:id/node-migration-command", h.MintMigration)
	r.GET("/node-bootstrap/:token", h.Download)
	r.POST("/api/node-bootstrap/complete", h.Complete)
	return &bootstrapFixture{h: h, r: r, panels: panels, provisioning: provisioning, preview: preview, pool: pool, migration: migrations, audit: audit}
}
func (f *bootstrapFixture) request(method, path, body, token string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, "https://panel.example"+path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	req = req.WithContext(panelpath.WithRequest(req.Context(), "/private-panel"))
	w := httptest.NewRecorder()
	f.r.ServeHTTP(w, req)
	return w
}

var commandToken = regexp.MustCompile(`/node-bootstrap/([A-Za-z0-9_-]{43})`)

func mintBootstrap(t *testing.T, f *bootstrapFixture, migration bool) (string, *bootstrapTicket) {
	t.Helper()
	action, body := "node-install-command", `{"version":"v0.0.1-beta3"}`
	if migration {
		action = "node-migration-command"
		body = `{"version":"v0.0.1-beta3","core_version":"26.6.27","fingerprint":"` + strings.Repeat("a", 64) + `","managed_only":true,"confirm_single_instance":true}`
	}
	w := f.request(http.MethodPost, "/api/admin/servers/41/"+action, body, "")
	if w.Code != 200 {
		t.Fatalf("mint: %d %s", w.Code, w.Body.String())
	}
	var data struct {
		ServerID int64     `json:"server_id"`
		Command  string    `json:"command"`
		Expires  time.Time `json:"expires_at"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &data); err != nil {
		t.Fatal(err)
	}
	if data.ServerID != 41 || data.Expires.IsZero() || len(data.Command) > 320 || strings.ContainsAny(data.Command, "\r\n\x00") || strings.Contains(data.Command, "pspn_") || !strings.Contains(data.Command, "/private-panel/node-bootstrap/") || !strings.Contains(data.Command, "bash -n") || !strings.Contains(data.Command, "--proto") {
		t.Fatalf("invalid private command: %+v", data)
	}
	match := commandToken.FindStringSubmatch(data.Command)
	if len(match) != 2 {
		t.Fatal("missing token")
	}
	return match[1], f.h.lookup(match[1])
}

func TestNodeBootstrapNativeOneUseIdentityAndPrivateAudit(t *testing.T) {
	f := newBootstrapFixture(t, false)
	raw := f.provisioning.credential
	token, _ := mintBootstrap(t, f, false)
	w := f.request(http.MethodGet, "/node-bootstrap/"+token, "", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), raw) || w.Header().Get("Cache-Control") != "no-store, private" || w.Header().Get("Content-Length") != strconv.Itoa(w.Body.Len()) {
		t.Fatalf("download: %d", w.Code)
	}
	if len(f.audit.entries) != 2 {
		t.Fatal("secret release missing mandatory audit")
	}
	for _, entry := range f.audit.entries {
		encoded, _ := json.Marshal(entry)
		if strings.Contains(string(encoded), token) || strings.Contains(string(encoded), raw) {
			t.Fatal("audit secret leak")
		}
	}
	w = f.request(http.MethodGet, "/node-bootstrap/"+token, "", "")
	if w.Code != http.StatusGone || strings.Contains(w.Body.String(), raw) {
		t.Fatal("download replay")
	}
	if f.provisioning.credential != raw || f.provisioning.agent.AgentID != "agt_existing" {
		t.Fatal("install rotated identity")
	}
}

func TestNodeBootstrapNativeRejectsRotationExpiryAndAuditFailure(t *testing.T) {
	for _, mode := range []string{"rotation", "expiry", "audit"} {
		t.Run(mode, func(t *testing.T) {
			f := newBootstrapFixture(t, false)
			token, ticket := mintBootstrap(t, f, false)
			switch mode {
			case "rotation":
				f.provisioning.agent.CredentialSHA256 = strings.Repeat("b", 64)
			case "expiry":
				f.h.now = func() time.Time { return ticket.expires.Add(time.Second) }
			case "audit":
				f.audit.err = errors.New("SECRET")
			}
			w := f.request("GET", "/node-bootstrap/"+token, "", "")
			if w.Code == 200 || strings.Contains(w.Body.String(), "pspn_") || strings.Contains(w.Body.String(), "SECRET") {
				t.Fatal("released stale/unaudited credential")
			}
		})
	}
}

func TestNodeBootstrapMigrationGatePreservesIdentityAndRetry(t *testing.T) {
	f := newBootstrapFixture(t, true)
	token, ticket := mintBootstrap(t, f, true)
	if f.migration.calls != 0 {
		t.Fatal("mint applied conversion")
	}
	w := f.request("GET", "/node-bootstrap/"+token, "", "")
	if w.Code != 200 || strings.Contains(w.Body.String(), "pspn_") || !strings.Contains(w.Body.String(), "old_backend_stopped") {
		t.Fatal("invalid wrapper")
	}
	// A held old operation must drain before Apply. This is only in-process.
	_, release, err := f.h.gate.Read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		done <- f.request("POST", "/api/node-bootstrap/complete", `{"old_backend_stopped":true}`, ticket.callbackToken)
	}()
	select {
	case <-done:
		t.Fatal("conversion bypassed admitted old operation")
	case <-time.After(25 * time.Millisecond):
	}
	release()
	w = <-done
	if w.Code != 200 || f.migration.calls != 1 || f.pool.replaced != 1 || f.pool.removed != 0 {
		t.Fatalf("completion %d %s calls=%d replace=%d", w.Code, w.Body.String(), f.migration.calls, f.pool.replaced)
	}
	identity := f.provisioning.agent.AgentID
	credential := f.provisioning.credential
	w = f.request("POST", "/api/node-bootstrap/complete", `{"old_backend_stopped":true}`, ticket.callbackToken)
	if w.Code != 200 || f.migration.calls != 1 || identity != f.provisioning.agent.AgentID || credential != f.provisioning.credential {
		t.Fatal("callback retry rebound identity")
	}
	if !strings.Contains(w.Body.String(), credential) {
		t.Fatal("retry did not return same installer")
	}
}

func TestNodeBootstrapAmbiguousCommitAndPoolFailureFailClosed(t *testing.T) {
	for _, mode := range []string{"committed_unknown", "uncommitted_unknown", "pool_failure", "known_conflict"} {
		t.Run(mode, func(t *testing.T) {
			f := newBootstrapFixture(t, true)
			token, ticket := mintBootstrap(t, f, true)
			f.request("GET", "/node-bootstrap/"+token, "", "")
			switch mode {
			case "committed_unknown":
				f.migration.err = errors.New("SECRET commit ack")
				f.migration.commitDespiteError = true
			case "uncommitted_unknown":
				f.migration.err = errors.New("SECRET database")
			case "pool_failure":
				f.pool.replaceErr = errors.New("SECRET adapter")
			case "known_conflict":
				f.migration.err = domain.ErrConflict
			}
			w := f.request("POST", "/api/node-bootstrap/complete", `{"old_backend_stopped":true}`, ticket.callbackToken)
			if w.Code == 200 || strings.Contains(w.Body.String(), "SECRET") || strings.Contains(w.Body.String(), "pspn_") {
				t.Fatal("failed completion released secrets")
			}
			if mode != "known_conflict" && f.pool.removed != 1 {
				t.Fatal("unknown state left adapter reachable")
			}
			f.migration.err = nil
			f.pool.replaceErr = nil
			w = f.request("POST", "/api/node-bootstrap/complete", `{"old_backend_stopped":true}`, ticket.callbackToken)
			if mode == "uncommitted_unknown" {
				if w.Code == 200 || f.migration.calls != 1 {
					t.Fatal("unknown state retried identity creation")
				}
			} else if w.Code != 200 {
				t.Fatalf("recoverable retry: %d %s", w.Code, w.Body.String())
			}
		})
	}
}

func TestNodeBootstrapRejectsUnreviewedVersionAndUnsupportedMigration(t *testing.T) {
	for _, mode := range []string{"version", "scope", "single_instance", "blockers", "stale", "audit"} {
		t.Run(mode, func(t *testing.T) {
			f := newBootstrapFixture(t, true)
			body := `{"version":"v0.0.1-beta3","core_version":"26.6.27","fingerprint":"` + strings.Repeat("a", 64) + `","managed_only":true,"confirm_single_instance":true}`
			switch mode {
			case "version":
				body = strings.Replace(body, "v0.0.1-beta3", "latest", 1)
			case "scope":
				body = strings.Replace(body, `"managed_only":true`, `"managed_only":false`, 1)
			case "single_instance":
				body = strings.Replace(body, `"confirm_single_instance":true`, `"confirm_single_instance":false`, 1)
			case "blockers":
				f.preview.blocked = true
			case "stale":
				f.preview.stale = true
			case "audit":
				f.audit.err = errors.New("SECRET")
			}
			w := f.request("POST", "/api/admin/servers/41/node-migration-command", body, "")
			if w.Code == 200 || len(f.h.tickets) != 0 || f.migration.calls != 0 || strings.Contains(w.Body.String(), "SECRET") {
				t.Fatal("mint bypassed preconditions")
			}
		})
	}
}

func TestNodeBootstrapCallbackScopeAndShutdownRequired(t *testing.T) {
	f := newBootstrapFixture(t, true)
	token, ticket := mintBootstrap(t, f, true)
	for _, test := range []struct{ token, body string }{{token, `{"old_backend_stopped":true}`}, {ticket.callbackToken, `{"old_backend_stopped":false}`}, {"", `{"old_backend_stopped":true}`}} {
		w := f.request("POST", "/api/node-bootstrap/complete", test.body, test.token)
		if w.Code != 401 || f.migration.calls != 0 {
			t.Fatal("invalid callback authorized")
		}
	}
	w := f.request("GET", "/node-bootstrap/"+ticket.callbackToken, "", "")
	if w.Code != 410 {
		t.Fatal("callback token downloaded wrapper")
	}
	w = f.request("POST", "/api/node-bootstrap/complete", `{"old_backend_stopped":true}`, ticket.callbackToken)
	if w.Code == 200 || f.migration.calls != 0 {
		t.Fatal("undownloaded wrapper authorized conversion")
	}
}
