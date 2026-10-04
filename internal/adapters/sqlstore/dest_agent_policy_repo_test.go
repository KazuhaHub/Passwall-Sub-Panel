package sqlstore

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-protocol/protocol"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/destpolicy"
	"gorm.io/gorm"
)

func policyObserverFixture(t *testing.T) (*gorm.DB, *nodeAgentRepo, *DestAgentPolicyRepo, domain.DestPolicyMint, time.Time) {
	t.Helper()
	db, mint, body, meta, now := policyMintFixture(t)
	if _, _, err := mint.MintConfigWithPolicyCandidate(t.Context(), "agt_policy_mint", body, meta, now); err != nil {
		t.Fatal(err)
	}
	return db, mint, NewDestAgentPolicyRepo(db), meta, now
}
func assertRuntimeMetadataOnly(t *testing.T, db *gorm.DB) func() {
	t.Helper()
	name := "runtime-metadata-only"
	db.Callback().Query().After("gorm:query").Register(name, func(tx *gorm.DB) {
		if tx.Statement.Table != "dest_agent_policy" {
			return
		}
		sql := tx.Statement.SQL.String()
		if strings.Contains(sql, "minted_body") || strings.Contains(sql, "applied_body") || strings.Contains(sql, "SELECT *") {
			tx.AddError(errors.New("runtime metadata query loaded policy blobs"))
		}
	})
	return func() { _ = db.Callback().Query().Remove(name) }
}
func TestDestinationObserverDurablyPromotesAndKeepsIdleMetadataOnly(t *testing.T) {
	db, _, repo, meta, now := policyObserverFixture(t)
	o := destpolicy.NewObserver(repo, func() time.Time { return now.Add(time.Minute) })
	invalidated := 0
	o.SetInvalidator(func(string) { invalidated++ })
	status := &protocol.PolicyStatus{State: "applied", Digest: meta.DesiredSHA256}
	caps := []string{protocol.CapabilityDestinationPolicy}
	if err := o.ObserveStatus(t.Context(), "agt_policy_mint", status, caps); err != nil {
		t.Fatal(err)
	}
	state, err := repo.Get(t.Context(), "agt_policy_mint", true)
	if err != nil || state.AppliedSHA256 != state.MintedSHA256 || !bytes.Equal(state.AppliedBody, state.MintedBody) || state.ReportedState != "applied" || state.AppliedAt == nil || invalidated != 1 {
		t.Fatalf("promotion not durable: %+v / %v invalidated=%d", state, err, invalidated)
	}
	remove := assertRuntimeMetadataOnly(t, db)
	defer remove()
	db.Callback().Update().Before("gorm:update").Register("runtime-idle-no-write", func(tx *gorm.DB) { tx.AddError(errors.New("idle runtime observation wrote a row")) })
	t.Cleanup(func() { _ = db.Callback().Update().Remove("runtime-idle-no-write") })
	// A new observer models a process restart; its idle behavior cannot depend
	// on a previously populated in-memory cache.
	restarted := destpolicy.NewObserver(NewDestAgentPolicyRepo(db), func() time.Time { return now.Add(2 * time.Minute) })
	if err := restarted.ObserveStatus(t.Context(), "agt_policy_mint", status, caps); err != nil {
		t.Fatal(err)
	}
	metadata, err := repo.Get(t.Context(), "agt_policy_mint", false)
	if err != nil || !metadata.AppliedAt.Equal(*state.AppliedAt) || !metadata.ReportedAt.Equal(*state.ReportedAt) || len(metadata.MintedBody) > 0 || len(metadata.AppliedBody) > 0 {
		t.Fatalf("idle state changed/loaded bodies: %+v / %v", metadata, err)
	}
}
func TestDestinationObserverExhaustsPrunedFallbackDurably(t *testing.T) {
	db, mint, repo, meta, now := policyObserverFixture(t)
	o := destpolicy.NewObserver(repo, func() time.Time { return now.Add(time.Minute) })
	caps := []string{protocol.CapabilityDestinationPolicy}
	if err := o.ObserveStatus(t.Context(), "agt_policy_mint", &protocol.PolicyStatus{State: "applied", Digest: meta.DesiredSHA256}, caps); err != nil {
		t.Fatal(err)
	}
	p := &protocol.DestinationPolicy{Rules: []protocol.DestinationRule{{ID: "p1", Action: protocol.RuleBlock, Ports: "80", Subjects: []protocol.SubjectKey{"usr_2"}}}}
	body, _ := json.Marshal(protocol.ConfigBody{Listeners: []protocol.Listener{}, Policy: p})
	meta.Kind = domain.DestCandidateFallback
	if _, _, err := mint.MintConfigWithPolicyCandidate(t.Context(), "agt_policy_mint", body, meta, now.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := o.ObserveStatus(t.Context(), "agt_policy_mint", &protocol.PolicyStatus{State: "rejected", Digest: protocol.PolicyDigest(p), IssueCode: protocol.IssueDestinationPolicyRejected}, caps); err != nil {
		t.Fatal(err)
	}
	state, err := NewDestAgentPolicyRepo(db).Get(t.Context(), "agt_policy_mint", true)
	if err != nil || !state.FallbackExhausted || state.AppliedSHA256 != "" || len(state.AppliedBody) != 0 || state.ReportedSHA256 != protocol.PolicyDigest(p) {
		t.Fatalf("fallback exhaustion not durable: %+v / %v", state, err)
	}
}
func TestDestinationObserverFailureRollsBackAllStateAndInvalidation(t *testing.T) {
	db, _, repo, meta, now := policyObserverFixture(t)
	failure := errors.New("injected observer update failure")
	db.Callback().Update().Before("gorm:update").Register("runtime-fail-write", func(tx *gorm.DB) {
		if tx.Statement.Table == "dest_agent_policy" {
			tx.AddError(failure)
		}
	})
	t.Cleanup(func() { _ = db.Callback().Update().Remove("runtime-fail-write") })
	o := destpolicy.NewObserver(repo, func() time.Time { return now })
	invalidated := false
	o.SetInvalidator(func(string) { invalidated = true })
	if err := o.ObserveStatus(t.Context(), "agt_policy_mint", &protocol.PolicyStatus{State: "applied", Digest: meta.DesiredSHA256}, []string{protocol.CapabilityDestinationPolicy}); !errors.Is(err, failure) {
		t.Fatalf("missing injected fault: %v", err)
	}
	state, err := repo.Get(t.Context(), "agt_policy_mint", true)
	if err != nil || state.AppliedSHA256 != "" || state.ReportedState != "" || invalidated {
		t.Fatalf("observer partially committed: %+v / %v invalidated=%v", state, err, invalidated)
	}
}
func TestDestinationStaleObservationDoesNotLoadBodiesOrChangeCandidate(t *testing.T) {
	db, _, repo, meta, now := policyObserverFixture(t)
	remove := assertRuntimeMetadataOnly(t, db)
	defer remove()
	o := destpolicy.NewObserver(repo, func() time.Time { return now })
	invalidated := false
	o.SetInvalidator(func(string) { invalidated = true })
	if err := o.ObserveStatus(t.Context(), "agt_policy_mint", &protocol.PolicyStatus{State: "rejected", Digest: strings.Repeat("c", 64), IssueCode: protocol.IssueDestinationPolicyRejected}, []string{protocol.CapabilityDestinationPolicy}); err != nil {
		t.Fatal(err)
	}
	state, err := repo.Get(t.Context(), "agt_policy_mint", false)
	if err != nil || state.ReportedSHA256 != strings.Repeat("c", 64) || state.MintedSHA256 != meta.DesiredSHA256 || state.FallbackReason != "" || state.RejectedGeneration != 0 || invalidated {
		t.Fatalf("stale report changed candidate: %+v / %v", state, err)
	}
}
func TestDestinationStateUpdatePreservesMintedSourceAndMissingAgent(t *testing.T) {
	_, _, repo, meta, now := policyObserverFixture(t)
	changed, err := repo.Update(t.Context(), "agt_policy_mint", now, func(state *domain.DestAgentPolicy, _ func() error) (bool, error) {
		state.MintedKind = domain.DestCandidatePaused
		return true, nil
	})
	if !errors.Is(err, domain.ErrValidation) || changed {
		t.Fatalf("reserved candidate fields updated: changed=%v err=%v", changed, err)
	}
	state, err := repo.Get(t.Context(), "agt_policy_mint", false)
	if err != nil || state.MintedKind != meta.Kind {
		t.Fatalf("source clobbered: %+v / %v", state, err)
	}
	if _, err := repo.Get(t.Context(), "missing", false); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("missing agent became empty state: %v", err)
	}
	if _, err := repo.Update(t.Context(), "missing", now, func(*domain.DestAgentPolicy, func() error) (bool, error) { return true, nil }); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("missing agent state recreated: %v", err)
	}
}

