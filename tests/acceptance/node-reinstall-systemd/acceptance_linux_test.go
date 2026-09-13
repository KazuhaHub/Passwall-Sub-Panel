//go:build linux && node_reinstall_acceptance

package acceptance_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	paneladapter "github.com/KazuhaHub/passwall-sub-panel/internal/adapters/panel"
	"github.com/KazuhaHub/passwall-sub-panel/internal/adapters/pspnode"
	"github.com/KazuhaHub/passwall-sub-panel/internal/adapters/sqlstore"
	"github.com/KazuhaHub/passwall-sub-panel/internal/adapters/xui"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/operationgate"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/panelpath"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/nodesync"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/servermigration"
	"github.com/KazuhaHub/passwall-sub-panel/internal/transport/http/handler"
	"github.com/KazuhaHub/passwall-sub-panel/internal/transport/http/middleware"
	"github.com/gin-gonic/gin"
)

const (
	nodeRoot    = "/opt/passwall-node"
	nodeUnit    = "/etc/systemd/system/passwall-node.service"
	installLock = "/opt/.passwall-node-install.lock"
	ownerMarker = nodeRoot + "/.psp-disposable-reinstall-owner"
	nodeVersion = "v0.0.1-beta3"
	coreVersion = "26.6.27"
)

// These fixture pins select a reviewed release, not a floating latest build.
// Production installer still obtains/verifies the published checksum/archive.
type pinnedCatalog struct{}

func (pinnedCatalog) List(context.Context) (ports.NodeReleaseList, error) {
	return ports.NodeReleaseList{Releases: []ports.NodeReleaseCatalogEntry{{Version: nodeVersion, Channel: "beta", Methods: []string{"linux"}, Platforms: []ports.NodeReleasePlatform{{OS: "linux", Arch: "amd64"}, {OS: "linux", Arch: "arm64"}}}}}, nil
}

type fixture struct {
	t                                                   *testing.T
	ctx                                                 context.Context
	repos                                               ports.Repos
	coordinator                                         *nodesync.Service
	server                                              *httptest.Server
	client                                              *http.Client
	panelID, nodeID, clientID, userID, groupID          int64
	credential, agentID, adminToken, nonce, dir, caPath string
	pausedPID                                           int
	syncMu                                              sync.Mutex
	syncStatuses                                        map[int]int
}

func TestDisposableSystemdNodeReinstall(t *testing.T) {
	assertDisposable(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	gin.SetMode(gin.ReleaseMode)
	sqlstore.ConfigureSecretKey("disposable-acceptance-only-strong-encryption-key-9c6376875de2")
	defer sqlstore.ConfigureSecretKey("")
	for _, standardOldUnit := range []bool{false, true} {
		f := newFixture(t, ctx)
		func() {
			defer f.cleanup()
			if standardOldUnit {
				t.Log("scenario: standard x-ui systemd/SQLite fixture; not a real upstream handshake")
				f.installOldFixture()
			} else {
				t.Log("scenario: clean OS/systemd node")
				f.checkPartialAndStaleRefused()
			}
			before := f.identity()
			command := f.mint(true)
			if standardOldUnit {
				must(t, os.Mkdir(installLock, 0o700), "create intentional installer lock")
				if f.runCommand(command) == nil {
					t.Fatal("installer lock failure unexpectedly succeeded")
				}
				must(t, os.Remove(installLock), "release intentional installer lock")
				f.readAgent()
				if state := f.systemd("x-ui.service", "ActiveState"); state != "inactive" {
					t.Fatal("failed installation resurrected old service")
				}
				if f.systemd("x-ui.service", "UnitFileState") == "enabled" {
					t.Fatal("old service remains enabled after conversion")
				}
				if _, err := os.Stat(nodeRoot); !errors.Is(err, os.ErrNotExist) {
					t.Fatal("failed installer published partial identity")
				}
				f.assertIdentity(before)
				f.assertOldBackup()
				command = f.mint(false)
				t.Log("installer failure was fail-closed; recover original server with fresh install command")
			}
			must(t, f.runCommand(command), "node-host one-command installation")
			f.readAgent()
			f.claimNode()
			f.waitReady()
			f.assertIdentity(before)
			f.proxyHTTP()
			f.checkPrivacy()
			f.checkOrdinaryReinstall()
			f.assertIdentity(before)
			f.removeOwnedNode()
			// This simulates loss of the node machine only. PSP DB is never reset.
			must(t, f.runCommand(f.mint(false)), "same-server clean OS reinstall")
			f.claimNode()
			f.waitReady()
			f.assertIdentity(before)
			f.proxyHTTP()
			t.Log("passed real systemd / published PN / applied streams / VLESS proxy / fixed identity reinstall")
		}()
	}
}

func assertDisposable(t *testing.T) {
	t.Helper()
	if os.Geteuid() != 0 || os.Getenv("GITHUB_ACTIONS") != "true" || os.Getenv("RUNNER_ENVIRONMENT") != "github-hosted" || os.Getenv("PSP_NODE_REINSTALL_EPHEMERAL") != "yes" {
		t.Fatal("refusing mutations outside explicitly enabled root ephemeral GitHub-hosted runner")
	}
	release, err := os.ReadFile("/etc/os-release")
	if err != nil || !bytes.Contains(release, []byte("ID=ubuntu\n")) || !bytes.Contains(release, []byte("VERSION_ID=\"24.04\"")) {
		t.Fatal("only Ubuntu 24.04 is accepted")
	}
	if _, err := os.Stat("/run/systemd/system"); err != nil {
		t.Fatal("real running systemd required")
	}
	if exec.Command("systemd-detect-virt", "--container", "--quiet").Run() == nil {
		t.Fatal("containers are not accepted")
	}
	for _, path := range []string{nodeRoot, nodeUnit, installLock, "/usr/local/x-ui", "/etc/x-ui", "/etc/systemd/system/x-ui.service", "/usr/local/share/ca-certificates/psp-reinstall-acceptance.crt"} {
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("refusing pre-existing installation or fixture artifact")
		}
	}
	for _, unit := range []string{"passwall-node.service", "x-ui.service"} {
		out, err := exec.Command("systemctl", "show", unit, "--property=LoadState", "--value").Output()
		if err != nil || strings.TrimSpace(string(out)) != "not-found" {
			t.Fatal("refusing pre-existing systemd unit")
		}
	}
	if err := exec.Command("getent", "passwd", "passwall-node").Run(); err == nil {
		t.Fatal("refusing pre-existing Node service account")
	}
}

