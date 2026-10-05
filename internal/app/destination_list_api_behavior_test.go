package app

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/destpolicy"
)

func TestBuildDestinationListOverviewCountsDisabledMembersAndReferences(t *testing.T) {
	f := buildDestinationPolicyFixture(t)
	a := f.a
	token := destinationRefreshAdminToken(t, a)
	disabled := &domain.User{UPN: "list-disabled@example.test", Role: domain.RoleUser, Enabled: false, GroupID: f.group.ID, UUID: "list-disabled-uuid", SubToken: "list-disabled-sub"}
	if err := a.repos.User.Create(t.Context(), disabled); err != nil {
		t.Fatal(err)
	}
	created := destinationListRequest(t, a, token, "POST", "lists", map[string]any{"name": "referenced", "kind": "custom", "text": "domain:example.test\n"})
	var list struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &list); created.Code != 201 || err != nil {
		t.Fatal("list fixture failed")
	}
	p := saveWiringPolicy(t, f)
	p.ListIDs = []int64{list.ID}
	if err := a.destDefinitions.SavePolicy(t.Context(), p, p.UpdatedAt, time.Now()); err != nil {
		t.Fatal(err)
	}
	response := destinationListRequest(t, a, token, "GET", "lists", nil)
	var overview struct {
		Items []struct {
			ID      int64                   `json:"id"`
			UsedBy  []domain.DestReference  `json:"used_by"`
			Summary *domain.DestParseReport `json:"parse_report_summary"`
			Entries json.RawMessage         `json:"entries"`
			Report  json.RawMessage         `json:"parse_report"`
		} `json:"items"`
		Hours  int               `json:"refresh_hours"`
		Budget destpolicy.Budget `json:"budget"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &overview); response.Code != 200 || err != nil || len(overview.Items) != 1 {
		t.Fatalf("list overview HTTP=%d", response.Code)
	}
	item := overview.Items[0]
	if overview.Budget.Subjects.Used != 2 || overview.Budget.Domains.Used != 1 || overview.Budget.Rules.Used != 1 || overview.Hours != 24 || item.Summary == nil || len(item.UsedBy) != 1 || item.UsedBy[0].ID != p.ID || item.Entries != nil || item.Report != nil {
		t.Fatal("list overview lost full quota membership, refs, or returned full parse data")
	}
	deleted := destinationListRequest(t, a, token, "DELETE", fmt.Sprintf("lists/%d", list.ID), nil)
	if deleted.Code != 409 || !strings.Contains(deleted.Body.String(), "used_by") || !strings.Contains(deleted.Body.String(), "dest_list_in_use") {
		t.Fatal("list deletion omitted references")
	}
}

func TestBuildDestinationEntryPatchPreservesConcurrentAddsAndRejectsBroadAllow(t *testing.T) {
	a := buildDestinationListsFixture(t)
	token := destinationRefreshAdminToken(t, a)
	created := destinationListRequest(t, a, token, "POST", "lists", map[string]any{"name": "patch", "kind": "custom", "text": "# preserve me\n0.0.0.0 first.example.test second.example.test\n"})
	var view struct {
		ID         int64  `json:"id"`
		UpdatedAt  int64  `json:"updated_at"`
		EntryCount int    `json:"entry_count"`
		SourceText string `json:"source_text"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &view); created.Code != 201 || err != nil {
		t.Fatal("patch fixture failed")
	}
	path := fmt.Sprintf("lists/%d", view.ID)
	var wg sync.WaitGroup
	results := make(chan int, 2)
	for _, entry := range []string{"domain:third.example.test", "full:fourth.example.test"} {
		wg.Go(func() {
			results <- destinationListRequest(t, a, token, "POST", path+"/entries", map[string]any{"add": []string{entry}, "remove": []string{"first.example.test"}}).Code
		})
	}
	wg.Wait()
	close(results)
	for status := range results {
		if status != 200 {
			t.Fatalf("concurrent patch HTTP=%d", status)
		}
	}
	detail := destinationListRequest(t, a, token, "GET", path+"?text=1", nil)
	if err := json.Unmarshal(detail.Body.Bytes(), &view); err != nil || view.EntryCount != 3 || !strings.Contains(view.SourceText, "# preserve me") || !strings.Contains(view.SourceText, "second.example.test") || strings.Contains(view.SourceText, "first.example.test") {
		t.Fatal("concurrent patches lost added or remaining hosts-line content")
	}
	policy := domain.DestPolicy{Name: "patch allow", Action: domain.DestAllow, Enabled: false, Scope: domain.DestScopeAll, ListIDs: []int64{view.ID}}
	if err := a.destDefinitions.SavePolicy(t.Context(), &policy, time.Time{}, time.Now()); err != nil {
		t.Fatal(err)
	}
	before, err := a.destDefinitions.State(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	w := destinationListRequest(t, a, token, "POST", path+"/entries", map[string]any{"add": []string{"domain:com"}})
	if w.Code != 400 || !strings.Contains(w.Body.String(), "dest_list_too_broad") {
		t.Fatal("entry endpoint allowed broad input behind an allow reference")
	}
	after, _ := a.destDefinitions.State(t.Context())
	if before.Generation != after.Generation {
		t.Fatal("rejected patch advanced publication")
	}
}

