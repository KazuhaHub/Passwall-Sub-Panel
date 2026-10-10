package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/jwtutil"
	"github.com/KazuhaHub/passwall-sub-panel/internal/transport/http/middleware"
	"github.com/gin-gonic/gin"
)

type usageAuditFixture struct {
	mu      sync.Mutex
	entries []domain.AuditEntry
	err     error
}

func TestUsageAuditCacheBoundsDistinctPairsWithoutEvictingLiveRecords(t *testing.T) {
	audit := &usageAuditFixture{}
	h := NewAdminDestinationUsageHandler(nil, audit)
	now := time.Now().UTC()
	h.now = func() time.Time { return now }
	q := domain.DestUsageQuery{UserID: 7, Since: now.Add(-time.Hour), Until: now, Limit: 20}
	for _, key := range []usageAuditKey{{actor: 1, user: 7}, {actor: 2, user: 7}, {actor: 1, user: 8}} {
		q.UserID = key.user
		if err := h.record(t.Context(), key, "admin@test", "/api/admin/dest/usage", q); err != nil {
			t.Fatal(err)
		}
	}
	if len(audit.entries) != 3 {
		t.Fatal("distinct administrator/account pair was not audited")
	}
	for i := len(h.records); i < 4096; i++ {
		h.records[usageAuditKey{actor: 99, user: int64(i + 100)}] = &usageAuditRecord{expires: now.Add(time.Minute)}
	}
	if err := h.record(t.Context(), usageAuditKey{actor: 100, user: 7}, "admin@test", "/api/admin/dest/usage", q); err == nil || len(h.records) != 4096 || len(audit.entries) != 3 {
		t.Fatal("capacity admitted an unaudited read or evicted a live record")
	}
	now = now.Add(2 * time.Minute)
	if err := h.record(t.Context(), usageAuditKey{actor: 100, user: 7}, "admin@test", "/api/admin/dest/usage", q); err != nil || len(h.records) != 4 || len(audit.entries) != 4 {
		t.Fatal("expired cache records did not release bounded admission")
	}
}

func (a *usageAuditFixture) Insert(_ context.Context, entry *domain.AuditEntry) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.err != nil {
		return a.err
	}
	a.entries = append(a.entries, *entry)
	return nil
}

func TestDestinationUsageReadsSharePrivateAuditAndRequireAccount(t *testing.T) {
	audit := &usageAuditFixture{}
	var mu sync.Mutex
	reads := 0
	h := NewAdminDestinationUsageHandler(func(_ context.Context, q domain.DestUsageQuery) (domain.DestUsagePage, error) {
		mu.Lock()
		reads++
		mu.Unlock()
		if q.UserID != 7 || q.Limit != 20 {
			t.Error("usage read lost account/top bound")
		}
		return domain.DestUsagePage{Items: []domain.DestUsageSite{{Site: "private-site.test", Count: 3}}}, nil
	}, audit)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set(middleware.CtxClaims, &jwtutil.Claims{UserID: 1, UPN: "admin@test", Role: domain.RoleAdmin})
	})
	r.GET("/api/admin/dest/usage", h.Get)
	r.GET("/api/admin/dest/users/:id", NewAdminDestinationUsersHandler(func(context.Context, int64) (domain.DestUserAccess, error) { return domain.DestUserAccess{}, nil }, h).Get)
	request := func(path string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		return w
	}
	for _, path := range []string{"/api/admin/dest/usage", "/api/admin/dest/usage?user_id=", "/api/admin/dest/usage?user_id=7&user_id=8", "/api/admin/dest/usage?user_id=7&q=secret", "/api/admin/dest/users/7?usage=0d", "/api/admin/dest/users/7?usage=1h", "/api/admin/dest/users/7?usage=24h&usage=1d"} {
		w := request(path)
		if w.Code != 400 {
			t.Errorf("%s HTTP=%d", path, w.Code)
		}
		if path == "/api/admin/dest/usage" && !strings.Contains(w.Body.String(), "dest_usage_user_required") {
			t.Error("missing required account error")
		}
	}
	if reads != 0 || len(audit.entries) != 0 {
		t.Fatal("invalid query read or audited private data")
	}
	if w := request("/api/admin/dest/users/7"); w.Code != 200 || strings.Contains(w.Body.String(), "usage_top") || reads != 0 {
		t.Fatal("base access read fetched usage")
	}
	var wg sync.WaitGroup
	for i := 0; i < 24; i++ {
		wg.Go(func() {
			if w := request("/api/admin/dest/usage?user_id=7&panel_id=9"); w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" {
				t.Error("usage read unavailable or cacheable")
			}
		})
	}
	wg.Wait()
	if w := request("/api/admin/dest/users/7?usage=24h"); w.Code != 200 || !strings.Contains(w.Body.String(), "usage_top") {
		t.Fatal("drawer usage boundary unavailable")
	}
	if len(audit.entries) != 1 {
		t.Fatalf("concurrent routes wrote %d audit rows, want 1", len(audit.entries))
	}
	e := audit.entries[0]
	var params map[string]any
	if e.Actor != "admin@test" || e.Action != "dest.usage.read" || e.Target != "/api/admin/dest/usage" || e.IP != "" || e.AfterJSON != "" || json.Unmarshal([]byte(e.BeforeJSON), &params) != nil || len(params) != 3 || params["user_id"] != float64(7) || params["since"] == nil || params["until"] == nil || strings.Contains(e.BeforeJSON, "private") {
		t.Fatalf("private audit contract: %+v", e)
	}
	h.now = func() time.Time { return time.Now().Add(11 * time.Minute) }
	if w := request("/api/admin/dest/usage?user_id=7"); w.Code != 200 || len(audit.entries) != 2 {
		t.Fatal("audit dedup did not expire")
	}
}

func TestDestinationUsageFailsClosedWithoutAuditAndSanitizesDependencies(t *testing.T) {
	audit := &usageAuditFixture{err: errors.New("private-audit-driver-marker")}
	h := NewAdminDestinationUsageHandler(func(context.Context, domain.DestUsageQuery) (domain.DestUsagePage, error) {
		return domain.DestUsagePage{Items: []domain.DestUsageSite{{Site: "private-site.test", Count: 9}}}, nil
	}, audit)
	role := domain.RoleAdmin
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set(middleware.CtxClaims, &jwtutil.Claims{UserID: 2, UPN: "admin2@test", Role: role})
	})
	r.GET("/api/admin/dest/usage", h.Get)
	request := func() *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest("GET", "/api/admin/dest/usage?user_id=7", nil))
		return w
	}
	if w := request(); w.Code != 503 || strings.Contains(w.Body.String(), "private") {
		t.Fatal("failed audit exposed data or driver detail")
	}
	audit.err = nil
	if w := request(); w.Code != 200 || len(audit.entries) != 1 {
		t.Fatal("audit failure incorrectly cached as success")
	}
	role = domain.RoleOperator
	if w := request(); w.Code != 403 || strings.Contains(w.Body.String(), "private") {
		t.Fatal("operator bypassed private read boundary")
	}
	role = domain.RoleAdmin
	h.read = func(context.Context, domain.DestUsageQuery) (domain.DestUsagePage, error) {
		return domain.DestUsagePage{}, errors.New("private-read-driver-marker")
	}
	if w := request(); w.Code != 503 || strings.Contains(w.Body.String(), "private") {
		t.Fatal("read dependency leaked private details")
	}
}