func newFixture(t *testing.T, ctx context.Context) *fixture {
	t.Helper()
	var random [24]byte
	_, err := rand.Read(random[:])
	must(t, err, "fixture random identity")
	f := &fixture{t: t, ctx: ctx, nonce: hex.EncodeToString(random[:8]), adminToken: hex.EncodeToString(random[8:]), dir: t.TempDir(), syncStatuses: map[int]int{}}
	db, err := sqlstore.OpenQuiet("sqlite", filepath.Join(f.dir, "psp.db"))
	must(t, err, "open fixture SQLite")
	must(t, sqlstore.EnsureSchema(db), "create fixture schema")
	sqlDB, err := db.DB()
	must(t, err, "fixture SQL handle")
	t.Cleanup(func() { _ = sqlDB.Close() })
	f.repos = sqlstore.NewRepos(db)
	settings, err := f.repos.Settings.Load(ctx, ports.UISettings{})
	must(t, err, "load native fixture cadence")
	settings.NodePollSeconds, settings.FullReportSeconds = 2, 0
	must(t, f.repos.Settings.Save(ctx, settings), "set documented live native cadence")
	panel := &domain.Panel{Kind: domain.PanelKind3XUI, Name: "retained server " + f.nonce, URL: "https://old.invalid", XrayVersion: coreVersion, Remark: "retained remark"}
	must(t, f.repos.XUIPanel.Save(ctx, panel), "create original server")
	f.panelID = panel.ID
	port := freePort(t)
	now := time.Now().UTC()
	node := &domain.Node{PanelID: panel.ID, InboundID: 91, DisplayName: "retained node", ServerAddress: "127.0.0.1", DesiredProtocol: "vless", DesiredPort: port, ObservedProtocol: "vless", ObservedPort: port, Enabled: true, Region: "CA", SortOrder: 27, Tags: []string{"retained"}, InboundListen: "127.0.0.1", InboundRemark: "retained inbound", InboundSettings: `{"decryption":"none"}`, StreamSettings: `{"network":"tcp","security":"none"}`, Sniffing: `{"enabled":true,"destOverride":["http","tls"]}`, Allocate: `{"strategy":"always"}`, ConfigSyncedAt: &now, ConfigSyncState: domain.ConfigSyncSynced, LifetimeUpBytes: 1000, LifetimeDownBytes: 2000, LifetimeTotalBytes: 3000, LastInboundUpBytes: 100, LastInboundDownBytes: 200, LastInboundTotalBytes: 300, LastInboundSeeded: true}
	must(t, f.repos.Node.Create(ctx, node), "create original node")
	f.nodeID = node.ID
	group := &domain.Group{Slug: "retained-group-" + f.nonce, Name: "retained subscribers", TagFilter: domain.TagFilter{Tags: []string{"retained"}}, Layout: domain.Layout{Sort: []domain.SortEntry{{NodeID: node.ID, Weight: 3}}, Separators: []domain.Separator{{Position: 0, Name: "retained separator"}}}}
	must(t, f.repos.Group.Create(ctx, group), "create retained group and layout")
	f.groupID = group.ID
	user := &domain.User{UPN: "fixture-" + f.nonce, Email: "fixture@example.invalid", SSOProvider: domain.SSOProviderLocal, SSOSubject: f.nonce, Role: domain.RoleUser, SubToken: "retained-sub-" + f.nonce, UUID: "22222222-2222-4222-8222-222222222222", Enabled: true, LifetimeUpBytes: 1500, LifetimeDownBytes: 2500, LifetimeTotalBytes: 4000, PeriodBaselineBytes: 1700}
	user.GroupID = group.ID
	must(t, f.repos.User.Create(ctx, user), "create original user")
	f.userID = user.ID
	must(t, f.repos.Traffic.Insert(ctx, &domain.TrafficSnapshot{UserID: user.ID, UpBytes: 1500, DownBytes: 2500, TotalBytes: 4000, CapturedAt: now}), "create retained user history")
	must(t, f.repos.NodeTraffic.Insert(ctx, &domain.NodeTrafficSnapshot{NodeID: node.ID, UpBytes: 1000, DownBytes: 2000, TotalBytes: 3000, CapturedAt: now}), "create retained node history")
	client := &domain.PSPClient{UserID: user.ID, PanelID: panel.ID, Email: "retained@psp.local", UUID: user.UUID, Password: "retained-user-password", DesiredEnable: true, DesiredMinted: true, LifetimeUpBytes: 1500, LifetimeDownBytes: 2500, LifetimeTotalBytes: 4000, LastRawUpBytes: 500, LastRawDownBytes: 800, LastRawTotalBytes: 1300, PeriodBaselineUpBytes: 700, PeriodBaselineDownBytes: 1000, PeriodBaselineTotalBytes: 1700}
	f.clientID, err = f.repos.PSPClient.Create(ctx, client)
	must(t, err, "create original client")
	attachment := domain.PSPClientInbound{ClientID: f.clientID, NodeID: node.ID, State: domain.ClientApplyApplied, AppliedVersion: 1, AppliedEmail: client.Email, AppliedUUID: client.UUID, AppliedPassword: client.Password}
	must(t, f.repos.PSPClient.SetInbounds(ctx, f.clientID, []domain.PSPClientInbound{attachment}), "attach original client")
	must(t, f.repos.PSPClient.UpdateInboundState(ctx, attachment), "record old applied credentials")
	f.coordinator, err = nodesync.New(nodesync.Options{Desired: f.repos.NativeDesired, Agents: f.repos.NodeAgent, Issues: f.repos.NodeAgentIssue, Tasks: f.repos.NodeAgentTask, Users: f.repos.User, Clients: f.repos.PSPClient, Nodes: f.repos.Node, Settings: f.repos.Settings, Panels: f.repos.XUIPanel})
	must(t, err, "production node coordinator")
	registry := paneladapter.NewRegistry()
	must(t, registry.Register(domain.PanelKind3XUI, func(p *domain.Panel) (ports.PanelClient, error) { return xui.New(p) }), "old adapter registry")
	must(t, registry.Register(domain.PanelKindPSP, func(p *domain.Panel) (ports.PanelClient, error) {
		return pspnode.New(p, f.coordinator, f.repos.Node, f.repos.NodeAgent)
	}), "native adapter registry")
	pool, err := paneladapter.NewPool(ctx, f.repos.XUIPanel, registry)
	must(t, err, "production adapter pool")
	gate := operationgate.New()
	servers := handler.NewAdminServersHandler(f.repos.XUIPanel, pool, f.repos.Node, f.repos.Audit, nil, nil).WithNativeAgentProvisioning(f.repos.NativeAgentProvisioning).WithNodeAgents(f.repos.NodeAgent).WithNodeSettings(f.repos.Settings).WithNodeReleaseCatalog(pinnedCatalog{}).WithServerMigrationPreviewer(servermigration.New(f.repos.ServerMigration))
	bootstrap := handler.NewNodeBootstrapHandler(servers, f.repos.ServerMigration, gate)
	auth, err := handler.NewNodeBearerAuthenticator(f.repos.NodeAgent)
	must(t, err, "production Bearer authenticator")
	syncHandler, err := handler.NewNodeSyncHandler(f.coordinator, auth)
	must(t, err, "production node HTTP handler")
	r := gin.New()
	r.Use(gin.Recovery(), middleware.BackendOperationGate(gate))
	admin := r.Group("/api/admin")
	admin.Use(func(c *gin.Context) {
		if c.GetHeader("Authorization") != "Bearer "+f.adminToken {
			c.AbortWithStatus(http.StatusUnauthorized)
			return
		}
		c.Set("upn", "disposable-fixture-admin")
	})
	admin.POST("/servers/:id/node-install-command", bootstrap.MintInstall)
	admin.POST("/servers/:id/node-migration-command", bootstrap.MintMigration)
	r.GET("/node-bootstrap/:token", bootstrap.Download)
	r.POST("/api/node-bootstrap/complete", bootstrap.Complete)
	r.POST("/v1/node/sync", func(c *gin.Context) {
		syncHandler.ServeHTTP(c.Writer, c.Request)
		f.syncMu.Lock()
		f.syncStatuses[c.Writer.Status()]++
		f.syncMu.Unlock()
	})
	f.server = httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, rq *http.Request) {
		const prefix = "/private-panel"
		if !strings.HasPrefix(rq.URL.Path, prefix+"/") {
			http.NotFound(w, rq)
			return
		}
		rq = rq.Clone(panelpath.WithRequest(rq.Context(), prefix))
		rq.URL.Path = strings.TrimPrefix(rq.URL.Path, prefix)
		r.ServeHTTP(w, rq)
	}))
	f.server.StartTLS()
	f.client = f.server.Client()
	f.caPath = "/usr/local/share/ca-certificates/psp-reinstall-acceptance.crt"
	cert := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: f.server.Certificate().Raw})
	must(t, os.WriteFile(f.caPath, cert, 0o644), "trust disposable fixture certificate")
	must(t, f.command("update-ca-certificates"), "refresh disposable VM CA store")
	return f
}

