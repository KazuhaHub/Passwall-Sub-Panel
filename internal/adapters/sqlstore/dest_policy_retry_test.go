package sqlstore

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-protocol/protocol"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/destpolicy"
	"gorm.io/gorm"
)

type policyRetryStore interface {
	RetryDestinationPolicy(context.Context, string, time.Time) (bool, error)
}

func requirePolicyRetryStore(t *testing.T, repo *DestAgentPolicyRepo) policyRetryStore {
	t.Helper()
	retry, ok := any(repo).(policyRetryStore)
	if !ok {
		t.Fatal("destination runtime repository omitted atomic retry")
	}
	return retry
}

func TestDestinationRetryAtomicallyClearsLocksAndRetiresOldReceipt(t *testing.T) {
	db, mint, repo, meta, now := policyObserverFixture(t)
	retry := requirePolicyRetryStore(t, repo)
	o := destpolicy.NewObserver(repo, func() time.Time { return now.Add(time.Minute) })
	caps := []string{protocol.CapabilityDestinationPolicy}
	if err := o.ObserveStatus(t.Context(), "agt_policy_mint", &protocol.PolicyStatus{State: "applied", Digest: meta.DesiredSHA256}, caps); err != nil {
		t.Fatal(err)
	}
	oldRejected := &protocol.PolicyStatus{State: "rejected", Digest: meta.DesiredSHA256, IssueCode: protocol.IssueDestinationPolicyRejected}
	if err := o.ObserveStatus(t.Context(), "agt_policy_mint", oldRejected, caps); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Update(t.Context(), "agt_policy_mint", now, func(s *domain.DestAgentPolicy, _ func() error) (bool, error) {
		s.FallbackExhausted = true
		return true, nil
	}); err != nil {
		t.Fatal(err)
	}
	before, err := repo.Get(t.Context(), "agt_policy_mint", true)
	if err != nil {
		t.Fatal(err)
	}
	streamBefore, err := mint.GetStream(t.Context(), "agt_policy_mint", domain.NodeAgentStreamConfig)
	if err != nil {
		t.Fatal(err)
	}
	remove := assertRuntimeMetadataOnly(t, db)
	changed, err := retry.RetryDestinationPolicy(t.Context(), "agt_policy_mint", now.Add(2*time.Minute))
	remove()
	if err != nil || !changed {
		t.Fatalf("retry changed=%v: %v", changed, err)
	}
	after, err := repo.Get(t.Context(), "agt_policy_mint", true)
	if err != nil || after.RejectedGeneration != 0 || after.RejectedContext != "" || after.FallbackExhausted || after.FallbackReason != "" || after.MintedAt != nil {
		t.Fatalf("retry retained locks or old confirmation marker: %v", err)
	}
	if after.DesiredSHA256 != before.DesiredSHA256 || after.MintedSHA256 != before.MintedSHA256 || after.MintedContext != before.MintedContext || !bytes.Equal(after.MintedBody, before.MintedBody) || !bytes.Equal(after.AppliedBody, before.AppliedBody) || after.AppliedSHA256 != before.AppliedSHA256 || !after.AppliedAt.Equal(*before.AppliedAt) {
		t.Fatal("retry rewrote exact candidate or confirmed policy")
	}
	streamAfter, err := mint.GetStream(t.Context(), "agt_policy_mint", domain.NodeAgentStreamConfig)
	if err != nil || !reflect.DeepEqual(streamBefore, streamAfter) {
		t.Fatal("retry changed the config stream before candidate compilation")
	}
	if err := o.ObserveStatus(t.Context(), "agt_policy_mint", oldRejected, caps); err != nil {
		t.Fatal(err)
	}
	state, err := repo.Get(t.Context(), "agt_policy_mint", false)
	if err != nil || state.FallbackReason != "" || state.RejectedGeneration != 0 {
		t.Fatal("old rejected receipt immediately canceled the requested retry")
	}
	if changed, err := retry.RetryDestinationPolicy(t.Context(), "agt_policy_mint", now.Add(3*time.Minute)); err != nil || changed {
		t.Fatal("repeated retry was not idempotent")
	}
}

