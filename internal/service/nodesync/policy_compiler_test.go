package nodesync

import (
	"context"
	"testing"

	"github.com/KazuhaHub/passwall-protocol/protocol"
	"github.com/KazuhaHub/passwall-sub-panel/internal/adapters/sqlstore"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/destpolicy"
)

type compilerFixtureInputs struct{}

func (compilerFixtureInputs) ForNode(context.Context, int64, *ports.NativeDesiredSnapshot, bool) (destpolicy.RosterInput, destpolicy.RosterInput, error) {
	input := destpolicy.RosterInput{Collect: domain.AuditCollectOff, CollectRevision: 1, UserGroups: map[int64]int64{}}
	return input, input, nil
}

func publishCompilerFixture(t *testing.T, f *configAppliedFixture, definitions *sqlstore.DestDefinitionRepo) {
	t.Helper()
	defs, err := definitions.ReadDefinitions(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	body, err := destpolicy.BuildDefinitionSnapshot(defs)
	if err != nil {
		t.Fatal(err)
	}
	if err := definitions.Publish(t.Context(), defs.State.Generation, defs.State.PublishedGeneration, body, f.now); err != nil {
		t.Fatal(err)
	}
}

func TestProductionPolicyCompilerSyncRejectsThenRecoversNewPublication(t *testing.T) {
	f := newConfigAppliedFixture(t)
	definitions := sqlstore.NewDestDefinitionRepo(f.db)
	p := &domain.DestPolicy{Name: "compiler policy", Enabled: true, Action: domain.DestBlock, Scope: domain.DestScopeAll, Inline: domain.DestInline{Ports: "443"}}
	if err := definitions.SavePolicy(t.Context(), p, p.UpdatedAt, f.now); err != nil {
		t.Fatal(err)
	}
	publishCompilerFixture(t, f, definitions)
	compiler, err := destpolicy.NewCompiler(destpolicy.CompilerOptions{Definitions: definitions, Runtime: f.repos.DestAgentPolicy, Inputs: compilerFixtureInputs{}, Now: f.service.now})
	if err != nil {
		t.Fatal(err)
	}
	f.service.policies, f.service.policyCandidates = compiler, f.repos.NodeAgent.(ports.NodePolicyCandidateRepo)
	report := policySyncReport(f)
	first, err := f.service.Sync(t.Context(), report)
	if err != nil || first.Config.Body == nil || first.Config.Body.Policy == nil {
		t.Fatalf("production compiler failed first mint: %+v / %v", first.Config, err)
	}
	digest := protocol.PolicyDigest(first.Config.Body.Policy)
	report.PolicyStatus = &protocol.PolicyStatus{State: "rejected", Digest: digest, IssueCode: protocol.IssueDestinationPolicyRejected}
	second, err := f.service.Sync(t.Context(), report)
	if err != nil || second.Config.Body == nil || second.Config.Body.Policy != nil {
		t.Fatalf("rejection did not fall back to empty: %+v / %v", second.Config, err)
	}
	state, err := f.repos.DestAgentPolicy.Get(t.Context(), f.agent.AgentID, false)
	if err != nil || state.FallbackReason != "rejected" || state.RejectedGeneration != 1 || state.RejectedContext == "" || state.MintedKind != domain.DestCandidateEmpty {
		t.Fatalf("production rejection state: %+v / %v", state, err)
	}
	p.Inline.Ports = "80"
	if err := definitions.SavePolicy(t.Context(), p, p.UpdatedAt, f.now); err != nil {
		t.Fatal(err)
	}
	publishCompilerFixture(t, f, definitions)
	third, err := f.service.Sync(t.Context(), report)
	if err != nil || third.Config.Body == nil || third.Config.Body.Policy == nil || third.Config.Body.Policy.Rules[0].Ports != "80" {
		t.Fatalf("new publication did not retry: %+v / %v", third.Config, err)
	}
	report.PolicyStatus = &protocol.PolicyStatus{State: "applied", Digest: protocol.PolicyDigest(third.Config.Body.Policy)}
	if _, err := f.service.Sync(t.Context(), report); err != nil {
		t.Fatal(err)
	}
	state, err = f.repos.DestAgentPolicy.Get(t.Context(), f.agent.AgentID, true)
	if err != nil || state.FallbackReason != "" || state.RejectedGeneration != 0 || state.RejectedContext != "" || state.AppliedSHA256 != report.PolicyStatus.Digest || len(state.AppliedBody) == 0 {
		t.Fatalf("desired success failed to recover: %+v / %v", state, err)
	}
}

func TestProductionPolicyCompilerPausePublishesImmediatelyAndPreservesLKG(t *testing.T) {
	f := newConfigAppliedFixture(t)
	definitions := sqlstore.NewDestDefinitionRepo(f.db)
	p := &domain.DestPolicy{Name: "pause compiler", Enabled: true, Action: domain.DestBlock, Scope: domain.DestScopeAll, Inline: domain.DestInline{Ports: "443"}}
	if err := definitions.SavePolicy(t.Context(), p, p.UpdatedAt, f.now); err != nil {
		t.Fatal(err)
	}
	publishCompilerFixture(t, f, definitions)
	compiler, err := destpolicy.NewCompiler(destpolicy.CompilerOptions{Definitions: definitions, Runtime: f.repos.DestAgentPolicy, Inputs: compilerFixtureInputs{}, Now: f.service.now})
	if err != nil {
		t.Fatal(err)
	}
	f.service.policies, f.service.policyCandidates = compiler, f.repos.NodeAgent.(ports.NodePolicyCandidateRepo)
	report := policySyncReport(f)
	first, err := f.service.Sync(t.Context(), report)
	if err != nil {
		t.Fatal(err)
	}
	report.PolicyStatus = &protocol.PolicyStatus{State: "applied", Digest: protocol.PolicyDigest(first.Config.Body.Policy)}
	if _, err := f.service.Sync(t.Context(), report); err != nil {
		t.Fatal(err)
	}
	before, err := f.repos.DestAgentPolicy.Get(t.Context(), f.agent.AgentID, true)
	if err != nil || before.AppliedSHA256 == "" {
		t.Fatalf("no confirmed LKG: %+v / %v", before, err)
	}
	if err := definitions.SetPaused(t.Context(), true, f.now); err != nil {
		t.Fatal(err)
	}
	paused, err := f.service.Sync(t.Context(), report)
	if err != nil || paused.Config.Body == nil || paused.Config.Body.Policy != nil {
		t.Fatalf("pause was debounced: %+v / %v", paused.Config, err)
	}
	state, err := definitions.State(t.Context())
	if err != nil || state.Generation != state.PublishedGeneration {
		t.Fatalf("pause did not publish immediately: %+v / %v", state, err)
	}
	// Success of the empty paused candidate is never confirmation of a new LKG.
	report.PolicyStatus = &protocol.PolicyStatus{State: "applied", Digest: ""}
	if _, err := f.service.Sync(t.Context(), report); err != nil {
		t.Fatal(err)
	}
	after, err := f.repos.DestAgentPolicy.Get(t.Context(), f.agent.AgentID, true)
	if err != nil || after.AppliedSHA256 != before.AppliedSHA256 || string(after.AppliedBody) != string(before.AppliedBody) || after.MintedKind != domain.DestCandidatePaused {
		t.Fatalf("pause replaced LKG: %+v / %v", after, err)
	}
}

func TestProductionPolicyCompilerPrunedFallbackRejectionExhaustsDurably(t *testing.T) {
	f := newConfigAppliedFixture(t)
	definitions := sqlstore.NewDestDefinitionRepo(f.db)
	p := &domain.DestPolicy{Name: "fallback compiler", Enabled: true, Action: domain.DestBlock, Scope: domain.DestScopeAll, Inline: domain.DestInline{Ports: "80"}}
	if err := definitions.SavePolicy(t.Context(), p, p.UpdatedAt, f.now); err != nil {
		t.Fatal(err)
	}
	publishCompilerFixture(t, f, definitions)
	compiler, err := destpolicy.NewCompiler(destpolicy.CompilerOptions{Definitions: definitions, Runtime: f.repos.DestAgentPolicy, Inputs: compilerFixtureInputs{}, Now: f.service.now})
	if err != nil {
		t.Fatal(err)
	}
	f.service.policies, f.service.policyCandidates = compiler, f.repos.NodeAgent.(ports.NodePolicyCandidateRepo)
	report := policySyncReport(f)
	first, err := f.service.Sync(t.Context(), report)
	if err != nil {
		t.Fatal(err)
	}
	report.PolicyStatus = &protocol.PolicyStatus{State: "applied", Digest: protocol.PolicyDigest(first.Config.Body.Policy)}
	if _, err := f.service.Sync(t.Context(), report); err != nil {
		t.Fatal(err)
	}
	p.Inline.Ports = "443"
	if err := definitions.SavePolicy(t.Context(), p, p.UpdatedAt, f.now); err != nil {
		t.Fatal(err)
	}
	publishCompilerFixture(t, f, definitions)
	desired, err := f.service.Sync(t.Context(), report)
	if err != nil || desired.Config.Body == nil || desired.Config.Body.Policy == nil {
		t.Fatalf("new desired candidate missing: %+v / %v", desired.Config, err)
	}
	report.PolicyStatus = &protocol.PolicyStatus{State: "rejected", Digest: protocol.PolicyDigest(desired.Config.Body.Policy), IssueCode: protocol.IssueDestinationPolicyRejected}
	fallback, err := f.service.Sync(t.Context(), report)
	if err != nil || fallback.Config.Body == nil || fallback.Config.Body.Policy == nil || fallback.Config.Body.Policy.Rules[0].Ports != "80" {
		t.Fatalf("exact LKG not selected: %+v / %v", fallback.Config, err)
	}
	report.PolicyStatus.Digest = protocol.PolicyDigest(fallback.Config.Body.Policy)
	empty, err := f.service.Sync(t.Context(), report)
	if err != nil || empty.Config.Body == nil || empty.Config.Body.Policy != nil {
		t.Fatalf("rejected fallback still dispatched: %+v / %v", empty.Config, err)
	}
	state, err := f.repos.DestAgentPolicy.Get(t.Context(), f.agent.AgentID, true)
	if err != nil || !state.FallbackExhausted || state.AppliedSHA256 != "" || len(state.AppliedBody) != 0 || state.MintedKind != domain.DestCandidateEmpty {
		t.Fatalf("fallback exhaustion not durable: %+v / %v", state, err)
	}
	// Reassembling the compiler uses persisted state, not a process-local lock.
	restarted, err := destpolicy.NewCompiler(destpolicy.CompilerOptions{Definitions: definitions, Runtime: sqlstore.NewDestAgentPolicyRepo(f.db), Inputs: compilerFixtureInputs{}, Now: f.service.now})
	if err != nil {
		t.Fatal(err)
	}
	f.service.policies = restarted
	again, err := f.service.Sync(t.Context(), report)
	if err != nil || again.Config.ETag != empty.Config.ETag || again.Config.Body == nil || again.Config.Body.Policy != nil {
		t.Fatalf("restart retried exhausted fallback: %+v / %v", again.Config, err)
	}
}
