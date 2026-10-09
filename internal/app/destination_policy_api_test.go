package app

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/jwtutil"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

type destinationPolicyAPIResult struct {
	ID         int64   `json:"id"`
	Name       string  `json:"name"`
	Action     string  `json:"action"`
	Priority   int     `json:"priority"`
	UpdatedAt  int64   `json:"updated_at"`
	Enabled    bool    `json:"enabled"`
	GroupIDs   []int64 `json:"group_ids"`
	HitsRecent *int64  `json:"hits_recent"`
	LastHitAt  *int64  `json:"last_hit_at"`
}

func destinationPolicyInput(name, action string) map[string]any {
	return map[string]any{"name": name, "action": action, "list_ids": []int64{}, "inline": map[string]any{"ports": "25"}, "scope": "all", "group_ids": []int64{}, "enabled": true, "counts_as_risk": false, "template_key": ""}
}

func TestBuildDestinationPolicyCRUDPreviewAndPriority(t *testing.T) {
	a := buildDestinationListsFixture(t)
	token := destinationRefreshAdminToken(t, a)
	input := destinationPolicyInput("Mail", "block")
	w := destinationListRequest(t, a, token, "POST", "policies/preview", input)
	var preview struct {
		Budget json.RawMessage `json:"budget"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &preview); w.Code != 200 || err != nil || len(preview.Budget) == 0 {
		t.Fatalf("policy preview did not return budget: HTTP %d", w.Code)
	}
	defs, err := a.destDefinitions.ReadDefinitions(t.Context())
	if err != nil || defs.State.Generation != 0 || len(defs.Policies) != 0 {
		t.Fatal("policy preview wrote definitions")
	}
	w = destinationListRequest(t, a, token, "POST", "policies", input)
	var first destinationPolicyAPIResult
	if err := json.Unmarshal(w.Body.Bytes(), &first); w.Code != 201 || err != nil || first.ID <= 0 || first.UpdatedAt <= 0 || first.Priority != 1 {
		t.Fatalf("policy create omitted version/priority: HTTP %d", w.Code)
	}
	previewEdit := destinationPolicyInput("Mail preview", "observe")
	previewEdit["id"], previewEdit["updated_at"] = first.ID, first.UpdatedAt
	w = destinationListRequest(t, a, token, "POST", "policies/preview", previewEdit)
	var editBudget struct {
		Budget struct {
			Rules struct {
				Used int `json:"used"`
			} `json:"rules"`
		} `json:"budget"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &editBudget); w.Code != 200 || err != nil || editBudget.Budget.Rules.Used != 1 {
		t.Fatal("edit preview appended instead of replacing the current rule")
	}
	defs, err = a.destDefinitions.ReadDefinitions(t.Context())
	if err != nil || defs.State.Generation != 1 || len(defs.Policies) != 1 || defs.Policies[0].Name != "Mail" {
		t.Fatal("edit preview persisted hypothetical form")
	}
	input2 := destinationPolicyInput("Second", "block")
	input2["enabled"] = false
	w = destinationListRequest(t, a, token, "POST", "policies", input2)
	var second destinationPolicyAPIResult
	if err := json.Unmarshal(w.Body.Bytes(), &second); w.Code != 201 || err != nil || second.Priority != 2 || second.Enabled {
		t.Fatal("disabled policy did not receive server priority")
	}
	w = destinationListRequest(t, a, token, "POST", "policies", input2)
	if w.Code != 409 || !strings.Contains(w.Body.String(), "dest_name_taken") {
		t.Fatal("duplicate policy name omitted stable conflict code")
	}
	input["name"], input["updated_at"] = "Edited mail", first.UpdatedAt
	w = destinationListRequest(t, a, token, "PUT", fmt.Sprintf("policies/%d", first.ID), input)
	var edited destinationPolicyAPIResult
	if err := json.Unmarshal(w.Body.Bytes(), &edited); w.Code != 200 || err != nil || edited.Priority != 1 || edited.UpdatedAt <= first.UpdatedAt {
		t.Fatal("same-action edit changed priority or failed version")
	}
	w = destinationListRequest(t, a, token, "PUT", fmt.Sprintf("policies/%d", first.ID), input)
	if w.Code != 409 || !strings.Contains(w.Body.String(), "dest_policy_stale") {
		t.Fatal("policy stale PUT not rejected")
	}
	w = destinationListRequest(t, a, token, "PUT", "policies/order", map[string]any{"action": "block", "ids": []int64{second.ID}})
	if w.Code != 409 || !strings.Contains(w.Body.String(), "dest_policy_order_stale") {
		t.Fatal("disabled policy omitted from reorder validation")
	}
	w = destinationListRequest(t, a, token, "PUT", "policies/order", map[string]any{"action": "block", "ids": []int64{second.ID, first.ID}})
	if w.Code != 204 {
		t.Fatalf("complete order HTTP=%d", w.Code)
	}
	defs, err = a.destDefinitions.ReadDefinitions(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range defs.Policies {
		if p.ID == first.ID {
			input["updated_at"] = p.UpdatedAt.UnixMilli()
		}
	}
	input["action"] = "allow"
	w = destinationListRequest(t, a, token, "PUT", fmt.Sprintf("policies/%d", first.ID), input)
	if err := json.Unmarshal(w.Body.Bytes(), &edited); w.Code != 200 || err != nil || edited.Action != "allow" || edited.Priority != 1 {
		t.Fatal("action change did not append to destination segment")
	}
	w = destinationListRequest(t, a, token, "GET", "policies", nil)
	var overview struct {
		Allow, Block, Observe []destinationPolicyAPIResult
		Budget                json.RawMessage `json:"budget"`
		HitWindowDays         int             `json:"hit_window_days"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &overview); w.Code != 200 || err != nil || len(overview.Allow) != 1 || len(overview.Block) != 1 || len(overview.Observe) != 0 || overview.HitWindowDays != 7 || len(overview.Budget) == 0 {
		t.Fatal("policy overview omitted action segments/window/budget")
	}
	if overview.Allow[0].HitsRecent != nil || overview.Allow[0].LastHitAt != nil {
		t.Fatal("pre-ingestion overview fabricated hit history")
	}
	w = destinationListRequest(t, a, token, "DELETE", fmt.Sprintf("policies/%d", first.ID), nil)
	if w.Code != 204 {
		t.Fatalf("policy delete HTTP=%d", w.Code)
	}
	w = destinationListRequest(t, a, token, "DELETE", fmt.Sprintf("policies/%d", first.ID), nil)
	if w.Code != 404 {
		t.Fatal("missing policy delete was not missing")
	}
}

func TestBuildDestinationPolicyValidationAndAuthorization(t *testing.T) {
	a := buildDestinationListsFixture(t)
	token := destinationRefreshAdminToken(t, a)
	for _, test := range []struct {
		name   string
		mutate func(map[string]any)
		code   string
	}{
		{"no match", func(v map[string]any) { v["inline"] = map[string]any{} }, "dest_policy_no_match"},
		{"forged priority", func(v map[string]any) { v["priority"] = 1 }, "dest_policy_invalid"},
		{"unknown action", func(v map[string]any) { v["action"] = "reject" }, "dest_policy_invalid"},
		{"missing enable flag", func(v map[string]any) { delete(v, "enabled") }, "dest_policy_invalid"},
		{"missing group", func(v map[string]any) { v["scope"] = "groups"; v["group_ids"] = []int64{987654} }, "dest_policy_invalid"},
		{"bad port", func(v map[string]any) { v["inline"] = map[string]any{"ports": "70000"} }, "dest_policy_invalid"},
	} {
		t.Run(test.name, func(t *testing.T) {
			input := destinationPolicyInput(test.name, "block")
			test.mutate(input)
			w := destinationListRequest(t, a, token, "POST", "policies", input)
			if w.Code != 400 || !strings.Contains(w.Body.String(), test.code) {
				t.Fatalf("invalid policy accepted: HTTP=%d", w.Code)
			}
		})
	}
	defs, err := a.destDefinitions.ReadDefinitions(t.Context())
	if err != nil || defs.State.Generation != 0 || len(defs.Policies) != 0 {
		t.Fatal("failed policy validation partially persisted")
	}
	u := &domain.User{UPN: "policy-operator@example.test", Role: domain.RoleOperator, Enabled: true, UUID: "policy-operator-uuid", SubToken: "policy-operator-sub"}
	if err := a.repos.User.Create(t.Context(), u); err != nil {
		t.Fatal(err)
	}
	s, err := a.settings.Load(t.Context(), ports.UISettings{})
	if err != nil {
		t.Fatal(err)
	}
	operator, err := jwtutil.NewIssuer(a.cfg.JWTSecret, func() jwtutil.Params {
		return jwtutil.Params{AccessTTL: time.Hour, RefreshTTL: time.Hour, Issuer: s.JWTIssuer}
	}).IssueAccess(u.ID, u.UPN, u.Role, u.TokenVersion)
	if err != nil {
		t.Fatal(err)
	}
	for _, route := range []struct{ method, path string }{{"GET", "policies"}, {"POST", "policies"}, {"POST", "policies/preview"}, {"PUT", "policies/1"}, {"DELETE", "policies/1"}, {"PUT", "policies/order"}} {
		for _, auth := range []struct {
			token  string
			status int
		}{{"", 401}, {operator, 403}} {
			w := destinationListRequest(t, a, auth.token, route.method, route.path, destinationPolicyInput("Denied", "block"))
			if w.Code != auth.status {
				t.Fatalf("%s %s authorization HTTP=%d", route.method, route.path, w.Code)
			}
		}
	}
}
