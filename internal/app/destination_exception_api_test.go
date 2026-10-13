package app

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

func TestBuildDestinationGlobalExceptionsCreateAtomicallyAtAllowHeadAndAppend(t *testing.T) {
	a := buildDestinationListsFixture(t)
	token := destinationRefreshAdminToken(t, a)
	for i := 0; i < 2; i++ {
		input := destinationPolicyInput(fmt.Sprintf("Existing allow %d", i), "allow")
		input["enabled"] = i == 0
		w := destinationListRequest(t, a, token, "POST", "policies", input)
		if w.Code != 201 {
			t.Fatal("allow fixture failed")
		}
	}
	w := destinationListRequest(t, a, token, "POST", "exceptions", map[string]any{"target": "Login.Example.co.uk.", "match": "site", "scope": "global"})
	var first struct {
		ListID   int64  `json:"list_id"`
		PolicyID int64  `json:"policy_id"`
		Entry    string `json:"entry"`
		Created  *struct {
			ListID   int64 `json:"list_id"`
			PolicyID int64 `json:"policy_id"`
		} `json:"created"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &first); w.Code != 201 || err != nil || first.Created == nil || first.Created.ListID != first.ListID || first.Created.PolicyID != first.PolicyID || first.Entry != "domain:example.co.uk" {
		t.Fatalf("global exception did not create bundle: HTTP=%d", w.Code)
	}
	defs, err := a.destDefinitions.ReadDefinitions(t.Context())
	if err != nil || defs.State.Generation != 3 || len(defs.Lists) != 1 || len(defs.Policies) != 3 {
		t.Fatal("exception bundle did not use one definition commit")
	}
	for _, p := range defs.Policies {
		if p.ID == first.PolicyID {
			if p.Priority != 1 || p.Action != domain.DestAllow || p.Scope != domain.DestScopeAll || !p.Enabled || len(p.ListIDs) != 1 || p.ListIDs[0] != first.ListID {
				t.Fatal("exception rule did not lead global allow segment")
			}
		} else if p.Priority < 2 {
			t.Fatal("existing allow priorities were not shifted")
		}
	}
	var wg sync.WaitGroup
	results := make(chan int, 2)
	for _, target := range []string{"one.example.test", "two.example.test"} {
		wg.Go(func() {
			results <- destinationListRequest(t, a, token, "POST", "exceptions", map[string]any{"target": target, "match": "host", "scope": "global"}).Code
		})
	}
	wg.Wait()
	close(results)
	for code := range results {
		if code != 200 {
			t.Fatalf("exception append HTTP=%d", code)
		}
	}
	list, err := a.destDefinitions.GetList(t.Context(), first.ListID)
	if err != nil || list.EntryCount != 3 || !strings.Contains(string(list.Entries), "full:one.example.test") || !strings.Contains(string(list.Entries), "full:two.example.test") {
		t.Fatal("concurrent global exceptions lost content")
	}
	before, _ := a.destDefinitions.State(t.Context())
	version := list.UpdatedAt
	w = destinationListRequest(t, a, token, "POST", "exceptions", map[string]any{"target": "one.example.test", "match": "host", "scope": "global"})
	after, _ := a.destDefinitions.State(t.Context())
	list, err = a.destDefinitions.GetList(t.Context(), first.ListID)
	if w.Code != 200 || err != nil || after.Generation != before.Generation || !list.UpdatedAt.Equal(version) {
		t.Fatal("duplicate exception was not an idempotent no-op")
	}
}

func TestBuildDestinationGlobalExceptionInvalidInputsDoNotCreateBundle(t *testing.T) {
	a := buildDestinationListsFixture(t)
	token := destinationRefreshAdminToken(t, a)
	for _, input := range []map[string]any{
		{"target": "com", "match": "site", "scope": "global"},
		{"target": "regexp:.*", "match": "host", "scope": "global"},
		{"target": "example.test", "match": "wildcard", "scope": "global"},
		{"target": "example.test", "match": "host", "scope": "group"},
		{"target": "example.test", "match": "host", "scope": "group", "group_id": 0},
		{"target": "example.test", "match": "host", "scope": "global", "group_id": 1},
		{"target": "example.test", "match": "host", "scope": "global", "policy_id": 1},
	} {
		w := destinationListRequest(t, a, token, "POST", "exceptions", input)
		if w.Code != 400 {
			t.Fatalf("invalid exception HTTP=%d", w.Code)
		}
	}
	defs, err := a.destDefinitions.ReadDefinitions(t.Context())
	if err != nil || defs.State.Generation != 0 || len(defs.Lists) != 0 || len(defs.Policies) != 0 {
		t.Fatal("invalid exception left orphan definitions")
	}
	w := destinationListRequest(t, a, "", "POST", "exceptions", map[string]any{"target": "example.test", "match": "site", "scope": "global"})
	if w.Code != 401 {
		t.Fatalf("exception anonymous HTTP=%d", w.Code)
	}
}
