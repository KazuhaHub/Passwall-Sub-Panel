package app

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-protocol/protocol"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/destpolicy"
)

func TestBuildDestinationExemptionExpiryRemainsPublishedUntilDurableCleanup(t *testing.T) {
	f := buildDestinationPolicyFixture(t)
	a := f.a
	token := destinationRefreshAdminToken(t, a)
	w := destinationListRequest(t, a, token, "POST", "policies", destinationPolicyInput("Exemption block", "block"))
	if w.Code != 201 {
		t.Fatal("block fixture failed")
	}
	w = destinationListRequest(t, a, token, "POST", "exemptions", map[string]any{"user_id": f.user.ID, "reason": "Expiry debugging", "expires_at": time.Now().Add(-time.Minute).UnixMilli()})
	if w.Code != 201 {
		t.Fatal("exemption fixture failed")
	}
	if err := destpolicy.NewPublisher(a.destDefinitions, nil).EnsurePublished(t.Context(), 60, true); err != nil {
		t.Fatal(err)
	}
	f.report.Capabilities = []string{protocol.CapabilityDestinationPolicy}
	before := syncNativeCacheFixture(t, a, f.credential, f.report)
	if before.Config.Body == nil || before.Config.Body.Policy == nil || len(before.Config.Body.Policy.Exempt) != 1 || before.Config.Body.Policy.Exempt[0] != protocol.NewSubjectKey(f.user.ID) {
		t.Fatal("expired row was silently removed before cleanup/publication")
	}
	removed, err := a.destDefinitions.PruneExpiredExemptions(t.Context(), time.Now())
	if err != nil || removed != 1 {
		t.Fatal("durable expiry cleanup failed")
	}
	if err := destpolicy.NewPublisher(a.destDefinitions, nil).EnsurePublished(t.Context(), 60, true); err != nil {
		t.Fatal(err)
	}
	after := syncNativeCacheFixture(t, a, f.credential, f.report)
	if after.Config.Body == nil || after.Config.Body.Policy == nil || len(after.Config.Body.Policy.Exempt) != 0 || protocol.PolicyDigest(after.Config.Body.Policy) == protocol.PolicyDigest(before.Config.Body.Policy) {
		t.Fatal("cleanup did not remove published exemption from actual candidate")
	}
}

func TestBuildDestinationExemptionConcurrentCreateAndDeleteFailure(t *testing.T) {
	f := buildDestinationPolicyFixture(t)
	a := f.a
	token := destinationRefreshAdminToken(t, a)
	var wg sync.WaitGroup
	results := make(chan int, 2)
	for _, reason := range []string{"first debugging", "second debugging"} {
		wg.Go(func() {
			results <- destinationListRequest(t, a, token, "POST", "exemptions", map[string]any{"user_id": f.user.ID, "reason": reason}).Code
		})
	}
	wg.Wait()
	close(results)
	created, conflict := 0, 0
	for code := range results {
		switch code {
		case 201:
			created++
		case 409:
			conflict++
		default:
			t.Fatalf("concurrent exemption HTTP=%d", code)
		}
	}
	state, _ := a.destDefinitions.State(t.Context())
	if created != 1 || conflict != 1 || state.Generation != 1 {
		t.Fatal("concurrent exemption creation advanced twice")
	}
	if _, err := a.database.ExecContext(t.Context(), "CREATE TRIGGER exemption_api_failure BEFORE UPDATE OF generation ON dest_policy_state BEGIN SELECT RAISE(ABORT, 'private-exemption-marker'); END"); err != nil {
		t.Fatal(err)
	}
	w := destinationListRequest(t, a, token, "DELETE", fmt.Sprintf("exemptions/%d", f.user.ID), nil)
	if w.Code != 500 || strings.Contains(w.Body.String(), "private-exemption-marker") {
		t.Fatal("failed exemption deletion hid rollback or exposed SQL")
	}
	w = destinationListRequest(t, a, token, "GET", fmt.Sprintf("exemptions/%d", f.user.ID), nil)
	state, _ = a.destDefinitions.State(t.Context())
	if w.Code != 200 || state.Generation != 1 {
		t.Fatal("failed deletion removed exemption or advanced generation")
	}
}