func TestDestinationRetryRollsBackAndPreservesOtherFallbacks(t *testing.T) {
	db, _, repo, _, now := policyObserverFixture(t)
	retry := requirePolicyRetryStore(t, repo)
	if _, err := repo.Update(t.Context(), "agt_policy_mint", now, func(s *domain.DestAgentPolicy, _ func() error) (bool, error) {
		s.FallbackReason = "sniffing"
		s.PrecheckListeners = []string{"lis_1"}
		s.FallbackExhausted = true
		return true, nil
	}); err != nil {
		t.Fatal(err)
	}
	problem := errors.New("retry write failed")
	db.Callback().Update().Before("gorm:update").Register("retry-write-failure", func(tx *gorm.DB) {
		if tx.Statement.Table == "dest_agent_policy" {
			tx.AddError(problem)
		}
	})
	t.Cleanup(func() { _ = db.Callback().Update().Remove("retry-write-failure") })
	if changed, err := retry.RetryDestinationPolicy(t.Context(), "agt_policy_mint", now); !errors.Is(err, problem) || changed {
		t.Fatal("failed retry committed or hid its error")
	}
	s, err := repo.Get(t.Context(), "agt_policy_mint", false)
	if err != nil || !s.FallbackExhausted || s.MintedAt == nil {
		t.Fatal("failed retry did not roll back every update")
	}
	if err := db.Callback().Update().Remove("retry-write-failure"); err != nil {
		t.Fatal(err)
	}
	if changed, err := retry.RetryDestinationPolicy(t.Context(), "agt_policy_mint", now); err != nil || !changed {
		t.Fatal(err)
	}
	s, err = repo.Get(t.Context(), "agt_policy_mint", false)
	if err != nil || s.FallbackExhausted || s.FallbackReason != "sniffing" || !reflect.DeepEqual(s.PrecheckListeners, []string{"lis_1"}) {
		t.Fatal("retry erased current sniffing preflight failure")
	}
	if _, err := retry.RetryDestinationPolicy(t.Context(), "agt_missing", now); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal("retry invented a missing agent")
	}
}

func TestDestinationMintRearmsRetiredConfirmationWithoutChangingStream(t *testing.T) {
	db, mint, repo, meta, now := policyObserverFixture(t)
	if err := db.Model(&destAgentPolicyRow{}).Where("agent_id = ?", "agt_policy_mint").UpdateColumn("minted_at", nil).Error; err != nil {
		t.Fatal(err)
	}
	stream, err := mint.GetStream(t.Context(), "agt_policy_mint", domain.NodeAgentStreamConfig)
	if err != nil {
		t.Fatal(err)
	}
	when := now.Add(time.Minute)
	again, changed, err := mint.MintConfigWithPolicyCandidate(t.Context(), "agt_policy_mint", stream.DesiredBody, meta, when)
	if err != nil || changed || again.DesiredVersion != stream.DesiredVersion || again.DesiredETag != stream.DesiredETag {
		t.Fatal("retry rearm changed the identical config stream")
	}
	state, err := repo.Get(t.Context(), "agt_policy_mint", false)
	if err != nil || state.MintedAt == nil || !state.MintedAt.Equal(when) {
		t.Fatal("same-byte mint did not rearm a retired confirmation marker")
	}
}

func TestDestinationRetryFreshAndCorruptStateBoundaries(t *testing.T) {
	db, mint, body, meta, now := policyMintFixture(t)
	repo := NewDestAgentPolicyRepo(db)
	retry := requirePolicyRetryStore(t, repo)
	if changed, err := retry.RetryDestinationPolicy(t.Context(), "agt_policy_mint", now); err != nil || changed {
		t.Fatal("fresh native agent retry was not a no-op")
	}
	var rows int64
	if err := db.Model(&destAgentPolicyRow{}).Count(&rows).Error; err != nil || rows != 0 {
		t.Fatal("fresh retry invented candidate provenance")
	}
	if _, err := retry.RetryDestinationPolicy(t.Context(), "", now); !errors.Is(err, domain.ErrValidation) {
		t.Fatal("empty retry identity was accepted")
	}
	var absent *DestAgentPolicyRepo
	if _, err := absent.RetryDestinationPolicy(t.Context(), "agt_policy_mint", now); !errors.Is(err, domain.ErrUnavailable) {
		t.Fatal("absent retry repository pretended to commit")
	}
	// Seed a current candidate, then corrupt only the projected fallback kind.
	if _, _, err := mint.MintConfigWithPolicyCandidate(t.Context(), "agt_policy_mint", body, meta, now); err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&destAgentPolicyRow{}).Where("agent_id = ?", "agt_policy_mint").UpdateColumn("fallback_reason", "unknown").Error; err != nil {
		t.Fatal(err)
	}
	if changed, err := retry.RetryDestinationPolicy(t.Context(), "agt_policy_mint", now); changed || !errors.Is(err, domain.ErrUnavailable) {
		t.Fatal("retry erased an unreadable enforcement verdict")
	}
}