func (f *fixture) mint(migration bool) string {
	f.t.Helper()
	body := map[string]any{"version": nodeVersion}
	action := "node-install-command"
	if migration {
		action = "node-migration-command"
		preview, err := servermigration.New(f.repos.ServerMigration).Preview(f.ctx, f.panelID, coreVersion, false)
		must(f.t, err, "migration preview")
		if !preview.CanMigrate {
			f.t.Fatal("fixture unexpectedly blocked")
		}
		body["core_version"], body["fingerprint"], body["managed_only"], body["confirm_single_instance"] = coreVersion, preview.Fingerprint, true, true
	}
	encoded, err := json.Marshal(body)
	must(f.t, err, "encode mint input")
	req, err := http.NewRequestWithContext(f.ctx, http.MethodPost, f.server.URL+"/private-panel/api/admin/servers/"+strconv.FormatInt(f.panelID, 10)+"/"+action, bytes.NewReader(encoded))
	must(f.t, err, "mint HTTP request")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+f.adminToken)
	resp, err := f.client.Do(req)
	must(f.t, err, "mint actual HTTPS request")
	defer resp.Body.Close()
	var reply struct {
		ServerID int64     `json:"server_id"`
		Command  string    `json:"command"`
		Expires  time.Time `json:"expires_at"`
	}
	if resp.StatusCode != 200 {
		f.t.Fatalf("mint HTTP status %d", resp.StatusCode)
	}
	must(f.t, json.NewDecoder(resp.Body).Decode(&reply), "decode private mint result")
	if reply.ServerID != f.panelID || reply.Command == "" || reply.Expires.IsZero() || strings.Contains(reply.Command, "pspn_") || !strings.Contains(reply.Command, "/private-panel/node-bootstrap/") {
		f.t.Fatal("invalid private one-command response")
	}
	return reply.Command
}