func TestBuildDestinationGlobalExceptionFailurePreservesPrioritiesAndDefinitions(t *testing.T) {
	a := buildDestinationListsFixture(t)
	token := destinationRefreshAdminToken(t, a)
	w := destinationListRequest(t, a, token, "POST", "policies", destinationPolicyInput("Existing allow", "allow"))
	if w.Code != 201 {
		t.Fatal("allow fixture failed")
	}
	before, err := a.destDefinitions.ReadDefinitions(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.database.ExecContext(t.Context(), "CREATE TRIGGER exception_api_failure BEFORE UPDATE OF generation ON dest_policy_state BEGIN SELECT RAISE(ABORT, 'private-exception-marker'); END"); err != nil {
		t.Fatal(err)
	}
	w = destinationListRequest(t, a, token, "POST", "exceptions", map[string]any{"target": "example.test", "match": "site", "scope": "global"})
	if w.Code != 500 || strings.Contains(w.Body.String(), "private-exception-marker") || strings.Contains(w.Body.String(), "\"created\"") {
		t.Fatal("failed exception bundle exposed creation identity or SQL")
	}
	after, err := a.destDefinitions.ReadDefinitions(t.Context())
	if err != nil || after.State.Generation != before.State.Generation || len(after.Lists) != 0 || len(after.Policies) != 1 || after.Policies[0].Priority != before.Policies[0].Priority || !after.Policies[0].UpdatedAt.Equal(before.Policies[0].UpdatedAt) {
		t.Fatal("failed bundle retained shifted priorities or orphan rows")
	}
}

func TestBuildDestinationGlobalExceptionIdentityCannotBeForgedOrSilentlyReenabled(t *testing.T) {
	a := buildDestinationListsFixture(t)
	token := destinationRefreshAdminToken(t, a)
	forged := destinationPolicyInput("Forged exception", "allow")
	forged["template_key"] = domain.DestGlobalExceptionTemplateKey
	w := destinationListRequest(t, a, token, "POST", "policies", forged)
	if w.Code != 400 {
		t.Fatal("ordinary policy creation took exception identity")
	}
	w = destinationListRequest(t, a, token, "POST", "exceptions", map[string]any{"target": "example.test", "match": "host", "scope": "global"})
	var result struct {
		PolicyID int64 `json:"policy_id"`
		ListID   int64 `json:"list_id"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); w.Code != 201 || err != nil {
		t.Fatal("exception fixture failed")
	}
	defs, err := a.destDefinitions.ReadDefinitions(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	var p domain.DestPolicy
	for _, row := range defs.Policies {
		if row.ID == result.PolicyID {
			p = row
		}
	}
	edit := destinationPolicyInput(p.Name, "allow")
	edit["list_ids"] = p.ListIDs
	edit["inline"] = map[string]any{}
	edit["updated_at"] = p.UpdatedAt.UnixMilli()
	edit["template_key"] = ""
	w = destinationListRequest(t, a, token, "PUT", fmt.Sprintf("policies/%d", p.ID), edit)
	if w.Code != 400 {
		t.Fatal("ordinary policy edit erased stable exception identity")
	}
	edit["template_key"] = p.TemplateKey
	edit["enabled"] = false
	w = destinationListRequest(t, a, token, "PUT", fmt.Sprintf("policies/%d", p.ID), edit)
	if w.Code != 200 {
		t.Fatal("explicit disable fixture failed")
	}
	before, _ := a.destDefinitions.State(t.Context())
	w = destinationListRequest(t, a, token, "POST", "exceptions", map[string]any{"target": "other.example.test", "match": "host", "scope": "global"})
	after, _ := a.destDefinitions.State(t.Context())
	list, err := a.destDefinitions.GetList(t.Context(), result.ListID)
	if w.Code != 409 || err != nil || list.EntryCount != 1 || before.Generation != after.Generation {
		t.Fatal("exception append silently reenabled an edited bundle")
	}
}

func TestBuildDestinationGlobalExceptionPublishedCandidateOverridesBlockForSelectedSite(t *testing.T) {
	f := buildDestinationPolicyFixture(t)
	a := f.a
	token := destinationRefreshAdminToken(t, a)
	w := destinationListRequest(t, a, token, "POST", "policies", destinationPolicyInput("SMTP block", "block"))
	if w.Code != 201 {
		t.Fatal("block fixture failed")
	}
	w = destinationListRequest(t, a, token, "POST", "exceptions", map[string]any{"target": "login.example.test", "match": "site", "scope": "global"})
	if w.Code != 201 {
		t.Fatal("exception fixture failed")
	}
	if err := destpolicy.NewPublisher(a.destDefinitions, nil).EnsurePublished(t.Context(), 60, true); err != nil {
		t.Fatal(err)
	}
	f.report.Capabilities = []string{protocol.CapabilityDestinationPolicy}
	response := syncNativeCacheFixture(t, a, f.credential, f.report)
	if response.Config.Body == nil || response.Config.Body.Policy == nil {
		t.Fatal("exception did not reach native candidate")
	}
	policy := response.Config.Body.Policy
	if len(policy.Rules) != 2 || policy.Rules[0].Action != protocol.RuleAllow || policy.Rules[1].Action != protocol.RuleBlock {
		t.Fatal("native candidate lost exception-before-block ordering")
	}
	subject := protocol.NewSubjectKey(f.user.ID)
	if got := protocol.MatchDestination(policy, subject, "login.example.test", 25, "tcp"); got.Verdict != "allow" {
		t.Fatal("published exception did not allow its selected site")
	}
	if got := protocol.MatchDestination(policy, subject, "other.unrelated.test", 25, "tcp"); got.Verdict != "block" {
		t.Fatal("exception broadened beyond its selected site")
	}
}

func TestBuildDestinationGlobalExceptionDefinitionQuotaFailureCreatesNoBundle(t *testing.T) {
	a := buildDestinationListsFixture(t)
	token := destinationRefreshAdminToken(t, a)
	tx, err := a.database.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	now := time.Now().UTC().Truncate(time.Millisecond)
	for i := 0; i < protocol.MaxDestinationRules; i++ {
		if _, err := tx.ExecContext(t.Context(), "INSERT INTO dest_policies (name, action, inline, scope, priority, enabled, template_key, created_at, updated_at) VALUES (?, 'block', '{\"ports\":\"25\"}', 'all', ?, 1, '', ?, ?)", fmt.Sprintf("Rule quota %d", i), i+1, now, now); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	w := destinationListRequest(t, a, token, "POST", "exceptions", map[string]any{"target": "example.test", "match": "host", "scope": "global"})
	if w.Code != 400 || !strings.Contains(w.Body.String(), "dest_policy_over_limit") || !strings.Contains(w.Body.String(), "rules") {
		t.Fatal("exception creation skipped definition quota")
	}
	defs, err := a.destDefinitions.ReadDefinitions(t.Context())
	if err != nil || defs.State.Generation != 0 || len(defs.Lists) != 0 || len(defs.Policies) != protocol.MaxDestinationRules {
		t.Fatal("quota rejection left partial exception definitions")
	}
}
