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
	"gorm.io/gorm"
)

func policyMintFixture(t *testing.T) (*gorm.DB, *nodeAgentRepo, []byte, domain.DestPolicyMint, time.Time) {
	t.Helper()
	db, err := openTestDB(t)
	if err != nil {
		t.Fatal(err)
	}
	if err := ensureTestSchema(db); err != nil {
		t.Fatal(err)
	}
	r := NewRepos(db).NodeAgent.(*nodeAgentRepo)
	if err := r.Create(t.Context(), &domain.NodeAgent{AgentID: "agt_policy_mint", PanelID: 81, CredentialSHA256: strings.Repeat("a", 64)}); err != nil {
		t.Fatal(err)
	}
	p := &protocol.DestinationPolicy{Rules: []protocol.DestinationRule{{ID: "p1", Action: protocol.RuleBlock, Ports: "443"}}}
	body, err := json.Marshal(protocol.ConfigBody{Listeners: []protocol.Listener{}, Policy: p})
	if err != nil {
		t.Fatal(err)
	}
	m := domain.DestPolicyMint{Kind: domain.DestCandidateDesired, Generation: 1, Context: strings.Repeat("b", 64), DesiredSHA256: protocol.PolicyDigest(p)}
	return db, r, body, m, time.UnixMilli(1791000000000).UTC()
}
func TestMintConfigPersistsExactCandidateAtomicallyAndSkipsIdleBlobs(t *testing.T) {
	db, r, body, m, now := policyMintFixture(t)
	stream, changed, err := r.MintConfigWithPolicyCandidate(t.Context(), "agt_policy_mint", body, m, now)
	if err != nil || !changed || stream == nil || stream.DesiredVersion != 1 || !bytes.Equal(stream.DesiredBody, body) {
		t.Fatalf("mint=%+v changed=%v err=%v", stream, changed, err)
	}
	var row destAgentPolicyRow
	if err := db.First(&row, "agent_id = ?", "agt_policy_mint").Error; err != nil {
		t.Fatal(err)
	}
	var config protocol.ConfigBody
	if err := json.Unmarshal(body, &config); err != nil {
		t.Fatal(err)
	}
	wantBody, _ := json.Marshal(config.Policy)
	if !bytes.Equal(row.MintedBody, wantBody) || row.MintedSHA256 != protocol.PolicyDigest(config.Policy) || row.MintedContext != m.Context || row.MintedKind != "desired" || row.MintedGeneration != 1 {
		t.Fatalf("candidate not exact: %+v", row)
	}
	db.Callback().Query().After("gorm:query").Register("policy-mint-no-blobs", func(tx *gorm.DB) {
		if tx.Statement.Table != "node_agent_streams" && tx.Statement.Table != "dest_agent_policy" {
			return
		}
		sql := tx.Statement.SQL.String()
		for _, field := range []string{"desired_body", "minted_body", "applied_body"} {
			if strings.Contains(sql, field) {
				tx.AddError(errors.New("idle path read blob: " + field))
			}
		}
	})
	db.Callback().Update().Before("gorm:update").Register("policy-mint-no-idle-write", func(tx *gorm.DB) { tx.AddError(errors.New("idle path wrote a row")) })
	t.Cleanup(func() { _ = db.Callback().Query().Remove("policy-mint-no-blobs") })
	t.Cleanup(func() { _ = db.Callback().Update().Remove("policy-mint-no-idle-write") })
	stream, changed, err = r.MintConfigWithPolicyCandidate(t.Context(), "agt_policy_mint", body, m, now.Add(time.Minute))
	if err != nil || changed || stream == nil || stream.DesiredVersion != 1 {
		t.Fatalf("idle mint=%+v changed=%v err=%v", stream, changed, err)
	}
}
func TestMintConfigUpdatesSourceEvenWhenETagUnchangedWithoutOverwritingLKG(t *testing.T) {
	db, r, body, m, now := policyMintFixture(t)
	if _, _, err := r.MintConfigWithPolicyCandidate(t.Context(), "agt_policy_mint", body, m, now); err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&destAgentPolicyRow{}).Where("agent_id = ?", "agt_policy_mint").Updates(map[string]any{"applied_body": []byte("confirmed"), "applied_sha256": strings.Repeat("c", 64), "fallback_reason": "rejected"}).Error; err != nil {
		t.Fatal(err)
	}
	m.Kind, m.Generation, m.Context = domain.DestCandidateFallback, 2, strings.Repeat("d", 64)
	stream, changed, err := r.MintConfigWithPolicyCandidate(t.Context(), "agt_policy_mint", body, m, now.Add(time.Minute))
	if err != nil || changed || stream == nil || stream.DesiredVersion != 1 {
		t.Fatalf("source-only mint=%+v changed=%v err=%v", stream, changed, err)
	}
	var row destAgentPolicyRow
	if err := db.First(&row, "agent_id = ?", "agt_policy_mint").Error; err != nil {
		t.Fatal(err)
	}
	if row.MintedKind != "fallback" || row.MintedGeneration != 2 || row.MintedContext != m.Context || string(row.AppliedBody) != "confirmed" || row.FallbackReason != "rejected" {
		t.Fatalf("source lost or LKG clobbered: %+v", row)
	}
}
func TestMintConfigCandidateFailureRollsBackStream(t *testing.T) {
	db, r, body, m, now := policyMintFixture(t)
	failure := errors.New("injected candidate failure")
	db.Callback().Create().Before("gorm:create").Register("fail-policy-candidate", func(tx *gorm.DB) {
		if tx.Statement.Table == "dest_agent_policy" {
			tx.AddError(failure)
		}
	})
	t.Cleanup(func() { _ = db.Callback().Create().Remove("fail-policy-candidate") })
	if _, _, err := r.MintConfigWithPolicyCandidate(t.Context(), "agt_policy_mint", body, m, now); !errors.Is(err, failure) {
		t.Fatalf("missing fault: %v", err)
	}
	stream, err := r.GetStream(t.Context(), "agt_policy_mint", domain.NodeAgentStreamConfig)
	if err != nil || stream.DesiredVersion != 0 || len(stream.DesiredBody) != 0 {
		t.Fatalf("stream committed without candidate: %+v / %v", stream, err)
	}
	var count int64
	if err := db.Model(&destAgentPolicyRow{}).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("candidate leaked: %d / %v", count, err)
	}
}
func TestMintConfigRejectsInconsistentCandidateMetadata(t *testing.T) {
	_, r, body, m, now := policyMintFixture(t)
	for _, bad := range []domain.DestPolicyMint{{Kind: "unknown"}, {Kind: domain.DestCandidateDesired, Generation: -1, Context: m.Context}, {Kind: domain.DestCandidateDesired, Generation: 1, Context: "bad"}, {Kind: domain.DestCandidatePaused, Generation: 1, Context: m.Context}} {
		if _, _, err := r.MintConfigWithPolicyCandidate(t.Context(), "agt_policy_mint", body, bad, now); !errors.Is(err, domain.ErrValidation) {
			t.Fatalf("bad metadata accepted: %+v / %v", bad, err)
		}
	}
}

