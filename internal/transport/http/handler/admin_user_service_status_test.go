package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/jwtutil"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/user"
	"github.com/KazuhaHub/passwall-sub-panel/internal/transport/http/middleware"
)

// countingSyncUsersFake records whether the request reached the user service at
// all: SetServiceSuspendedAndSync's first repository touch is GetByID.
type countingSyncUsersFake struct {
	syncUsersFake
	gets int
}

func (f *countingSyncUsersFake) GetByID(ctx context.Context, id int64) (*domain.User, error) {
	f.gets++
	return f.syncUsersFake.GetByID(ctx, id)
}

// geo_auto is the detector's reason, and the lift keys on it: whatever carries
// it is lifted on the detector's timer. An admin who wrote it by hand would get
// a "manual" suspension that silently expires, and would muddy the automation's
// false-positive count (an admin resume of geo_auto). So the API refuses it and
// points at the human reason instead, before any write is attempted.
func TestSetServiceStatus_RejectsGeoAutoFromARequest(t *testing.T) {
	// Same shape as syncStatusAdminHandler, over the counting fake so a
	// forwarded request shows.
	users := &countingSyncUsersFake{}
	h := &AdminUserHandler{user: user.New(users, nil, nil, &syncTasksFake{}, nil, nil, nil, nil)}

	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	body := `{"enabled":false,"reason":"geo_auto","detail":"typed by hand"}`
	c.Request = httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set(middleware.CtxClaims, &jwtutil.Claims{UserID: 1, Role: domain.RoleAdmin})
	c.Params = gin.Params{{Key: "id", Value: "7"}}

	h.SetServiceStatus(c)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400 (body %s)", rec.Code, rec.Body.String())
	}
	var resp struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v (body %s)", err, rec.Body.String())
	}
	const want = "Geo_auto is applied by the location detector only; use geo_anomaly for a manual geo suspension"
	if resp.Error != want {
		t.Fatalf("error = %q, want %q", resp.Error, want)
	}
	if users.gets != 0 {
		t.Fatalf("the request reached the user service (%d lookups); it must be refused before any write", users.gets)
	}
}

// serviceStateUsersFake is the user repository as the resume paths use it:
// the fresh read, the unconditional write (ResumeServiceAndSync), the
// conditional clear (ResumeServiceIfReason) and the violation reset. Reads
// hand out copies, as a database does, so a service that edits what it read
// cannot pass for one that wrote. gets counts the reads, which is how a
// request refused before the service shows.
type serviceStateUsersFake struct {
	ports.UserRepo
	mu   sync.Mutex
	rows map[int64]*domain.User
	gets int
}

func (f *serviceStateUsersFake) GetByID(_ context.Context, id int64) (*domain.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.gets++
	u, ok := f.rows[id]
	if !ok {
		return nil, domain.ErrNotFound
	}
	cp := *u
	return &cp, nil
}

func (f *serviceStateUsersFake) UpdateServiceState(_ context.Context, id int64, reason domain.AutoDisabledReason, detail string, at *time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if u, ok := f.rows[id]; ok {
		u.ServiceDisabledReason, u.ServiceDisableDetail, u.ServiceDisabledAt = reason, detail, at
	}
	return nil
}

func (f *serviceStateUsersFake) ClearServiceStateIfReason(_ context.Context, id int64, reason domain.AutoDisabledReason) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	u, ok := f.rows[id]
	if !ok || u.ServiceDisabledReason != reason {
		return false, nil
	}
	u.ServiceDisabledReason, u.ServiceDisableDetail, u.ServiceDisabledAt = domain.DisabledNone, "", nil
	return true, nil
}

func (f *serviceStateUsersFake) ClearBlockViolation(_ context.Context, id int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if u, ok := f.rows[id]; ok {
		u.BlockViolationCount, u.LastBlockViolationAt = 0, nil
	}
	return nil
}

func (f *serviceStateUsersFake) row(id int64) domain.User {
	f.mu.Lock()
	defer f.mu.Unlock()
	return *f.rows[id]
}

// noOwnershipFake is a fully migrated install: no legacy per-node clients, so
// a push has nothing to send beyond the (unwired) shared-client sync.
type noOwnershipFake struct{ ports.OwnershipRepo }

func (noOwnershipFake) ListByUser(context.Context, int64) ([]*domain.XUIClientEntry, error) {
	return nil, nil
}

// serviceStatusHandler is an AdminUserHandler over a real user service with
// one held account (id 7). No task repository: HasPendingSync answers false.
func serviceStatusHandler(held domain.AutoDisabledReason) (*AdminUserHandler, *serviceStateUsersFake) {
	at := time.Date(2026, 9, 27, 8, 0, 0, 0, time.UTC)
	users := &serviceStateUsersFake{rows: map[int64]*domain.User{
		7: {ID: 7, UPN: "u@example.com", Role: domain.RoleUser, Enabled: true,
			ServiceDisabledReason: held, ServiceDisableDetail: "theirs", ServiceDisabledAt: &at},
	}}
	return &AdminUserHandler{user: user.New(users, nil, noOwnershipFake{}, nil, nil, nil, nil, nil)}, users
}