func (f *fixture) runCommand(command string) error {
	// Deliberately do not print the command, ticket, private script, or journal.
	cmd := exec.CommandContext(f.ctx, "bash", "-c", command)
	cmd.Env = append(os.Environ(), "BASH_ENV=", "ENV=")
	output, err := cmd.CombinedOutput()
	if f.credential != "" && bytes.Contains(output, []byte(f.credential)) {
		f.t.Fatal("private command leaked fixed credential; diagnostics withheld")
	}
	return err
}
func (f *fixture) command(name string, args ...string) error {
	return exec.CommandContext(f.ctx, name, args...).Run()
}
func (f *fixture) systemd(unit, property string) string {
	out, err := exec.CommandContext(f.ctx, "systemctl", "show", unit, "--property="+property, "--value").Output()
	must(f.t, err, "inspect actual systemd")
	return strings.TrimSpace(string(out))
}
func (f *fixture) readAgent() {
	a, err := f.repos.NodeAgent.GetByPanelID(f.ctx, f.panelID)
	must(f.t, err, "read persistent original-server agent")
	raw, err := f.repos.NativeAgentProvisioning.GetCredential(f.ctx, f.panelID)
	must(f.t, err, "read encrypted fixed credential")
	if f.agentID != "" && (a.AgentID != f.agentID || raw != f.credential) {
		f.t.Fatal("agent or fixed credential rotated")
	}
	f.agentID, f.credential = a.AgentID, raw
}
func (f *fixture) claimNode() {
	f.readAgent()
	actual, err := os.ReadFile(nodeRoot + "/config/credential")
	must(f.t, err, "read owned installed credential")
	if string(actual) != f.credential+"\n" {
		f.t.Fatal("installed identity mismatch")
	}
	must(f.t, os.WriteFile(ownerMarker, []byte(f.nonce), 0o600), "mark disposable owned Node tree")
}

func (f *fixture) waitReady() {
	f.t.Helper()
	deadline := time.Now().Add(4 * time.Minute)
	for time.Now().Before(deadline) {
		a, err := f.repos.NodeAgent.GetByPanelID(f.ctx, f.panelID)
		attachments, e2 := f.repos.PSPClient.ListInbounds(f.ctx, f.clientID)
		streams, e3 := f.repos.NodeAgent.ListStreams(f.ctx, f.agentID)
		all := err == nil && e2 == nil && e3 == nil && a.LastSeen != nil && time.Since(*a.LastSeen) < 90*time.Second && len(attachments) == 1 && attachments[0].Applied() && len(streams) == 3
		if a != nil {
			for _, s := range streams {
				all = all && s.DesiredVersion > 0 && s.AppliedVersion == s.DesiredVersion && s.AppliedEpoch == a.Epoch && s.AppliedETag == s.DesiredETag
			}
		}
		if all {
			snapshot, e := f.coordinator.NativePanelSnapshot(f.ctx, f.panelID)
			if e == nil && snapshot != nil && len(snapshot.Inbounds) == 1 && len(snapshot.Clients) == 1 && snapshot.Status.PanelVersion == nodeVersion && snapshot.Status.XrayVersion == coreVersion && snapshot.Status.XrayState == "running" {
				return
			}
		}
		time.Sleep(time.Second)
	}
	f.readinessDiagnostics()
	f.t.Fatalf("timed out waiting for actual published Node/core applied streams (systemd state=%s)", f.systemd("passwall-node.service", "ActiveState"))
}

