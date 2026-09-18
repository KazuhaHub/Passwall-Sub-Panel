package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/nodemetrics"
)

// The contract the SPA is written against: fixed statuses, a fixed shape, and no
// raw counter anywhere.

type nodeMetricsPanelRepo struct {
	ports.XUIPanelRepo
	panel *domain.XUIPanel
}

func (r nodeMetricsPanelRepo) GetByID(context.Context, int64) (*domain.XUIPanel, error) {
	if r.panel == nil {
		return nil, domain.ErrNotFound
	}
	return r.panel, nil
}

type nodeMetricsAgentRepo struct {
	ports.NodeAgentRepo
	agent *domain.NodeAgent
}

func (r nodeMetricsAgentRepo) GetByPanelID(context.Context, int64) (*domain.NodeAgent, error) {
	if r.agent == nil {
		return nil, domain.ErrNotFound
	}
	return r.agent, nil
}

func (r nodeMetricsAgentRepo) GetByAgentID(context.Context, string) (*domain.NodeAgent, error) {
	if r.agent == nil {
		return nil, domain.ErrNotFound
	}
	return r.agent, nil
}

// nodeMetricsServiceStub answers with whatever the case needs, and records the
// refresh so the endpoint's effect can be asserted rather than assumed.
type nodeMetricsServiceStub struct {
	snapshot    *nodemetrics.Snapshot
	snapshotErr error
	history     []nodemetrics.SeriesPoint
	refreshed   string
}

func (s *nodeMetricsServiceStub) Current(context.Context, string) (*nodemetrics.Snapshot, error) {
	return s.snapshot, s.snapshotErr
}

func (s *nodeMetricsServiceStub) History(context.Context, string, time.Time, time.Time, nodemetrics.Resolution) ([]nodemetrics.SeriesPoint, error) {
	return s.history, nil
}

func (s *nodeMetricsServiceStub) Interfaces(context.Context, string, string, time.Time, time.Time) (nodemetrics.InterfaceSeries, error) {
	return nodemetrics.InterfaceSeries{}, nil
}

func (s *nodeMetricsServiceStub) RequestRefresh(agentID string) { s.refreshed = agentID }

func newNodeMetricsRouter(t *testing.T, panel *domain.XUIPanel, agent *domain.NodeAgent, service NodeMetricsService) *gin.Engine {
	t.Helper()
	handler, err := NewAdminNodeMetricsHandler(
		nodeMetricsPanelRepo{panel: panel}, nodeMetricsAgentRepo{agent: agent}, service, nil)
	if err != nil {
		t.Fatal(err)
	}
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/servers/:id/node-metrics/current", handler.Current)
	router.GET("/servers/:id/node-metrics/history", handler.History)
	router.POST("/servers/:id/node-metrics/refresh", handler.Refresh)
	return router
}

func nativePanel() *domain.XUIPanel {
	return &domain.XUIPanel{ID: 7, Kind: domain.PanelKindPSP, Name: "node-1"}
}

func nativeAgent(capable bool) *domain.NodeAgent {
	agent := &domain.NodeAgent{AgentID: "agt_1", PanelID: 7}
	if capable {
		agent.ObservedCapabilities = []string{"host.telemetry.v1"}
	}
	return agent
}

// A 3X-UI PANEL HAS NO AGENT AND CANNOT REPORT, which is a different answer from
// "this server reported nothing" — an empty series would be the second one.
func TestNodeMetricsRefusesAServerThatCannotReport(t *testing.T) {
	router := newNodeMetricsRouter(t,
		&domain.XUIPanel{ID: 3, Kind: domain.PanelKind3XUI}, nativeAgent(true), &nodeMetricsServiceStub{})
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/servers/3/node-metrics/current", nil))
	if recorder.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", recorder.Code)
	}
	if !strings.Contains(recorder.Body.String(), "node_metrics_unsupported") {
		t.Fatalf("body = %s", recorder.Body.String())
	}
}

// THE TWO EMPTY STATES DIFFER, and the client branches on them: a node that
// claimed the capability is waiting for its first report, and one that never did
// is a version that cannot report at all.
func TestNodeMetricsDistinguishesUnsupportedFromAPendingFirstReport(t *testing.T) {
	cases := []struct {
		name          string
		capable       bool
		wantAvailable bool
		wantFreshness string
	}{
		{"a node that claimed telemetry", true, true, "missing"},
		{"a node that never did", false, false, "unsupported"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			router := newNodeMetricsRouter(t, nativePanel(), nativeAgent(testCase.capable),
				&nodeMetricsServiceStub{snapshotErr: domain.ErrNotFound})
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/servers/7/node-metrics/current", nil))
			if recorder.Code != http.StatusOK {
				t.Fatalf("status = %d", recorder.Code)
			}
			var body struct {
				Available bool   `json:"available"`
				Freshness string `json:"freshness"`
			}
			if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if body.Available != testCase.wantAvailable || body.Freshness != testCase.wantFreshness {
				t.Fatalf("available=%v freshness=%q, want %v/%q",
					body.Available, body.Freshness, testCase.wantAvailable, testCase.wantFreshness)
			}
		})
	}
}

