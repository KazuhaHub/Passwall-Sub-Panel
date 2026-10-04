package nodesync

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/KazuhaHub/passwall-protocol/protocol"
	"github.com/KazuhaHub/passwall-sub-panel/internal/adapters/sqlstore"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/destpolicy"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/group"
	"gorm.io/gorm"
)

type compilerFixtureInputs struct{}

func TestProductionPolicyPublicationCacheAvoidsIdleSnapshotReads(t *testing.T) {
	f := newConfigAppliedFixture(t)
	definitions := sqlstore.NewDestDefinitionRepo(f.db)
	p := &domain.DestPolicy{Name: "cached publication", Enabled: true, Action: domain.DestBlock, Scope: domain.DestScopeAll, Inline: domain.DestInline{Ports: "443"}}
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
	block, reads := true, 0
	f.db.Callback().Query().After("gorm:query").Register("publication-cache-guard", func(tx *gorm.DB) {
		if tx.Statement.Table != "dest_policy_snapshots" {
			return
		}
		reads++
		if block {
			tx.AddError(errors.New("idle compilation read snapshot body"))
		}
	})
	t.Cleanup(func() { _ = f.db.Callback().Query().Remove("publication-cache-guard") })
	for range 2 {
		again, err := f.service.Sync(t.Context(), report)
		if err != nil || again.Config.ETag != first.Config.ETag || reads != 0 {
			t.Fatalf("idle publication cache: reads=%d / %v", reads, err)
		}
	}
	p.Inline.Ports = "80"
	if err := definitions.SavePolicy(t.Context(), p, p.UpdatedAt, f.now); err != nil {
		t.Fatal(err)
	}
	publishCompilerFixture(t, f, definitions)
	block = false
	updated, err := f.service.Sync(t.Context(), report)
	if err != nil || updated.Config.Body == nil || updated.Config.Body.Policy == nil || updated.Config.Body.Policy.Rules[0].Ports != "80" || reads != 1 {
		t.Fatalf("new publication not loaded once: reads=%d / %v", reads, err)
	}
	block = true
	if _, err := f.service.Sync(t.Context(), report); err != nil || reads != 1 {
		t.Fatalf("new snapshot not cached: reads=%d / %v", reads, err)
	}
}

