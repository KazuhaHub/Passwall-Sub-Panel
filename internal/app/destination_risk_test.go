package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-protocol/protocol"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

type destinationRiskProbe struct {
	read func(context.Context, time.Time, time.Time) (domain.DestRiskWindow, error)
}

func (p destinationRiskProbe) ReadDestinationRiskWindow(ctx context.Context, since, until time.Time) (domain.DestRiskWindow, error) {
	return p.read(ctx, since, until)
}

func TestBuildDestinationRiskReaderUsesBulkWindowAndCurrentAppliedCollectionProof(t *testing.T) {
	f := buildDestinationPolicyFixture(t)
	if f.a.destRiskRead == nil {
		t.Fatal("production risk reader not wired")
	}
	initial, err := f.a.destinationRiskInputs(t.Context(), time.Now().UTC())
	if err != nil || initial[f.user.ID].CollectingNodes != 0 {
		t.Fatal("unreported node became a collector")
	}
	policy := saveWiringPolicy(t, f)
	f.report.Capabilities = []string{protocol.CapabilityDestinationPolicy, protocol.CapabilityAuditHits}
	f.report.CoreEngine = "xray"
	first := syncNativeCacheFixture(t, f.a, f.credential, f.report)
	if first.Config.Body == nil || first.Config.Body.Policy == nil {
		t.Fatal("missing minted policy")
	}
	clients := []int64{f.agent.PanelID, f.agent.PanelID}
	reads := 0
	f.a.destRiskRead = destinationRiskProbe{read: func(_ context.Context, since, until time.Time) (domain.DestRiskWindow, error) {
		reads++
		if until.Sub(since) != 24*time.Hour {
			t.Error("risk window is not fixed24h")
		}
		return domain.DestRiskWindow{Users: map[int64]domain.DestRiskUserWindow{f.user.ID: {ClientPanelIDs: clients, Sources: []domain.DestBlockSource{{Source: "p12", Count: 37}}, Losses: domain.DestAuditLosses{Rows: 2, Events: 3, Unmatched: 4, Scope: "panel"}}, f.user.ID + 100: {ClientPanelIDs: []int64{f.agent.PanelID + 100}, Sources: []domain.DestBlockSource{{Source: "p12", Count: 7}}}}}, nil
	}}
	check := func(at time.Time, want int) {
		t.Helper()
		before := reads
		got, err := f.a.destinationRiskInputs(t.Context(), at)
		row := got[f.user.ID]
		if err != nil || reads != before+1 || row.CollectingNodes != want || !reflect.DeepEqual(row.Sources, []domain.DestBlockSource{{Source: "p12", Count: 37}}) || row.Losses.Rows != 2 || row.Losses.Events != 3 || row.Losses.Unmatched != 4 || got[f.user.ID+100].CollectingNodes != 0 {
			t.Fatalf("bulk collector proof count=%d wanted=%d error=%v", row.CollectingNodes, want, err)
		}
	}
	now := time.Now().UTC()
	check(now, 0)
	if err := f.a.destCompiler.ObserveStatus(t.Context(), f.agent.AgentID, &protocol.PolicyStatus{State: "applied", Digest: protocol.PolicyDigest(first.Config.Body.Policy)}, f.report.Capabilities); err != nil {
		t.Fatal(err)
	}
	check(now, 1) // Duplicate client rows never multiply one physical node.
	if err := f.a.operationGate.RunRead(t.Context(), f.a.risk.RefreshOnce); err != nil {
		t.Fatal(err)
	}
	var state, code string
	var raw []byte
	if err := f.a.database.QueryRowContext(t.Context(), "SELECT state, code, evidence FROM risk_signals WHERE user_id = ? AND kind = ?", f.user.ID, "dest_block").Scan(&state, &code, &raw); err != nil {
		t.Fatal("assembled worker did not persist destination verdict", err)
	}
	var evidence domain.DestBlockEvidence
	if err := json.Unmarshal(raw, &evidence); err != nil || state != "flagged" || code != "over" || evidence.Total != 37 || evidence.Threshold != 20 || evidence.Nodes != 1 || evidence.CoverageComplete {
		t.Fatalf("assembled worker changed its lower-bound evidence: state=%s code=%s evidence=%+v err=%v", state, code, evidence, err)
	}
	check(now.Add(4*time.Hour), 0)
	clients = nil
	check(now, 0) // Retained historical counts are not membership.
	clients = []int64{f.agent.PanelID}
	if err := f.a.repos.NodeAgent.UpdateCoreObservation(t.Context(), f.agent.AgentID, domain.NodeCoreSingBox); err != nil {
		t.Fatal(err)
	}
	check(now, 0)
	if err := f.a.repos.NodeAgent.UpdateCoreObservation(t.Context(), f.agent.AgentID, domain.NodeCoreXray); err != nil {
		t.Fatal(err)
	}
	if err := f.a.repos.NodeAgent.UpdateProtocolObservation(t.Context(), f.agent.AgentID, protocol.ProtocolVersion1, []string{protocol.CapabilityDestinationPolicy}, now); err != nil {
		t.Fatal(err)
	}
	check(now, 0)
	if err := f.a.repos.NodeAgent.UpdateProtocolObservation(t.Context(), f.agent.AgentID, protocol.ProtocolVersion1, f.report.Capabilities, now); err != nil {
		t.Fatal(err)
	}
	check(now, 1)
	token := destinationRefreshAdminToken(t, f.a)
	for _, mode := range []string{"off", "hits"} {
		if w := serverAuditRequest(t, f.a, token, "PUT", fmt.Sprintf("/%d", f.agent.PanelID), map[string]any{"audit_collect": mode}); w.Code != 200 {
			t.Fatal("collection mode update failed")
		}
		check(now, 0)
	}
	next := syncNativeCacheFixture(t, f.a, f.credential, f.report)
	check(time.Now().UTC(), 0)
	if err := f.a.destCompiler.ObserveStatus(t.Context(), f.agent.AgentID, &protocol.PolicyStatus{State: "applied", Digest: protocol.PolicyDigest(next.Config.Body.Policy)}, f.report.Capabilities); err != nil {
		t.Fatal(err)
	}
	check(time.Now().UTC(), 1)
	// Hits-only without rules omits the policy. The prior LKG and counters
	// must not prove collection after the rule-free config has been minted.
	policy.Enabled = false
	if err := f.a.destDefinitions.SavePolicy(t.Context(), policy, policy.UpdatedAt, time.Now()); err != nil {
		t.Fatal(err)
	}
	if w := destinationListRequest(t, f.a, token, "POST", "publish", nil); w.Code != 200 {
		t.Fatal("empty policy publication failed")
	}
	noHits := syncNativeCacheFixture(t, f.a, f.credential, f.report)
	if noHits.Config.Body == nil || noHits.Config.Body.Policy != nil {
		t.Fatal("hits-only without rules did not omit the policy")
	}
	check(time.Now().UTC(), 0)
	// Usage collection can retain an acknowledged, non-nil policy without
	// executable hit rules. It likewise cannot prove destination-hit coverage.
	f.report.Capabilities = append(f.report.Capabilities, "audit.usage.v1")
	if w := serverAuditRequest(t, f.a, token, "PUT", fmt.Sprintf("/%d", f.agent.PanelID), map[string]any{"audit_collect": "hits_and_usage"}); w.Code != 200 {
		t.Fatal("usage collection fixture failed")
	}
	empty := syncNativeCacheFixture(t, f.a, f.credential, f.report)
	if empty.Config.Body == nil || empty.Config.Body.Policy == nil || len(empty.Config.Body.Policy.Rules) != 0 {
		t.Fatal("missing executable-rule-free candidate")
	}
	if err := f.a.destCompiler.ObserveStatus(t.Context(), f.agent.AgentID, &protocol.PolicyStatus{State: "applied", Digest: protocol.PolicyDigest(empty.Config.Body.Policy)}, f.report.Capabilities); err != nil {
		t.Fatal(err)
	}
	check(time.Now().UTC(), 0)
}

