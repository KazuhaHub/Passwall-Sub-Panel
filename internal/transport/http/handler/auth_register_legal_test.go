package handler

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/adapters/sqlstore"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/registration"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/user"
	"github.com/gin-gonic/gin"
)

type legalRegistrationSelector struct{}

func (legalRegistrationSelector) NodesFor(_ context.Context, _ *domain.Group) ([]*domain.Node, error) {
	return nil, nil
}

func TestAuthRegister_LegalVersionGuardAndPayload(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db, err := sqlstore.Open("sqlite", filepath.Join(t.TempDir(), "legal-registration.db"))
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
	repos := sqlstore.NewRepos(db)
	ctx := t.Context()
	g := &domain.Group{Name: "legal-handler"}
	if err := repos.Group.Create(ctx, g); err != nil {
		t.Fatal(err)
	}
	if err := repos.Settings.Save(ctx, ports.UISettings{RegistrationEnabled: true, RegistrationAllowUnverified: true, RegistrationDefaultGroupID: g.ID, LegalEnabled: true}); err != nil {
		t.Fatal(err)
	}
	for _, bump := range []bool{false, true} {
		if _, err := repos.Legal.Publish(ctx, domain.LegalDraft{Kind: "terms", Locale: "en-US", Content: "terms", ConsentBump: bump, PublishedBy: 1}); err != nil {
			t.Fatal(err)
		}
	}
	users := user.New(repos.User, repos.Group, nil, nil, legalRegistrationSelector{}, nil, nil, repos.ScopedSettings)
	reg := registration.New(registration.Deps{Users: users, Groups: repos.Group, Tokens: repos.AuthToken, Settings: repos.Settings})
	h := NewAuthRegisterHandler(reg, repos.Settings, nil)
	r := gin.New()
	r.POST("/api/auth/register", h.Register)
	for _, body := range []string{
		`{"email":"visitor@example.com","password":"GoodPass123"}`,
		`{"email":"visitor@example.com","password":"GoodPass123","accepted_consent_version":1}`,
		`{"email":"visitor@example.com","password":"GoodPass123","accepted_consent_version":3}`,
	} {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/auth/register", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		r.ServeHTTP(w, req)
		if w.Code != http.StatusConflict || decodeErrBody(t, w) != "legal_consent_outdated" {
			t.Fatalf("stale response %d %s", w.Code, w.Body.String())
		}
		if _, err := repos.User.GetByUPN(ctx, "visitor@example.com"); !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("rejected signup left user: %v", err)
		}
	}
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/auth/register", strings.NewReader(`{"email":"visitor@example.com","password":"GoodPass123","accepted_consent_version":2}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("current version rejected: %d %s", w.Code, w.Body.String())
	}
	u, err := repos.User.GetByUPN(ctx, "visitor@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if state, err := repos.Legal.Status(ctx, u.ID); err != nil || state.Pending || state.ConsentVersion != 2 {
		t.Fatalf("consent not stored %+v: %v", state, err)
	}
}
