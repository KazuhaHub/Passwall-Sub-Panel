package app

import (
	"encoding/json"
	"fmt"
	"github.com/KazuhaHub/passwall-protocol/protocol"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/destpolicy"
	"reflect"
	"testing"
	"time"
)

func TestBuildDestinationStatusIsReadOnlyAndReportsPendingPublication(t *testing.T) {
	f := buildDestinationPolicyFixture(t)
	saveWiringPolicy(t, f)
	token := destinationRefreshAdminToken(t, f.a)
	before, err := f.a.destDefinitions.State(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	runtimeBefore, err := f.a.repos.DestAgentPolicy.Get(t.Context(), f.agent.AgentID, false)
	if err != nil {
		t.Fatal(err)
	}
	w := destinationListRequest(t, f.a, token, "GET", "status", nil)
	var view struct {
		Generation          int64  `json:"generation"`
		PublishedGeneration int64  `json:"published_generation"`
		NextPublishAt       *int64 `json:"next_publish_at"`
		ApplyETA            int64  `json:"apply_eta_ms"`
		Nodes               []struct {
			Collecting bool
			Hits24h    *int64 `json:"hits_24h"`
			Losses     any
		} `json:"nodes"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &view) != nil || view.Generation != before.Generation || view.PublishedGeneration != 0 || view.NextPublishAt == nil || view.ApplyETA <= 0 || len(view.Nodes) != 1 || view.Nodes[0].Collecting || view.Nodes[0].Hits24h != nil || view.Nodes[0].Losses != nil {
		t.Fatalf("readonly status must return pending publication and unknown telemetry: HTTP=%d", w.Code)
	}
	after, err := f.a.destDefinitions.State(t.Context())
	runtimeAfter, runtimeErr := f.a.repos.DestAgentPolicy.Get(t.Context(), f.agent.AgentID, false)
	if err != nil || runtimeErr != nil || !reflect.DeepEqual(before, after) || !reflect.DeepEqual(runtimeBefore, runtimeAfter) {
		t.Fatal("status published or minted state")
	}
}

func TestBuildDestinationStatusCollectingWaitsForCurrentRevisionAndConfirmation(t *testing.T) {
	f := buildDestinationPolicyFixture(t)
	saveWiringPolicy(t, f)
	token := destinationRefreshAdminToken(t, f.a)
	f.report.Capabilities = []string{protocol.CapabilityDestinationPolicy, "audit.hits.v1"}
	f.report.CoreEngine = "xray"
	first := syncNativeCacheFixture(t, f.a, f.credential, f.report)
	if first.Config.Body == nil || first.Config.Body.Policy == nil {
		t.Fatal("candidate fixture missing")
	}
	check := func(want bool) {
		t.Helper()
		w := destinationListRequest(t, f.a, token, "GET", "status", nil)
		var view destpolicy.DestinationStatus
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &view) != nil || len(view.Nodes) != 1 || view.Nodes[0].Collecting != want {
			t.Fatalf("collecting should be %t HTTP=%d", want, w.Code)
		}
	}
	check(false)
	if err := f.a.destCompiler.ObserveStatus(t.Context(), f.agent.AgentID, &protocol.PolicyStatus{State: "applied", Digest: protocol.PolicyDigest(first.Config.Body.Policy)}, f.report.Capabilities); err != nil {
		t.Fatal(err)
	}
	check(true)
	check(true)
	for _, mode := range []string{"off", "hits"} {
		if w := serverAuditRequest(t, f.a, token, "PUT", fmt.Sprintf("/%d", f.agent.PanelID), map[string]any{"audit_collect": mode}); w.Code != 200 {
			t.Fatal("collection update failed")
		}
		check(false)
	}
	next := syncNativeCacheFixture(t, f.a, f.credential, f.report)
	check(false)
	if err := f.a.destCompiler.ObserveStatus(t.Context(), f.agent.AgentID, &protocol.PolicyStatus{State: "applied", Digest: protocol.PolicyDigest(next.Config.Body.Policy)}, f.report.Capabilities); err != nil {
		t.Fatal(err)
	}
	check(true)
	// A new publication is pending enforcement, even while the previous
	// exact candidate is still acknowledged and collecting.
	p := &domain.DestPolicy{Name: "new revision", Action: domain.DestBlock, Scope: domain.DestScopeAll, Enabled: true, Inline: domain.DestInline{Ports: "80"}}
	if err := f.a.destDefinitions.SavePolicy(t.Context(), p, time.Time{}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if w := destinationListRequest(t, f.a, token, "POST", "publish", nil); w.Code != 200 {
		t.Fatal("publication failed")
	}
	w := destinationListRequest(t, f.a, token, "GET", "status", nil)
	var view destpolicy.DestinationStatus
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &view) != nil || view.Nodes[0].State != "pending" || !view.Nodes[0].Collecting {
		t.Fatal("new publication was mistaken for confirmed enforcement")
	}
}
