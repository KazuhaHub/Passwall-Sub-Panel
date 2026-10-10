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
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/destlist"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/destpolicy"
)

func TestBuildDestinationPolicyOverviewUsesPublishedAccessDefinition(t *testing.T) {
	a := buildDestinationListsFixture(t)
	token := destinationRefreshAdminToken(t, a)
	check := func(want bool) {
		t.Helper()
		w := destinationListRequest(t, a, token, "GET", "policies", nil)
		var body struct {
			PublishedHasAccessControl *bool `json:"published_has_access_control"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &body); w.Code != 200 || err != nil || body.PublishedHasAccessControl == nil || *body.PublishedHasAccessControl != want {
			t.Fatalf("published access fact: HTTP=%d, want=%v", w.Code, want)
		}
	}
	check(false)
	input := destinationPolicyInput("First publish", "block")
	input["enabled"] = false
	w := destinationListRequest(t, a, token, "POST", "policies", input)
	var p destinationPolicyAPIResult
	if err := json.Unmarshal(w.Body.Bytes(), &p); w.Code != 201 || err != nil {
		t.Fatal("create disabled policy")
	}
	publish := func() {
		t.Helper()
		if err := destpolicy.NewPublisher(a.destDefinitions, nil).EnsurePublished(t.Context(), 60, true); err != nil {
			t.Fatal(err)
		}
	}
	publish()
	check(false)
	input["enabled"], input["updated_at"] = true, p.UpdatedAt
	w = destinationListRequest(t, a, token, "PUT", fmt.Sprintf("policies/%d", p.ID), input)
	if err := json.Unmarshal(w.Body.Bytes(), &p); w.Code != 200 || err != nil {
		t.Fatal("enable policy")
	}
	check(false) // Saved enabled definitions are not yet a published first policy.
	publish()
	check(true)
	for _, paused := range []bool{true, false} {
		w = destinationListRequest(t, a, token, "PUT", "pause", map[string]any{"paused": paused})
		if w.Code != 200 {
			t.Fatal("pause/resume policy fixture")
		}
		check(true) // Pausing retains enabled definitions for resume.
	}
	input["enabled"], input["updated_at"] = false, p.UpdatedAt
	w = destinationListRequest(t, a, token, "PUT", fmt.Sprintf("policies/%d", p.ID), input)
	if w.Code != 200 {
		t.Fatal("disable policy")
	}
	check(true) // The published version still has access control until removal deploys.
	publish()
	check(false)
}

func TestBuildDestinationPolicyOverviewIncludesPublishedAllowlistOnly(t *testing.T) {
	f := buildDestinationPolicyFixture(t)
	a := f.a
	token := destinationRefreshAdminToken(t, a)
	prepared, err := destlist.ParseCustom([]byte("domain:example.test\n"))
	if err != nil {
		t.Fatal(err)
	}
	owned := [2]domain.DestList{}
	for i := range owned {
		owned[i] = domain.DestList{Name: fmt.Sprintf("Allowlist fact %d", i), Kind: domain.DestListCustom, Entries: prepared.Entries, ContentSHA256: prepared.ContentSHA256, EntryCount: prepared.EntryCount, ParseReport: &prepared.Report}
	}
	mode := &domain.DestGroupMode{GroupID: f.group.ID, Mode: "allowlist", Stage: "trial"}
	if err := a.destDefinitions.SaveGroupMode(t.Context(), mode, time.Time{}, owned, time.Now()); err != nil {
		t.Fatal(err)
	}
	publisher := destpolicy.NewPublisher(a.destDefinitions, nil)
	if err := publisher.EnsurePublished(t.Context(), 60, true); err != nil {
		t.Fatal(err)
	}
	check := func(want bool) {
		t.Helper()
		w := destinationListRequest(t, a, token, "GET", "policies", nil)
		var body struct {
			PublishedHasAccessControl *bool `json:"published_has_access_control"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &body); w.Code != 200 || err != nil || body.PublishedHasAccessControl == nil || *body.PublishedHasAccessControl != want {
			t.Fatalf("published allowlist-only fact: HTTP=%d want=%v", w.Code, want)
		}
	}
	check(true)
	version := mode.UpdatedAt
	mode.Mode, mode.Stage = "open", ""
	if err := a.destDefinitions.SaveGroupMode(t.Context(), mode, version, [2]domain.DestList{}, time.Now()); err != nil {
		t.Fatal(err)
	}
	check(true) // Saved open mode has not removed the published allowlist yet.
	if err := publisher.EnsurePublished(t.Context(), 60, true); err != nil {
		t.Fatal(err)
	}
	check(false)
}

