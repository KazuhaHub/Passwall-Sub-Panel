package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	nodeprotocol "github.com/KazuhaHub/passwall-protocol/protocol"

	"github.com/KazuhaHub/passwall-sub-panel/internal/adapters/sqlstore"
	"github.com/KazuhaHub/passwall-sub-panel/internal/config"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// WHAT MAKES A GROUP'S ALLOWLIST FOLLOW A NODE IS FIVE TRIGGERS, EACH WIRED
// LATE, and a forgotten one is silent: the node keeps (or never gets) its
// members' clients until a heal sweep happens to look. The eligibility answer
// is cached for 30 seconds, so each trigger is observable as "the answer
// changed right now, not half a minute later". This drives each through the
// application Build assembled: a node reporting different capabilities, an
// inbound's sniffing edited, and a node refusing the policy it was sent.
func TestBuildWiresTheAllowlistResyncTriggers(t *testing.T) {
	ctx := t.Context()
	directory := t.TempDir()
	cfg := &config.Config{
		Listen: "127.0.0.1:0", JWTSecret: strings.Repeat("j", 48), EncryptionKey: strings.Repeat("e", 48),
		ConfigDir: filepath.Join(directory, "config"), DataDir: filepath.Join(directory, "data"),
	}
	a, err := Build(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := a.Shutdown(shutdownCtx); err != nil {
			t.Error(err)
		}
		sqlstore.ConfigureSecretKey("")
	})

	credential := strings.Repeat("fixture-allowlist-credential-", 2)
	digest := sha256.Sum256([]byte(credential))
	agent := &domain.NodeAgent{AgentID: "agt_allowlist_wiring", Epoch: 7, CredentialSHA256: hex.EncodeToString(digest[:]),
		DesiredCoreEngine: domain.NodeCoreXray, DesiredCoreVersion: "26.6.27"}
	panel := &domain.XUIPanel{Name: "allowlist-native", Kind: domain.PanelKindPSP, URL: "psp://" + agent.AgentID}
	if err := a.repos.NativeAgentProvisioning.Create(ctx, panel, agent); err != nil {
		t.Fatal(err)
	}
	if err := a.xuiPool.Add(panel); err != nil {
		t.Fatal(err)
	}
	node := &domain.Node{
		PanelID: panel.ID, InboundID: 1, DisplayName: "allowlist-node", ServerAddress: "node.example.test",
		DesiredProtocol: "vless", DesiredPort: 444, InboundListen: "0.0.0.0", InboundSettings: `{"decryption":"none"}`,
		StreamSettings: `{"network":"tcp","security":"none"}`, Sniffing: `{}`, Allocate: `{}`, Enabled: true,
	}
	if err := a.repos.Node.Create(ctx, node); err != nil {
		t.Fatal(err)
	}
	g := &domain.Group{Slug: "allowlist-wiring", Name: "allowlist", TagFilter: domain.TagFilter{All: true},
		DestAllowlist: domain.DestAllowlist{State: domain.DestStateOn}}
	if err := a.repos.Group.Create(ctx, g); err != nil {
		t.Fatal(err)
	}

	sync := func(caps []string, mutate func(map[string]any)) int {
		t.Helper()
		report := nodeprotocol.NodeReport{
			AgentID: agent.AgentID, ProtocolVersion: nodeprotocol.ProtocolVersion1, CoreEngine: "xray", Capabilities: caps,
			Have: map[string]nodeprotocol.StreamState{
				nodeprotocol.StreamConfig: {}, nodeprotocol.StreamRoster: {}, nodeprotocol.StreamDirectives: {},
			},
		}
		raw, err := json.Marshal(report)
		if err != nil {
			t.Fatal(err)
		}
		var generic map[string]any
		if err := json.Unmarshal(raw, &generic); err != nil {
			t.Fatal(err)
		}
		if mutate != nil {
			mutate(generic)
		}
		payload, err := json.Marshal(generic)
		if err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest(http.MethodPost, "/v1/node/sync", bytes.NewReader(payload))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+credential)
		rec := httptest.NewRecorder()
		a.server.Handler.ServeHTTP(rec, req)
		return rec.Code
	}
	included := func() bool {
		t.Helper()
		got, err := a.group.NodesFor(ctx, g)
		if err != nil {
			t.Fatal(err)
		}
		return len(got) == 1 && got[0].ID == node.ID
	}
	both := []string{nodeprotocol.CapabilityDestinationPolicy, nodeprotocol.CapabilityDestinationGroups}
	policyOnly := []string{nodeprotocol.CapabilityDestinationPolicy}

	if included() {
		t.Fatal("a node that has declared nothing is eligible for an allowlist")
	}

	// A node starts to declare the capabilities: the answer that was just cached
	// as "no" must not stand for half a minute.
	if code := sync(both, nil); code != http.StatusOK {
		t.Fatalf("sync = %d", code)
	}
	if !included() {
		t.Fatal("a capability appearing did not make the node eligible at once (is nativeSync.SetAllowlistResyncer called?)")
	}
	if code := sync(policyOnly, nil); code != http.StatusOK {
		t.Fatalf("sync = %d", code)
	}
	if included() {
		t.Fatal("a capability disappearing did not make the node ineligible at once")
	}
	if code := sync(both, nil); code != http.StatusOK || !included() {
		t.Fatal("the capability coming back did not make the node eligible at once")
	}

	// An inbound's sniffing edited so it can no longer supply domains.
	spec := ports.InboundSpec{
		Protocol: "vless", Port: 444, Listen: "0.0.0.0", Enable: true, Remark: "allowlist-node",
		Settings: `{"decryption":"none"}`, StreamSettings: `{"network":"tcp","security":"none"}`, Allocate: `{}`,
		Sniffing: `{"enabled":true,"metadataOnly":true}`,
	}
	if err := a.node.UpdateInboundConfig(ctx, node.ID, spec); err != nil {
		t.Fatal(err)
	}
	if included() {
		t.Fatal("an inbound edit that broke the sniffing did not make the node ineligible at once (is nodeSvc.SetAllowlistResyncer called?)")
	}
	spec.Sniffing = `{"enabled":true,"destOverride":["http","tls","quic"]}`
	if err := a.node.UpdateInboundConfig(ctx, node.ID, spec); err != nil {
		t.Fatal(err)
	}
	if !included() {
		t.Fatal("an inbound edit that fixed the sniffing did not make the node eligible at once")
	}

	// The node refuses the policy it was sent.
	if _, err := a.destCatalog.Instantiate(ctx, "smtp", "en-US", domain.DestStateOn); err != nil {
		t.Fatal(err)
	}
	if _, err := a.destPolicy.Publish(ctx); err != nil {
		t.Fatal(err)
	}
	reply := func(mutate func(map[string]any)) {
		t.Helper()
		if code := sync(both, mutate); code != http.StatusOK {
			t.Fatalf("sync = %d", code)
		}
	}
	reply(nil)
	row, err := a.destRepos.Agents.Get(ctx, agent.AgentID)
	if err != nil || row.MintedSHA256 == "" {
		t.Fatalf("no policy was minted for the node: %+v (%v)", row, err)
	}
	minted := row.MintedSHA256
	if !included() {
		t.Fatal("setup: the node is not eligible before it refuses anything")
	}
	reply(func(m map[string]any) {
		m["policy_status"] = map[string]any{"digest": minted, "state": "rejected", "issue_code": nodeprotocol.IssueDestinationPolicyRejected}
	})
	if row, _ := a.destRepos.Agents.Get(ctx, agent.AgentID); row == nil || row.FallbackReason != domain.DestFallbackRejected {
		t.Fatalf("setup: the refusal was not recorded: %+v", row)
	}
	if included() {
		t.Fatal("a node refusing the policy stayed eligible (is destpolicy.Deps.FallbackChanged wired?)")
	}
}
