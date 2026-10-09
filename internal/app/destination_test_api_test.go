package app

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"reflect"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-protocol/protocol"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

type destinationTestAPIResult struct {
	Verdict         string  `json:"verdict"`
	TerminatingStep *string `json:"terminating_step"`
	Unpublished     bool    `json:"unpublished"`
	Steps           []struct {
		Step, Result string
		PolicyID     int64 `json:"policy_id"`
	} `json:"steps"`
	Notes []string `json:"notes"`
	Nodes []struct {
		PanelID     int64 `json:"panel_id"`
		Name, State string
	} `json:"nodes"`
}

func TestBuildDestinationTestNodeStateDoesNotTreatNewPublicationAsApplied(t *testing.T) {
	f := buildDestinationPolicyFixture(t)
	p := saveWiringPolicy(t, f)
	token := destinationRefreshAdminToken(t, f.a)
	f.report.Capabilities = []string{protocol.CapabilityDestinationPolicy}
	first := syncNativeCacheFixture(t, f.a, f.credential, f.report)
	if first.Config.Body == nil || first.Config.Body.Policy == nil {
		t.Fatal("native publication fixture missing")
	}
	if err := f.a.destCompiler.ObserveStatus(t.Context(), f.agent.AgentID, &protocol.PolicyStatus{State: "applied", Digest: protocol.PolicyDigest(first.Config.Body.Policy)}, f.report.Capabilities); err != nil {
		t.Fatal(err)
	}
	check := func(state, verdict string) {
		t.Helper()
		w := destinationListRequest(t, f.a, token, "POST", "test", map[string]any{"target": "example.test", "user_id": f.user.ID, "panel_id": f.agent.PanelID})
		var view destinationTestAPIResult
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &view) != nil || len(view.Nodes) != 1 || view.Nodes[0].State != state || view.Verdict != verdict {
			t.Fatalf("simulation incorrectly labeled a node: expected=%s/%s HTTP=%d", state, verdict, w.Code)
		}
	}
	check("applied", "block")
	p.Inline.Ports = "80"
	if err := f.a.destDefinitions.SavePolicy(t.Context(), p, p.UpdatedAt, time.Now()); err != nil {
		t.Fatal(err)
	}
	if w := destinationListRequest(t, f.a, token, "POST", "publish", nil); w.Code != 200 {
		t.Fatal("new publication fixture failed")
	}
	check("pending", "direct")
}

func TestBuildDestinationTestUsesOnlyPublishedDefinitionsAndDoesNotMint(t *testing.T) {
	f := buildDestinationPolicyFixture(t)
	a := f.a
	token := destinationRefreshAdminToken(t, a)
	p := domain.DestPolicy{Name: "Published test block", Action: domain.DestBlock, Scope: domain.DestScopeAll, Enabled: true, Inline: domain.DestInline{Ports: "443"}}
	if err := a.destDefinitions.SavePolicy(t.Context(), &p, time.Time{}, time.Now().Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	request := func(port int, verdict string, unpublished bool) {
		t.Helper()
		before, _ := a.destDefinitions.State(t.Context())
		var streamBefore, streamAfter int64
		if err := a.database.QueryRowContext(t.Context(), "SELECT COALESCE(SUM(desired_version), 0) FROM node_agent_streams").Scan(&streamBefore); err != nil {
			t.Fatal(err)
		}
		runtimeBefore, err := a.repos.DestAgentPolicy.Get(t.Context(), f.agent.AgentID, false)
		if err != nil {
			t.Fatal(err)
		}
		w := destinationListRequest(t, a, token, "POST", "test", map[string]any{"target": "unresolvable.example.invalid", "port": port})
		var view destinationTestAPIResult
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &view) != nil || view.Verdict != verdict || view.Unpublished != unpublished || view.Steps == nil || view.Notes == nil || view.Nodes == nil {
			t.Fatalf("destination test did not use the selected publication: HTTP=%d expected=%s unpublished=%t", w.Code, verdict, unpublished)
		}
		after, err := a.destDefinitions.State(t.Context())
		runtimeAfter, runtimeErr := a.repos.DestAgentPolicy.Get(t.Context(), f.agent.AgentID, false)
		if err := a.database.QueryRowContext(t.Context(), "SELECT COALESCE(SUM(desired_version), 0) FROM node_agent_streams").Scan(&streamAfter); err != nil {
			t.Fatal(err)
		}
		if err != nil || runtimeErr != nil || streamBefore != streamAfter || !reflect.DeepEqual(before, after) || !reflect.DeepEqual(runtimeBefore, runtimeAfter) {
			t.Fatal("readonly destination test published definitions or minted runtime state")
		}
	}
	request(443, "direct", true)
	if w := destinationListRequest(t, a, token, "POST", "publish", nil); w.Code != 200 {
		t.Fatal("publication fixture failed")
	}
	request(443, "block", false)
	p.Inline.Ports = "80"
	if err := a.destDefinitions.SavePolicy(t.Context(), &p, p.UpdatedAt, time.Now()); err != nil {
		t.Fatal(err)
	}
	request(443, "block", true)
	request(80, "direct", true)
}