func TestBuildDestinationPolicyWritesDoNotDecodePublishedOverviewFacts(t *testing.T) {
	a := buildDestinationListsFixture(t)
	token := destinationRefreshAdminToken(t, a)
	if w := destinationListRequest(t, a, token, "POST", "policies", destinationPolicyInput("Published fixture", "block")); w.Code != 201 {
		t.Fatal("published policy fixture")
	}
	if err := destpolicy.NewPublisher(a.destDefinitions, nil).EnsurePublished(t.Context(), 60, true); err != nil {
		t.Fatal(err)
	}
	if _, err := a.database.ExecContext(t.Context(), "UPDATE dest_policy_snapshots SET body = ?", []byte(`{"schema":9}`)); err != nil {
		t.Fatal(err)
	}
	w := destinationListRequest(t, a, token, "GET", "policies", nil)
	if w.Code == 200 {
		t.Fatal("corrupt snapshot must not fabricate a published first-enable fact")
	}
	for _, path := range []string{"policies/preview", "policies"} {
		w = destinationListRequest(t, a, token, "POST", path, destinationPolicyInput("Repair draft", "block"))
		want := 200
		if path == "policies" {
			want = 201
		}
		if w.Code != want {
			t.Fatalf("published overview decode obstructed draft repair: %s HTTP=%d", path, w.Code)
		}
	}
}

