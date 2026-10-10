package sqlstore

import (
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-protocol/protocol"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/boundedcache"
	"gorm.io/gorm"
)

func TestPolicyMintCachesCanonicalProofButChecksEverySourceAndTransaction(t *testing.T) {
	db, r, body, meta, now := policyMintFixture(t)
	decoded := 0
	r.policyConfigDecode = func(body []byte) (policyConfigProof, error) { decoded++; return decodePolicyConfig(body) }
	for generation := range 2 {
		meta.Generation = int64(generation + 1)
		if _, _, err := r.MintConfigWithPolicyCandidate(t.Context(), "agt_policy_mint", body, meta, now); err != nil {
			t.Fatal(err)
		}
	}
	if decoded != 1 {
		t.Fatalf("idle/source-only mint decoded config again: %d", decoded)
	}
	bad := meta
	bad.DesiredSHA256 = strings.Repeat("e", 64)
	if _, _, err := r.MintConfigWithPolicyCandidate(t.Context(), "agt_policy_mint", body, bad, now); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("cached proof ignored new metadata: %v", err)
	}
	failure := errors.New("cached source transaction failed")
	mutated := false
	db.Callback().Update().Before("gorm:update").Register("fail-cached-source", func(tx *gorm.DB) {
		if tx.Statement.Table == "dest_agent_policy" {
			if values, ok := tx.Statement.Dest.(map[string]any); ok {
				if bytes, ok := values["minted_body"].([]byte); ok && len(bytes) > 0 {
					bytes[0] = 0
					mutated = true
				}
			}
			tx.AddError(failure)
		}
	})
	t.Cleanup(func() { _ = db.Callback().Update().Remove("fail-cached-source") })
	meta.Generation = 3
	if _, _, err := r.MintConfigWithPolicyCandidate(t.Context(), "agt_policy_mint", body, meta, now.Add(time.Minute)); !errors.Is(err, failure) {
		t.Fatalf("cached proof bypassed failed transaction: %v", err)
	}
	var state destAgentPolicyRow
	if err := db.First(&state, "agent_id = ?", "agt_policy_mint").Error; err != nil || state.MintedGeneration != 2 {
		t.Fatalf("failed source committed: %+v / %v", state, err)
	}
	if decoded != 1 {
		t.Fatalf("metadata checks or failed writes redecoded immutable config: %d", decoded)
	}
	if !mutated {
		t.Fatal("failure callback did not exercise repository byte isolation")
	}
	if err := db.Callback().Update().Remove("fail-cached-source"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.MintConfigWithPolicyCandidate(t.Context(), "agt_policy_mint", body, meta, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := db.First(&state, "agent_id = ?", "agt_policy_mint").Error; err != nil {
		t.Fatal(err)
	}
	var policy *protocol.DestinationPolicy
	if json.Unmarshal(state.MintedBody, &policy) != nil || protocol.PolicyDigest(policy) != meta.DesiredSHA256 || state.MintedGeneration != 3 || decoded != 1 {
		t.Fatal("failed callback corrupted cached proof or source recovery")
	}
}

func TestPolicyMintDoesNotCacheInvalidCanonicalBodies(t *testing.T) {
	_, r, body, meta, now := policyMintFixture(t)
	decoded := 0
	r.policyConfigDecode = func(body []byte) (policyConfigProof, error) { decoded++; return decodePolicyConfig(body) }
	if _, _, err := r.MintConfigWithPolicyCandidate(t.Context(), "agt_policy_mint", body, meta, now); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, _, err := r.MintConfigWithPolicyCandidate(t.Context(), "agt_policy_mint", append([]byte(" "), body...), meta, now); !errors.Is(err, domain.ErrValidation) {
			t.Fatalf("noncanonical body accepted: %v", err)
		}
	}
	if _, _, err := r.MintConfigWithPolicyCandidate(t.Context(), "agt_policy_mint", body, meta, now); err != nil {
		t.Fatal(err)
	}
	if decoded != 3 {
		t.Fatalf("invalid body cached or valid body redecoded: %d", decoded)
	}
}

func TestPolicyMintProofCacheSharesColdLoadsAndDoesNotTruncateOverBudget(t *testing.T) {
	_, r, body, meta, _ := policyMintFixture(t)
	var decoded atomic.Int32
	r.policyConfigDecode = func(body []byte) (policyConfigProof, error) { decoded.Add(1); return decodePolicyConfig(body) }
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			proof, err := r.policyConfig(body)
			if err != nil || proof.digest != meta.DesiredSHA256 || !proof.validMint(meta) {
				t.Errorf("concurrent proof: %+v / %v", proof, err)
			}
		})
	}
	wg.Wait()
	if decoded.Load() != 1 {
		t.Fatalf("cold proof decoded once per node: %d", decoded.Load())
	}
	// A payload exceeding the cache budget remains valid and complete; only
	// retention is omitted. This uses the same weighted cache as production.
	r.policyConfigProofs = boundedcache.New[policyConfigProof](64, 1)
	for range 2 {
		proof, err := r.policyConfig(body)
		if err != nil || proof.digest != meta.DesiredSHA256 || !proof.validMint(meta) {
			t.Fatalf("over-budget proof truncated: %+v / %v", proof, err)
		}
	}
	if decoded.Load() != 3 {
		t.Fatalf("over-budget proof retained: %d", decoded.Load())
	}
}
