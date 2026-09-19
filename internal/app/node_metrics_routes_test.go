package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/adapters/sqlstore"
	"github.com/KazuhaHub/passwall-sub-panel/internal/config"
)

// THE READ API IS OPTIONAL IN THE ROUTER, WHICH MAKES FORGETTING IT SILENT.
//
// The node host-telemetry routes are registered only when the service reaches
// Deps. Leaving that field unset therefore leaves the ingest, the rollup and the
// server list's telemetry all working while every chart 404s — and nothing
// notices: the handler is tested by constructing it directly, and the SPA tests
// mock its client. Only a request through the assembled application sees it.
//
// The assertion is made against the REAL router Build produced, and it is "not
// 404": a registered admin route with no token answers 401, while an
// unregistered one falls through to the SPA fallback.
func TestNodeMetricsReadRoutesAreWired(t *testing.T) {
	ctx := t.Context()
	directory := t.TempDir()
	cfg := &config.Config{
		Listen: "127.0.0.1:0", JWTSecret: strings.Repeat("j", 48), EncryptionKey: strings.Repeat("e", 48),
		ConfigDir: filepath.Join(directory, "config"), DataDir: filepath.Join(directory, "data"),
	}
	application, err := Build(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := application.Shutdown(shutdownCtx); err != nil {
			t.Error(err)
		}
		sqlstore.ConfigureSecretKey("")
	})

	for _, path := range []string{
		"/api/admin/servers/1/node-metrics/current",
		"/api/admin/servers/1/node-metrics/history",
		"/api/admin/servers/1/node-metrics/interfaces",
		"/api/admin/servers/1/node-health",
	} {
		t.Run(path, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			application.server.Handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
			if recorder.Code == http.StatusNotFound {
				t.Fatalf("%s reached the fallback, so the route is not registered: the telemetry a node reports would be readable by nothing", path)
			}
		})
	}
}