func TestBuildDestinationPolicyAPICompilesAndUsesFullQuotaMembership(t *testing.T) {
	f := buildDestinationPolicyFixture(t)
	a := f.a
	token := destinationRefreshAdminToken(t, a)
	disabled := &domain.User{UPN: "policy-disabled@example.test", Role: domain.RoleUser, Enabled: false, GroupID: f.group.ID, UUID: "policy-disabled-uuid", SubToken: "policy-disabled-sub"}
	if err := a.repos.User.Create(t.Context(), disabled); err != nil {
		t.Fatal(err)
	}
	input := destinationPolicyInput("Scoped SMTP", "block")
	input["scope"] = "groups"
	input["group_ids"] = []int64{f.group.ID, f.group.ID}
	w := destinationListRequest(t, a, token, "POST", "policies", input)
	var p destinationPolicyAPIResult
	if err := json.Unmarshal(w.Body.Bytes(), &p); w.Code != 201 || err != nil || len(p.GroupIDs) != 1 {
		t.Fatalf("scoped policy create HTTP=%d", w.Code)
	}
	w = destinationListRequest(t, a, token, "GET", "policies", nil)
	var overview struct {
		Budget destpolicy.Budget `json:"budget"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &overview); w.Code != 200 || err != nil || overview.Budget.Subjects.Used != 2 {
		t.Fatal("policy quota omitted disabled user without client")
	}
	if err := destpolicy.NewPublisher(a.destDefinitions, nil).EnsurePublished(t.Context(), 60, true); err != nil {
		t.Fatal(err)
	}
	f.report.Capabilities = []string{protocol.CapabilityDestinationPolicy}
	response := syncNativeCacheFixture(t, a, f.credential, f.report)
	if response.Config.Body == nil || response.Config.Body.Policy == nil || len(response.Config.Body.Policy.Rules) != 1 {
		t.Fatal("HTTP policy did not reach native config candidate")
	}
	rule := response.Config.Body.Policy.Rules[0]
	if rule.ID != fmt.Sprintf("p%d", p.ID) || rule.Ports != "25" || len(rule.Subjects) != 1 || rule.Subjects[0] != protocol.NewSubjectKey(f.user.ID) {
		t.Fatal("HTTP policy confused full quota with current delivered roster")
	}
	state, err := a.repos.DestAgentPolicy.Get(t.Context(), f.agent.AgentID, true)
	if err != nil || state.DesiredSHA256 != protocol.PolicyDigest(response.Config.Body.Policy) || len(state.MintedBody) == 0 {
		t.Fatal("HTTP policy bypassed atomic candidate mint")
	}
}

func TestBuildDestinationPolicyConcurrentCASAndNoOp(t *testing.T) {
	a := buildDestinationListsFixture(t)
	token := destinationRefreshAdminToken(t, a)
	input := destinationPolicyInput("CAS", "block")
	w := destinationListRequest(t, a, token, "POST", "policies", input)
	var p destinationPolicyAPIResult
	if err := json.Unmarshal(w.Body.Bytes(), &p); w.Code != 201 || err != nil {
		t.Fatal("policy fixture failed")
	}
	input["updated_at"] = p.UpdatedAt
	w = destinationListRequest(t, a, token, "PUT", fmt.Sprintf("policies/%d", p.ID), input)
	var noOp destinationPolicyAPIResult
	state, _ := a.destDefinitions.State(t.Context())
	if err := json.Unmarshal(w.Body.Bytes(), &noOp); w.Code != 200 || err != nil || noOp.UpdatedAt != p.UpdatedAt || state.Generation != 1 {
		t.Fatal("unchanged HTTP edit advanced generation/version")
	}
	var wg sync.WaitGroup
	results := make(chan int, 2)
	for _, name := range []string{"CAS first", "CAS second"} {
		wg.Go(func() {
			edit := destinationPolicyInput(name, "block")
			edit["updated_at"] = p.UpdatedAt
			results <- destinationListRequest(t, a, token, "PUT", fmt.Sprintf("policies/%d", p.ID), edit).Code
		})
	}
	wg.Wait()
	close(results)
	success, conflict := 0, 0
	for status := range results {
		switch status {
		case 200:
			success++
		case 409:
			conflict++
		default:
			t.Fatalf("concurrent edit HTTP=%d", status)
		}
	}
	state, _ = a.destDefinitions.State(t.Context())
	if success != 1 || conflict != 1 || state.Generation != 2 {
		t.Fatal("concurrent CAS lost update or advanced generation twice")
	}
}

func TestBuildDestinationPolicyRejectsEmptyPrivateAndQuotaWithoutWrites(t *testing.T) {
	f := buildDestinationPolicyFixture(t)
	a := f.a
	token := destinationRefreshAdminToken(t, a)
	empty := domain.DestList{Name: "empty draft", Kind: domain.DestListCustom}
	if err := a.destDefinitions.SaveList(t.Context(), &empty, time.Time{}, time.Now()); err != nil {
		t.Fatal(err)
	}
	prepared, err := destlist.ParseCustom([]byte("domain:example.test\n"))
	if err != nil {
		t.Fatal(err)
	}
	owned := [2]domain.DestList{}
	for i := range owned {
		owned[i] = domain.DestList{Name: fmt.Sprintf("Owned %d", i), Kind: domain.DestListCustom, SourceText: []byte("domain:example.test\n"), Entries: prepared.Entries, ContentSHA256: prepared.ContentSHA256, EntryCount: prepared.EntryCount, ParseReport: &prepared.Report}
	}
	mode := &domain.DestGroupMode{GroupID: f.group.ID, Mode: "allowlist", Stage: "trial"}
	if err := a.destDefinitions.SaveGroupMode(t.Context(), mode, time.Time{}, owned, time.Now()); err != nil {
		t.Fatal(err)
	}
	before, _ := a.destDefinitions.State(t.Context())
	for _, test := range []struct {
		id   int64
		code string
	}{{empty.ID, "dest_policy_no_match"}, {mode.BaseListID, "dest_policy_invalid"}} {
		input := destinationPolicyInput("Invalid referenced list", "allow")
		input["list_ids"] = []int64{test.id}
		w := destinationListRequest(t, a, token, "POST", "policies", input)
		if w.Code != 400 || !strings.Contains(w.Body.String(), test.code) {
			t.Fatalf("list precondition HTTP=%d", w.Code)
		}
	}
	after, _ := a.destDefinitions.State(t.Context())
	if after.Generation != before.Generation {
		t.Fatal("invalid list reference changed definitions")
	}
	// Use a separate fixture so allowlist rules do not contribute regexp quota.
	b := buildDestinationListsFixture(t)
	auth := destinationRefreshAdminToken(t, b)
	var source strings.Builder
	for i := 0; i < 128; i++ {
		fmt.Fprintf(&source, "regexp:^h%d\\.example\\.test$\n", i)
	}
	w := destinationListRequest(t, b, auth, "POST", "lists", map[string]any{"name": "regex source", "kind": "custom", "text": source.String()})
	var list struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &list); w.Code != 201 || err != nil {
		t.Fatal("quota list fixture failed")
	}
	for i := 0; i < 2; i++ {
		input := destinationPolicyInput(fmt.Sprintf("quota %d", i), "block")
		input["list_ids"] = []int64{list.ID}
		w = destinationListRequest(t, b, auth, "POST", "policies", input)
		if w.Code != 201 {
			t.Fatalf("valid quota policy HTTP=%d", w.Code)
		}
	}
	input := destinationPolicyInput("quota overflow", "block")
	input["list_ids"] = []int64{list.ID}
	for _, path := range []string{"policies/preview", "policies"} {
		w = destinationListRequest(t, b, auth, "POST", path, input)
		if w.Code != 400 || !strings.Contains(w.Body.String(), "dest_policy_over_limit") || !strings.Contains(w.Body.String(), "regexps") {
			t.Fatal("policy quota was not checked before preview/save")
		}
	}
	defs, err := b.destDefinitions.ReadDefinitions(t.Context())
	if err != nil || defs.State.Generation != 3 || len(defs.Policies) != 2 {
		t.Fatal("quota refusal partially persisted")
	}
}

func TestBuildDestinationPolicyOverviewReportsMissingScopeAndPendingLists(t *testing.T) {
	a := buildDestinationListsFixture(t)
	token := destinationRefreshAdminToken(t, a)
	list := domain.DestList{Name: "Pending remote", Kind: domain.DestListRemote, SourceURL: "https://example.test/rules"}
	if err := a.destDefinitions.SaveList(t.Context(), &list, time.Time{}, time.Now()); err != nil {
		t.Fatal(err)
	}
	policy := domain.DestPolicy{Name: "Deleted scope", Action: domain.DestObserve, Enabled: false, Scope: domain.DestScopeGroups, GroupIDs: []int64{987654}, ListIDs: []int64{list.ID}}
	if err := a.destDefinitions.SavePolicy(t.Context(), &policy, time.Time{}, time.Now()); err != nil {
		t.Fatal(err)
	}
	w := destinationListRequest(t, a, token, "PUT", "settings", map[string]any{"settings": map[string]any{"dest_hit_retention_days": 3, "dest_trial_retention_days": 3}})
	if w.Code != 200 {
		t.Fatal("retention fixture failed")
	}
	w = destinationListRequest(t, a, token, "GET", "policies", nil)
	var overview struct {
		Observe []struct {
			ScopeMissing bool `json:"scope_missing"`
			ListStates   []struct {
				State string `json:"state"`
			} `json:"list_states"`
			Hits json.RawMessage `json:"hits_recent"`
		} `json:"observe"`
		Window int `json:"hit_window_days"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &overview); w.Code != 200 || err != nil || overview.Window != 3 || len(overview.Observe) != 1 || !overview.Observe[0].ScopeMissing || len(overview.Observe[0].ListStates) != 1 || overview.Observe[0].ListStates[0].State != "pending" || string(overview.Observe[0].Hits) != "null" {
		t.Fatal("policy overview lost missing scope/pending source/unknown hit semantics")
	}
}