func TestBuildDestinationGeositeCatalogFiltersBroadEntriesWithoutDownloading(t *testing.T) {
	const catalog = "lists:\n  - name: finance\n    length: 3\n    rules:\n      - domain:hsbc\n      - domain:hsbc.com:@cn\n      - full:login.example.test:@!cn\n"
	a := buildDestinationListsFixtureWithCatalog(t, []byte(catalog))
	token := destinationRefreshAdminToken(t, a)
	response := destinationListRequest(t, a, token, "GET", "geosite/categories", nil)
	var view struct {
		Categories []struct {
			Name        string   `json:"name"`
			Count       int      `json:"count"`
			SourceCount int      `json:"source_count"`
			Ignored     int      `json:"ignored_broad_count"`
			Attrs       []string `json:"attrs"`
		} `json:"categories"`
		UpdatedAt int64 `json:"updated_at"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &view); response.Code != 200 || err != nil || len(view.Categories) != 1 || view.Categories[0].Count != 2 || view.Categories[0].SourceCount != 3 || view.Categories[0].Ignored != 1 || view.UpdatedAt <= 0 {
		t.Fatal("cached category endpoint omitted filtering totals")
	}
	preview := destinationListRequest(t, a, token, "POST", "lists/preview", map[string]any{"name": "finance", "kind": "geosite", "geosite_category": "finance"})
	var result struct {
		Report domain.DestParseReport `json:"parse_report"`
	}
	if err := json.Unmarshal(preview.Body.Bytes(), &result); preview.Code != 200 || err != nil || result.Report.Accepted != 2 || result.Report.IgnoredBroad != 1 {
		t.Fatal("geosite preview did not expose ignored broad report")
	}
	created := destinationListRequest(t, a, token, "POST", "lists", map[string]any{"name": "finance", "kind": "geosite", "geosite_category": "finance"})
	if created.Code != 201 {
		t.Fatal("filtered finance category could not be saved")
	}
	var list struct {
		ID        int64 `json:"id"`
		UpdatedAt int64 `json:"updated_at"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	policy := domain.DestPolicy{Name: "filtered finance allow", Action: domain.DestAllow, Enabled: true, Scope: domain.DestScopeAll, ListIDs: []int64{list.ID}}
	if err := a.destDefinitions.SavePolicy(t.Context(), &policy, time.Time{}, time.Now()); err != nil {
		t.Fatal(err)
	}
	edited := destinationListRequest(t, a, token, "PUT", fmt.Sprintf("lists/%d", list.ID), map[string]any{"name": "finance renamed", "kind": "geosite", "geosite_category": "finance", "updated_at": list.UpdatedAt})
	if edited.Code != 200 {
		t.Fatal("ignored broad report blocked valid filtered finance edit under allow reference")
	}
}

func TestBuildDestinationManualRefreshCommitsAfterHTTPReturns(t *testing.T) {
	a := buildDestinationListsFixture(t)
	token := destinationRefreshAdminToken(t, a)
	list := destinationRefreshLegacySource(t, a, 24)
	response := destinationListRequest(t, a, token, "POST", fmt.Sprintf("lists/%d/refresh", list.ID), nil)
	if response.Code != 202 {
		t.Fatalf("manual refresh HTTP=%d", response.Code)
	}
	waitDestinationRefreshError(t, a, list.ID)
	detail := destinationListRequest(t, a, token, "GET", fmt.Sprintf("lists/%d", list.ID), nil)
	if !strings.Contains(detail.Body.String(), "dest_list_insecure_url") {
		t.Fatal("refresh error not exposed after real SQL commit")
	}
	missing := destinationListRequest(t, a, token, "GET", "geosite/categories", nil)
	if missing.Code != 503 || !strings.Contains(missing.Body.String(), "dest_geosite_unavailable") {
		t.Fatal("empty catalog was presented as available")
	}
}

func TestBuildDestinationListEditRejectsDefinitionQuotaBeforePersistence(t *testing.T) {
	a := buildDestinationListsFixture(t)
	token := destinationRefreshAdminToken(t, a)
	created := destinationListRequest(t, a, token, "POST", "lists", map[string]any{"name": "quota list", "kind": "custom", "text": "domain:example.test\n"})
	var view struct {
		ID        int64 `json:"id"`
		UpdatedAt int64 `json:"updated_at"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &view); created.Code != 201 || err != nil {
		t.Fatal("quota fixture failed")
	}
	policy := domain.DestPolicy{Name: "quota policy", Action: domain.DestBlock, Enabled: true, Scope: domain.DestScopeAll, ListIDs: []int64{view.ID}}
	if err := a.destDefinitions.SavePolicy(t.Context(), &policy, time.Time{}, time.Now()); err != nil {
		t.Fatal(err)
	}
	var text strings.Builder
	for i := 0; i < 257; i++ {
		fmt.Fprintf(&text, "regexp:^h%d\\.example\\.test$\n", i)
	}
	w := destinationListRequest(t, a, token, "PUT", fmt.Sprintf("lists/%d", view.ID), map[string]any{"name": "quota list", "kind": "custom", "text": text.String(), "updated_at": view.UpdatedAt})
	if w.Code != 400 || !strings.Contains(w.Body.String(), "dest_policy_over_limit") || !strings.Contains(w.Body.String(), "regexps") {
		t.Fatal("list endpoint skipped publication definition quota")
	}
	stored, err := a.destDefinitions.GetList(t.Context(), view.ID)
	state, _ := a.destDefinitions.State(t.Context())
	if err != nil || stored.EntryCount != 1 || state.Generation != 2 {
		t.Fatal("quota refusal changed list or publication")
	}
}
