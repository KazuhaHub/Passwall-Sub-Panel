package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/KazuhaHub/passwall-sub-panel/internal/adapters/mysql"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

type fakeScopeGroups struct{ exists map[int64]bool }

func (f fakeScopeGroups) GetByID(_ context.Context, id int64) (*domain.Group, error) {
	if f.exists[id] {
		return &domain.Group{ID: id}, nil
	}
	return nil, domain.ErrNotFound
}

// fakeScopeRepo is an in-memory ports.ScopeSettingsRepo for one scope.
type fakeScopeRepo struct{ rows map[string]ports.ScopeOverride }

func newFakeScopeRepo() *fakeScopeRepo { return &fakeScopeRepo{rows: map[string]ports.ScopeOverride{}} }

func (f *fakeScopeRepo) ListOverrides(_ context.Context, _ string, _ int64) ([]ports.ScopeOverride, error) {
	out := make([]ports.ScopeOverride, 0, len(f.rows))
	for _, o := range f.rows {
		out = append(out, o)
	}
	return out, nil
}
func (f *fakeScopeRepo) SetOverride(_ context.Context, _ string, _ int64, o ports.ScopeOverride) error {
	f.rows[o.Type+"."+o.Name] = o
	return nil
}
func (f *fakeScopeRepo) DeleteOverride(_ context.Context, _ string, _ int64, typ, name string) error {
	delete(f.rows, typ+"."+name)
	return nil
}
func (f *fakeScopeRepo) DeleteScope(_ context.Context, _ string, _ int64) error {
	f.rows = map[string]ports.ScopeOverride{}
	return nil
}

func scopeRouter(h *AdminScopeSettingsHandler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/api/admin/groups/:id/scope-settings", h.Get)
	r.PUT("/api/admin/groups/:id/scope-settings", h.SetOverride)
	r.DELETE("/api/admin/groups/:id/scope-settings/:type/:name", h.DeleteOverride)
	return r
}

func TestScopeSettingsHandler_SetGetDelete(t *testing.T) {
	repo := newFakeScopeRepo()
	h := NewAdminScopeSettingsHandler(fakeScopeGroups{exists: map[int64]bool{5: true}}, repo)
	r := scopeRouter(h)

	body, _ := json.Marshal(setScopeOverrideRequest{Type: "security", Name: "totp_enabled", Value: "1"})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPut, "/api/admin/groups/5/scope-settings", bytes.NewReader(body)))
	if w.Code != http.StatusOK {
		t.Fatalf("PUT = %d, want 200; body=%s", w.Code, w.Body.String())
	}

	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/admin/groups/5/scope-settings", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("GET = %d", w.Code)
	}
	var dto scopeSettingsDTO
	if err := json.Unmarshal(w.Body.Bytes(), &dto); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if dto.Overrides["security.totp_enabled"] != "1" {
		t.Errorf("GET overrides = %+v, want the set value", dto.Overrides)
	}
	if len(dto.Overridable) == 0 {
		t.Error("GET must list the overridable catalog")
	}

	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodDelete, "/api/admin/groups/5/scope-settings/security/totp_enabled", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("DELETE = %d", w.Code)
	}
	if _, ok := repo.rows["security.totp_enabled"]; ok {
		t.Error("override must be gone after DELETE (inheritance restored)")
	}
}

func TestScopeSettingsHandler_RejectsNonOverridable(t *testing.T) {
	repo := newFakeScopeRepo()
	h := NewAdminScopeSettingsHandler(fakeScopeGroups{exists: map[int64]bool{5: true}}, repo)
	r := scopeRouter(h)
	// jwt_issuer is a real setting but global-only (not in the allowlist).
	body, _ := json.Marshal(setScopeOverrideRequest{Type: "auth", Name: "jwt_issuer", Value: "x"})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPut, "/api/admin/groups/5/scope-settings", bytes.NewReader(body)))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("PUT non-overridable = %d, want 400", w.Code)
	}
	if len(repo.rows) != 0 {
		t.Error("a rejected override must not be written")
	}
}

func TestScopeSettingsHandler_GroupNotFound(t *testing.T) {
	h := NewAdminScopeSettingsHandler(fakeScopeGroups{exists: map[int64]bool{}}, newFakeScopeRepo())
	r := scopeRouter(h)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/admin/groups/99/scope-settings", nil))
	if w.Code != http.StatusNotFound {
		t.Fatalf("GET missing group = %d, want 404", w.Code)
	}
}

// TestOverridableScopeKeysAreKnown drift-guards the allowlist: every overridable
// key must be a live setting name (else a write would strand on a dead key).
func TestOverridableScopeKeysAreKnown(t *testing.T) {
	known := mysql.KnownSettingNames()
	for key := range overridableScopeKeys {
		parts := strings.SplitN(key, ".", 2)
		if len(parts) != 2 {
			t.Errorf("overridable key %q must be \"type.name\"", key)
			continue
		}
		if !known[parts[1]] {
			t.Errorf("overridable key %q is not a known setting name", key)
		}
	}
}