func TestBuildDestinationPolicyHTTPGenerationFailureRollsBackDefinition(t *testing.T) {
	a := buildDestinationListsFixture(t)
	token := destinationRefreshAdminToken(t, a)
	_, err := a.database.ExecContext(t.Context(), "CREATE TRIGGER destination_policy_api_failure BEFORE UPDATE OF generation ON dest_policy_state BEGIN SELECT RAISE(ABORT, 'private-generation-marker'); END")
	if err != nil {
		t.Fatal(err)
	}
	w := destinationListRequest(t, a, token, "POST", "policies", destinationPolicyInput("Rolled back", "block"))
	if w.Code != 500 || strings.Contains(w.Body.String(), "private-generation-marker") || strings.Contains(w.Body.String(), "\"id\"") {
		t.Fatal("failed transaction exposed SQL details or success identity")
	}
	defs, err := a.destDefinitions.ReadDefinitions(t.Context())
	if err != nil || defs.State.Generation != 0 || len(defs.Policies) != 0 {
		t.Fatal("HTTP generation failure retained a partial policy")
	}
	form := domain.DestPolicy{Name: "Uncommitted form", Action: domain.DestBlock, Scope: domain.DestScopeAll, Enabled: true, Inline: domain.DestInline{Ports: "25"}}
	if err := a.destAdmin.Save(t.Context(), &form, time.Time{}); err == nil || form.ID != 0 || form.Priority != 0 || !form.UpdatedAt.IsZero() {
		t.Fatal("failed save changed caller form identity/version")
	}
}
