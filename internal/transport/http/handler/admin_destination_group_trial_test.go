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

func TestDestinationGroupTrialStrictQueriesAndSafeFailures(t *testing.T) {
	calls := 0
	read := func(context.Context, int64, int) (destpolicy.GroupTrialReport, error) {
		calls++
		return destpolicy.GroupTrialReport{Items: []destpolicy.GroupTrialReportSite{{DestGroupTrialSite: domain.DestGroupTrialSite{Site: "private-partial.test"}}}}, errors.New("private-driver-detail")
	}
	r := gin.New()
	r.GET("/groups/:group_id/trial-report", NewAdminDestinationGroupTrialHandler(read).Get)
	for _, path := range []string{
		"/groups/0/trial-report", "/groups/-1/trial-report", "/groups/01/trial-report", "/groups/nope/trial-report",
		"/groups/7/trial-report?days=0", "/groups/7/trial-report?days=-1", "/groups/7/trial-report?days=01",
		"/groups/7/trial-report?days=", "/groups/7/trial-report?days=1&days=2", "/groups/7/trial-report?user_id=1",
		"/groups/7/trial-report?days=999999999999999999999999999999", "/groups/7/trial-report?days=%zz",
	} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != 400 || w.Header().Get("Cache-Control") != "no-store" {
			t.Errorf("invalid query %s HTTP=%d", path, w.Code)
		}
	}
	if calls != 0 {
		t.Fatal("invalid query invoked private report reader")
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/groups/7/trial-report", nil))
	if w.Code != 503 || calls != 1 || strings.Contains(w.Body.String(), "private") || strings.Contains(w.Body.String(), "items") || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("report failure leaked partial output or driver error")
	}
}
