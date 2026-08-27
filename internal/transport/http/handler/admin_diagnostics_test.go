package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/metrics"
)

func serveDiagnostics(t *testing.T, method, path string, h gin.HandlerFunc) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Handle(method, path, h)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(method, path, nil))
	return w
}

func TestDiagnosticsMetricsSnapshot(t *testing.T) {
	// Drive a couple of real metrics so the response isn't trivially empty.
	metrics.PollTotal.Inc()
	metrics.PanelRTT.With("GetClient").Observe(123)

	h := NewAdminDiagnosticsHandler()
	w := serveDiagnostics(t, http.MethodGet, "/diagnostics/metrics", h.Metrics)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	var got diagnosticsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v (body %s)", err, w.Body.String())
	}
	if got.Version == "" {
		t.Error("version missing — a snapshot has to say which build produced it")
	}
	if got.Goroutines <= 0 {
		t.Errorf("goroutines = %d, want > 0", got.Goroutines)
	}

	var poll *metrics.CounterSnapshot
	for i, c := range got.Metrics.Counters {
		if c.Name == "psp_poll_total" {
			poll = &got.Metrics.Counters[i]
		}
	}
	if poll == nil {
		t.Fatal("psp_poll_total missing from snapshot")
	}
	if poll.Value < 1 {
		t.Errorf("psp_poll_total = %d, want >= 1", poll.Value)
	}
	if poll.Help == "" {
		t.Error("help text missing — the snapshot is meant to be readable without the source")
	}

	// Vec children must be reachable under their rendered label name, or
	// the RTT breakdown is invisible to whoever reads the endpoint.
	var found bool
	for _, hs := range got.Metrics.Histograms {
		if hs.Name == `psp_panel_rtt_ms{op=GetClient}` {
			found = true
			if hs.Count < 1 {
				t.Errorf("labelled histogram count = %d, want >= 1", hs.Count)
			}
			if hs.Unit != "ms" {
				t.Errorf("unit = %q, want ms", hs.Unit)
			}
		}
	}
	if !found {
		t.Error("labelled histogram child missing from snapshot")
	}
}

func TestDiagnosticsResetReturnsTheWindowItCloses(t *testing.T) {
	metrics.Reset()
	for i := 0; i < 7; i++ {
		metrics.PollTotal.Inc()
	}

	h := NewAdminDiagnosticsHandler()
	w := serveDiagnostics(t, http.MethodPost, "/diagnostics/metrics/reset", h.ResetMetrics)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var got struct {
		Reset    bool             `json:"reset"`
		Previous metrics.Snapshot `json:"previous"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !got.Reset {
		t.Error("reset flag not set")
	}
	// The closing window must come back in the response — otherwise a
	// reset silently destroys the measurement it ends.
	var prev int64 = -1
	for _, c := range got.Previous.Counters {
		if c.Name == "psp_poll_total" {
			prev = c.Value
		}
	}
	if prev != 7 {
		t.Errorf("previous psp_poll_total = %d, want 7", prev)
	}
	if metrics.PollTotal.Value() != 0 {
		t.Errorf("counter after reset = %d, want 0", metrics.PollTotal.Value())
	}
}
