package nodesync

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	nodeprotocol "github.com/KazuhaHub/passwall-protocol/protocol"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/destpolicy"
)

// The destination-policy hook has two steps and their order is the point:
// ObserveStatus (what the node says it runs) BEFORE the config is built, so a
// refusal changes this very round's answer; Compile AFTER it, once the config
// the node is about to get exists.

type fakePolicies struct {
	calls     []string
	observed  *nodeprotocol.PolicyStatus
	observedC []string
	input     destpolicy.CompileInput
	result    destpolicy.Result
	observeIs []domain.NodeAgentIssue
	err       error
}

func (f *fakePolicies) ObserveStatus(_ context.Context, _ string, caps []string, status *nodeprotocol.PolicyStatus) ([]domain.NodeAgentIssue, error) {
	f.calls = append(f.calls, "observe")
	f.observed, f.observedC = status, caps
	return f.observeIs, nil
}

func (f *fakePolicies) Compile(_ context.Context, in destpolicy.CompileInput) (destpolicy.Result, error) {
	f.calls = append(f.calls, "compile")
	f.input = in
	return f.result, f.err
}

func (f *configAppliedFixture) syncWith(t *testing.T, mutate func(*nodeprotocol.NodeReport)) (nodeprotocol.SyncResponse, error) {
	t.Helper()
	report := nodeprotocol.NodeReport{
		AgentID: f.agent.AgentID, ProtocolVersion: nodeprotocol.ProtocolVersion1,
		ReportedAtMS: f.now.UnixMilli(), Have: emptyProtocolHave(),
	}
	if mutate != nil {
		mutate(&report)
	}
	return f.service.Sync(t.Context(), report)
}

func (f *configAppliedFixture) configBody(t *testing.T) []byte {
	t.Helper()
	stream, err := f.repos.NodeAgent.GetStream(t.Context(), f.agent.AgentID, domain.NodeAgentStreamConfig)
	if err != nil {
		t.Fatal(err)
	}
	return stream.DesiredBody
}

