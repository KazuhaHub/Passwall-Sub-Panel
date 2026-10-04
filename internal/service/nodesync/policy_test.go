package nodesync

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/KazuhaHub/passwall-protocol/protocol"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/destpolicy"
)

type testPolicyCoordinator struct {
	observe func(context.Context, string, *protocol.PolicyStatus, []string) error
	compile func(context.Context, *domain.NodeAgent, *ports.NativeDesiredSnapshot, []string, protocol.ConfigBody) (PolicyCandidate, error)
}

func (p *testPolicyCoordinator) ObserveStatus(ctx context.Context, agent string, status *protocol.PolicyStatus, caps []string) error {
	return p.observe(ctx, agent, status, caps)
}
func (p *testPolicyCoordinator) Compile(ctx context.Context, agent *domain.NodeAgent, snapshot *ports.NativeDesiredSnapshot, caps []string, base protocol.ConfigBody) (PolicyCandidate, error) {
	return p.compile(ctx, agent, snapshot, caps, base)
}
func policySyncReport(f *configAppliedFixture) protocol.NodeReport {
	return protocol.NodeReport{AgentID: f.agent.AgentID, ProtocolVersion: protocol.ProtocolVersion1, ReportedAtMS: f.now.UnixMilli(), Have: emptyProtocolHave(), Capabilities: []string{protocol.CapabilityDestinationPolicy}}
}
func policySyncCandidate() PolicyCandidate {
	p := &protocol.DestinationPolicy{Rules: []protocol.DestinationRule{{ID: "p1", Action: protocol.RuleBlock, Ports: "443"}}}
	return PolicyCandidate{Policy: p, Mint: domain.DestPolicyMint{Kind: domain.DestCandidateDesired, Generation: 1, Context: strings.Repeat("b", 64), DesiredSHA256: protocol.PolicyDigest(p)}}
}

func TestPolicySyncObservesBeforeCompilationAndMintsExactCandidate(t *testing.T) {
	f := newConfigAppliedFixture(t)
	f.service.policyCandidates = f.repos.NodeAgent.(ports.NodePolicyCandidateRepo)
	observer := destpolicy.NewObserver(f.repos.DestAgentPolicy, f.service.now)
	candidate := policySyncCandidate()
	observed, compiled := 0, 0
	f.service.policies = &testPolicyCoordinator{
		observe: func(ctx context.Context, id string, status *protocol.PolicyStatus, caps []string) error {
			observed++
			return observer.ObserveStatus(ctx, id, status, caps)
		},
		compile: func(ctx context.Context, agent *domain.NodeAgent, snapshot *ports.NativeDesiredSnapshot, caps []string, base protocol.ConfigBody) (PolicyCandidate, error) {
			compiled++
			if observed != compiled || agent.AgentID != f.agent.AgentID || len(snapshot.Nodes) != 1 || len(base.Listeners) != 1 || !slices.Contains(caps, protocol.CapabilityDestinationPolicy) {
				t.Fatal("compile ran before observation or received incomplete input")
			}
			if compiled == 2 {
				state, err := f.repos.DestAgentPolicy.Get(ctx, agent.AgentID, false)
				if err != nil || state.FallbackReason != "rejected" || state.RejectedGeneration != 1 {
					t.Fatalf("same-round rejection not visible: %+v / %v", state, err)
				}
				return PolicyCandidate{Mint: domain.DestPolicyMint{Kind: domain.DestCandidateEmpty, Generation: 1, Context: strings.Repeat("b", 64), DesiredSHA256: candidate.Mint.DesiredSHA256}}, nil
			}
			return candidate, nil
		},
	}
	report := policySyncReport(f)
	first, err := f.service.Sync(t.Context(), report)
	if err != nil || first.Config.Body == nil || first.Config.Body.Policy == nil {
		t.Fatalf("policy not minted: %+v / %v", first.Config, err)
	}
	state, err := f.repos.DestAgentPolicy.Get(t.Context(), f.agent.AgentID, true)
	canonical, _ := json.Marshal(candidate.Policy)
	if err != nil || state.MintedSHA256 != candidate.Mint.DesiredSHA256 || string(state.MintedBody) != string(canonical) {
		t.Fatalf("wire candidate differs from stored source: %+v / %v", state, err)
	}
	report.PolicyStatus = &protocol.PolicyStatus{State: "rejected", Digest: state.MintedSHA256, IssueCode: protocol.IssueDestinationPolicyRejected}
	second, err := f.service.Sync(t.Context(), report)
	if err != nil || second.Config.Body == nil || second.Config.Body.Policy != nil || second.Config.Version.Version <= first.Config.Version.Version {
		t.Fatalf("rejection did not affect this response: %+v / %v", second.Config, err)
	}
	if cached, _, ok := f.service.fullReport(f.agent.AgentID); !ok || cached.PolicyStatus != nil {
		t.Fatal("one-shot policy status retained in full report cache")
	}
}

