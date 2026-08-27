package handler

import (
	"net/http"
	"runtime"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/metrics"
	"github.com/KazuhaHub/passwall-sub-panel/internal/version"
)

// AdminDiagnosticsHandler exposes the in-process metrics registry.
//
// adminGroup only, not staffGroup: the snapshot reveals deployment shape
// (how many users are active per cycle, how many panels, how slow they
// are). That is nobody's business but the owner's, and an operator role
// exists to run day-to-day user work, not to profile the install.
//
// Read-only and side-effect free apart from the explicit reset. Nothing
// here is on a hot path — the cost is one pass over a few dozen metrics.
type AdminDiagnosticsHandler struct {
	startedAt time.Time
}

func NewAdminDiagnosticsHandler() *AdminDiagnosticsHandler {
	return &AdminDiagnosticsHandler{startedAt: time.Now()}
}

// diagnosticsResponse wraps the metric snapshot with the context needed to
// interpret it. A snapshot without the uptime and the build is not
// actionable: a counter of 40,000 means nothing until you know whether the
// process has been up for an hour or a month, and a measurement taken
// against the wrong binary is worse than no measurement.
type diagnosticsResponse struct {
	Version    string           `json:"version"`
	Commit     string           `json:"commit,omitempty"`
	UptimeMS   int64            `json:"uptime_ms"`
	Goroutines int              `json:"goroutines"`
	Metrics    metrics.Snapshot `json:"metrics"`
}

// Metrics returns the current snapshot of every declared metric.
func (h *AdminDiagnosticsHandler) Metrics(c *gin.Context) {
	c.JSON(http.StatusOK, diagnosticsResponse{
		Version:    version.Version,
		Commit:     version.Commit,
		UptimeMS:   time.Since(h.startedAt).Milliseconds(),
		Goroutines: runtime.NumGoroutine(),
		Metrics:    metrics.Take(),
	})
}

// ResetMetrics zeroes the counters and histograms and rebases the gauge
// peaks, opening a fresh measurement window.
//
// This exists so a measurement is a window rather than a subtraction. The
// alternative — snapshot, wait, snapshot, difference by hand — cannot
// difference the histograms (a quantile is not subtractable), which are
// the half of Phase 0 that matters. Destructive only to observability;
// the returned snapshot is the state as of just before the reset, so a
// reset never loses the window it closes.
func (h *AdminDiagnosticsHandler) ResetMetrics(c *gin.Context) {
	before := metrics.Take()
	metrics.Reset()
	c.JSON(http.StatusOK, gin.H{
		"reset":    true,
		"previous": before,
	})
}