func TestBuildDestinationTestNeverResolvesDomainsAndRetainsWriteAudit(t *testing.T) {
	f := buildDestinationPolicyFixture(t)
	token := destinationRefreshAdminToken(t, f.a)
	p := domain.DestPolicy{Name: "Literal private addresses", Action: domain.DestBlock, Scope: domain.DestScopeAll, Enabled: true, Inline: domain.DestInline{Private: true}}
	if err := f.a.destDefinitions.SavePolicy(t.Context(), &p, time.Time{}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if w := destinationListRequest(t, f.a, token, "POST", "publish", nil); w.Code != 200 {
		t.Fatal("publication fixture failed")
	}
	var queries atomic.Int64
	previous := net.DefaultResolver
	net.DefaultResolver = &net.Resolver{PreferGo: true, Dial: func(context.Context, string, string) (net.Conn, error) {
		queries.Add(1)
		return nil, errors.New("simulation must not resolve")
	}}
	t.Cleanup(func() { net.DefaultResolver = previous })
	for _, test := range []struct{ target, verdict, note string }{
		{"http://169.254.169.254.nip.io/path?ignored=1", "direct", "url_host_only"},
		{"169.254.169.254", "block", "ip_domain_rules_not_matched"},
		{"bücher.example.test", "direct", "domain_not_resolved"},
	} {
		w := destinationListRequest(t, f.a, token, "POST", "test", map[string]any{"target": test.target})
		var view destinationTestAPIResult
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &view) != nil || view.Verdict != test.verdict || !slices.Contains(view.Notes, test.note) {
			t.Fatal("destination test resolved a domain, matched derived IPs or lost normalization notes")
		}
	}
	if queries.Load() != 0 {
		t.Fatal("destination test attempted DNS")
	}
	rows, _, err := f.a.repos.Audit.List(t.Context(), ports.AuditFilter{})
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, row := range rows {
		if strings.Contains(row.Target, "/api/admin/dest/test") {
			count++
		}
	}
	if count != 3 {
		t.Fatal("simulation route lost its required write-audit capture")
	}
}

func TestBuildDestinationTestInputMissingOwnersAndCorruptSnapshotAreSafe(t *testing.T) {
	f := buildDestinationPolicyFixture(t)
	token := destinationRefreshAdminToken(t, f.a)
	for _, input := range []map[string]any{
		{"target": ""}, {"target": "http://user:secret@example.test"}, {"target": "bad host"},
		{"target": "ftp://example.test/file"}, {"target": "fe80::1%eth0"},
		{"target": "example.test", "port": 0}, {"target": "example.test", "port": 65536},
		{"target": "example.test", "network": "icmp"}, {"target": "example.test", "user_id": 0},
		{"target": "example.test", "panel_id": -1}, {"target": "example.test", "forged": true},
	} {
		if w := destinationListRequest(t, f.a, token, "POST", "test", input); w.Code != 400 {
			t.Fatalf("invalid simulation input HTTP=%d", w.Code)
		}
	}
	for _, field := range []string{"user_id", "panel_id"} {
		w := destinationListRequest(t, f.a, token, "POST", "test", map[string]any{"target": "example.test", field: int64(9223372036854775807)})
		if w.Code != 404 {
			t.Fatal("simulation accepted a missing owner")
		}
	}
	saveWiringPolicy(t, f)
	if w := destinationListRequest(t, f.a, token, "POST", "publish", nil); w.Code != 200 {
		t.Fatal("publication fixture failed")
	}
	if _, err := f.a.database.ExecContext(t.Context(), "UPDATE dest_policy_snapshots SET body = ?", []byte(`{"schema":9}`)); err != nil {
		t.Fatal(err)
	}
	w := destinationListRequest(t, f.a, token, "POST", "test", map[string]any{"target": "example.test"})
	if w.Code != 503 || strings.Contains(w.Body.String(), "verdict") || strings.Contains(w.Body.String(), "schema") {
		t.Fatal("corrupt publication became a simulation verdict or leaked storage content")
	}
}

func TestBuildDestinationTestSelectsNativeClientAndCurrentMembership(t *testing.T) {
	f := buildDestinationPolicyFixture(t)
	p := saveWiringPolicy(t, f)
	token := destinationRefreshAdminToken(t, f.a)
	if w := destinationListRequest(t, f.a, token, "POST", "publish", nil); w.Code != 200 {
		t.Fatal("publication fixture failed")
	}
	for _, input := range []map[string]any{
		{"target": "example.test", "port": 443, "user_id": f.user.ID},
		{"target": "example.test", "port": 443, "user_id": f.user.ID, "panel_id": f.agent.PanelID},
	} {
		w := destinationListRequest(t, f.a, token, "POST", "test", input)
		var view destinationTestAPIResult
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &view) != nil || view.Verdict != "block" || view.TerminatingStep == nil || *view.TerminatingStep != "block" || len(view.Nodes) != 1 || view.Nodes[0].PanelID != f.agent.PanelID {
			t.Fatal("destination test omitted current native-client scope")
		}
		found := false
		for _, step := range view.Steps {
			found = found || step.PolicyID == p.ID && step.Result == "hit"
		}
		if !found {
			t.Fatal("destination test lost matching policy identity")
		}
	}
	if _, err := f.a.database.ExecContext(t.Context(), "UPDATE users SET group_id = 0 WHERE id = ?", f.user.ID); err != nil {
		t.Fatal(err)
	}
	w := destinationListRequest(t, f.a, token, "POST", "test", map[string]any{"target": "example.test", "user_id": f.user.ID, "panel_id": f.agent.PanelID})
	var view destinationTestAPIResult
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &view) != nil || view.Verdict != "direct" {
		t.Fatal("destination test reused stale group membership")
	}
}
