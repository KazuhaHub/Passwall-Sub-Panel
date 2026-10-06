package app

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-protocol/protocol"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/metrics"
)

func TestBuildDestinationManualPublicationReportsRejectionAndRetainsPreviousCandidate(t *testing.T) {
	f := buildDestinationPolicyFixture(t)
	a := f.a
	token := destinationRefreshAdminToken(t, a)
	if w := destinationListRequest(t, a, token, "POST", "policies", destinationPolicyInput("Publication block", "block")); w.Code != 201 {
		t.Fatal("policy fixture failed")
	}
	w := destinationListRequest(t, a, token, "POST", "publish", nil)
	var view struct {
		PublishedGeneration int64 `json:"published_generation"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &view) != nil || view.PublishedGeneration != 1 {
		t.Fatal("manual publication did not bypass the fresh definition debounce")
	}
	old, found, err := a.destDefinitions.Published(t.Context())
	if err != nil || !found || old.Generation != 1 {
		t.Fatal("manual publication did not durably commit the snapshot")
	}
	f.report.Capabilities = []string{protocol.CapabilityDestinationPolicy}
	before := syncNativeCacheFixture(t, a, f.credential, f.report)
	if before.Config.Body == nil || before.Config.Body.Policy == nil {
		t.Fatal("published candidate fixture missing")
	}
	bad := domain.DestPolicy{Name: "Persisted invalid rule", Action: domain.DestBlock, Scope: domain.DestScopeAll, Enabled: true, Inline: domain.DestInline{Ports: "invalid-port"}}
	if err := a.destDefinitions.SavePolicy(t.Context(), &bad, time.Time{}, time.Now()); err != nil {
		t.Fatal(err)
	}
	w = destinationListRequest(t, a, token, "POST", "publish", nil)
	var rejection struct {
		Error        string                   `json:"error"`
		PublishError *domain.DestPublishError `json:"publish_error"`
	}
	if w.Code != 409 || json.Unmarshal(w.Body.Bytes(), &rejection) != nil || rejection.Error != "dest_policy_over_limit" || rejection.PublishError == nil || rejection.PublishError.Kind != "invalid" {
		t.Fatal("failed publication was reported as success or lost its structured error")
	}
	state, err := a.destDefinitions.State(t.Context())
	current, found, snapErr := a.destDefinitions.Published(t.Context())
	if err != nil || snapErr != nil || !found || state.PublishedGeneration != 1 || state.Generation != 2 || state.PublishError == nil || !bytes.Equal(old.Body, current.Body) {
		t.Fatal("rejected publication advanced the snapshot or lost the old policy")
	}
	after := syncNativeCacheFixture(t, a, f.credential, f.report)
	if after.Config.Body == nil || protocol.PolicyDigest(after.Config.Body.Policy) != protocol.PolicyDigest(before.Config.Body.Policy) {
		t.Fatal("rejected manual publication changed the actual native candidate")
	}
	bad.Inline.Ports = "2525"
	if err := a.destDefinitions.SavePolicy(t.Context(), &bad, bad.UpdatedAt, time.Now()); err != nil {
		t.Fatal(err)
	}
	w = destinationListRequest(t, a, token, "POST", "publish", nil)
	state, err = a.destDefinitions.State(t.Context())
	if w.Code != 200 || err != nil || state.PublishedGeneration != 3 || state.PublishError != nil || state.PublishErrorAt != nil {
		t.Fatal("corrected publication did not advance and clear the stored error")
	}
}

func TestBuildDestinationPauseImmediatelyPublishesAndPreservesConfirmedPolicy(t *testing.T) {
	f := buildDestinationPolicyFixture(t)
	a := f.a
	token := destinationRefreshAdminToken(t, a)
	if w := destinationListRequest(t, a, token, "POST", "policies", destinationPolicyInput("Pause block", "block")); w.Code != 201 {
		t.Fatal("policy fixture failed")
	}
	if w := destinationListRequest(t, a, token, "POST", "publish", nil); w.Code != 200 {
		t.Fatal("publish fixture failed")
	}
	f.report.Capabilities = []string{protocol.CapabilityDestinationPolicy}
	first := syncNativeCacheFixture(t, a, f.credential, f.report)
	if first.Config.Body == nil || first.Config.Body.Policy == nil {
		t.Fatal("active policy fixture missing")
	}
	if err := a.destCompiler.ObserveStatus(t.Context(), f.agent.AgentID, &protocol.PolicyStatus{State: "applied", Digest: protocol.PolicyDigest(first.Config.Body.Policy)}, f.report.Capabilities); err != nil {
		t.Fatal(err)
	}
	confirmed, err := a.repos.DestAgentPolicy.Get(t.Context(), f.agent.AgentID, true)
	if err != nil || confirmed.AppliedSHA256 == "" {
		t.Fatal("confirmed policy fixture missing")
	}
	publicationCounter := metrics.DestPolicyPublishTotal.With(metrics.DestPublishPause)
	beforePauses := publicationCounter.Value()
	w := destinationListRequest(t, a, token, "PUT", "pause", map[string]any{"paused": true})
	state, err := a.destDefinitions.State(t.Context())
	if w.Code != 200 || err != nil || !state.Paused || state.Generation != 2 || state.PublishedGeneration != 2 {
		t.Fatal("pause did not save and immediately publish its definition generation")
	}
	paused := syncNativeCacheFixture(t, a, f.credential, f.report)
	runtime, err := a.repos.DestAgentPolicy.Get(t.Context(), f.agent.AgentID, true)
	if err != nil || paused.Config.Body == nil || paused.Config.Body.Policy != nil || runtime.MintedKind != domain.DestCandidatePaused || runtime.AppliedSHA256 != confirmed.AppliedSHA256 || !bytes.Equal(runtime.AppliedBody, confirmed.AppliedBody) {
		t.Fatal("pause failed to stop execution or replaced the confirmed policy")
	}
	if w = destinationListRequest(t, a, token, "PUT", "pause", map[string]any{"paused": true}); w.Code != 200 {
		t.Fatal("idempotent pause failed")
	}
	state, _ = a.destDefinitions.State(t.Context())
	if state.Generation != 2 || publicationCounter.Value() != beforePauses+1 {
		t.Fatal("unchanged pause advanced definition generation")
	}
	for _, bad := range []map[string]any{{}, {"paused": nil}, {"paused": "true"}, {"paused": false, "generation": 99}} {
		if w = destinationListRequest(t, a, token, "PUT", "pause", bad); w.Code != 400 {
			t.Fatal("invalid pause request was accepted")
		}
	}
	w = destinationListRequest(t, a, token, "PUT", "pause", map[string]any{"paused": false})
	state, err = a.destDefinitions.State(t.Context())
	resumed := syncNativeCacheFixture(t, a, f.credential, f.report)
	if w.Code != 200 || err != nil || state.Paused || state.PublishedGeneration != 3 || resumed.Config.Body == nil || resumed.Config.Body.Policy == nil || len(resumed.Config.Body.Policy.Rules) != 1 {
		t.Fatal("resume did not immediately restore the actual desired policy")
	}
	if publicationCounter.Value() != beforePauses+2 {
		t.Fatal("pause/resume publications were not recorded under the bounded pause trigger")
	}
}

func TestBuildDestinationPauseDefinitionFailureRollsBackFlagAndGeneration(t *testing.T) {
	f := buildDestinationPolicyFixture(t)
	a := f.a
	token := destinationRefreshAdminToken(t, a)
	saveWiringPolicy(t, f)
	if _, err := a.database.ExecContext(t.Context(), "CREATE TRIGGER pause_definition_failure BEFORE UPDATE OF generation ON dest_policy_state BEGIN SELECT RAISE(ABORT, 'pause-definition-private-marker'); END"); err != nil {
		t.Fatal(err)
	}
	w := destinationListRequest(t, a, token, "PUT", "pause", map[string]any{"paused": true})
	state, err := a.destDefinitions.State(t.Context())
	if w.Code != 500 || bytes.Contains(w.Body.Bytes(), []byte("pause-definition-private-marker")) || bytes.Contains(w.Body.Bytes(), []byte("pause_saved")) || err != nil || state.Paused || state.Generation != 1 || state.PublishedGeneration != 0 {
		t.Fatal("failed pause definition write left a flag, generation or false saved-pause response")
	}
}

func TestBuildDestinationSavedPauseSurvivesSnapshotWriteFailureAndRetries(t *testing.T) {
	f := buildDestinationPolicyFixture(t)
	a := f.a
	token := destinationRefreshAdminToken(t, a)
	saveWiringPolicy(t, f)
	if w := destinationListRequest(t, a, token, "POST", "publish", nil); w.Code != 200 {
		t.Fatal("publication fixture failed")
	}
	f.report.Capabilities = []string{protocol.CapabilityDestinationPolicy}
	first := syncNativeCacheFixture(t, a, f.credential, f.report)
	if first.Config.Body == nil || first.Config.Body.Policy == nil {
		t.Fatal("candidate fixture failed")
	}
	if err := a.destCompiler.ObserveStatus(t.Context(), f.agent.AgentID, &protocol.PolicyStatus{State: "applied", Digest: protocol.PolicyDigest(first.Config.Body.Policy)}, f.report.Capabilities); err != nil {
		t.Fatal(err)
	}
	confirmed, err := a.repos.DestAgentPolicy.Get(t.Context(), f.agent.AgentID, true)
	if err != nil || confirmed.AppliedSHA256 == "" {
		t.Fatal("confirmed policy fixture failed")
	}
	if _, err := a.database.ExecContext(t.Context(), "CREATE TRIGGER pause_snapshot_failure BEFORE INSERT ON dest_policy_snapshots BEGIN SELECT RAISE(ABORT, 'pause-snapshot-private-marker'); END"); err != nil {
		t.Fatal(err)
	}
	w := destinationListRequest(t, a, token, "PUT", "pause", map[string]any{"paused": true})
	var failure struct {
		Error      string `json:"error"`
		PauseSaved bool   `json:"pause_saved"`
		Paused     bool   `json:"paused"`
	}
	state, err := a.destDefinitions.State(t.Context())
	if w.Code != 503 || json.Unmarshal(w.Body.Bytes(), &failure) != nil || !failure.PauseSaved || !failure.Paused || failure.Error != "dest_policy_publish_unavailable" || bytes.Contains(w.Body.Bytes(), []byte("pause-snapshot-private-marker")) || err != nil || !state.Paused || state.Generation != 2 || state.PublishedGeneration != 1 {
		t.Fatal("publication failure lost saved pause or exposed private driver details")
	}
	paused := syncNativeCacheFixture(t, a, f.credential, f.report)
	runtime, err := a.repos.DestAgentPolicy.Get(t.Context(), f.agent.AgentID, true)
	if err != nil || paused.Config.Body == nil || paused.Config.Body.Policy != nil || runtime.MintedKind != domain.DestCandidatePaused || runtime.AppliedSHA256 != confirmed.AppliedSHA256 || !bytes.Equal(runtime.AppliedBody, confirmed.AppliedBody) {
		t.Fatal("snapshot failure vetoed emergency pause or corrupted its confirmed policy")
	}
	if _, err := a.database.ExecContext(t.Context(), "DROP TRIGGER pause_snapshot_failure"); err != nil {
		t.Fatal(err)
	}
	w = destinationListRequest(t, a, token, "PUT", "pause", map[string]any{"paused": true})
	state, err = a.destDefinitions.State(t.Context())
	if w.Code != 200 || err != nil || state.Generation != 2 || state.PublishedGeneration != 2 {
		t.Fatal("idempotent saved-pause retry did not publish the same generation")
	}
}

func TestBuildDestinationPauseWinsOverInvalidUnpublishedDefinitions(t *testing.T) {
	f := buildDestinationPolicyFixture(t)
	a := f.a
	token := destinationRefreshAdminToken(t, a)
	saveWiringPolicy(t, f)
	if w := destinationListRequest(t, a, token, "POST", "publish", nil); w.Code != 200 {
		t.Fatal("publication fixture failed")
	}
	bad := domain.DestPolicy{Name: "Invalid pause definition", Action: domain.DestBlock, Scope: domain.DestScopeAll, Enabled: true, Inline: domain.DestInline{Ports: "not-a-port"}}
	if err := a.destDefinitions.SavePolicy(t.Context(), &bad, time.Time{}, time.Now()); err != nil {
		t.Fatal(err)
	}
	w := destinationListRequest(t, a, token, "PUT", "pause", map[string]any{"paused": true})
	state, err := a.destDefinitions.State(t.Context())
	var response struct {
		Paused       bool                     `json:"paused"`
		PublishError *domain.DestPublishError `json:"publish_error"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &response) != nil || !response.Paused || response.PublishError == nil || err != nil || !state.Paused || state.Generation != 3 || state.PublishedGeneration != 1 {
		t.Fatal("invalid latest definitions vetoed pause or falsely advanced publication")
	}
	f.report.Capabilities = []string{protocol.CapabilityDestinationPolicy}
	paused := syncNativeCacheFixture(t, a, f.credential, f.report)
	if paused.Config.Body == nil || paused.Config.Body.Policy != nil {
		t.Fatal("invalid unpublished definitions kept execution enabled after pause")
	}
}
