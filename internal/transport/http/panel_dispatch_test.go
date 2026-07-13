package http

import (
	"context"
	stdhttp "net/http"
	"net/http/httptest"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/panelpath"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

type dispatchSettingsRepo struct{ settings ports.UISettings }

func (r *dispatchSettingsRepo) Load(context.Context, ports.UISettings) (ports.UISettings, error) {
	return r.settings, nil
}

func (r *dispatchSettingsRepo) Save(context.Context, ports.UISettings) error { return nil }

func TestPanelDispatchRootRedirectIsTemporaryAndNotCacheable(t *testing.T) {
	paths := panelpath.NewResolver(&dispatchSettingsRepo{settings: ports.UISettings{PanelPath: "/mypanel"}})
	h := panelDispatch(paths, stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, _ *stdhttp.Request) {
		t.Fatal("root redirect unexpectedly reached the wrapped handler")
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(stdhttp.MethodGet, "http://example.test/", nil))

	if rec.Code != stdhttp.StatusTemporaryRedirect {
		t.Fatalf("status = %d", rec.Code)
	}
	if got := rec.Header().Get("Location"); got != "/mypanel/" {
		t.Fatalf("Location = %q", got)
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q", got)
	}
}

func TestPanelDispatchStripsPrefixAndRecordsExternalPath(t *testing.T) {
	paths := panelpath.NewResolver(&dispatchSettingsRepo{settings: ports.UISettings{PanelPath: "/tools/mypanel"}})
	h := panelDispatch(paths, stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		if r.URL.Path != "/api/version" {
			t.Fatalf("stripped path = %q", r.URL.Path)
		}
		if got := panelpath.FromRequest(r); got != "/tools/mypanel" {
			t.Fatalf("request panel path = %q", got)
		}
		w.WriteHeader(stdhttp.StatusNoContent)
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(stdhttp.MethodGet, "http://example.test/tools/mypanel/api/version", nil))
	if rec.Code != stdhttp.StatusNoContent {
		t.Fatalf("status = %d", rec.Code)
	}
}
