package nodesync_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

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
// deterministic contract runtime.
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
		Desired: repos.NativeDesired, Agents: repos.NodeAgent, Issues: repos.NodeAgentIssue, Users: repos.User,
		Clients: repos.PSPClient, Nodes: repos.Node, Panels: repos.XUIPanel, Settings: repos.Settings,
	})
	if err != nil {
		t.Fatal(err)
	}
	syncHandler, err := handler.NewNodeSyncHandler(coordinator, handler.NodeAuthenticatorFunc(
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

	statePath := filepath.Join(t.TempDir(), "node.db")
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
}