func TestPolicySyncFailuresDoNotMintConfig(t *testing.T) {
	for _, stage := range []string{"observe", "compile", "mint"} {
		t.Run(stage, func(t *testing.T) {
			f := newConfigAppliedFixture(t)
			failure := errors.New("policy failure")
			candidate := policySyncCandidate()
			f.service.policyCandidates = f.repos.NodeAgent.(ports.NodePolicyCandidateRepo)
			f.service.policies = &testPolicyCoordinator{
				observe: func(context.Context, string, *protocol.PolicyStatus, []string) error {
					if stage == "observe" {
						return failure
					}
					return nil
				},
				compile: func(context.Context, *domain.NodeAgent, *ports.NativeDesiredSnapshot, []string, protocol.ConfigBody) (PolicyCandidate, error) {
					if stage == "compile" {
						return PolicyCandidate{}, failure
					}
					if stage == "mint" {
						candidate.Mint.DesiredSHA256 = strings.Repeat("c", 64)
					}
					return candidate, nil
				},
			}
			_, err := f.service.Sync(t.Context(), policySyncReport(f))
			if err == nil || stage != "mint" && !errors.Is(err, failure) {
				t.Fatalf("failure ignored: %v", err)
			}
			stream, err := f.repos.NodeAgent.GetStream(t.Context(), f.agent.AgentID, domain.NodeAgentStreamConfig)
			if err != nil || stream.DesiredVersion != 0 {
				t.Fatalf("failed policy minted config: %+v / %v", stream, err)
			}
		})
	}
}

func TestPolicySyncPartialReportStillObserves(t *testing.T) {
	f := newConfigAppliedFixture(t)
	f.service.policyCandidates = f.repos.NodeAgent.(ports.NodePolicyCandidateRepo)
	observed := false
	f.service.policies = &testPolicyCoordinator{
		observe: func(context.Context, string, *protocol.PolicyStatus, []string) error { observed = true; return nil },
		compile: func(context.Context, *domain.NodeAgent, *ports.NativeDesiredSnapshot, []string, protocol.ConfigBody) (PolicyCandidate, error) {
			return policySyncCandidate(), nil
		},
	}
	report := policySyncReport(f)
	report.Partial = true
	if _, err := f.service.Sync(t.Context(), report); err != nil || !observed {
		t.Fatalf("partial report lost policy status: %v observed=%v", err, observed)
	}
}

func TestPolicySyncConstructionRequiresAtomicMintPort(t *testing.T) {
	f := newConfigAppliedFixture(t)
	options := Options{Desired: f.service.desired, Agents: f.service.agents, Issues: f.service.issues, Tasks: f.service.tasks, Users: f.service.users, Clients: f.service.clients, Nodes: f.service.nodes, Settings: f.service.settings, CoreCatalog: f.service.coreCatalog}
	options.Policies = &testPolicyCoordinator{}
	if _, err := New(options); err == nil {
		t.Fatal("policy compiler accepted without atomic candidate mint port")
	}
	options.Policies = nil
	options.PolicyCandidates = f.repos.NodeAgent.(ports.NodePolicyCandidateRepo)
	if _, err := New(options); err == nil {
		t.Fatal("candidate mint port accepted without policy coordinator")
	}
}

func TestPolicySyncRejectsCompilerPolicyWithoutCapability(t *testing.T) {
	f := newConfigAppliedFixture(t)
	f.service.policyCandidates = f.repos.NodeAgent.(ports.NodePolicyCandidateRepo)
	f.service.policies = &testPolicyCoordinator{
		observe: func(context.Context, string, *protocol.PolicyStatus, []string) error { return nil },
		compile: func(context.Context, *domain.NodeAgent, *ports.NativeDesiredSnapshot, []string, protocol.ConfigBody) (PolicyCandidate, error) {
			return policySyncCandidate(), nil
		},
	}
	report := policySyncReport(f)
	report.Capabilities = nil
	if _, err := f.service.Sync(t.Context(), report); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("unsupported policy emitted: %v", err)
	}
	stream, err := f.repos.NodeAgent.GetStream(t.Context(), f.agent.AgentID, domain.NodeAgentStreamConfig)
	if err != nil || stream.DesiredVersion != 0 {
		t.Fatalf("unsupported policy persisted: %+v / %v", stream, err)
	}
}
