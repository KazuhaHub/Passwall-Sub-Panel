package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/adapters/sqlstore"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/jwtutil"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/user"
	"github.com/KazuhaHub/passwall-sub-panel/internal/transport/http/middleware"
	"github.com/gin-gonic/gin"
)

func legalHTTPRepos(t *testing.T) ports.Repos {
	t.Helper()
	db, err := sqlstore.Open("sqlite", filepath.Join(t.TempDir(), "legal.db"))
	if err != nil {
		t.Fatal(err)
	}
	pool, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pool.Close() })
	if err := sqlstore.EnsureSchema(db); err != nil {
		t.Fatal(err)
	}
	return sqlstore.NewRepos(db)
}

func legalHTTPUser(t *testing.T, repos ports.Repos, role domain.Role, n int) *domain.User {
	t.Helper()
	u := &domain.User{UPN: fmt.Sprintf("legal-http-%d@example.com", n), UUID: "legal-http-uuid", SubToken: fmt.Sprintf("legal-http-token-%d", n), Role: role, Enabled: true}
	if err := repos.User.Create(t.Context(), u); err != nil {
		t.Fatal(err)
	}
	return u
}

func TestLegalConsentHTTP_ActorScopeAndVersion(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repos := legalHTTPRepos(t)
	ctx := t.Context()
	u := legalHTTPUser(t, repos, domain.RoleUser, 1)
	other := legalHTTPUser(t, repos, domain.RoleUser, 2)
	staff := legalHTTPUser(t, repos, domain.RoleOperator, 3)
	if err := repos.Settings.Save(ctx, ports.UISettings{LegalEnabled: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := repos.Legal.Publish(ctx, domain.LegalDraft{Kind: "terms", Locale: "en-US", Content: "terms", PublishedBy: staff.ID}); err != nil {
		t.Fatal(err)
	}
	h := NewLegalConsentHandler(repos.Legal)
	request := func(actor int64, body string) *httptest.ResponseRecorder {
		r := gin.New()
		r.POST("/accept", func(c *gin.Context) {
			if actor > 0 {
				c.Set(middleware.CtxClaims, &jwtutil.Claims{UserID: actor})
			}
			h.Accept(c)
		})
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/accept", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		r.ServeHTTP(w, req)
		return w
	}
	for _, tc := range []struct {
		actor  int64
		body   string
		status int
	}{
		{0, `{"consent_version":1}`, 401},
		{staff.ID, `{"consent_version":1}`, 403},
		{u.ID, `{"consent_version":0}`, 409},
		{u.ID, `{"consent_version":2}`, 409},
		{u.ID, `{`, 400},
	} {
		if w := request(tc.actor, tc.body); w.Code != tc.status {
			t.Fatalf("request %+v: %d %s", tc, w.Code, w.Body.String())
		}
	}
	for range 2 {
		if w := request(u.ID, fmt.Sprintf(`{"consent_version":1,"user_id":%d}`, other.ID)); w.Code != 200 {
			t.Fatalf("accept: %d %s", w.Code, w.Body.String())
		}
	}
	if s, err := repos.Legal.Status(ctx, u.ID); err != nil || s.Pending {
		t.Fatalf("actor acceptance %+v: %v", s, err)
	}
	if s, err := repos.Legal.Status(ctx, other.ID); err != nil || !s.Pending {
		t.Fatalf("request changed another user %+v: %v", s, err)
	}
	if _, err := repos.Legal.Publish(ctx, domain.LegalDraft{Kind: "privacy", Locale: "en-US", Content: "major", ConsentBump: true, PublishedBy: staff.ID}); err != nil {
		t.Fatal(err)
	}
	if w := request(u.ID, `{"consent_version":1}`); w.Code != 409 || decodeErrBody(t, w) != "legal_consent_outdated" {
		t.Fatalf("outdated response %d %s", w.Code, w.Body.String())
	}
	if err := repos.Settings.Save(ctx, ports.UISettings{LegalEnabled: false}); err != nil {
		t.Fatal(err)
	}
	if w := request(u.ID, `{"consent_version":2}`); w.Code != 404 {
		t.Fatalf("disabled response %d %s", w.Code, w.Body.String())
	}
}

func TestLegalProfileAndAuthMethods_RealRepository(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repos := legalHTTPRepos(t)
	ctx := t.Context()
	u := legalHTTPUser(t, repos, domain.RoleUser, 4)
	staff := legalHTTPUser(t, repos, domain.RoleAdmin, 5)
	users := user.New(repos.User, repos.Group, nil, nil, legalRegistrationSelector{}, nil, nil, repos.ScopedSettings)
	me := NewUserMeHandler(users, nil, repos.ScopedSettings, nil, nil, nil, nil, repos.Legal)
	methods := NewAuthLocalHandler(nil, nil, nil, nil, repos.ScopedSettings, nil, nil, nil, nil, nil, nil)
	profile := func(actor int64, pending bool, version int64) {
		t.Helper()
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodGet, "/api/user/me", nil)
		c.Set(middleware.CtxClaims, &jwtutil.Claims{UserID: actor})
		me.Profile(c)
		if w.Code != 200 {
			t.Fatalf("pending does not gate profile: %d %s", w.Code, w.Body.String())
		}
		var body struct {
			Pending bool  `json:"legal_pending"`
			Version int64 `json:"legal_consent_version"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if body.Pending != pending || body.Version != version {
			t.Fatalf("profile %+v want %t/%d", body, pending, version)
		}
	}
	profile(u.ID, false, 0)
	if err := repos.Settings.Save(ctx, ports.UISettings{LegalEnabled: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := repos.Legal.Publish(ctx, domain.LegalDraft{Kind: "terms", Locale: "en-US", Content: "terms", PublishedBy: staff.ID}); err != nil {
		t.Fatal(err)
	}
	profile(u.ID, true, 1)
	profile(staff.ID, false, 1)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/auth/methods", nil)
	methods.Methods(c)
	var body struct {
		Legal domain.LegalConsentStatus `json:"legal"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if w.Code != 200 || !body.Legal.Enabled || body.Legal.ConsentVersion != 1 {
		t.Fatalf("methods %d %+v", w.Code, body)
	}
	if err := repos.Legal.Accept(ctx, u.ID, 1); err != nil {
		t.Fatal(err)
	}
	profile(u.ID, false, 1)
}
