package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/jwtutil"
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