// THE SUMMARY IS PRESENT AND NULL, never absent. A client that has to branch on
// which keys exist is one that will render the wrong thing the first time a
// metric is unavailable.
func TestNodeMetricsReturnsEverySummaryFieldAsNullWhenThereIsNoSample(t *testing.T) {
	router := newNodeMetricsRouter(t, nativePanel(), nativeAgent(true), &nodeMetricsServiceStub{snapshotErr: domain.ErrNotFound})
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/servers/7/node-metrics/current", nil))

	var body struct {
		Summary map[string]any `json:"summary"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"cpu_percent", "memory_used_percent", "disk_used_percent", "rx_bps", "tx_bps"} {
		value, exists := body.Summary[key]
		if !exists {
			t.Fatalf("summary has no %q", key)
		}
		if value != nil {
			t.Fatalf("summary[%q] = %v, want null", key, value)
		}
	}
}

// NO RAW COUNTER EVER REACHES THE CLIENT. A uint64 above 2^53 loses precision in
// JavaScript, and a counter the browser rounds disagrees with the panel that
// stored it — so the response carries rates and percentages only.
func TestNodeMetricsNeverSerialisesARawCounter(t *testing.T) {
	snapshot := &nodemetrics.Snapshot{
		Observation: &domain.NodeHostObservation{
			AgentID: "agt_1", SampleID: strings.Repeat("a", 32),
			ReceivedAt: time.Now().UTC(), CollectedAt: time.Now().UTC().Add(-time.Second),
		},
		Sample:  &domain.NodeHostMetricSample{CgroupVersion: 2, DataFilesystemScope: "host_mount"},
		Derived: nodemetrics.Derived{CPUPercent: percentPtr(21.4)},
	}
	router := newNodeMetricsRouter(t, nativePanel(), nativeAgent(true), &nodeMetricsServiceStub{snapshot: snapshot})
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/servers/7/node-metrics/current", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", recorder.Code, recorder.Body.String())
	}
	for _, forbidden := range []string{"counter", "total_bytes", "rx_bytes", "segments"} {
		if strings.Contains(recorder.Body.String(), forbidden) {
			t.Fatalf("the response carries %q: %s", forbidden, recorder.Body.String())
		}
	}
}

func percentPtr(value float64) *float64 { return &value }

// The history bounds are fixed statuses: a bad range is a 400, and an
// unavailable resolution is a 422 rather than a silent substitution.
func TestNodeMetricsHistoryBoundsAreFixedStatuses(t *testing.T) {
	router := newNodeMetricsRouter(t, nativePanel(), nativeAgent(true), &nodeMetricsServiceStub{})
	cases := []struct {
		name   string
		query  string
		status int
	}{
		{"no range", "", http.StatusBadRequest},
		{"reversed range", "?from=2026-09-18T12:00:00Z&to=2026-09-18T11:00:00Z", http.StatusBadRequest},
		{"wider than ninety days", "?from=2020-01-01T00:00:00Z&to=2026-09-18T12:00:00Z", http.StatusBadRequest},
		{"minute beyond the raw window", "?from=2026-01-01T00:00:00Z&to=2026-01-01T02:00:00Z&resolution=minute", http.StatusUnprocessableEntity},
		{"an unknown resolution", "?from=2026-09-18T11:00:00Z&to=2026-09-18T12:00:00Z&resolution=second", http.StatusBadRequest},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/servers/7/node-metrics/history"+testCase.query, nil))
			if recorder.Code != testCase.status {
				t.Fatalf("status = %d, want %d (body %s)", recorder.Code, testCase.status, recorder.Body.String())
			}
		})
	}
}

// Refresh opens the window and waits for nothing, so the answer is a 202 rather
// than a body the caller would have to poll.
func TestNodeMetricsRefreshIsAcceptedWithoutWaiting(t *testing.T) {
	service := &nodeMetricsServiceStub{}
	router := newNodeMetricsRouter(t, nativePanel(), nativeAgent(true), service)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/servers/7/node-metrics/refresh", nil))
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", recorder.Code)
	}
	if service.refreshed != "agt_1" {
		t.Fatalf("refreshed %q, want the panel's agent", service.refreshed)
	}
}