func TestBuildDestinationRiskReadAdmissionFailureAndCancellation(t *testing.T) {
	f := buildDestinationPolicyFixture(t)
	f.a.destRiskRead = nil
	if got, err := f.a.destinationRiskInputs(t.Context(), time.Now()); !errors.Is(err, domain.ErrUnavailable) || got != nil {
		t.Fatal("missing risk reader became empty success")
	}
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	finish := func() { once.Do(func() { close(release) }) }
	defer finish()
	f.a.destRiskRead = destinationRiskProbe{read: func(ctx context.Context, _, _ time.Time) (domain.DestRiskWindow, error) {
		close(entered)
		select {
		case <-release:
			return domain.DestRiskWindow{Users: map[int64]domain.DestRiskUserWindow{f.user.ID: {Sources: []domain.DestBlockSource{{Source: "private-host.test", Count: 12345}}}}}, errors.New("private driver risk detail")
		case <-ctx.Done():
			return domain.DestRiskWindow{}, ctx.Err()
		}
	}}
	done := make(chan error, 1)
	go func() {
		got, err := f.a.destinationRiskInputs(t.Context(), time.Now())
		if got != nil {
			t.Error("failed read exposed partial risk counts")
		}
		done <- err
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("risk read not called")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	err := f.a.operationGate.Exclusive(ctx, func(context.Context) error { t.Error("backend switch crossed risk read"); return nil })
	cancel()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("risk read released backend admission too early")
	}
	finish()
	if !errors.Is(<-done, domain.ErrUnavailable) {
		t.Fatal("private driver error escaped risk reader")
	}
	if err := f.a.operationGate.Exclusive(t.Context(), func(context.Context) error {
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		got, err := f.a.destinationRiskInputs(ctx, time.Now())
		if !errors.Is(err, context.Canceled) || got != nil {
			t.Error("cancelled risk read crossed backend admission")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