func TestPolicyHook_ObservesBeforeBuildingAndCompilesAfter(t *testing.T) {
	f := newConfigAppliedFixture(t)
	fake := &fakePolicies{}
	f.service.SetPolicies(fake)
	status := &nodeprotocol.PolicyStatus{Digest: strings.Repeat("a", 64), State: nodeprotocol.PolicyStateApplied}
	response, err := f.syncWith(t, func(r *nodeprotocol.NodeReport) {
		r.PolicyStatus = status
		r.Capabilities = []string{nodeprotocol.CapabilityDestinationPolicy}
		r.CoreEngine = "xray"
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(fake.calls, ",") != "observe,compile" {
		t.Fatalf("calls = %v", fake.calls)
	}
	if fake.observed != status || len(fake.observedC) != 1 {
		t.Fatalf("observed %+v with %v", fake.observed, fake.observedC)
	}
	in := fake.input
	if in.AgentID != f.agent.AgentID || in.PanelID != f.agent.PanelID || in.Snapshot == nil || len(in.Snapshot.Nodes) != 1 ||
		len(in.Capabilities) != 1 || in.Config.Core.Engine != "xray" || len(in.Config.Listeners) != 1 {
		t.Fatalf("compile input = %+v", in)
	}
	if response.Config.Body == nil || response.Config.Body.Policy != nil {
		t.Fatalf("config = %+v", response.Config.Body)
	}
}

// With no policy the config stream is byte-for-byte what it was before the
// feature: a panel upgrade restarts no core.
func TestPolicyHook_NoPolicyLeavesTheConfigBytesUntouched(t *testing.T) {
	without := newConfigAppliedFixture(t)
	if _, err := without.syncWith(t, nil); err != nil {
		t.Fatal(err)
	}
	golden := without.configBody(t)
	if strings.Contains(string(golden), `"policy"`) {
		t.Fatalf("the baseline carries a policy key: %s", golden)
	}

	with := newConfigAppliedFixture(t)
	with.service.SetPolicies(&fakePolicies{})
	if _, err := with.syncWith(t, func(r *nodeprotocol.NodeReport) { r.Capabilities = []string{nodeprotocol.CapabilityDestinationPolicy} }); err != nil {
		t.Fatal(err)
	}
	if got := with.configBody(t); string(got) != string(golden) {
		t.Fatalf("a nil policy changed the minted config:\n%s\nwant\n%s", got, golden)
	}
}

func TestPolicyHook_ThePolicyIsMintedAndStable(t *testing.T) {
	f := newConfigAppliedFixture(t)
	policy := &nodeprotocol.DestinationPolicy{Rules: []nodeprotocol.DestinationRule{{ID: "p1", Action: nodeprotocol.RuleBlock, Ports: "25"}}}
	fake := &fakePolicies{result: destpolicy.Result{Policy: policy, Digest: nodeprotocol.PolicyDigest(policy)}}
	f.service.SetPolicies(fake)
	caps := func(r *nodeprotocol.NodeReport) { r.Capabilities = []string{nodeprotocol.CapabilityDestinationPolicy} }

	first, err := f.syncWith(t, caps)
	if err != nil {
		t.Fatal(err)
	}
	if first.Config.Body == nil || first.Config.Body.Policy == nil || first.Config.Body.Policy.Rules[0].ID != "p1" {
		t.Fatalf("policy not in the config: %+v", first.Config.Body)
	}
	var stored nodeprotocol.ConfigBody
	if err := json.Unmarshal(f.configBody(t), &stored); err != nil || stored.Policy == nil {
		t.Fatalf("stored body: %v %+v", err, stored)
	}
	// The same input again: same ETag, same version (no restart).
	second, err := f.syncWith(t, caps)
	if err != nil {
		t.Fatal(err)
	}
	if second.Config.ETag != first.Config.ETag || second.Config.Version != first.Config.Version {
		t.Fatal("an unchanged policy moved the config identity")
	}
	// A different policy moves it.
	other := &nodeprotocol.DestinationPolicy{Rules: []nodeprotocol.DestinationRule{{ID: "p1", Action: nodeprotocol.RuleBlock, Ports: "26"}}}
	fake.result = destpolicy.Result{Policy: other, Digest: nodeprotocol.PolicyDigest(other)}
	third, err := f.syncWith(t, caps)
	if err != nil || third.Config.ETag == first.Config.ETag {
		t.Fatalf("a changed policy kept the ETag (%v)", err)
	}
}

func TestPolicyHook_PSPSideIssuesAreRecorded(t *testing.T) {
	f := newConfigAppliedFixture(t)
	fake := &fakePolicies{
		observeIs: []domain.NodeAgentIssue{{Code: destpolicy.IssueLKGRejected, Detail: "digest abc"}},
		result: destpolicy.Result{Issues: []domain.NodeAgentIssue{
			{Code: destpolicy.IssueRejected, Detail: "digest def"},
			{Code: destpolicy.IssueOverLimit, Detail: "subjects 51000/50000"},
		}},
	}
	f.service.SetPolicies(fake)
	if _, err := f.syncWith(t, func(r *nodeprotocol.NodeReport) {
		r.Capabilities = []string{nodeprotocol.CapabilityDestinationPolicy}
		r.PolicyStatus = &nodeprotocol.PolicyStatus{State: nodeprotocol.PolicyStateApplied}
	}); err != nil {
		t.Fatal(err)
	}
	items, _, err := f.repos.NodeAgentIssue.List(t.Context(), ports.NodeAgentIssueFilter{AgentID: f.agent.AgentID})
	if err != nil {
		t.Fatal(err)
	}
	codes := map[string]bool{}
	for _, it := range items {
		codes[it.Code] = true
	}
	for _, want := range []string{destpolicy.IssueLKGRejected, destpolicy.IssueRejected, destpolicy.IssueOverLimit} {
		if !codes[want] {
			t.Fatalf("issue %s was not recorded (have %v)", want, codes)
		}
	}
}

func TestPolicyHook_ACompileFailureFailsTheSyncAndMintsNothingNew(t *testing.T) {
	f := newConfigAppliedFixture(t)
	f.service.SetPolicies(&fakePolicies{err: errors.New("boom")})
	if _, err := f.syncWith(t, func(r *nodeprotocol.NodeReport) { r.Capabilities = []string{nodeprotocol.CapabilityDestinationPolicy} }); err == nil {
		t.Fatal("a failing compile must fail the sync: the node keeps what it has")
	}
	stream, err := f.repos.NodeAgent.GetStream(t.Context(), f.agent.AgentID, domain.NodeAgentStreamConfig)
	if err != nil || stream.DesiredVersion != 0 {
		t.Fatalf("config stream = (%+v, %v): nothing may be minted when the policy could not be decided", stream, err)
	}
}

func TestPolicyHook_TheStatusIsNotKeptInTheLatestReportCache(t *testing.T) {
	f := newConfigAppliedFixture(t)
	f.service.SetPolicies(&fakePolicies{})
	if _, err := f.syncWith(t, func(r *nodeprotocol.NodeReport) {
		r.Capabilities = []string{nodeprotocol.CapabilityDestinationPolicy}
		r.PolicyStatus = &nodeprotocol.PolicyStatus{State: nodeprotocol.PolicyStateApplied}
	}); err != nil {
		t.Fatal(err)
	}
	f.service.mu.RLock()
	cached := f.service.reports[f.agent.AgentID].report
	f.service.mu.RUnlock()
	if cached.PolicyStatus != nil {
		t.Fatal("the policy status was retained in the latest-full cache")
	}
}

// Without a compiler (a build without the feature) the sync is exactly what it
// was.
func TestPolicyHook_AbsentCompilerIsInert(t *testing.T) {
	f := newConfigAppliedFixture(t)
	if _, err := f.syncWith(t, func(r *nodeprotocol.NodeReport) {
		r.Capabilities = []string{nodeprotocol.CapabilityDestinationPolicy}
		r.PolicyStatus = &nodeprotocol.PolicyStatus{State: nodeprotocol.PolicyStateApplied}
	}); err != nil {
		t.Fatal(err)
	}
}
