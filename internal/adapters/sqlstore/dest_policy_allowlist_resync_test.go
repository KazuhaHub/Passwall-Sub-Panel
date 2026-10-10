package sqlstore

import (
	"context"
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

func TestDestinationObserverAllowlistResyncFollowsCommittedEligibility(t *testing.T) {
	db, mint, repo, meta, now := policyObserverFixture(t)
	o := destpolicy.NewObserver(repo, func() time.Time { return now.Add(time.Minute) })
	caps := []string{protocol.CapabilityDestinationPolicy}
	calls, blocked := 0, true
	o.SetAllowlistResyncer(func(ctx context.Context, agentID string) {
		calls++
		state, err := repo.Get(ctx, agentID, false)
		if err != nil || agentID != "agt_policy_mint" || (state.FallbackReason != "" || state.FallbackExhausted) != blocked {
			t.Fatalf("resync ran before durable eligibility change: %+v / %v", state, err)
		}
	})
	rejected := &protocol.PolicyStatus{State: "rejected", Digest: meta.DesiredSHA256, IssueCode: protocol.IssueDestinationPolicyRejected}
	failure := errors.New("failed policy observation")
	db.Callback().Update().Before("gorm:update").Register("failed-allowlist-observation", func(tx *gorm.DB) {
		if tx.Statement.Table == "dest_agent_policy" {
			tx.AddError(failure)
		}
	})
	t.Cleanup(func() { _ = db.Callback().Update().Remove("failed-allowlist-observation") })
	if err := o.ObserveStatus(t.Context(), "agt_policy_mint", rejected, caps); !errors.Is(err, failure) || calls != 0 {
		t.Fatalf("uncommitted rejection resynced: calls=%d / %v", calls, err)
	}
	if err := db.Callback().Update().Remove("failed-allowlist-observation"); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := o.ObserveStatus(t.Context(), "agt_policy_mint", rejected, caps); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 1 {
		t.Fatalf("matching rejection/idle: %d", calls)
	}
	if err := o.ObserveStatus(t.Context(), "agt_policy_mint", &protocol.PolicyStatus{State: "applied", Digest: strings.Repeat("f", 64)}, caps); err != nil || calls != 1 {
		t.Fatalf("stale report changed eligibility: calls=%d / %v", calls, err)
	}
	blocked = false
	for range 2 {
		if err := o.ObserveStatus(t.Context(), "agt_policy_mint", &protocol.PolicyStatus{State: "applied", Digest: meta.DesiredSHA256}, caps); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 2 {
		t.Fatalf("desired recovery/idle: %d", calls)
	}
	// Exhaustion blocks the allowlist even if the reason column was cleared
	// independently. Do not base the notification solely on that one column.
	policy := &protocol.DestinationPolicy{Rules: []protocol.DestinationRule{{ID: "p1", Action: protocol.RuleBlock, Ports: "80"}}}
	body, err := json.Marshal(protocol.ConfigBody{Listeners: []protocol.Listener{}, Policy: policy})
	if err != nil {
		t.Fatal(err)
	}
	meta.Kind = domain.DestCandidateFallback
	if _, _, err := mint.MintConfigWithPolicyCandidate(t.Context(), "agt_policy_mint", body, meta, now); err != nil {
		t.Fatal(err)
	}
	blocked = true
	status := &protocol.PolicyStatus{State: "rejected", Digest: protocol.PolicyDigest(policy), IssueCode: protocol.IssueDestinationPolicyRejected}
	for range 2 {
		if err := o.ObserveStatus(t.Context(), "agt_policy_mint", status, caps); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 3 {
		t.Fatalf("fallback exhaustion/idle: %d", calls)
	}
}
