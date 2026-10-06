package handler

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/destlist"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/destpolicy"
	"github.com/gin-gonic/gin"
)

type templatePolicyStore struct {
	defs            domain.DestDefinitions
	pairs, ordinary int
}

func (s *templatePolicyStore) ReadDefinitions(context.Context) (domain.DestDefinitions, error) {
	return s.defs, nil
}
func (s *templatePolicyStore) SavePolicy(context.Context, *domain.DestPolicy, time.Time, time.Time) error {
	s.ordinary++
	return nil
}
func (s *templatePolicyStore) DeletePolicy(context.Context, int64, time.Time) error { return nil }
func (s *templatePolicyStore) ReorderPolicies(context.Context, domain.DestAction, []int64, time.Time) error {
	return nil
}
func (s *templatePolicyStore) SavePolicyWithList(_ context.Context, policy *domain.DestPolicy, list *domain.DestList, at time.Time) error {
	s.pairs++
	list.ID, list.CreatedAt, list.UpdatedAt = 41, at, at
	policy.ID, policy.CreatedAt, policy.UpdatedAt, policy.Priority = 13, at, at, 1
	policy.ListIDs = append(policy.ListIDs, list.ID)
	s.defs.Lists, s.defs.Policies = append(s.defs.Lists, *list), append(s.defs.Policies, *policy)
	s.defs.State.Generation++
	return nil
}

const policyTemplateCatalog = `lists:
  - name: category-cryptocurrency
    length: 4
    rules:
      - "domain:exchange.test"
      - "full:login.exchange.test"
      - "regexp:^trade[.]exchange[.]test$"
      - "domain:hsbc"
`

const policyTemplateInput = `{"name":"Exchanges","action":"observe","list_ids":[],"inline":{},"scope":"all","group_ids":[],"enabled":true,"counts_as_risk":false,"template_key":"crypto","new_list":{"name":"Exchange category","kind":"geosite","geosite_category":"category-cryptocurrency","geosite_attrs":""}}`

func templatePolicyHandler(t *testing.T, cached bool) (*gin.Engine, *templatePolicyStore) {
	t.Helper()
	path := t.TempDir()
	if cached {
		if err := os.MkdirAll(filepath.Join(path, "destlists"), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(path, "destlists", "dlc_plain.yml"), []byte(policyTemplateCatalog), 0600); err != nil {
			t.Fatal(err)
		}
	}
	store := &templatePolicyStore{}
	admin := destpolicy.NewAdministrator(store, func(context.Context) (destpolicy.AdministrationContext, error) {
		return destpolicy.AdministrationContext{}, nil
	}, func(_ context.Context, defs domain.DestDefinitions) (destpolicy.Budget, error) {
		return destpolicy.DefinitionBudget(defs, nil)
	})
	handler := NewAdminDestinationPoliciesHandler(admin, destlist.NewService(nil, destlist.NewGeositeCache(path)))
	router := gin.New()
	router.POST("/policies", handler.Create)
	router.POST("/preview", handler.Preview)
	router.PUT("/policies/:id", handler.Put)
	return router, store
}

