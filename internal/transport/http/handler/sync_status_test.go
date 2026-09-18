package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/jwtutil"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/user"
	"github.com/KazuhaHub/passwall-sub-panel/internal/transport/http/middleware"
)

type syncUsersFake struct {
	ports.UserRepo
	rows []*domain.User
	err  error
}

func (f *syncUsersFake) GetByID(_ context.Context, id int64) (*domain.User, error) {
	if f.err != nil {
		return nil, f.err
	}
	for _, u := range f.rows {
		if u.ID == id {
			return u, nil
		}
	}
	return nil, domain.ErrNotFound
}

type syncTasksFake struct {
	ports.SyncTaskRepo
	active []*domain.SyncTask
	err    error
}

func (f *syncTasksFake) ListActiveByTarget(context.Context, []domain.SyncTaskType, string, int64, int) ([]*domain.SyncTask, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.active, nil
}

func (f *syncTasksFake) ListTerminalByTarget(context.Context, []domain.SyncTaskType, string, int64, int) ([]*domain.SyncTask, error) {
	if f.err != nil {
		return nil, f.err
	}
	return nil, nil
}

func syncStatusAdminHandler(users *syncUsersFake, tasks *syncTasksFake) *AdminUserHandler {
	return &AdminUserHandler{user: user.New(users, nil, nil, tasks, nil, nil, nil, nil)}
}

// callWithClaims runs a handler with the given role and :id param, mirroring
// what RequireAuth leaves in the context.
func callWithClaims(t *testing.T, role domain.Role, id string, h func(*gin.Context)) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	if role != "" {
		c.Set(middleware.CtxClaims, &jwtutil.Claims{UserID: 1, Role: role})
	}
	c.Params = gin.Params{{Key: "id", Value: id}}
	h(c)
	return rec
}

func decodeSyncStatus(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v (body %s)", err, rec.Body.String())
	}
	return body
}

func syncStatusErrorCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		Code string `json:"code"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	return body.Code
}

func TestAdminSyncStatusAdminMayInspectMissingTarget(t *testing.T) {
	// Deleting a user is exactly when an admin wants to see what its tasks did,
	// so a gone row is an answer, not a 404.
	h := syncStatusAdminHandler(&syncUsersFake{}, &syncTasksFake{})
	rec := callWithClaims(t, domain.RoleAdmin, "404", h.GetSyncStatus)

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, want 200 for an admin reading a missing target", rec.Code)
	}
	if got := decodeSyncStatus(t, rec)["target_exists"]; got != false {
		t.Fatalf("target_exists = %v, want false", got)
	}
}

func TestAdminSyncStatusOperatorMayNotSeeMissingTarget(t *testing.T) {
	h := syncStatusAdminHandler(&syncUsersFake{}, &syncTasksFake{})
	rec := callWithClaims(t, domain.RoleOperator, "404", h.GetSyncStatus)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status %d, want 404 — only an admin may observe a deleted target", rec.Code)
	}
}

func TestAdminSyncStatusOperatorMayNotSeeStaffAccounts(t *testing.T) {
	users := &syncUsersFake{rows: []*domain.User{{ID: 9, Role: domain.RoleAdmin}}}
	h := syncStatusAdminHandler(users, &syncTasksFake{})
	rec := callWithClaims(t, domain.RoleOperator, "9", h.GetSyncStatus)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status %d, want 403 for an operator reading an admin account", rec.Code)
	}
}

func TestAdminSyncStatusOperatorMaySeeRegularUsers(t *testing.T) {
	users := &syncUsersFake{rows: []*domain.User{{ID: 9, Role: domain.RoleUser}}}
	h := syncStatusAdminHandler(users, &syncTasksFake{})
	rec := callWithClaims(t, domain.RoleOperator, "9", h.GetSyncStatus)

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, want 200 for an operator reading a regular user", rec.Code)
	}
}

// A lookup fault during the operator gate must fail CLOSED. Waving it through
// would let a transient DB error grant access to a staff account.
func TestAdminSyncStatusOperatorGateFailsClosedOnLookupError(t *testing.T) {
	users := &syncUsersFake{err: errors.New("pool exhausted")}
	h := syncStatusAdminHandler(users, &syncTasksFake{})
	rec := callWithClaims(t, domain.RoleOperator, "9", h.GetSyncStatus)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status %d, want 503 — an unverifiable target must not be waved through", rec.Code)
	}
}

func TestAdminSyncStatusStoreFailureIs503WithStableCode(t *testing.T) {
	users := &syncUsersFake{rows: []*domain.User{{ID: 7, Role: domain.RoleUser}}}
	tasks := &syncTasksFake{err: errors.New("db down")}
	h := syncStatusAdminHandler(users, tasks)
	rec := callWithClaims(t, domain.RoleAdmin, "7", h.GetSyncStatus)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status %d, want 503", rec.Code)
	}
	if code := syncStatusErrorCode(t, rec); code != syncStatusUnavailableCode {
		t.Fatalf("code %q, want %q", code, syncStatusUnavailableCode)
	}
}

func TestAdminSyncStatusRequiresAuth(t *testing.T) {
	h := syncStatusAdminHandler(&syncUsersFake{}, &syncTasksFake{})
	rec := callWithClaims(t, "", "7", h.GetSyncStatus)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status %d, want 401", rec.Code)
	}
}

// The read is an observation of local tasks. It must say so, and must never
// carry a field that could be read as "upstream is in sync".
func TestAdminSyncStatusPayloadNeverClaimsUpstreamSync(t *testing.T) {
	users := &syncUsersFake{rows: []*domain.User{{ID: 7, Role: domain.RoleUser}}}
	h := syncStatusAdminHandler(users, &syncTasksFake{})
	rec := callWithClaims(t, domain.RoleAdmin, "7", h.GetSyncStatus)

	body := decodeSyncStatus(t, rec)
	if body["state"] != string(user.SyncStatusNoActiveTasks) {
		t.Fatalf("state = %v, want %q", body["state"], user.SyncStatusNoActiveTasks)
	}
	if body["history_scope"] != user.SyncStatusHistoryScopeRetained {
		t.Fatalf("history_scope = %v, want %q", body["history_scope"], user.SyncStatusHistoryScopeRetained)
	}
	for _, forbidden := range []string{"synced", "upstream_synced", "verified"} {
		if _, ok := body[forbidden]; ok {
			t.Fatalf("payload exposes %q, which reads as an upstream verdict", forbidden)
		}
	}
}
