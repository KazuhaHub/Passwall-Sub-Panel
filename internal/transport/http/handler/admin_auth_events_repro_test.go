package handler

import (
	"context"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/KazuhaHub/passwall-sub-panel/internal/adapters/mysql"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/geo"
)

// Exercises GET /api/admin/auth-events end-to-end (handler + repo + query +
// marshal) on a real SQLite repo. No gin.Recovery, so any panic surfaces as the
// test stack. (The beta.5 500 was a nil AuthEvent repo from app.go's wiring,
// not this path — guarded separately by mysql.TestNewReposPopulatesEveryDBRepo.)
func TestAuthEventsListHandler(t *testing.T) {
	db, err := mysql.Open("sqlite", filepath.Join(t.TempDir(), "p.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if s, e := db.DB(); e == nil {
			_ = s.Close()
		}
	})
	if err := mysql.EnsureSchema(db); err != nil {
		t.Fatal(err)
	}
	repos := mysql.NewRepos(db)
	if err := repos.AuthEvent.Insert(context.Background(), &domain.AuthEvent{
		UserID: 8, UPN: "x@y", Method: domain.AuthMethodLocal, Outcome: domain.AuthOutcomeSuccess, IP: "1.2.3.4", At: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	h := NewAdminAuthEventsHandler(repos.AuthEvent, geo.New(repos.Settings, t.TempDir()))
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/x", h.List)
	for _, url := range []string{"/x?page=1&page_size=100", "/x?user_id=8&page_size=8"} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest("GET", url, nil))
		if w.Code != 200 {
			t.Fatalf("%s -> %d: %s", url, w.Code, w.Body.String())
		}
	}
}
