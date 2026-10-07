package handler

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/destpolicy"
	"github.com/gin-gonic/gin"
)

func TestPolicyOverviewSeparatesListRefreshStateFromUsableContent(t *testing.T) {
	at := time.Now().UTC()
	for _, test := range []struct {
		name             string
		kind             domain.DestListKind
		count            int
		fetched          *time.Time
		lastError, state string
		available        bool
	}{
		{"cached failure", domain.DestListRemote, 1, &at, "dest_list_fetch_failed", "failed", true},
		{"first failure", domain.DestListRemote, 0, nil, "dest_list_fetch_failed", "failed", false},
		{"first download", domain.DestListGeosite, 0, nil, "", "pending", false},
		{"custom ready", domain.DestListCustom, 1, nil, "", "ready", true},
		{"custom empty", domain.DestListCustom, 0, nil, "", "empty", false},
		{"downloaded empty", domain.DestListRemote, 0, &at, "", "ready", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := &templatePolicyStore{defs: domain.DestDefinitions{
				Lists:    []domain.DestList{{ID: 1, Name: "List", Kind: test.kind, EntryCount: test.count, LastFetchedAt: test.fetched, LastError: test.lastError}},
				Policies: []domain.DestPolicy{{ID: 1, Name: "Policy", Action: domain.DestBlock, Scope: domain.DestScopeAll, Enabled: true, ListIDs: []int64{1, 99}}},
			}}
			admin := destpolicy.NewAdministrator(store, func(context.Context) (destpolicy.AdministrationContext, error) {
				return destpolicy.AdministrationContext{}, nil
			},
				func(context.Context, domain.DestDefinitions) (destpolicy.Budget, error) {
					return destpolicy.Budget{}, nil
				})
			router := gin.New()
			router.GET("/policies", NewAdminDestinationPoliciesHandler(admin, nil).List)
			w := httptest.NewRecorder()
			router.ServeHTTP(w, httptest.NewRequest("GET", "/policies", nil))
			var result struct {
				Block []struct {
					Lists []struct {
						State     string `json:"state"`
						Available *bool  `json:"available"`
					} `json:"list_states"`
				} `json:"block"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil || w.Code != 200 || len(result.Block) != 1 || len(result.Block[0].Lists) != 2 {
				t.Fatalf("overview HTTP %d: %s", w.Code, w.Body.String())
			}
			list, missing := result.Block[0].Lists[0], result.Block[0].Lists[1]
			if list.State != test.state || list.Available == nil || *list.Available != test.available {
				t.Fatalf("list state=%q availability=%v, want %q/%v", list.State, list.Available, test.state, test.available)
			}
			if missing.State != "missing" || missing.Available == nil || *missing.Available {
				t.Fatal("missing list was not reported unavailable")
			}
		})
	}
}