func TestPolicyTemplatePreviewUsesFilteredCachedCategoryWithoutSaving(t *testing.T) {
	router, store := templatePolicyHandler(t, true)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest("POST", "/preview", strings.NewReader(policyTemplateInput)))
	var response struct {
		Budget destpolicy.Budget `json:"budget"`
		List   struct {
			EntryCount int                    `json:"entry_count"`
			Report     domain.DestParseReport `json:"parse_report"`
		} `json:"new_list_preview"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); w.Code != 200 || err != nil {
		t.Fatalf("template preview HTTP=%d: %s", w.Code, w.Body.String())
	}
	if response.Budget.Rules.Used != 1 || response.Budget.Regexps.Used != 1 || response.List.EntryCount != 3 || response.List.Report.IgnoredBroad != 1 {
		t.Fatalf("preview lost actual category or filtered report: %+v", response)
	}
	if store.pairs != 0 || store.ordinary != 0 || len(store.defs.Lists) != 0 || len(store.defs.Policies) != 0 || store.defs.State.Generation != 0 {
		t.Fatal("opening/previewing a template wrote definitions")
	}
}

func TestPolicyTemplateCreateSavesPolicyAndFilteredListTogether(t *testing.T) {
	router, store := templatePolicyHandler(t, true)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest("POST", "/policies", strings.NewReader(policyTemplateInput)))
	if w.Code != 201 {
		t.Fatalf("template create HTTP=%d: %s", w.Code, w.Body.String())
	}
	if store.pairs != 1 || store.ordinary != 0 || len(store.defs.Lists) != 1 || len(store.defs.Policies) != 1 || store.defs.State.Generation != 1 {
		t.Fatal("template did not use one paired definition write")
	}
	list, policy := store.defs.Lists[0], store.defs.Policies[0]
	if list.Kind != domain.DestListGeosite || list.EntryCount != 3 || list.ParseReport == nil || list.ParseReport.IgnoredBroad != 1 || strings.Contains(string(list.Entries), "domain:hsbc") || list.LastFetchedAt == nil {
		t.Fatalf("saved list lost filtering/provenance: %+v", list)
	}
	if policy.Action != domain.DestObserve || policy.CountsAsRisk || !policy.Enabled || policy.TemplateKey != "crypto" || len(policy.ListIDs) != 1 || policy.ListIDs[0] != list.ID {
		t.Fatalf("saved policy changed template defaults or list identity: %+v", policy)
	}
}

func TestPolicyTemplateMissingCatalogAndUpdateDoNotCreateLists(t *testing.T) {
	for _, method := range []string{"POST", "PUT"} {
		t.Run(method, func(t *testing.T) {
			router, store := templatePolicyHandler(t, false)
			path, input, want := "/policies", policyTemplateInput, 503
			if method == "PUT" {
				path, input, want = "/policies/13", strings.Replace(policyTemplateInput, `"new_list":`, `"updated_at":2000,"new_list":`, 1), 400
			}
			w := httptest.NewRecorder()
			router.ServeHTTP(w, httptest.NewRequest(method, path, strings.NewReader(input)))
			if w.Code != want {
				t.Fatalf("template %s HTTP=%d, want=%d", method, w.Code, want)
			}
			if store.pairs != 0 || store.ordinary != 0 {
				t.Fatal("unavailable/invalid template wrote definitions")
			}
		})
	}
}

func TestPolicyTemplateRejectsUnallocatedReferencesAndNonCategorySources(t *testing.T) {
	for name, input := range map[string]string{
		"future-reference":      strings.Replace(policyTemplateInput, `"list_ids":[]`, `"list_ids":[1]`, 1),
		"remote":                strings.Replace(policyTemplateInput, `"kind":"geosite"`, `"kind":"remote","source_url":"https://source.invalid/private"`, 1),
		"custom":                strings.Replace(policyTemplateInput, `"kind":"geosite"`, `"kind":"custom","text":"exchange.test"`, 1),
		"category-edit-version": strings.Replace(policyTemplateInput, `"kind":"geosite"`, `"kind":"geosite","updated_at":2000`, 1),
	} {
		t.Run(name, func(t *testing.T) {
			for _, path := range []string{"/preview", "/policies"} {
				router, store := templatePolicyHandler(t, true)
				w := httptest.NewRecorder()
				router.ServeHTTP(w, httptest.NewRequest("POST", path, strings.NewReader(input)))
				if w.Code != 400 {
					t.Fatalf("%s %s HTTP=%d: %s", name, path, w.Code, w.Body.String())
				}
				if store.pairs != 0 || store.ordinary != 0 {
					t.Fatal("invalid combined source wrote definitions")
				}
			}
		})
	}
}
