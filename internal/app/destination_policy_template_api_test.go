package app

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestBuildDestinationCategoryTemplatePreviewAndAtomicCreation(t *testing.T) {
	catalog := []byte(`lists:
  - name: category-cryptocurrency
    length: 3
    rules:
      - "domain:exchange.test"
      - "regexp:^trade[.]exchange[.]test$"
      - "domain:hsbc"
`)
	a := buildDestinationListsFixtureWithCatalog(t, catalog)
	token := destinationRefreshAdminToken(t, a)
	input := destinationPolicyInput("Exchanges", "observe")
	input["inline"] = map[string]any{}
	input["template_key"] = "crypto"
	input["counts_as_risk"] = false
	input["new_list"] = map[string]any{"name": "Exchange category", "kind": "geosite", "geosite_category": "category-cryptocurrency", "geosite_attrs": ""}
	w := destinationListRequest(t, a, token, "POST", "policies/preview", input)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"ignored_broad":1`) || !strings.Contains(w.Body.String(), `"new_list_preview"`) {
		t.Fatalf("combined template preview HTTP=%d", w.Code)
	}
	before, err := a.destDefinitions.ReadDefinitions(t.Context())
	if err != nil || before.State.Generation != 0 || len(before.Lists) != 0 || len(before.Policies) != 0 {
		t.Fatal("preview persisted template definitions")
	}
	w = destinationListRequest(t, a, token, "POST", "policies", input)
	var policy destinationPolicyAPIResult
	if err := json.Unmarshal(w.Body.Bytes(), &policy); w.Code != 201 || err != nil {
		t.Fatalf("combined template create HTTP=%d: %s", w.Code, w.Body.String())
	}
	after, err := a.destDefinitions.ReadDefinitions(t.Context())
	if err != nil || after.State.Generation != 1 || len(after.Lists) != 1 || len(after.Policies) != 1 {
		t.Fatal("creation did not persist one generation with both rows")
	}
	list := after.Lists[0]
	if len(after.Policies[0].ListIDs) != 1 || after.Policies[0].ListIDs[0] != list.ID || list.EntryCount != 2 || list.ParseReport == nil || list.ParseReport.IgnoredBroad != 1 || strings.Contains(string(list.Entries), "domain:hsbc") {
		t.Fatal("paired category identity or filtered report was lost")
	}
	w = destinationListRequest(t, a, token, "GET", fmt.Sprintf("lists/%d", list.ID), nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"ignored_broad":1`) {
		t.Fatal("saved category report is not available through normal list management")
	}
	input["new_list"] = map[string]any{"name": "Another category", "kind": "geosite", "geosite_category": "category-cryptocurrency"}
	w = destinationListRequest(t, a, token, "POST", "policies", input)
	if w.Code != 409 {
		t.Fatalf("duplicate template policy HTTP=%d", w.Code)
	}
	failed, err := a.destDefinitions.ReadDefinitions(t.Context())
	if err != nil || failed.State.Generation != after.State.Generation || len(failed.Lists) != 1 || len(failed.Policies) != 1 {
		t.Fatal("duplicate policy left a second category list")
	}
}