// Retain classifications, never raw reports, errors, URLs, ETags, config or
// journal text. The disposable VM disappears after failure, so these bounded
// facts distinguish transport/authentication from real-core convergence.
func (f *fixture) readinessDiagnostics() {
	f.t.Helper()
	f.syncMu.Lock()
	statuses := make(map[int]int, len(f.syncStatuses))
	for k, v := range f.syncStatuses {
		statuses[k] = v
	}
	f.syncMu.Unlock()
	f.t.Logf("readiness diagnostics: sync_http_status_counts=%v", statuses)
	a, err := f.repos.NodeAgent.GetByPanelID(f.ctx, f.panelID)
	if err == nil && a != nil {
		f.t.Logf("readiness diagnostics: agent_seen=%t observed_core_is_xray=%t epoch=%d", a.LastSeen != nil, a.ObservedCoreEngine == domain.NodeCoreXray, a.Epoch)
	} else {
		f.t.Log("readiness diagnostics: agent_lookup_failed=true")
	}
	streams, err := f.repos.NodeAgent.ListStreams(f.ctx, f.agentID)
	if err == nil {
		for _, s := range streams {
			if !s.Stream.Valid() {
				continue
			}
			f.t.Logf("readiness diagnostics: stream=%s desired=%d applied=%d applied_epoch=%d etag_matches=%t", s.Stream, s.DesiredVersion, s.AppliedVersion, s.AppliedEpoch, s.DesiredETag != "" && s.DesiredETag == s.AppliedETag)
		}
	}
	attachments, err := f.repos.PSPClient.ListInbounds(f.ctx, f.clientID)
	if err == nil {
		f.t.Logf("readiness diagnostics: attachment_count=%d", len(attachments))
		for _, a := range attachments {
			f.t.Logf("readiness diagnostics: attachment_applied=%t attachment_pending=%t attachment_rejected=%t attachment_blocked=%t", a.Applied(), a.State == domain.ClientApplyPending, a.State == domain.ClientApplyRejected, a.State == domain.ClientApplyBlocked)
		}
	}
	snapshot, err := f.coordinator.NativePanelSnapshot(f.ctx, f.panelID)
	if err == nil && snapshot != nil {
		f.t.Logf("readiness diagnostics: full_snapshot_present=true inbounds=%d clients=%d node_version_expected=%t core_version_expected=%t core_running=%t", len(snapshot.Inbounds), len(snapshot.Clients), snapshot.Status.PanelVersion == nodeVersion, snapshot.Status.XrayVersion == coreVersion, snapshot.Status.XrayState == "running")
	} else {
		f.t.Logf("readiness diagnostics: full_snapshot_present=false snapshot_not_found=%t", errors.Is(err, domain.ErrNotFound))
	}
	n, err := f.repos.Node.GetByID(f.ctx, f.nodeID)
	if err == nil {
		conn, e := net.DialTimeout("tcp", "127.0.0.1:"+strconv.Itoa(n.DesiredPort), time.Second)
		if e == nil {
			_ = conn.Close()
		}
		f.t.Logf("readiness diagnostics: desired_listener_tcp_open=%t", e == nil)
	}
	environment, err := os.ReadFile(nodeRoot + "/config/environment")
	f.t.Logf("readiness diagnostics: installed_endpoint_matches=%t", err == nil && bytes.Contains(environment, []byte(f.server.URL+"/private-panel/v1/node/sync")))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	journal, err := exec.CommandContext(ctx, "journalctl", "--unit=passwall-node.service", "--no-pager", "--output=cat", "--lines=100").Output()
	if err != nil {
		f.t.Log("readiness diagnostics: bounded_journal_unavailable=true")
		return
	}
	journal = bytes.ToLower(journal)
	for _, category := range []struct {
		name    string
		needles []string
	}{
		{"tls_unknown_ca", []string{"unknown authority", "unknown ca"}},
		{"tls_certificate_error", []string{"x509:", "certificate verification", "certificate is not valid"}},
		{"http_unauthorized", []string{"status 401", "status=401", "unauthorized"}},
		{"connection_refused", []string{"connection refused"}},
		{"permission_denied", []string{"permission denied"}},
		{"core_install_error", []string{"install core", "core install", "download", "checksum"}},
		{"configuration_error", []string{"configuration", "compile", "config test", "invalid config"}},
		{"sync_error", []string{"sync failed", "sync error", "sync: ", "sync request"}},
	} {
		found := false
		for _, needle := range category.needles {
			found = found || bytes.Contains(journal, []byte(needle))
		}
		f.t.Logf("readiness diagnostics: journal_category_%s=%t", category.name, found)
	}
}

type retained struct {
	PanelName, Remark, NodeName, NodeSettings, Streams, UUID, Password, SubToken string
	Group, UserHistory, NodeHistory                                              string
	IDs                                                                          []int64
	Counters                                                                     []int64
	Tags                                                                         []string
	Sort                                                                         int
}