func TestProductionPolicyInputsSyncTracksGroupAndCollectionChanges(t *testing.T) {
	f := newConfigAppliedFixture(t)
	panel := &domain.Panel{ID: f.agent.PanelID, Kind: domain.PanelKindPSP, Name: "production-inputs", URL: "psp://agt_config_ack"}
	if err := f.repos.XUIPanel.Save(t.Context(), panel); err != nil {
		t.Fatal(err)
	}
	matched := &domain.Group{Slug: "matched", Name: "Matched", TagFilter: domain.TagFilter{All: true}}
	other := &domain.Group{Slug: "other", Name: "Other", TagFilter: domain.TagFilter{Tags: []string{"region:JP"}}}
	for _, g := range []*domain.Group{matched, other} {
		if err := f.repos.Group.Create(t.Context(), g); err != nil {
			t.Fatal(err)
		}
	}
	var users []*domain.User
	for n := range 2 {
		u := &domain.User{UPN: fmt.Sprintf("input%d@example.test", n), Role: domain.RoleUser, SubToken: fmt.Sprintf("input-token-%d", n), UUID: fmt.Sprintf("00000000-0000-0000-0000-%012d", n+1), GroupID: matched.ID, Enabled: n == 0}
		if err := f.repos.User.Create(t.Context(), u); err != nil {
			t.Fatal(err)
		}
		users = append(users, u)
	}
	client := &domain.PSPClient{UserID: users[0].ID, PanelID: panel.ID, Email: "production@psp.local", UUID: users[0].UUID, DesiredEnable: true, DesiredMinted: true}
	id, err := f.repos.PSPClient.Create(t.Context(), client)
	if err != nil {
		t.Fatal(err)
	}
	client.ID = id
	if err := f.repos.PSPClient.SetInbounds(t.Context(), id, []domain.PSPClientInbound{{ClientID: id, NodeID: f.node.ID, State: domain.ClientApplyPending}}); err != nil {
		t.Fatal(err)
	}
	membership := f.repos.User.(ports.UserMembershipRepo)
	groupService := group.New(f.repos.Group, f.repos.Node, nil)
	groupService.SetMembershipRepo(membership)
	inputs, err := destpolicy.NewInputs(f.repos.XUIPanel.(ports.PanelAuditSettingsRepo), membership, groupService)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := f.repos.NativeDesired.Load(t.Context(), panel.ID)
	if err != nil {
		t.Fatal(err)
	}
	roster, quota, err := inputs.ForNode(t.Context(), panel.ID, snapshot, true)
	if err != nil || !reflect.DeepEqual(roster.UserIDs, []int64{users[0].ID}) || !reflect.DeepEqual(quota.UserIDs, []int64{users[0].ID, users[1].ID}) {
		t.Fatalf("production quota lost disabled member without client: roster=%+v quota=%+v / %v", roster, quota, err)
	}
	definitions := sqlstore.NewDestDefinitionRepo(f.db)
	p := &domain.DestPolicy{Name: "scoped input", Enabled: true, Action: domain.DestBlock, Scope: domain.DestScopeGroups, GroupIDs: []int64{matched.ID}, Inline: domain.DestInline{Ports: "443"}}
	if err := definitions.SavePolicy(t.Context(), p, p.UpdatedAt, f.now); err != nil {
		t.Fatal(err)
	}
	publishCompilerFixture(t, f, definitions)
	compiler, err := destpolicy.NewCompiler(destpolicy.CompilerOptions{Definitions: definitions, Runtime: f.repos.DestAgentPolicy, Inputs: inputs, Now: f.service.now})
	if err != nil {
		t.Fatal(err)
	}
	f.service.policies, f.service.policyCandidates = compiler, f.repos.NodeAgent.(ports.NodePolicyCandidateRepo)
	report := policySyncReport(f)
	report.Capabilities = append(report.Capabilities, "audit.hits.v1")
	first, err := f.service.Sync(t.Context(), report)
	if err != nil || first.Config.Body == nil || first.Config.Body.Policy == nil || len(first.Config.Body.Policy.Rules) != 1 || !reflect.DeepEqual(first.Config.Body.Policy.Rules[0].Subjects, []protocol.SubjectKey{protocol.NewSubjectKey(users[0].ID)}) {
		t.Fatalf("production scoped rule: %+v / %v", first.Config, err)
	}
	if first.Config.Body.Policy.Collect != protocol.CollectHits || first.Config.Body.Policy.CollectRevision != 1 {
		t.Fatalf("production collection defaults not compiled: %+v", first.Config.Body.Policy)
	}
	users[0].GroupID = other.ID
	if err := f.repos.User.Update(t.Context(), users[0]); err != nil {
		t.Fatal(err)
	}
	writer := f.repos.XUIPanel.(interface {
		UpdateNativeMetadata(context.Context, int64, *string, *string, *domain.PanelUpdateChannel, *domain.AuditCollect) error
	})
	off := domain.AuditCollectOff
	if err := writer.UpdateNativeMetadata(t.Context(), panel.ID, nil, nil, nil, &off); err != nil {
		t.Fatal(err)
	}
	second, err := f.service.Sync(t.Context(), report)
	if err != nil || second.Config.Body == nil || second.Config.Body.Policy != nil || second.Config.ETag == first.Config.ETag {
		t.Fatalf("group/control change retained stale policy: %+v / %v", second.Config, err)
	}
	state, err := f.repos.DestAgentPolicy.Get(t.Context(), f.agent.AgentID, false)
	if err != nil || state.CollectEffective != "" {
		t.Fatalf("collection off retained effective collection: %+v / %v", state, err)
	}
}

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