func TestDestinationObserverMissingCandidateBodyRollsBackReportedState(t *testing.T) {
	db, _, repo, meta, now := policyObserverFixture(t)
	if err := db.Model(&destAgentPolicyRow{}).Where("agent_id = ?", "agt_policy_mint").UpdateColumn("minted_body", nil).Error; err != nil {
		t.Fatal(err)
	}
	o := destpolicy.NewObserver(repo, func() time.Time { return now })
	if err := o.ObserveStatus(t.Context(), "agt_policy_mint", &protocol.PolicyStatus{State: "applied", Digest: meta.DesiredSHA256}, []string{protocol.CapabilityDestinationPolicy}); !errors.Is(err, domain.ErrUnavailable) {
		t.Fatalf("missing body became successful/empty state: %v", err)
	}
	state, err := repo.Get(t.Context(), "agt_policy_mint", false)
	if err != nil || state.ReportedState != "" || state.AppliedSHA256 != "" {
		t.Fatalf("bad body partially committed: %+v / %v", state, err)
	}
}

func TestDestinationStateInitialObservationAndNoOpCreate(t *testing.T) {
	db, _, _, meta, now := policyMintFixture(t)
	repo := NewDestAgentPolicyRepo(db)
	state, err := repo.Get(t.Context(), "agt_policy_mint", false)
	if err != nil || state.AgentID != "agt_policy_mint" || state.MintedAt != nil {
		t.Fatalf("initial state: %+v / %v", state, err)
	}
	if changed, err := repo.Update(t.Context(), "agt_policy_mint", now, func(*domain.DestAgentPolicy, func() error) (bool, error) { return true, nil }); err != nil || changed {
		t.Fatalf("no-op created runtime state: %v / %v", changed, err)
	}
	var count int64
	if err := db.Model(&destAgentPolicyRow{}).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("no-op row leaked: %d / %v", count, err)
	}
	o := destpolicy.NewObserver(repo, func() time.Time { return now })
	if err := o.ObserveStatus(t.Context(), "agt_policy_mint", &protocol.PolicyStatus{State: "applied", Digest: meta.DesiredSHA256}, []string{protocol.CapabilityDestinationPolicy}); err != nil {
		t.Fatal(err)
	}
	state, err = repo.Get(t.Context(), "agt_policy_mint", true)
	if err != nil || state.ReportedState != "applied" || state.AppliedSHA256 != "" || state.MintedSHA256 != "" {
		t.Fatalf("unminted report promoted a policy: %+v / %v", state, err)
	}
}

