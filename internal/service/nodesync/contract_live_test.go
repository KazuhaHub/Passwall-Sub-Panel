package nodesync_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	nodeprotocol "github.com/KazuhaHub/passwall-node/protocol"

	"github.com/KazuhaHub/passwall-sub-panel/internal/adapters/pspnode"
	"github.com/KazuhaHub/passwall-sub-panel/internal/adapters/sqlstore"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/nodesync"
	"github.com/KazuhaHub/passwall-sub-panel/internal/transport/http/handler"
)

// TestLive_RealNodeAgentContract is C2's cross-repository gate. It executes the
// Passwall-Node binary's real HTTP client, SQLite durability, segment receiver,
// convergence processor and report builder against PSP's real coordinator and
// HTTP boundary. Only B3's core process is represented by the node repository's
// deterministic contract runtime. Authentication is a fixed AgentID fixture,
// not a Bearer-credential or installer test. Recreating the node SQLite store
// below proves declarative rehydration, not real core-counter reset accounting
// or recovery from PSP/VM rollback.
//
// Run from the PSP repository with:
//
//	PSP_LIVE_NODE_REPO=/absolute/path/to/Passwall-Node \
//	  go test ./internal/service/nodesync -run TestLive_RealNodeAgentContract -v
func TestLive_RealNodeAgentContract(t *testing.T) {
	nodeRepoPath := os.Getenv("PSP_LIVE_NODE_REPO")
	if nodeRepoPath == "" {
		t.Skip("set PSP_LIVE_NODE_REPO to run the real Passwall-Node contract test")
	}
	if info, err := os.Stat(filepath.Join(nodeRepoPath, "go.mod")); err != nil || info.IsDir() {
		t.Fatalf("PSP_LIVE_NODE_REPO does not contain go.mod: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	db, err := sqlstore.Open("sqlite", filepath.Join(t.TempDir(), "psp.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := sqlstore.EnsureSchema(db); err != nil {
		t.Fatal(err)
	}
	repos := sqlstore.NewRepos(db)
	digest := sha256.Sum256([]byte("contract-credential"))
	agentRow := &domain.NodeAgent{
		AgentID: "agt_contract", Epoch: 1, CredentialSHA256: hex.EncodeToString(digest[:]),
	}
	panel := &domain.XUIPanel{
		Kind: domain.PanelKindPSP, Name: "contract-native", URL: "psp://" + agentRow.AgentID,
	}
	if err := repos.NativeAgentProvisioning.Create(ctx, panel, agentRow); err != nil {
		t.Fatal(err)
	}
	limit := int64(1_000)
	user := &domain.User{
		UPN: "contract@example.test", Email: "contract@example.test",
		SSOProvider: domain.SSOProviderLocal, SSOSubject: "contract@example.test",
		Role: domain.RoleUser, SubToken: "contract-sub-token",
		UUID: "22222222-2222-4222-8222-222222222222", Enabled: true,
		Limits: domain.LimitOverrides{TrafficLimitBytes: &limit},
	}
	if err := repos.User.Create(ctx, user); err != nil {
		t.Fatal(err)
	}
	node := &domain.Node{
		PanelID: panel.ID, InboundID: 1, DisplayName: "contract", ServerAddress: "node.example.test",
		DesiredProtocol: "vless", DesiredPort: 443, InboundListen: "0.0.0.0",
		InboundRemark: "contract", InboundSettings: `{}`, StreamSettings: `{}`,
		Sniffing: `{}`, Allocate: `{}`, Region: "CA", Enabled: true,
	}
	if err := repos.Node.Create(ctx, node); err != nil {
		t.Fatal(err)
	}
	client := &domain.PSPClient{
		UserID: user.ID, PanelID: node.PanelID, Email: "contract@psp.local",
		UUID: user.UUID, Password: "contract-secret", DesiredEnable: true, DesiredMinted: true,
	}
	client.ID, err = repos.PSPClient.Create(ctx, client)
	if err != nil {
		t.Fatal(err)
	}
	if err := repos.PSPClient.SetInbounds(ctx, client.ID, []domain.PSPClientInbound{{
		ClientID: client.ID, NodeID: node.ID, State: domain.ClientApplyPending,
	}}); err != nil {
		t.Fatal(err)
	}
	coordinator, err := nodesync.New(nodesync.Options{
		Desired: repos.NativeDesired, Agents: repos.NodeAgent, Issues: repos.NodeAgentIssue, Tasks: repos.NodeAgentTask, Users: repos.User,
		Clients: repos.PSPClient, Nodes: repos.Node, Panels: repos.XUIPanel, Settings: repos.Settings,
	})
	if err != nil {
		t.Fatal(err)
	}
	capture := &realNodeContractCapture{coordinator: coordinator}
	syncHandler, err := handler.NewNodeSyncHandler(capture, handler.NodeAuthenticatorFunc(
		func(*http.Request) (string, error) { return agentRow.AgentID, nil },
	))
	if err != nil {
		t.Fatal(err)
	}
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		syncHandler.ServeHTTP(w, request)
	}))
	defer server.Close()

	runAgent := func(statePath string) []byte {
		t.Helper()
		command := exec.CommandContext(ctx, "go", "run", "./cmd/contract-agent",
			"-endpoint", server.URL+"/v1/node/sync",
			"-agent-id", agentRow.AgentID,
			"-state", statePath,
			"-rounds", "2",
			"-allow-insecure-http",
		)
		command.Dir = nodeRepoPath
		output, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("real agent failed: %v\n%s", err, output)
		}
		return output
	}
	statePath := filepath.Join(t.TempDir(), "node.db")
	output := runAgent(statePath)
	if requests.Load() != 2 {
		t.Fatalf("sync requests = %d, want 2; output=%s", requests.Load(), output)
	}

	attachments, err := repos.PSPClient.ListInbounds(ctx, client.ID)
	if err != nil || len(attachments) != 1 || !attachments[0].Applied() {
		t.Fatalf("real agent did not converge attachment: (%+v, %v); output=%s", attachments, err, output)
	}
	view, err := coordinator.NativePanelSnapshot(ctx, node.PanelID)
	if err != nil || len(view.Inbounds) != 1 || len(view.Inbounds[0].ClientStats) != 1 {
		t.Fatalf("real report did not reach PanelClient projection: (%+v, %v); output=%s", view, err, output)
	}
	if view.Status.CoreEngine != "xray" || view.Status.XrayVersion != "coreless" {
		t.Fatalf("real report core identity = %+v; output=%s", view.Status, output)
	}
	observedAgent, err := repos.NodeAgent.GetByAgentID(ctx, agentRow.AgentID)
	if err != nil || observedAgent.ObservedCoreEngine != domain.NodeCoreXray {
		t.Fatalf("real report core observation = (%+v, %v); output=%s", observedAgent, err, output)
	}
	adapter, err := pspnode.New(panel, coordinator, repos.Node, repos.NodeAgent)
	if err != nil {
		t.Fatal(err)
	}
	detail, err := adapter.GetClient(ctx, client.Email)
	if err != nil || detail == nil || len(detail.InboundIDs) != 1 || detail.InboundIDs[0] != node.InboundID {
		t.Fatalf("real agent not visible through native adapter: (%+v, %v)", detail, err)
	}

	type stableIdentity struct {
		PanelID, AgentRowID, NodeID, ClientID, UserID          int64
		InboundID                                              int
		AgentID, PanelURL, PanelName, NodeAddress, ClientEmail string
		ClientUUID, CredentialSHA256                           string
		AgentEpoch                                             uint64
	}
	loadIdentity := func() stableIdentity {
		t.Helper()
		storedPanel, err := repos.XUIPanel.GetByID(ctx, panel.ID)
		if err != nil {
			t.Fatal(err)
		}
		storedAgent, err := repos.NodeAgent.GetByAgentID(ctx, agentRow.AgentID)
		if err != nil {
			t.Fatal(err)
		}
		storedNode, err := repos.Node.GetByID(ctx, node.ID)
		if err != nil {
			t.Fatal(err)
		}
		storedClient, err := repos.PSPClient.GetByID(ctx, client.ID)
		if err != nil {
			t.Fatal(err)
		}
		if storedAgent.PanelID != storedPanel.ID || storedNode.PanelID != storedPanel.ID || storedClient.PanelID != storedPanel.ID {
			t.Fatal("contract identity lost its durable panel ownership")
		}
		return stableIdentity{
			PanelID: storedPanel.ID, AgentRowID: storedAgent.ID, NodeID: storedNode.ID,
			ClientID: storedClient.ID, UserID: storedClient.UserID, InboundID: storedNode.InboundID,
			AgentID: storedAgent.AgentID, PanelURL: storedPanel.URL, PanelName: storedPanel.Name,
			NodeAddress: storedNode.ServerAddress, ClientEmail: storedClient.Email, ClientUUID: storedClient.UUID,
			CredentialSHA256: storedAgent.CredentialSHA256, AgentEpoch: storedAgent.Epoch,
		}
	}
	beforeIdentity := loadIdentity()
	beforeStreams, err := repos.NodeAgent.ListStreams(ctx, agentRow.AgentID)
	if err != nil || len(beforeStreams) != 3 {
		t.Fatalf("initial durable streams = (%+v, %v)", beforeStreams, err)
	}
	savedStreams := make(map[domain.NodeAgentStreamName]domain.NodeAgentStream, len(beforeStreams))
	for _, stream := range beforeStreams {
		if stream.DesiredVersion == 0 || stream.DesiredETag == "" || len(stream.DesiredBody) == 0 {
			t.Fatalf("initial stream is not durably published: %s", stream.Stream)
		}
		saved := *stream
		saved.DesiredBody = bytes.Clone(stream.DesiredBody)
		savedStreams[stream.Stream] = saved
		if stream.Stream != domain.NodeAgentStreamDirectives && !stream.Converged() {
			t.Fatalf("initial declarative stream did not converge: %s", stream.Stream)
		}
	}

	// A fresh local database represents an ordinary machine reinstall. PSP keeps
	// its database, stable identities and declarative documents. No registration,
	// rotation, node creation or attachment mutation is performed between runs.
	freshStatePath := filepath.Join(t.TempDir(), "reinstalled-node.db")
	if _, err := os.Stat(freshStatePath); !os.IsNotExist(err) {
		t.Fatalf("reinstall state path was not fresh: %v", err)
	}
	output = runAgent(freshStatePath)
	if requests.Load() != 4 {
		t.Fatalf("sync requests after reinstall = %d, want 4; output=%s", requests.Load(), output)
	}
	exchanges := capture.snapshot()
	if len(exchanges) != 4 {
		t.Fatalf("captured real sync exchanges = %d, want 4", len(exchanges))
	}
	freshFirst, freshSecond := exchanges[2], exchanges[3]
	if freshFirst.report.AgentID != beforeIdentity.AgentID || freshFirst.report.Partial ||
		len(freshFirst.report.Objects) != 0 || len(freshFirst.report.ListenerCounters) != 0 || len(freshFirst.report.Clients) != 0 {
		t.Fatal("reinstalled agent did not start with a fresh full observation")
	}
	for _, name := range []string{nodeprotocol.StreamConfig, nodeprotocol.StreamRoster, nodeprotocol.StreamDirectives} {
		if have := freshFirst.report.Have[name]; !have.Applied.Zero() || have.ETag != "" {
			t.Fatalf("reinstalled agent unexpectedly retained stream %s", name)
		}
	}
	if freshFirst.response.Config.Unchanged || freshFirst.response.Config.Body == nil ||
		freshFirst.response.Roster.Unchanged || freshFirst.response.Roster.Body == nil ||
		freshFirst.response.Directives.Unchanged || freshFirst.response.Directives.Body == nil {
		t.Fatal("PSP did not send all three full streams to the reinstalled agent")
	}
	if len(freshFirst.response.Config.Body.Listeners) != 1 || len(freshFirst.response.Roster.Body.Clients) != 1 {
		t.Fatal("rehydrated streams lost the existing listener or client")
	}
	if freshSecond.report.Partial || len(freshSecond.report.ListenerCounters) != 1 || len(freshSecond.report.Clients) != 1 {
		t.Fatal("reinstalled agent did not report its rehydrated counters")
	}
	if counter := freshSecond.report.ListenerCounters[0]; counter.Key != nodeprotocol.NewListenerKey(node.ID) || !counter.Present {
		t.Fatal("reinstalled listener report lost the stable PSP node identity")
	}
	if counter := freshSecond.report.Clients[0]; counter.Key != nodeprotocol.NewClientKey(client.ID) || !counter.Present {
		t.Fatal("reinstalled client report lost the stable PSP client identity")
	}
	wantObjects := map[string]string{
		string(nodeprotocol.NewListenerKey(node.ID)): nodeprotocol.StreamConfig,
		string(nodeprotocol.NewClientKey(client.ID)): nodeprotocol.StreamRoster,
	}
	for _, object := range freshSecond.report.Objects {
		if stream, exists := wantObjects[object.Key]; exists && object.Stream == stream && object.State == nodeprotocol.ObjectApplied {
			delete(wantObjects, object.Key)
		}
	}
	if len(wantObjects) != 0 {
		t.Fatal("reinstalled agent did not freshly report both existing objects as applied")
	}
	for name, expected := range map[string]nodeprotocol.StreamState{
		nodeprotocol.StreamConfig:     {Applied: freshFirst.response.Config.Version, ETag: freshFirst.response.Config.ETag},
		nodeprotocol.StreamRoster:     {Applied: freshFirst.response.Roster.Version, ETag: freshFirst.response.Roster.ETag},
		nodeprotocol.StreamDirectives: {Applied: freshFirst.response.Directives.Version, ETag: freshFirst.response.Directives.ETag},
	} {
		if freshSecond.report.Have[name] != expected {
			t.Fatalf("reinstalled agent did not durably receive and report stream %s", name)
		}
	}
	if !freshSecond.response.Config.Unchanged || freshSecond.response.Config.Body != nil ||
		!freshSecond.response.Roster.Unchanged || freshSecond.response.Roster.Body != nil {
		t.Fatal("rehydrated declarative streams did not return to conditional delivery")
	}
	if afterIdentity := loadIdentity(); afterIdentity != beforeIdentity {
		t.Fatal("node reinstall recreated or rebound PSP panel/agent/node/client identity")
	}
	afterStreams, err := repos.NodeAgent.ListStreams(ctx, agentRow.AgentID)
	if err != nil || len(afterStreams) != len(beforeStreams) {
		t.Fatalf("durable streams after reinstall = (%+v, %v)", afterStreams, err)
	}
	for _, stream := range afterStreams {
		before, exists := savedStreams[stream.Stream]
		if !exists || stream.AgentID != before.AgentID {
			t.Fatal("node reinstall recreated the durable stream identity")
		}
		if stream.Stream == domain.NodeAgentStreamDirectives {
			// Directives deliberately reflect this report's counters/coverage. The
			// fresh empty report followed by the populated report can mint newer
			// directives; two rounds acknowledge the first response, not the latest
			// response. Do not confuse declarative recovery with final quota ACK.
			if stream.AppliedEpoch != freshFirst.response.Directives.Version.Epoch ||
				stream.AppliedVersion != freshFirst.response.Directives.Version.Version ||
				stream.AppliedETag != string(freshFirst.response.Directives.ETag) {
				t.Fatal("reinstalled agent's received directives were not persisted by PSP")
			}
			continue
		}
		if stream.DesiredVersion != before.DesiredVersion || stream.DesiredETag != before.DesiredETag ||
			!bytes.Equal(stream.DesiredBody, before.DesiredBody) || !stream.Converged() ||
			stream.AppliedEpoch != before.AppliedEpoch || stream.AppliedVersion != before.AppliedVersion || stream.AppliedETag != before.AppliedETag {
			t.Fatalf("node reinstall changed the existing config/roster document: %s", stream.Stream)
		}
	}
	attachments, err = repos.PSPClient.ListInbounds(ctx, client.ID)
	if err != nil || len(attachments) != 1 || attachments[0].NodeID != node.ID || !attachments[0].Applied() {
		t.Fatalf("reinstalled real agent did not converge the existing attachment: (%+v, %v); output=%s", attachments, err, output)
	}
	view, err = coordinator.NativePanelSnapshot(ctx, panel.ID)
	if err != nil || len(view.Inbounds) != 1 || len(view.Inbounds[0].ClientStats) != 1 {
		t.Fatalf("reinstalled real report did not restore the native projection: (%+v, %v)", view, err)
	}
	detail, err = adapter.GetClient(ctx, client.Email)
	if err != nil || detail == nil || len(detail.InboundIDs) != 1 || detail.InboundIDs[0] != node.InboundID {
		t.Fatalf("reinstalled real agent not visible through the existing adapter: (%+v, %v)", detail, err)
	}
}

type realNodeContractExchange struct {
	report   nodeprotocol.NodeReport
	response nodeprotocol.SyncResponse
}

// Capture surrounds, rather than replaces, the production coordinator so the
// assertions inspect what the real Node HTTP/report/receiver path exchanged.
type realNodeContractCapture struct {
	coordinator *nodesync.Service
	mu          sync.Mutex
	exchanges   []realNodeContractExchange
}

func (c *realNodeContractCapture) Sync(ctx context.Context, report nodeprotocol.NodeReport) (nodeprotocol.SyncResponse, error) {
	response, err := c.coordinator.Sync(ctx, report)
	if err == nil {
		c.mu.Lock()
		c.exchanges = append(c.exchanges, realNodeContractExchange{report: report, response: response})
		c.mu.Unlock()
	}
	return response, err
}

func (c *realNodeContractCapture) snapshot() []realNodeContractExchange {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]realNodeContractExchange(nil), c.exchanges...)
}
