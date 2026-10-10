package handler

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/service/destlist"
	"github.com/gin-gonic/gin"
)

func TestCategoryHTTPReportsQueuedAndFailedRefreshWithAndWithoutCache(t *testing.T) {
	for _, cached := range []bool{false, true} {
		t.Run(map[bool]string{false: "missing", true: "cached"}[cached], func(t *testing.T) {
			root := t.TempDir()
			if cached {
				if err := os.MkdirAll(filepath.Join(root, "destlists"), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(root, "destlists", "dlc_plain.yml"), []byte(policyTemplateCatalog), 0600); err != nil {
					t.Fatal(err)
				}
			}
			h := NewAdminDestinationListsHandler(destlist.NewService(nil, destlist.NewGeositeCache(root)))
			var work func(context.Context)
			dispatched := 0
			h.SetDispatcher(func(_ string, job func(context.Context)) { work = job; dispatched++ })
			router := gin.New()
			router.GET("/categories", h.Categories)
			router.POST("/refresh", h.RefreshCategories)
			read := func(pending, failed bool) {
				t.Helper()
				response := httptest.NewRecorder()
				router.ServeHTTP(response, httptest.NewRequest("GET", "/categories", nil))
				want := 503
				if cached {
					want = 200
				}
				var view struct {
					Refreshing *bool               `json:"refreshing"`
					LastError  *string             `json:"last_error"`
					Categories []destlist.Category `json:"categories"`
				}
				if err := json.Unmarshal(response.Body.Bytes(), &view); err != nil || response.Code != want || view.Refreshing == nil || *view.Refreshing != pending || view.LastError == nil || (*view.LastError != "") != failed {
					t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
				}
				if cached && len(view.Categories) == 0 {
					t.Fatal("refresh lost cached categories")
				}
			}
			read(false, false)
			for range 2 {
				response := httptest.NewRecorder()
				router.ServeHTTP(response, httptest.NewRequest("POST", "/refresh", nil))
				if response.Code != 202 {
					t.Fatal(response.Code, response.Body.String())
				}
			}
			if dispatched != 1 {
				t.Fatal("duplicate download dispatched")
			}
			read(true, false)
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			work(ctx)
			read(false, true)
			h.SetDispatcher(nil)
			response := httptest.NewRecorder()
			router.ServeHTTP(response, httptest.NewRequest("POST", "/refresh", nil))
			if response.Code != 503 || response.Body.String() != `{"error":"dest_geosite_unavailable"}` {
				t.Fatal("unavailable dispatcher response changed", response.Code, response.Body.String())
			}
		})
	}
}