// postServiceStatus runs SetServiceStatus as an admin on :id with body.
func postServiceStatus(t *testing.T, h *AdminUserHandler, id, body string) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set(middleware.CtxClaims, &jwtutil.Claims{UserID: 1, Role: domain.RoleAdmin})
	c.Params = gin.Params{{Key: "id", Value: id}}
	h.SetServiceStatus(c)
	// c.Status alone does not reach the recorder; a 204 has no body to flush it.
	c.Writer.WriteHeaderNow()
	return rec
}

// The risk center's drawer resumes the hold it SHOWED. If the account now
// carries another reason — another admin paused it, the detector's hold was
// replaced by a person's — the resume is refused with a code the SPA can act
// on (refresh, toast), and the newer hold stays exactly as written.
func TestSetServiceStatus_ExpectReasonMismatchIs409(t *testing.T) {
	h, users := serviceStatusHandler(domain.DisabledServiceManual)

	rec := postServiceStatus(t, h, "7", `{"enabled":true,"expect_reason":"geo_auto"}`)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status %d, want 409 (body %s)", rec.Code, rec.Body.String())
	}
	var resp struct {
		Error string `json:"error"`
		Code  string `json:"code"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v (body %s)", err, rec.Body.String())
	}
	if resp.Error != "Service state changed" || resp.Code != "reason_changed" {
		t.Fatalf("body = %+v, want {Service state changed, reason_changed}", resp)
	}
	if got := users.row(7); got.ServiceDisabledReason != domain.DisabledServiceManual || got.ServiceDisableDetail != "theirs" || got.ServiceDisabledAt == nil {
		t.Fatalf("row = %q/%q/%v, want service_manual/theirs untouched", got.ServiceDisabledReason, got.ServiceDisableDetail, got.ServiceDisabledAt)
	}
}

// The reason still on the row is the one the admin saw: lifted, 204.
func TestSetServiceStatus_ExpectReasonMatchResumes(t *testing.T) {
	for _, held := range []domain.AutoDisabledReason{domain.DisabledGeoAutoSuspend, domain.DisabledGeoAnomaly, domain.DisabledServiceManual} {
		t.Run(string(held), func(t *testing.T) {
			h, users := serviceStatusHandler(held)

			rec := postServiceStatus(t, h, "7", `{"enabled":true,"expect_reason":"`+string(held)+`"}`)

			if rec.Code != http.StatusNoContent {
				t.Fatalf("status %d, want 204 (body %s)", rec.Code, rec.Body.String())
			}
			if got := users.row(7); got.ServiceDisabledReason != domain.DisabledNone || got.ServiceDisableDetail != "" || got.ServiceDisabledAt != nil {
				t.Fatalf("row = %q/%q/%v, want cleared", got.ServiceDisabledReason, got.ServiceDisableDetail, got.ServiceDisabledAt)
			}
		})
	}
}

// expect_reason names a service hold or it is a malformed request: refused
// with the pause path's own message, before the service reads anything.
func TestSetServiceStatus_BadExpectReasonIs400(t *testing.T) {
	for _, bad := range []string{"manual", "bogus", "pending_delete"} {
		t.Run(bad, func(t *testing.T) {
			h, users := serviceStatusHandler(domain.DisabledServiceManual)

			rec := postServiceStatus(t, h, "7", `{"enabled":true,"expect_reason":"`+bad+`"}`)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status %d, want 400 (body %s)", rec.Code, rec.Body.String())
			}
			var resp struct {
				Error string `json:"error"`
			}
			_ = json.Unmarshal(rec.Body.Bytes(), &resp)
			if resp.Error != "Invalid service status reason" {
				t.Fatalf("error = %q, want %q", resp.Error, "Invalid service status reason")
			}
			if users.gets != 0 {
				t.Fatalf("the request reached the user service (%d lookups); it must be refused first", users.gets)
			}
			if got := users.row(7).ServiceDisabledReason; got != domain.DisabledServiceManual {
				t.Fatalf("reason = %q, want service_manual untouched", got)
			}
		})
	}
}

// A conditional resume of an account that does not exist is the service's
// not-found, mapped as every other handler here maps it.
func TestSetServiceStatus_ExpectReasonUnknownUserIs404(t *testing.T) {
	h, _ := serviceStatusHandler(domain.DisabledServiceManual)

	rec := postServiceStatus(t, h, "404", `{"enabled":true,"expect_reason":"service_manual"}`)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status %d, want 404 (body %s)", rec.Code, rec.Body.String())
	}
}

// Without expect_reason (the Users page, older SPAs) the resume is what it
// always was: ResumeServiceAndSync, unconditional, whatever holds the row.
func TestSetServiceStatus_WithoutExpectReasonUnchanged(t *testing.T) {
	for _, held := range []domain.AutoDisabledReason{domain.DisabledGeoAutoSuspend, domain.DisabledBlockedClient, domain.DisabledTrafficExceeded} {
		t.Run(string(held), func(t *testing.T) {
			h, users := serviceStatusHandler(held)

			rec := postServiceStatus(t, h, "7", `{"enabled":true}`)

			if rec.Code != http.StatusNoContent {
				t.Fatalf("status %d, want 204 (body %s)", rec.Code, rec.Body.String())
			}
			if got := users.row(7); got.ServiceDisabledReason != domain.DisabledNone || got.ServiceDisabledAt != nil {
				t.Fatalf("row = %q/%v, want cleared", got.ServiceDisabledReason, got.ServiceDisabledAt)
			}
		})
	}
}
