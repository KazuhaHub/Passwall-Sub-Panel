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

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// fakeLocaleRepo is an in-memory ports.LocaleRepo.
type fakeLocaleRepo struct{ rows map[string]*domain.LocalePack }

func newFakeLocaleRepo() *fakeLocaleRepo { return &fakeLocaleRepo{rows: map[string]*domain.LocalePack{}} }

func (f *fakeLocaleRepo) List(_ context.Context) ([]domain.LocaleMeta, error) {
	out := []domain.LocaleMeta{}
	for _, p := range f.rows {
		out = append(out, domain.LocaleMeta{Code: p.Code, Name: p.Name, ETag: `"etag-` + p.Code + `"`})
	}
	return out, nil
}
func (f *fakeLocaleRepo) Get(_ context.Context, code string) (*domain.LocalePack, error) {
	if p, ok := f.rows[code]; ok {
		return p, nil
	}
	return nil, domain.ErrNotFound
}
func (f *fakeLocaleRepo) Save(_ context.Context, p *domain.LocalePack) error {
	f.rows[p.Code] = p
	return nil
}
func (f *fakeLocaleRepo) Delete(_ context.Context, code string) error {
	if _, ok := f.rows[code]; !ok {
		return domain.ErrNotFound
	}
	delete(f.rows, code)
	return nil
}

func localeRouter(repo *fakeLocaleRepo) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	admin := NewAdminLocalesHandler(repo)
	pub := NewI18nPublicHandler(repo)
	r.GET("/api/admin/locales", admin.List)
	r.PUT("/api/admin/locales/:code", admin.Save)
	r.DELETE("/api/admin/locales/:code", admin.Delete)
	r.GET("/api/i18n/langs", pub.Langs)
	r.GET("/api/i18n/:lang", pub.Bundle)
	return r
}

const validPackJSON = `{"psp_language_pack":1,"code":"fr-FR","name":"Français","namespaces":{"nav":{"home":"Accueil"}}}`

func TestLocalesHandler_SaveListBundleDelete(t *testing.T) {
	repo := newFakeLocaleRepo()
	r := localeRouter(repo)

	// Save
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPut, "/api/admin/locales/fr-FR", bytes.NewReader([]byte(validPackJSON))))
	if w.Code != http.StatusNoContent {
		t.Fatalf("Save code = %d body=%s, want 204", w.Code, w.Body.String())
	}

	// Admin list
	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/admin/locales", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("List code = %d, want 200", w.Code)
	}
	var metas []map[string]any
	json.Unmarshal(w.Body.Bytes(), &metas)
	if len(metas) != 1 || metas[0]["code"] != "fr-FR" || metas[0]["name"] != "Français" {
		t.Fatalf("unexpected list: %s", w.Body.String())
	}

	// Public bundle
	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/i18n/fr-FR", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("Bundle code = %d, want 200", w.Code)
	}
	if et := w.Header().Get("ETag"); et == "" {
		t.Fatal("bundle missing ETag")
	}
	var body map[string]any
	json.Unmarshal(w.Body.Bytes(), &body)
	ns, _ := body["namespaces"].(map[string]any)
	if ns == nil || ns["nav"] == nil {
		t.Fatalf("bundle missing namespaces: %s", w.Body.String())
	}

	// Conditional GET → 304
	w = httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/i18n/fr-FR", nil)
	req.Header.Set("If-None-Match", `"etag-fr-FR"`)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNotModified {
		t.Fatalf("conditional Bundle code = %d, want 304", w.Code)
	}

	// Delete
	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodDelete, "/api/admin/locales/fr-FR", nil))
	if w.Code != http.StatusNoContent {
		t.Fatalf("Delete code = %d, want 204", w.Code)
	}
	if _, ok := repo.rows["fr-FR"]; ok {
		t.Fatal("pack not deleted")
	}
}

func TestLocalesHandler_SaveRejectsInvalid(t *testing.T) {
	repo := newFakeLocaleRepo()
	r := localeRouter(repo)

	cases := map[string]string{
		"reserved code":     `{"psp_language_pack":1,"code":"en-US","name":"X","namespaces":{}}`,
		"bad format":        `{"psp_language_pack":9,"code":"fr-FR","name":"X","namespaces":{}}`,
		"unknown namespace": `{"psp_language_pack":1,"code":"fr-FR","name":"X","namespaces":{"bogus":{"a":"b"}}}`,
		"non-string leaf":   `{"psp_language_pack":1,"code":"fr-FR","name":"X","namespaces":{"nav":{"n":5}}}`,
		"missing name":      `{"psp_language_pack":1,"code":"fr-FR","name":"","namespaces":{}}`,
	}
	for name, jsonBody := range cases {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodPut, "/api/admin/locales/fr-FR", bytes.NewReader([]byte(jsonBody))))
		if w.Code != http.StatusBadRequest {
			t.Fatalf("%s: code = %d body=%s, want 400", name, w.Code, w.Body.String())
		}
	}
}

func TestLocalesHandler_CodeMismatch(t *testing.T) {
	repo := newFakeLocaleRepo()
	r := localeRouter(repo)
	// URL code de-DE but body code fr-FR → 400.
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPut, "/api/admin/locales/de-DE", bytes.NewReader([]byte(validPackJSON))))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("mismatch code = %d, want 400", w.Code)
	}
}

func TestLocalesHandler_SaveTooLarge(t *testing.T) {
	repo := newFakeLocaleRepo()
	r := localeRouter(repo)
	// Build a >512 KiB body via one huge string value.
	huge := strings.Repeat("x", 600*1024)
	body := `{"psp_language_pack":1,"code":"fr-FR","name":"X","namespaces":{"nav":{"n":"` + huge + `"}}}`
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPut, "/api/admin/locales/fr-FR", bytes.NewReader([]byte(body))))
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized pack code = %d, want 413", w.Code)
	}
}

func TestLocalesHandler_DeleteReserved(t *testing.T) {
	repo := newFakeLocaleRepo()
	r := localeRouter(repo)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodDelete, "/api/admin/locales/zh-CN", nil))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("delete reserved code = %d, want 400", w.Code)
	}
}

func TestLocalesHandler_BundleReservedOrMissing404(t *testing.T) {
	repo := newFakeLocaleRepo()
	r := localeRouter(repo)
	for _, lang := range []string{"en-US", "xx-YY"} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/i18n/"+lang, nil))
		if w.Code != http.StatusNotFound {
			t.Fatalf("bundle %s code = %d, want 404", lang, w.Code)
		}
	}
}