func (f *fixture) identity() retained {
	p, err := f.repos.XUIPanel.GetByID(f.ctx, f.panelID)
	must(f.t, err, "read original panel")
	n, err := f.repos.Node.GetByID(f.ctx, f.nodeID)
	must(f.t, err, "read original node")
	u, err := f.repos.User.GetByID(f.ctx, f.userID)
	must(f.t, err, "read original user")
	c, err := f.repos.PSPClient.GetByID(f.ctx, f.clientID)
	must(f.t, err, "read original client")
	g, err := f.repos.Group.GetByID(f.ctx, f.groupID)
	must(f.t, err, "read retained group")
	userHistory, err := f.repos.Traffic.LatestForUser(f.ctx, f.userID)
	must(f.t, err, "read retained user snapshot")
	nodeHistory, err := f.repos.NodeTraffic.LatestForNode(f.ctx, f.nodeID)
	must(f.t, err, "read retained node snapshot")
	groupJSON, err := json.Marshal(g)
	must(f.t, err, "encode retained group")
	userJSON, err := json.Marshal(userHistory)
	must(f.t, err, "encode retained user history")
	nodeJSON, err := json.Marshal(nodeHistory)
	must(f.t, err, "encode retained node history")
	return retained{PanelName: p.Name, Remark: p.Remark, NodeName: n.DisplayName, NodeSettings: n.InboundSettings, Streams: n.StreamSettings, UUID: c.UUID, Password: c.Password, SubToken: u.SubToken, Group: string(groupJSON), UserHistory: string(userJSON), NodeHistory: string(nodeJSON), IDs: []int64{p.ID, n.ID, n.PanelID, int64(n.InboundID), int64(n.DesiredPort), u.ID, u.GroupID, c.ID, c.UserID, c.PanelID}, Counters: []int64{n.LifetimeUpBytes, n.LifetimeDownBytes, n.LifetimeTotalBytes, u.LifetimeUpBytes, u.LifetimeDownBytes, u.LifetimeTotalBytes, u.PeriodBaselineBytes, c.LifetimeUpBytes, c.LifetimeDownBytes, c.LifetimeTotalBytes, c.LastRawUpBytes, c.LastRawDownBytes, c.LastRawTotalBytes, c.PeriodBaselineUpBytes, c.PeriodBaselineDownBytes, c.PeriodBaselineTotalBytes}, Tags: n.Tags, Sort: n.SortOrder}
}
func (f *fixture) assertIdentity(before retained) {
	if !reflect.DeepEqual(before, f.identity()) {
		f.t.Fatal("reinstall/conversion changed original PSP identity, credentials, config, order or counter history")
	}
}

func (f *fixture) checkPartialAndStaleRefused() {
	command := f.mint(true)
	must(f.t, os.Mkdir("/usr/local/x-ui", 0o755), "create partial old fixture")
	if f.runCommand(command) == nil {
		f.t.Fatal("partial deployment accepted")
	}
	must(f.t, os.Remove("/usr/local/x-ui"), "remove exact empty partial fixture")
	p, err := f.repos.XUIPanel.GetByID(f.ctx, f.panelID)
	must(f.t, err, "check partial no-conversion")
	if p.Kind != domain.PanelKind3XUI {
		f.t.Fatal("partial deployment converted database")
	}
	// Minting did not convert. Change an authoritative field after mint to prove
	// the completion callback checks the fingerprint again at the write boundary.
	p.Remark = "retained remark changed after mint"
	must(f.t, f.repos.XUIPanel.Save(f.ctx, p), "change stale-preview field")
	if f.runCommand(f.mintCommandBeforeChange()) == nil {
		f.t.Fatal("stale fingerprint accepted")
	}
	p, err = f.repos.XUIPanel.GetByID(f.ctx, f.panelID)
	must(f.t, err, "check stale no-conversion")
	if p.Kind != domain.PanelKind3XUI {
		f.t.Fatal("stale fingerprint converted database")
	}
	f.t.Log("partial deployment and stale fingerprint refused before DB conversion")
}
func (f *fixture) mintCommandBeforeChange() string {
	command := f.mint(true)
	p, err := f.repos.XUIPanel.GetByID(f.ctx, f.panelID)
	must(f.t, err, "read stale input")
	p.Remark += " stale"
	must(f.t, f.repos.XUIPanel.Save(f.ctx, p), "invalidate fingerprint")
	return command
}

