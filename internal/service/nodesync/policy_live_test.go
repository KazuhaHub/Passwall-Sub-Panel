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
	"slices"
	"strconv"
	"testing"
	"time"

	nodeprotocol "github.com/KazuhaHub/passwall-protocol/protocol"

	"github.com/KazuhaHub/passwall-sub-panel/internal/adapters/sqlstore"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/corefixtures"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/destlist"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/destpolicy"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/nodesync"
	"github.com/KazuhaHub/passwall-sub-panel/internal/transport/http/handler"
)

// TestLive_RealNodePolicyContract is the destination-policy round trip against
// the Passwall-Node binary's own contract agent: PSP mints a policy, the real
// agent declares the capability, receives the policy in its config document,
// stores it and reports a PolicyStatus about it, and PSP records and shows what
// the node said.
//
// IT NEEDS A NODE THAT DECLARES policy.destination.v1, which is why it is not
// in the node-wire-v1 profile: that profile runs against every release the
// matrix covers, and a release older than the capability cannot satisfy this
// test. It runs in the pinned-source contract job instead, against the release
// contract_source names.
//
// The agent is a real process with a real SQLite state, but no core: a policy
// that validates is "applied", one that does not is "rejected", and a node that
// holds none reports "applied" with an empty digest. What this proves is the
// wire contract and PSP's reading of it, not that any core enforces a rule.
//
//	PSP_LIVE_NODE_REPO=/absolute/path/to/Passwall-Node \
//	  go test ./internal/service/nodesync -run TestLive_RealNodePolicyContract -v
func TestLive_RealNodePolicyContract(t *testing.T) {
	nodeRepoPath := os.Getenv("PSP_LIVE_NODE_REPO")
	if nodeRepoPath == "" {
		t.Skip("set PSP_LIVE_NODE_REPO to run the real Passwall-Node policy contract test")
	}
	if info, err := os.Stat(filepath.Join(nodeRepoPath, "go.mod")); err != nil || info.IsDir() {
		t.Fatalf("PSP_LIVE_NODE_REPO does not contain go.mod: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	db, err := sqlstore.OpenQuiet("sqlite", filepath.Join(t.TempDir(), "psp.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := sqlstore.EnsureSchema(db); err != nil {
		t.Fatal(err)
	}
	repos := sqlstore.NewRepos(db)
	destRepos := sqlstore.NewDestRepos(db)

	credential := "pspn_0123456789abcdefghijklmnopqrstuvwxyzABCDEFG"
	digest := sha256.Sum256([]byte(credential))
	agentRow := &domain.NodeAgent{
		AgentID: "agt_policy_contract", Epoch: 1, CredentialSHA256: hex.EncodeToString(digest[:]),
		DesiredCoreEngine: domain.NodeCoreXray, DesiredCoreVersion: "26.6.27",
	}
	panel := &domain.XUIPanel{Kind: domain.PanelKindPSP, Name: "policy-contract-native", URL: "psp://" + agentRow.AgentID}
	if err := repos.NativeAgentProvisioning.Create(ctx, panel, agentRow); err != nil {
		t.Fatal(err)
	}
	limit := int64(1_000)
	user := &domain.User{
		UPN: "policy@example.test", Email: "policy@example.test",
		SSOProvider: domain.SSOProviderLocal, SSOSubject: "policy@example.test",
		Role: domain.RoleUser, SubToken: "policy-sub-token",
		UUID: "33333333-3333-4333-8333-333333333333", Enabled: true,
		Limits: domain.LimitOverrides{TrafficLimitBytes: &limit},
	}
	if err := repos.User.Create(ctx, user); err != nil {
		t.Fatal(err)
	}
	node := &domain.Node{
		PanelID: panel.ID, InboundID: 1, DisplayName: "policy", ServerAddress: "node.example.test",
		DesiredProtocol: "vless", DesiredPort: 443, InboundListen: "0.0.0.0",
		InboundRemark: "policy", InboundSettings: `{}`, StreamSettings: `{}`,
		Sniffing: `{"enabled":true,"destOverride":["http","tls","quic"],"routeOnly":false}`, Allocate: `{}`,
		Region: "CA", Enabled: true,
	}
	if err := repos.Node.Create(ctx, node); err != nil {
		t.Fatal(err)
	}
	client := &domain.PSPClient{
		UserID: user.ID, PanelID: node.PanelID, Email: "policy@psp.local",
		UUID: user.UUID, Password: "policy-secret", DesiredEnable: true, DesiredMinted: true,
	}
	client.ID, err = repos.PSPClient.Create(ctx, client)
	if err != nil {
		t.Fatal(err)
	}
	if err := repos.PSPClient.SetInbounds(ctx, client.ID, []domain.PSPClientInbound{
		{ClientID: client.ID, NodeID: node.ID, State: domain.ClientApplyPending},
	}); err != nil {
		t.Fatal(err)
	}

	// The compiler is the production one over the production store, bound to the
	// coordinator the way the application binds it.
	lists := destlist.New(destlist.Deps{
		Lists: destRepos.Lists, Policies: destRepos.Policies, SourceRules: destRepos.SourceRules, State: destRepos.State,
		Settings: policyLiveSettings{}, Locator: policyLiveLocator{}, DataDir: t.TempDir(),
	})
	policies, err := destpolicy.New(destpolicy.Deps{
		Definitions: destRepos, State: destRepos.State, Agents: destRepos.Agents,
		Lists: lists, Countries: lists.Countries, Settings: policyLiveSettings{},
	})
	if err != nil {
		t.Fatal(err)
	}
	admin, err := destpolicy.NewAdmin(destpolicy.AdminDeps{
		Service: policies, Policies: destRepos.Policies, Lists: destRepos.Lists, Counter: destRepos,
		Groups: repos.Group, Panels: repos.XUIPanel, Nodes: repos.Node, Clients: repos.PSPClient,
		Agents: repos.NodeAgent, Desired: repos.NativeDesired, Settings: policyLiveSettings{},
	})
	if err != nil {
		t.Fatal(err)
	}
	coordinator, err := nodesync.New(nodesync.Options{CoreCatalog: corefixtures.Static{},
		Desired: repos.NativeDesired, Agents: repos.NodeAgent, Issues: repos.NodeAgentIssue, Tasks: repos.NodeAgentTask, Users: repos.User,
		Clients: repos.PSPClient, Nodes: repos.Node, Panels: repos.XUIPanel, Settings: repos.ScopedSettings,
	})
	if err != nil {
		t.Fatal(err)
	}
	coordinator.SetPolicies(policies)
	capture := &realNodeContractCapture{coordinator: coordinator}
	syncHandler, err := handler.NewNodeSyncHandler(capture, handler.NodeAuthenticatorFunc(
		func(*http.Request) (string, error) { return agentRow.AgentID, nil },
	))
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(syncHandler)
	defer server.Close()

	statePath := filepath.Join(t.TempDir(), "node.db")
	// THREE ROUNDS, because a policy takes two to be reported: the first round's
	// response carries it, the second round's report is the first that can say
	// what the node holds, and the third confirms the status is stable.
	const rounds = 3
	runAgent := func() []realNodeContractExchange {
		t.Helper()
		before := len(capture.snapshot())
		command := exec.CommandContext(ctx, "go", "run", "./cmd/contract-agent",
			"-endpoint", server.URL+"/v1/node/sync",
			"-agent-id", agentRow.AgentID,
			"-state", statePath,
			"-rounds", strconv.Itoa(rounds),
			"-allow-insecure-http",
		)
		command.Dir = nodeRepoPath
		output, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("real agent failed: %v\n%s", err, output)
		}
		exchanges := capture.snapshot()[before:]
		if len(exchanges) != rounds {
			t.Fatalf("captured %d sync exchanges, want %d; output=%s", len(exchanges), rounds, output)
		}
		return exchanges
	}
	nodeState := func() string {
		t.Helper()
		status, err := admin.Status(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for _, n := range status.Nodes {
			if n.PanelID == panel.ID {
				return n.State
			}
		}
		t.Fatalf("the status page does not list panel %d: %+v", panel.ID, status.Nodes)
		return ""
	}

	// WITH NO POLICY the node still says something: it holds none, and an empty
	// digest is how it says so. PSP must not read that as a missing report.
	exchanges := runAgent()
	last := exchanges[rounds-1].report
	if !slices.Contains(last.Capabilities, nodeprotocol.CapabilityDestinationPolicy) {
		t.Fatalf("the agent does not declare %s: %v", nodeprotocol.CapabilityDestinationPolicy, last.Capabilities)
	}
	if got := last.PolicyStatus; got == nil || got.Digest != "" || got.State != nodeprotocol.PolicyStateApplied {
		t.Fatalf("a node with no policy reported %+v, want applied with an empty digest", got)
	}
	for _, exchange := range exchanges {
		if exchange.response.Config.Body != nil && exchange.response.Config.Body.Policy != nil {
			t.Fatalf("PSP sent a policy while none is defined: %+v", exchange.response.Config.Body.Policy)
		}
	}
	if state := nodeState(); state != "none" {
		t.Fatalf("with no policy the status page shows %q, want none", state)
	}

	// PSP MINTS A BITTORRENT BLOCK. It is a definition write, so the published
	// snapshot is what the node is sent; Publish stands in for the debounce.
	torrent := &domain.DestPolicy{
		Name: "BitTorrent", Action: domain.DestActionBlock, State: domain.DestStateOn, Scope: domain.DestScopeAll,
		Inline: domain.DestPolicyInline{Protocols: []string{"bittorrent"}},
	}
	if err := destRepos.Policies.Create(ctx, torrent); err != nil {
		t.Fatal(err)
	}
	if _, err := policies.Publish(ctx); err != nil {
		t.Fatal(err)
	}
	exchanges = runAgent()
	var sent *nodeprotocol.DestinationPolicy
	for _, exchange := range exchanges {
		if body := exchange.response.Config.Body; body != nil && body.Policy != nil {
			sent = body.Policy
			break
		}
	}
	if sent == nil {
		t.Fatal("PSP never sent the policy to the real agent")
	}
	if len(sent.Rules) != 1 || sent.Rules[0].Action != nodeprotocol.RuleBlock ||
		!slices.Equal(sent.Rules[0].Protocols, []string{"bittorrent"}) {
		t.Fatalf("the policy on the wire is not the BitTorrent block: %+v", sent.Rules)
	}
	want := nodeprotocol.PolicyDigest(sent)
	if want == "" {
		t.Fatal("the policy PSP sent has no digest")
	}
	// The node's report is the one the compiler consumed.
	got := exchanges[rounds-1].report.PolicyStatus
	if got == nil || got.State != nodeprotocol.PolicyStateApplied || got.Digest != want || got.IssueCode != "" {
		t.Fatalf("the real agent reported %+v for the policy, want applied with digest %s", got, want)
	}
	row, err := destRepos.Agents.Get(ctx, agentRow.AgentID)
	if err != nil {
		t.Fatal(err)
	}
	if row.MintedSHA256 != want || row.ReportedSHA256 != want || row.ReportedState != nodeprotocol.PolicyStateApplied ||
		row.AppliedSHA256 != want || row.FallbackReason != domain.DestFallbackNone {
		t.Fatalf("dest_agent_policy does not show the policy applied: %+v", row)
	}
	if state := nodeState(); state != "applied" {
		t.Fatalf("the status page shows %q after the node applied the policy, want applied", state)
	}

	// WITHDRAWING THE POLICY is also a definition write. The node must be told,
	// replace what it stored, and report the empty digest again.
	if err := destRepos.Policies.Delete(ctx, torrent.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := policies.Publish(ctx); err != nil {
		t.Fatal(err)
	}
	exchanges = runAgent()
	got = exchanges[rounds-1].report.PolicyStatus
	if got == nil || got.Digest != "" || got.State != nodeprotocol.PolicyStateApplied {
		t.Fatalf("after the policy was withdrawn the agent reported %+v, want applied with an empty digest", got)
	}
	row, err = destRepos.Agents.Get(ctx, agentRow.AgentID)
	if err != nil {
		t.Fatal(err)
	}
	if row.MintedSHA256 != "" || row.ReportedSHA256 != "" || row.ReportedState != nodeprotocol.PolicyStateApplied ||
		row.FallbackReason != domain.DestFallbackNone {
		t.Fatalf("dest_agent_policy still shows a policy after it was withdrawn: %+v", row)
	}
	if state := nodeState(); state != "none" {
		t.Fatalf("the status page shows %q after the policy was withdrawn, want none", state)
	}
}

// policyLiveSettings is the settings reader the compiler and the list service
// take: every access-control setting at its default.
type policyLiveSettings struct{}

func (policyLiveSettings) Load(context.Context, ports.UISettings) (ports.UISettings, error) {
	return ports.UISettings{}, nil
}

// policyLiveLocator says there is no GeoIP database, which no rule here needs.
type policyLiveLocator struct{}

func (policyLiveLocator) ActiveMMDBPath(context.Context) string { return "" }
