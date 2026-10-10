package sqlstore

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-protocol/protocol"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/destpolicy"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/group"
	"gorm.io/gorm"
)

func TestDestinationEligibilityReadsOnlyModeAndEnforcementFacts(t *testing.T) {
	db, agent, _, _, now := policyMintFixture(t)
	if err := db.Create(&xuiPanelRow{ID: 81, Name: "native", Kind: string(domain.PanelKindPSP), URL: "psp://agt_policy_mint", APIToken: "enc:v1:invalid"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := agent.UpdateProtocolObservation(t.Context(), "agt_policy_mint", protocol.ProtocolVersion1, []string{protocol.CapabilityDestinationPolicy}, now); err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&destGroupModeRow{GroupID: 8, Mode: "allowlist", Stage: "trial"}).Error; err != nil {
		t.Fatal(err)
	}
	r := NewRepos(db).DestinationEligibility
	queries := 0
	db.Callback().Query().After("gorm:query").Register("eligibility-narrow", func(tx *gorm.DB) {
		queries++
		sql := strings.ToLower(tx.Statement.SQL.String())
		for _, forbidden := range []string{"select *", "password", "username", "token", "credential", "url", "minted_body", "applied_body", "list_ids"} {
			if strings.Contains(sql, forbidden) {
				tx.AddError(errors.New("eligibility query loaded unrelated or secret fields: " + forbidden))
			}
		}
	})
	t.Cleanup(func() { _ = db.Callback().Query().Remove("eligibility-narrow") })
	mode, err := r.GroupEligibilityMode(t.Context(), 8)
	if err != nil || mode != "allowlist" {
		t.Fatalf("trial mode: %s / %v", mode, err)
	}
	mode, err = r.GroupEligibilityMode(t.Context(), 99)
	if err != nil || mode != "open" {
		t.Fatalf("unset mode: %s / %v", mode, err)
	}
	state, err := r.PanelDestinationEligibility(t.Context(), 81)
	if err != nil || !state.Native || !state.PolicyCapable || state.FallbackBlocked {
		t.Fatalf("native facts without runtime row: %+v / %v", state, err)
	}
	if queries != 3 {
		t.Fatalf("unexpected selection queries: %d", queries)
	}
	if err := db.Create(&destAgentPolicyRow{AgentID: "agt_policy_mint", FallbackReason: "sniffing"}).Error; err != nil {
		t.Fatal(err)
	}
	state, err = r.PanelDestinationEligibility(t.Context(), 81)
	if err != nil || !state.FallbackBlocked {
		t.Fatalf("current fallback ignored: %+v / %v", state, err)
	}
	if err := db.Model(&destAgentPolicyRow{}).Where("agent_id = ?", "agt_policy_mint").UpdateColumns(map[string]any{"fallback_reason": "", "fallback_exhausted": true}).Error; err != nil {
		t.Fatal(err)
	}
	state, err = r.PanelDestinationEligibility(t.Context(), 81)
	if err != nil || !state.FallbackBlocked {
		t.Fatalf("exhaustion ignored: %+v / %v", state, err)
	}
	if err := agent.UpdateProtocolObservation(t.Context(), "agt_policy_mint", protocol.ProtocolVersion1, nil, now); err != nil {
		t.Fatal(err)
	}
	state, err = r.PanelDestinationEligibility(t.Context(), 81)
	if err != nil || state.PolicyCapable {
		t.Fatalf("removed capability retained: %+v / %v", state, err)
	}
}

func TestDestinationEligibilityTracksCommittedSQLObservationBeforeResync(t *testing.T) {
	db, agent, runtime, meta, now := policyObserverFixture(t)
	ctx := t.Context()
	caps := []string{protocol.CapabilityDestinationPolicy}
	if err := db.Create(&xuiPanelRow{ID: 81, Name: "native", Kind: "psp", URL: "psp://agt_policy_mint"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&xuiPanelRow{ID: 82, Name: "legacy", Kind: "3xui", URL: "https://legacy.invalid"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := agent.UpdateProtocolObservation(ctx, "agt_policy_mint", protocol.ProtocolVersion1, caps, now); err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&destGroupModeRow{GroupID: 8, Mode: "allowlist", Stage: "trial"}).Error; err != nil {
		t.Fatal(err)
	}
	repos := NewRepos(db)
	for _, panelID := range []int64{81, 82} {
		if err := repos.Node.Create(ctx, &domain.Node{PanelID: panelID, InboundID: 1, DisplayName: "eligibility", Enabled: true}); err != nil {
			t.Fatal(err)
		}
	}
	selector := group.New(repos.Group, repos.Node, nil)
	selector.SetDestinationEligibilityRepo(repos.DestinationEligibility)
	g := &domain.Group{ID: 8, TagFilter: domain.TagFilter{All: true}}
	check := func(ctx context.Context, want int) {
		t.Helper()
		nodes, err := selector.NodesFor(ctx, g)
		if err != nil || len(nodes) != want || want == 1 && nodes[0].PanelID != 81 {
			t.Fatalf("SQL eligibility membership: nodes=%d want=%d / %v", len(nodes), want, err)
		}
	}
	check(ctx, 1) // Warm both the mode and panel-fact caches.
	observer := destpolicy.NewObserver(runtime, func() time.Time { return now.Add(time.Minute) })
	calls, want := 0, 0
	observer.SetAllowlistResyncer(func(ctx context.Context, agentID string) {
		if agentID != "agt_policy_mint" {
			t.Fatal("resync crossed agent identity")
		}
		selector.InvalidateEligibility(81)
		check(ctx, want) // Models the membership read after enqueueing resync.
		calls++
	})
	if err := observer.ObserveStatus(ctx, "agt_policy_mint", &protocol.PolicyStatus{State: "rejected", Digest: meta.DesiredSHA256, IssueCode: protocol.IssueDestinationPolicyRejected}, caps); err != nil {
		t.Fatal(err)
	}
	check(ctx, 0)
	want = 1
	if err := observer.ObserveStatus(ctx, "agt_policy_mint", &protocol.PolicyStatus{State: "applied", Digest: meta.DesiredSHA256}, caps); err != nil {
		t.Fatal(err)
	}
	check(ctx, 1)
	if calls != 2 {
		t.Fatalf("committed rejection/recovery did not resync: %d", calls)
	}
	// Trial and enforce share eligibility. A capability loss excludes native
	// nodes immediately after invalidation, while open mode still selects both.
	if err := db.Model(&destGroupModeRow{}).Where("group_id = ?", 8).UpdateColumn("stage", "enforce").Error; err != nil {
		t.Fatal(err)
	}
	selector.InvalidateEligibility(0)
	check(ctx, 1)
	if err := agent.UpdateProtocolObservation(ctx, "agt_policy_mint", protocol.ProtocolVersion1, nil, now); err != nil {
		t.Fatal(err)
	}
	selector.InvalidateEligibility(81)
	check(ctx, 0)
	if err := db.Model(&destGroupModeRow{}).Where("group_id = ?", 8).UpdateColumns(map[string]any{"mode": "open", "stage": ""}).Error; err != nil {
		t.Fatal(err)
	}
	selector.InvalidateEligibility(0)
	nodes, err := selector.NodesFor(ctx, g)
	if err != nil || len(nodes) != 2 {
		t.Fatalf("open mode retained allowlist filtering: %d / %v", len(nodes), err)
	}
}

func TestDestinationEligibilityRejectsReadFailuresAndCorruptControl(t *testing.T) {
	db, _, _, _, _ := policyMintFixture(t)
	r := NewDestinationEligibilityRepo(db)
	if _, err := r.PanelDestinationEligibility(t.Context(), 81); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("missing panel: %v", err)
	}
	if _, err := r.GroupEligibilityMode(t.Context(), 0); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("invalid group: %v", err)
	}
	if _, err := r.PanelDestinationEligibility(t.Context(), 0); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("invalid panel: %v", err)
	}
	if _, err := NewDestinationEligibilityRepo(nil).GroupEligibilityMode(t.Context(), 8); !errors.Is(err, domain.ErrUnavailable) {
		t.Fatalf("missing DB: %v", err)
	}
	if err := db.Create(&xuiPanelRow{ID: 82, Name: "legacy", Kind: string(domain.PanelKind3XUI), URL: "https://legacy.invalid"}).Error; err != nil {
		t.Fatal(err)
	}
	state, err := r.PanelDestinationEligibility(t.Context(), 82)
	if err != nil || state.Native {
		t.Fatalf("legacy panel: %+v / %v", state, err)
	}
	if err := db.Model(&xuiPanelRow{}).Where("id = ?", 82).UpdateColumn("kind", "psp").Error; err != nil {
		t.Fatal(err)
	}
	if _, err := r.PanelDestinationEligibility(t.Context(), 82); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("missing native identity became false: %v", err)
	}
	if err := db.Create(&destGroupModeRow{GroupID: 8, Mode: "allowlist", Stage: "broken"}).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := r.GroupEligibilityMode(t.Context(), 8); !errors.Is(err, domain.ErrUnavailable) {
		t.Fatalf("corrupt stage accepted: %v", err)
	}
	if err := db.Create(&xuiPanelRow{ID: 81, Name: "native", Kind: "psp", URL: "psp://agt_policy_mint"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&nodeAgentRow{}).Where("agent_id = ?", "agt_policy_mint").UpdateColumn("observed_capabilities", "not-json").Error; err != nil {
		t.Fatal(err)
	}
	if _, err := r.PanelDestinationEligibility(t.Context(), 81); err == nil {
		t.Fatal("corrupt capabilities became a verdict")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := r.GroupEligibilityMode(ctx, 99); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled read: %v", err)
	}
}