func TestMintConfigRejectsMalformedAndNoncanonicalConfig(t *testing.T) {
	_, r, body, m, now := policyMintFixture(t)
	for _, bad := range [][]byte{[]byte(`null`), []byte(`[]`), []byte(`{"unknown":7}`), append(append([]byte(nil), body...), []byte(`{}`)...)} {
		if _, _, err := r.MintConfigWithPolicyCandidate(t.Context(), "agt_policy_mint", bad, domain.DestPolicyMint{Kind: domain.DestCandidateEmpty, Context: m.Context}, now); !errors.Is(err, domain.ErrValidation) {
			t.Fatalf("invalid config accepted: %s / %v", bad, err)
		}
	}
}

func TestMintConfigStreamFailurePreservesExistingCandidate(t *testing.T) {
	db, r, body, m, now := policyMintFixture(t)
	if _, _, err := r.MintConfigWithPolicyCandidate(t.Context(), "agt_policy_mint", body, m, now); err != nil {
		t.Fatal(err)
	}
	failure := errors.New("injected stream failure")
	db.Callback().Update().Before("gorm:update").Register("fail-policy-stream", func(tx *gorm.DB) {
		if tx.Statement.Table == "node_agent_streams" {
			tx.AddError(failure)
		}
	})
	t.Cleanup(func() { _ = db.Callback().Update().Remove("fail-policy-stream") })
	var config protocol.ConfigBody
	if err := json.Unmarshal(body, &config); err != nil {
		t.Fatal(err)
	}
	config.Policy.Rules[0].Ports = "80"
	newBody, _ := json.Marshal(config)
	newMeta := m
	newMeta.DesiredSHA256 = protocol.PolicyDigest(config.Policy)
	if _, _, err := r.MintConfigWithPolicyCandidate(t.Context(), "agt_policy_mint", newBody, newMeta, now.Add(time.Minute)); !errors.Is(err, failure) {
		t.Fatalf("missing fault: %v", err)
	}
	var row destAgentPolicyRow
	if err := db.First(&row, "agent_id = ?", "agt_policy_mint").Error; err != nil {
		t.Fatal(err)
	}
	stream, err := r.GetStream(t.Context(), "agt_policy_mint", domain.NodeAgentStreamConfig)
	if err != nil || stream.DesiredVersion != 1 || row.MintedSHA256 != m.DesiredSHA256 {
		t.Fatalf("partial commit: stream=%+v candidate=%+v err=%v", stream, row, err)
	}
}

func TestMintConfigDoesNotRecreateDeletedAgentPolicy(t *testing.T) {
	db, r, body, m, now := policyMintFixture(t)
	if err := db.Where("agent_id = ?", "agt_policy_mint").Delete(&nodeAgentRow{}).Error; err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.MintConfigWithPolicyCandidate(t.Context(), "agt_policy_mint", body, m, now); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("deleted agent minted: %v", err)
	}
	var count int64
	if err := db.Model(&destAgentPolicyRow{}).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("deleted policy recreated: %d / %v", count, err)
	}
}