func (f *fixture) installOldFixture() {
	must(f.t, os.Mkdir("/usr/local/x-ui", 0o755), "create owned standard old installation")
	must(f.t, os.Mkdir("/etc/x-ui", 0o755), "create owned old config")
	must(f.t, os.WriteFile("/usr/local/x-ui/.psp-disposable-owner", []byte(f.nonce), 0o600), "mark owned old fixture")
	source := filepath.Join(f.dir, "x-ui-fixture.c")
	must(f.t, os.WriteFile(source, []byte("#include <unistd.h>\nint main(void){for(;;)pause();return 0;}\n"), 0o600), "write non-network old service fixture")
	must(f.t, f.command("gcc", "-O2", "-o", "/usr/local/x-ui/x-ui", source), "compile labeled old service fixture")
	must(f.t, f.command("sqlite3", "/etc/x-ui/x-ui.db", "CREATE TABLE acceptance (identity TEXT); INSERT INTO acceptance VALUES ('"+f.nonce+"');"), "create actual old SQLite database")
	must(f.t, os.Chmod("/etc/x-ui/x-ui.db", 0o600), "old DB permissions")
	unit := "[Unit]\nDescription=PSP disposable old x-ui fixture " + f.nonce + "\n[Service]\nType=simple\nWorkingDirectory=/usr/local/x-ui\nExecStart=/usr/local/x-ui/x-ui\nKillMode=control-group\n[Install]\nWantedBy=multi-user.target\n"
	must(f.t, os.WriteFile("/etc/systemd/system/x-ui.service", []byte(unit), 0o644), "write owned standard unit")
	must(f.t, f.command("systemctl", "daemon-reload"), "load old fixture unit")
	must(f.t, f.command("systemctl", "enable", "--now", "x-ui.service"), "start real old fixture systemd service")
	if f.systemd("x-ui.service", "ActiveState") != "active" {
		f.t.Fatal("old unit fixture not running")
	}
}
func (f *fixture) assertOldBackup() {
	entries, err := filepath.Glob("/var/backups/passwall-node-migration/backup.*/final-x-ui.db")
	must(f.t, err, "find verified old backups")
	matched := false
	for _, path := range entries {
		out, e := exec.CommandContext(f.ctx, "sqlite3", path, "SELECT identity FROM acceptance;").Output()
		if e == nil && strings.TrimSpace(string(out)) == f.nonce {
			matched = true
			info, e := os.Stat(path)
			must(f.t, e, "backup metadata")
			if info.Mode().Perm() != 0o600 {
				f.t.Fatal("backup is not private")
			}
		}
	}
	if !matched {
		f.t.Fatal("no verified final old database backup for this run")
	}
	if _, err := os.Stat("/usr/local/x-ui/x-ui"); err != nil {
		f.t.Fatal("migration uninstalled old backend")
	}
}

func (f *fixture) checkOrdinaryReinstall() {
	command := f.mint(false)
	pid, err := strconv.Atoi(f.systemd("passwall-node.service", "MainPID"))
	must(f.t, err, "read actual agent PID")
	if pid <= 1 {
		f.t.Fatal("no live systemd agent")
	}
	must(f.t, syscall.Kill(pid, syscall.SIGSTOP), "pause owned agent for exact DB check")
	f.pausedPID = pid
	deadline := time.Now().Add(5 * time.Second)
	paused := false
	for time.Now().Before(deadline) {
		data, e := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/status")
		if e == nil && (bytes.Contains(data, []byte("State:\tT ")) || bytes.Contains(data, []byte("State:\tt "))) {
			paused = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !paused {
		f.t.Fatal("owned agent did not enter paused state")
	}
	before := f.manifest([]string{nodeRoot + "/data/state.db", nodeRoot + "/data/state.db-wal", nodeRoot + "/data/state.db-shm"}, true)
	static := f.manifest([]string{nodeRoot + "/bin/passwall-node", nodeRoot + "/config/credential", nodeRoot + "/config/environment", nodeRoot + "/config/version", nodeRoot + "/passwall-node.service", nodeUnit}, false)
	must(f.t, f.runCommand(command), "ordinary same-identity reinstall command")
	if f.systemd("passwall-node.service", "MainPID") != strconv.Itoa(pid) {
		f.t.Fatal("ordinary reinstall replaced agent process")
	}
	if !reflect.DeepEqual(before, f.manifest([]string{nodeRoot + "/data/state.db", nodeRoot + "/data/state.db-wal", nodeRoot + "/data/state.db-shm"}, true)) {
		f.t.Fatal("ordinary reinstall modified durable node SQLite bytes/inode")
	}
	if !reflect.DeepEqual(static, f.manifest([]string{nodeRoot + "/bin/passwall-node", nodeRoot + "/config/credential", nodeRoot + "/config/environment", nodeRoot + "/config/version", nodeRoot + "/passwall-node.service", nodeUnit}, false)) {
		f.t.Fatal("ordinary reinstall changed fixed config/binary/unit bytes")
	}
	must(f.t, syscall.Kill(pid, syscall.SIGCONT), "resume owned agent")
	f.pausedPID = 0
}
func (f *fixture) manifest(paths []string, inode bool) map[string]string {
	result := map[string]string{}
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if errors.Is(err, os.ErrNotExist) {
			result[path] = "absent"
			continue
		}
		must(f.t, err, "read private manifest file")
		digest := sha256.Sum256(data)
		value := hex.EncodeToString(digest[:])
		if inode {
			info, e := os.Stat(path)
			must(f.t, e, "manifest inode")
			value += fmt.Sprint(info.Sys().(*syscall.Stat_t).Ino)
		}
		result[path] = value
	}
	return result
}
func (f *fixture) checkPrivacy() {
	pid := f.systemd("passwall-node.service", "MainPID")
	for _, path := range []string{"/proc/" + pid + "/cmdline", "/proc/" + pid + "/environ", nodeUnit, nodeRoot + "/config/environment"} {
		data, err := os.ReadFile(path)
		must(f.t, err, "privacy check")
		if bytes.Contains(data, []byte(f.credential)) {
			f.t.Fatal("fixed credential leaked into process or unit")
		}
	}
	for _, path := range []string{nodeRoot + "/config/credential", nodeRoot + "/data/state.db"} {
		info, err := os.Stat(path)
		must(f.t, err, "private file metadata")
		if info.Mode().Perm() != 0o600 {
			f.t.Fatal("private credential/SQLite file mode is not 0600")
		}
	}
}

func (f *fixture) proxyHTTP() {
	n, err := f.repos.Node.GetByID(f.ctx, f.nodeID)
	must(f.t, err, "proxy inbound port")
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-PSP-Acceptance", f.nonce)
		_, _ = io.WriteString(w, "real VLESS proxy "+f.nonce)
	}))
	defer target.Close()
	var binaries []string
	must(f.t, filepath.WalkDir(nodeRoot+"/data", func(path string, e os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !e.IsDir() && e.Name() == "xray" {
			binaries = append(binaries, path)
		}
		return nil
	}), "find actually installed catalog core")
	if len(binaries) != 1 {
		f.t.Fatalf("expected one real Xray binary, found %d", len(binaries))
	}
	port := freePort(f.t)
	config := map[string]any{"log": map[string]any{"loglevel": "none"}, "inbounds": []any{map[string]any{"listen": "127.0.0.1", "port": port, "protocol": "socks", "settings": map[string]any{"auth": "noauth", "udp": false}}}, "outbounds": []any{map[string]any{"protocol": "vless", "settings": map[string]any{"vnext": []any{map[string]any{"address": "127.0.0.1", "port": n.DesiredPort, "users": []any{map[string]any{"id": "22222222-2222-4222-8222-222222222222", "encryption": "none"}}}}}, "streamSettings": map[string]any{"network": "tcp", "security": "none"}}}}
	encoded, err := json.Marshal(config)
	must(f.t, err, "encode local real client config")
	path := filepath.Join(f.dir, "private-client.json")
	must(f.t, os.WriteFile(path, encoded, 0o600), "write private client config")
	cmd := exec.CommandContext(f.ctx, binaries[0], "run", "-c", path)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	must(f.t, cmd.Start(), "start real VLESS client core")
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	proxy, err := url.Parse("socks5://127.0.0.1:" + strconv.Itoa(port))
	must(f.t, err, "local SOCKS URL")
	client := &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{Proxy: http.ProxyURL(proxy), TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12}}}
	defer client.CloseIdleConnections()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		resp, e := client.Get(target.URL)
		if e == nil {
			body, readErr := io.ReadAll(io.LimitReader(resp.Body, 4096))
			_ = resp.Body.Close()
			if readErr == nil && resp.StatusCode == 200 && resp.Header.Get("X-PSP-Acceptance") == f.nonce && string(body) == "real VLESS proxy "+f.nonce {
				return
			}
		}
		time.Sleep(250 * time.Millisecond)
	}
	f.t.Fatal("actual VLESS client/server HTTP handshake failed")
}

