package handler

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/destpolicy"
	"github.com/gin-gonic/gin"
)

func TestDestinationGroupReadsRequireCanonicalPathsAndWithholdPartialFailures(t *testing.T) {
	calls := 0
	h := NewAdminDestinationGroupsHandler(nil)
	h.SetReaders(func(context.Context) ([]destpolicy.GroupAccessSummary, error) {
		calls++
		return []destpolicy.GroupAccessSummary{{GroupName: "private-partial-group"}}, errors.New("private-driver-detail")
	}, func(context.Context, int64) (destpolicy.GroupAccessDetail, error) {
		calls++
		return destpolicy.GroupAccessDetail{GroupAccessSummary: destpolicy.GroupAccessSummary{GroupName: "private-partial-group"}}, errors.New("private-driver-detail")
	})
	r := gin.New()
	r.GET("/groups", h.List)
	r.GET("/groups/:group_id", h.Get)
	for _, path := range []string{"/groups?days=1", "/groups?x=1&x=2", "/groups/0", "/groups/01", "/groups/nope", "/groups/7?days=1"} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != 400 || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("group read accepted unsafe parameters")
		}
	}
	if calls != 0 {
		t.Fatal("invalid read invoked private dependency")
	}
	for _, path := range []string{"/groups", "/groups/7"} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != 503 || strings.Contains(w.Body.String(), "private") || strings.Contains(w.Body.String(), "items") || strings.Contains(w.Body.String(), "readiness") {
			t.Fatal("failed group read exposed partial data")
		}
	}
	h.SetReaders(func(context.Context) ([]destpolicy.GroupAccessSummary, error) {
		return []destpolicy.GroupAccessSummary{}, nil
	}, func(context.Context, int64) (destpolicy.GroupAccessDetail, error) {
		return destpolicy.GroupAccessDetail{}, domain.ErrNotFound
	})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/groups/7", nil))
	if w.Code != 404 || !strings.Contains(w.Body.String(), "dest_group_not_found") {
		t.Fatal("missing group error lost")
	}
	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/groups", nil))
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"items":[]`) {
		t.Fatal("empty fleet became unavailable or null")
	}
}
