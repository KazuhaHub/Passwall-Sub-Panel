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
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/jwtutil"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/metrics"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// THE POLICY COMPILER, ITS NODE-SYNC HOOK AND ITS ADMIN API ARE WIRED LATE, and
// each link fails silently when forgotten: nativeSync without SetPolicies simply
// never sends a policy, a Deps.Dest left out answers 503 only when the page is
// opened, and a refresh loop that does not sweep exemptions lets them linger.
// So this drives one policy from the admin's definition to a node and back
// through the application Build assembled: the node is sent the policy, what it
// reports about it is recorded (even when the report's status is malformed, the
// sync is still answered), the admin API sees the node's state, and an expired
// exemption is removed.
func TestBuildWiresTheAccessControlPolicy(t *testing.T) {
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
	if a.destPolicy == nil {
		t.Fatal("Build did not construct the policy compiler")
	}

	credential := strings.Repeat("fixture-dest-policy-credential-", 2)
	digest := sha256.Sum256([]byte(credential))
	agent := &domain.NodeAgent{AgentID: "agt_dest_wiring", Epoch: 7, CredentialSHA256: hex.EncodeToString(digest[:]),
		DesiredCoreEngine: domain.NodeCoreXray, DesiredCoreVersion: "26.6.27"}
	panel := &domain.XUIPanel{Name: "dest-native", Kind: domain.PanelKindPSP, URL: "psp://" + agent.AgentID}
	if err := a.repos.NativeAgentProvisioning.Create(ctx, panel, agent); err != nil {
		t.Fatal(err)
	}
	if err := a.xuiPool.Add(panel); err != nil {
		t.Fatal(err)
	}
	node := &domain.Node{
		PanelID: panel.ID, InboundID: 1, DisplayName: "dest-node", ServerAddress: "node.example.test",
		DesiredProtocol: "vless", DesiredPort: 444, InboundListen: "0.0.0.0", InboundSettings: `{"decryption":"none"}`,
		StreamSettings: `{"network":"tcp","security":"none"}`, Sniffing: `{}`, Allocate: `{}`, Enabled: true,
	}
	if err := a.repos.Node.Create(ctx, node); err != nil {
		t.Fatal(err)
	}

	sync := func(mutate func(map[string]any)) (int, nodeprotocol.SyncResponse) {
		t.Helper()
		report := nodeprotocol.NodeReport{
			AgentID: agent.AgentID, ProtocolVersion: nodeprotocol.ProtocolVersion1, CoreEngine: "xray",
			Capabilities: []string{nodeprotocol.CapabilityDestinationPolicy},
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
		var response nodeprotocol.SyncResponse
		if rec.Code == http.StatusOK {
			if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
				t.Fatal(err)
			}
		}
		return rec.Code, response
	}

	// Nothing defined: no policy, and a config with no policy key at all.
	if code, res := sync(nil); code != http.StatusOK || res.Config.Body == nil || res.Config.Body.Policy != nil {
		t.Fatalf("before any policy: status %d, config %+v", code, res.Config.Body)
	}

	// A rule group, switched on and published, reaches the node.
	pol, err := a.destCatalog.Instantiate(ctx, "smtp", "en-US", domain.DestStateOn)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.destPolicy.Publish(ctx); err != nil {
		t.Fatal(err)
	}
	code, res := sync(nil)
	if code != http.StatusOK || res.Config.Body == nil || res.Config.Body.Policy == nil || len(res.Config.Body.Policy.Rules) != 1 ||
		res.Config.Body.Policy.Rules[0].ID != "p"+itoa(pol.ID) || res.Config.Body.Policy.Rules[0].Ports != "25,465,587" {
		t.Fatalf("the policy did not reach the node (is nativeSync.SetPolicies called?): status %d, config %+v", code, res.Config.Body)
	}
	want := nodeprotocol.PolicyDigest(res.Config.Body.Policy)

	// The node says it applied that policy; the record of it is the last known
	// good. This goes through the handler's isolated decode of policy_status.
	status := map[string]any{"digest": want, "state": "applied"}
	if code, _ := sync(func(m map[string]any) { m["policy_status"] = status }); code != http.StatusOK {
		t.Fatalf("a valid policy status failed the sync: %d", code)
	}
	row, err := a.destRepos.Agents.Get(ctx, agent.AgentID)
	if err != nil || row.AppliedSHA256 != want || row.MintedSHA256 != want || row.ReportedState != "applied" {
		t.Fatalf("the node's status was not recorded: %+v (%v)", row, err)
	}

	// A policy status that is the wrong shape costs the status and nothing else.
	before := metrics.NodePolicyStatusDroppedTotal.Value()
	if code, _ := sync(func(m map[string]any) { m["policy_status"] = map[string]any{"digest": 7, "state": []int{1}} }); code != http.StatusOK {
		t.Fatalf("a malformed policy status failed the sync: %d", code)
	}
	if metrics.NodePolicyStatusDroppedTotal.Value() != before+1 {
		t.Fatal("a dropped policy status was not counted")
	}

	// The admin API reads the same state through the router Build assembled.
	admin := &domain.User{
		UPN: "dest-admin@example.test", Email: "dest-admin@example.test", SSOProvider: domain.SSOProviderLocal,
		SSOSubject: "dest-admin@example.test", Role: domain.RoleAdmin, Enabled: true,
		UUID: "77777777-7777-4777-8777-777777777777", SubToken: "fixture-dest-admin-token", TrafficResetPeriod: domain.ResetMonthly,
	}
	if err := a.repos.User.Create(ctx, admin); err != nil {
		t.Fatal(err)
	}
	set, err := a.repos.Settings.Load(ctx, ports.UISettings{})
	if err != nil {
		t.Fatal(err)
	}
	token, err := jwtutil.NewIssuer(cfg.JWTSecret, func() jwtutil.Params {
		return jwtutil.Params{AccessTTL: time.Hour, RefreshTTL: time.Hour, Issuer: set.JWTIssuer}
	}).IssueAccess(admin.ID, admin.UPN, admin.Role, admin.TokenVersion)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/admin/dest/status", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	a.server.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /dest/status = %d: %s — is Deps.Dest wired into the router?", rec.Code, rec.Body.String())
	}
	var st struct {
		Nodes []struct {
			PanelID int64  `json:"panel_id"`
			State   string `json:"state"`
		} `json:"nodes"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &st); err != nil {
		t.Fatal(err)
	}
	state := ""
	for _, n := range st.Nodes {
		if n.PanelID == panel.ID {
			state = n.State
		}
	}
	if state != "applied" {
		t.Fatalf("the status page shows the node as %q, want applied: %s", state, rec.Body.String())
	}

	// An expired exemption is swept by the refresh loop's pass.
	past := time.Now().Add(-time.Hour)
	if err := a.destRepos.Exemptions.Upsert(ctx, &domain.DestExemption{UserID: admin.ID, ExpiresAt: &past}); err != nil {
		t.Fatal(err)
	}
	a.sweepExpiredExemptions(ctx)
	if _, err := a.destRepos.Exemptions.Get(ctx, admin.ID); err == nil {
		t.Fatal("the expired exemption was not swept")
	}
}

func itoa(n int64) string {
	b, _ := json.Marshal(n)
	return string(b)
}