func (f *fixture) removeOwnedNode() {
	marker, err := os.ReadFile(ownerMarker)
	must(f.t, err, "refuse cleanup without owned marker")
	cred, err := os.ReadFile(nodeRoot + "/config/credential")
	must(f.t, err, "refuse cleanup without fixed owned credential")
	if string(marker) != f.nonce || string(cred) != f.credential+"\n" {
		f.t.Fatal("refusing foreign Node installation cleanup")
	}
	if f.pausedPID > 1 {
		_ = syscall.Kill(f.pausedPID, syscall.SIGCONT)
		f.pausedPID = 0
	}
	must(f.t, f.command("systemctl", "disable", "--now", "passwall-node.service"), "stop owned Node for clean-node reinstall")
	must(f.t, os.Remove(nodeUnit), "remove exact owned Node unit")
	// Target is explicit and ownership was checked immediately above. PSP DB,
	// old installation, backups, and service account are never recursively erased.
	must(f.t, os.RemoveAll(nodeRoot), "remove exact owned disposable Node tree")
	must(f.t, f.command("systemctl", "daemon-reload"), "reload after exact owned removal")
}
func (f *fixture) cleanup() {
	if f.pausedPID > 1 {
		_ = syscall.Kill(f.pausedPID, syscall.SIGCONT)
		f.pausedPID = 0
	}
	if marker, err := os.ReadFile(ownerMarker); err == nil && string(marker) == f.nonce {
		f.removeOwnedNode()
	}
	oldMarker := "/usr/local/x-ui/.psp-disposable-owner"
	if data, err := os.ReadFile(oldMarker); err == nil && string(data) == f.nonce {
		_ = f.command("systemctl", "disable", "--now", "x-ui.service")
		unit, err := os.ReadFile("/etc/systemd/system/x-ui.service")
		if err == nil && bytes.Contains(unit, []byte(f.nonce)) {
			_ = os.Remove("/etc/systemd/system/x-ui.service")
		}
		_ = os.RemoveAll("/usr/local/x-ui")
		_ = os.RemoveAll("/etc/x-ui")
		_ = f.command("systemctl", "daemon-reload")
	}
	if f.caPath != "" {
		_ = os.Remove(f.caPath)
		_ = f.command("update-ca-certificates")
	}
	if f.server != nil {
		f.server.Close()
	}
}
func freePort(t *testing.T) int {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	must(t, err, "allocate fixture port")
	port := listener.Addr().(*net.TCPAddr).Port
	must(t, listener.Close(), "release fixture port")
	return port
}
func must(t *testing.T, err error, stage string) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s failed (private diagnostics withheld)", stage)
	}
}