func TestDestinationStateRejectsUnconfirmedAppliedMetadata(t *testing.T) {
	for _, field := range []string{"count", "groups", "time"} {
		t.Run(field, func(t *testing.T) {
			_, _, repo, _, now := policyObserverFixture(t)
			changed, err := repo.Update(t.Context(), "agt_policy_mint", now, func(state *domain.DestAgentPolicy, _ func() error) (bool, error) {
				switch field {
				case "count":
					state.AppliedRuleCount = 1
				case "groups":
					state.AppliedGroups = []int64{1}
				case "time":
					state.AppliedAt = &now
				}
				return true, nil
			})
			if !errors.Is(err, domain.ErrValidation) || changed {
				t.Fatalf("unconfirmed metadata committed: %v / %v", changed, err)
			}
		})
	}
}

func TestDestinationObserverInvalidatesNewRejectedGeneration(t *testing.T) {
	_, mint, repo, meta, now := policyObserverFixture(t)
	o := destpolicy.NewObserver(repo, func() time.Time { return now })
	invalidated := 0
	o.SetInvalidator(func(string) { invalidated++ })
	status := &protocol.PolicyStatus{State: "rejected", Digest: meta.DesiredSHA256, IssueCode: protocol.IssueDestinationPolicyRejected}
	caps := []string{protocol.CapabilityDestinationPolicy}
	if err := o.ObserveStatus(t.Context(), "agt_policy_mint", status, caps); err != nil {
		t.Fatal(err)
	}
	_, _, body, _, _ := policyMintFixture(t)
	meta.Generation++
	if _, _, err := mint.MintConfigWithPolicyCandidate(t.Context(), "agt_policy_mint", body, meta, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := o.ObserveStatus(t.Context(), "agt_policy_mint", status, caps); err != nil {
		t.Fatal(err)
	}
	state, err := repo.Get(t.Context(), "agt_policy_mint", false)
	if err != nil || state.RejectedGeneration != meta.Generation || invalidated != 2 {
		t.Fatalf("new rejection failed to invalidate: %+v / %v invalidated=%d", state, err, invalidated)
	}
}

func TestDestinationStateRejectsIncorrectConfirmedGroups(t *testing.T) {
	_, _, repo, meta, now := policyObserverFixture(t)
	o := destpolicy.NewObserver(repo, func() time.Time { return now })
	if err := o.ObserveStatus(t.Context(), "agt_policy_mint", &protocol.PolicyStatus{State: "applied", Digest: meta.DesiredSHA256}, []string{protocol.CapabilityDestinationPolicy}); err != nil {
		t.Fatal(err)
	}
	changed, err := repo.Update(t.Context(), "agt_policy_mint", now, func(state *domain.DestAgentPolicy, load func() error) (bool, error) {
		if err := load(); err != nil {
			return false, err
		}
		state.AppliedGroups = []int64{123}
		return true, nil
	})
	if !errors.Is(err, domain.ErrValidation) || changed {
		t.Fatalf("fabricated group acceptance persisted: %v / %v", changed, err)
	}
}

func TestDestinationObserverPersistsRejectionContextAndTracksSameGenerationRetry(t *testing.T) {
	_, mint, repo, meta, now := policyObserverFixture(t)
	o := destpolicy.NewObserver(repo, func() time.Time { return now })
	invalidated := 0
	o.SetInvalidator(func(string) { invalidated++ })
	status := &protocol.PolicyStatus{State: "rejected", Digest: meta.DesiredSHA256, IssueCode: protocol.IssueDestinationPolicyRejected}
	caps := []string{protocol.CapabilityDestinationPolicy}
	if err := o.ObserveStatus(t.Context(), "agt_policy_mint", status, caps); err != nil {
		t.Fatal(err)
	}
	state, err := repo.Get(t.Context(), "agt_policy_mint", true)
	if err != nil || state.RejectedContext != meta.Context {
		t.Fatalf("rejection context not durable: %+v / %v", state, err)
	}
	var p *protocol.DestinationPolicy
	if err := json.Unmarshal(state.MintedBody, &p); err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(protocol.ConfigBody{Listeners: []protocol.Listener{}, Policy: p})
	meta.Context = strings.Repeat("c", 64)
	if _, _, err := mint.MintConfigWithPolicyCandidate(t.Context(), "agt_policy_mint", body, meta, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	state, err = repo.Get(t.Context(), "agt_policy_mint", false)
	if err != nil || state.RejectedContext == meta.Context {
		t.Fatalf("mint rewrote the rejected source: %+v / %v", state, err)
	}
	if err := o.ObserveStatus(t.Context(), "agt_policy_mint", status, caps); err != nil {
		t.Fatal(err)
	}
	state, err = repo.Get(t.Context(), "agt_policy_mint", false)
	if err != nil || state.RejectedContext != meta.Context || state.RejectedGeneration != meta.Generation || invalidated != 2 {
		t.Fatalf("retry rejection stayed on old context: %+v / %v invalidated=%d", state, err, invalidated)
	}
}
