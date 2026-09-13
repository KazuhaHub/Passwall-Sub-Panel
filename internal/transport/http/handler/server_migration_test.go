package handler

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/gin-gonic/gin"
)

type fakeMigrationPreview struct {
	calls int
	err   error
	core  string
	allow bool
}

func (f *fakeMigrationPreview) Preview(_ context.Context, id int64, core string, allow bool) (*domain.ServerMigrationPreview, error) {
	f.calls++
	f.core, f.allow = core, allow
	return &domain.ServerMigrationPreview{ServerID: id, Fingerprint: strings.Repeat("a", 64), Blockers: []domain.MigrationIssue{}, Warnings: []domain.MigrationIssue{}, CanMigrate: true}, f.err
}

func TestServerMigrationPreviewReadOnlyAndPrivate(t *testing.T) {
	gin.SetMode(gin.TestMode)
	f := &fakeMigrationPreview{}
	h := NewAdminServersHandler(nil, nil, nil, nil, nil, nil).WithServerMigrationPreviewer(f)
	r := gin.New()
	r.GET("/servers/:id/node-migration-preview", h.NodeMigrationPreview)
	for _, test := range []struct {
		url    string
		status int
	}{
		{"/servers/12/node-migration-preview?core_version=26.7.28&allow_restricted_reality=true", http.StatusOK},
		{"/servers/0/node-migration-preview", http.StatusBadRequest},
		{"/servers/12/node-migration-preview?allow_restricted_reality=yes", http.StatusBadRequest},
	} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, test.url, nil))
		if w.Code != test.status || w.Header().Get("Cache-Control") != "no-store, private" {
			t.Fatalf("%s: code=%d headers=%v body=%s", test.url, w.Code, w.Header(), w.Body.String())
		}
	}
	if f.calls != 1 || f.core != "26.7.28" || !f.allow {
		t.Fatalf("preview arguments: %+v", f)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/servers/12/node-migration-preview", nil))
	if w.Code != http.StatusNotFound || f.calls != 1 {
		t.Fatal("live endpoint exposed a conversion write")
	}
}

func TestServerMigrationPreviewSanitizesInternalErrors(t *testing.T) {
	for _, test := range []struct {
		err    error
		status int
	}{
		{domain.ErrNotFound, http.StatusNotFound}, {domain.ErrValidation, http.StatusBadRequest},
		{errors.New("SECRET in database SQL"), http.StatusServiceUnavailable},
	} {
		f := &fakeMigrationPreview{err: test.err}
		h := NewAdminServersHandler(nil, nil, nil, nil, nil, nil).WithServerMigrationPreviewer(f)
		r := gin.New()
		r.GET("/servers/:id/node-migration-preview", h.NodeMigrationPreview)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/servers/12/node-migration-preview", nil))
		if w.Code != test.status || strings.Contains(w.Body.String(), "SECRET") {
			t.Fatalf("error leaked/status mismatch: %d %s", w.Code, w.Body.String())
		}
	}
}
