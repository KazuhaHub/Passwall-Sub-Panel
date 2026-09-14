package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

type nodeIssueRepoStub struct {
	ports.NodeAgentIssueRepo
	filter    ports.NodeAgentIssueFilter
	ackID     int64
	ackErr    error
	listCalls int
}

type nodeIssueLabelsStub struct {
	ports.NodeAgentIssueRepo
	items      []*domain.NodeAgentIssue
	labels     map[string]ports.NodeAgentIssueServer
	labelErr   error
	labelCalls int
	agentIDs   []string
}

func (r *nodeIssueLabelsStub) List(_ context.Context, _ ports.NodeAgentIssueFilter) ([]*domain.NodeAgentIssue, int64, error) {
	return r.items, int64(len(r.items)), nil
}

func (r *nodeIssueLabelsStub) ListIssueServers(_ context.Context, agentIDs []string) (map[string]ports.NodeAgentIssueServer, error) {
	r.labelCalls++
	r.agentIDs = append([]string(nil), agentIDs...)
	return r.labels, r.labelErr
}

func TestAdminNodeIssuesListEnrichesOneBatchWithoutChangingReports(t *testing.T) {
	gin.SetMode(gin.TestMode)
	firstSeen := time.Date(2026, 9, 14, 1, 0, 0, 0, time.UTC)
	acknowledgedAt := firstSeen.Add(time.Minute)
	repo := &nodeIssueLabelsStub{
		items: []*domain.NodeAgentIssue{
			{ID: 7, AgentID: "agt_native", Code: "pending", Key: "cli_7", Detail: "raw detail", FirstSeenAt: firstSeen, LastSeenAt: firstSeen, AcknowledgedAt: &acknowledgedAt},
			{ID: 8, AgentID: "agt_native", Code: "telemetry", Key: "xray/26.7.28"},
			{ID: 9, AgentID: "agt_deleted", Code: "pending"},
		},
		labels: map[string]ports.NodeAgentIssueServer{
			"agt_native": {ServerID: 3, ServerName: "Canada Home"},
		},
	}
	router := gin.New()
	router.GET("/issues", NewAdminNodeIssuesHandler(repo).List)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/issues", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", response.Code, response.Body.String())
	}
	var body struct {
		Items []nodeAgentIssueDTO `json:"items"`
		Total int64               `json:"total"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if repo.labelCalls != 1 || len(repo.agentIDs) != 2 || repo.agentIDs[0] != "agt_native" || repo.agentIDs[1] != "agt_deleted" {
		t.Fatalf("label batch = calls %d IDs %+v", repo.labelCalls, repo.agentIDs)
	}
	if body.Total != 3 || len(body.Items) != 3 || body.Items[0].ServerID != 3 || body.Items[0].ServerName != "Canada Home" || body.Items[1].ServerID != 3 {
		t.Fatalf("enriched response = %+v", body)
	}
	got := body.Items[0]
	if got.ID != 7 || got.AgentID != "agt_native" || got.Code != "pending" || got.Key != "cli_7" || got.Detail != "raw detail" ||
		!got.FirstSeenAt.Equal(firstSeen) || got.AcknowledgedAt == nil || !got.AcknowledgedAt.Equal(acknowledgedAt) {
		t.Fatalf("raw report changed = %+v", got)
	}
	if body.Items[2].ServerID != 0 || body.Items[2].ServerName != "" || body.Items[2].AgentID != "agt_deleted" {
		t.Fatalf("unknown agent was guessed = %+v", body.Items[2])
	}
	var raw struct {
		Items []map[string]json.RawMessage `json:"items"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &raw); err != nil {
		t.Fatal(err)
	}
	if _, present := raw.Items[2]["server_id"]; present {
		t.Fatal("unknown agent should omit server_id")
	}
	if _, present := raw.Items[2]["server_name"]; present {
		t.Fatal("unknown agent should omit server_name")
	}
}

func TestAdminNodeIssuesListMetadataFailureAndEmptyPageRemainUsable(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, empty := range []bool{false, true} {
		t.Run(strconv.FormatBool(empty), func(t *testing.T) {
			repo := &nodeIssueLabelsStub{
				items:    []*domain.NodeAgentIssue{{ID: 7, AgentID: "agt_native", Code: "pending"}},
				labelErr: errors.New("metadata unavailable"),
				labels:   map[string]ports.NodeAgentIssueServer{"agt_native": {ServerID: 3, ServerName: "must not leak failed results"}},
			}
			if empty {
				repo.items = nil
			}
			router := gin.New()
			router.GET("/issues", NewAdminNodeIssuesHandler(repo).List)
			response := httptest.NewRecorder()
			router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/issues", nil))
			if response.Code != http.StatusOK {
				t.Fatalf("status = %d body=%s", response.Code, response.Body.String())
			}
			var body struct {
				Items []nodeAgentIssueDTO `json:"items"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if empty {
				if repo.labelCalls != 0 || body.Items == nil || len(body.Items) != 0 {
					t.Fatalf("empty page = calls %d items %+v", repo.labelCalls, body.Items)
				}
			} else if repo.labelCalls != 1 || len(body.Items) != 1 || body.Items[0].ID != 7 || body.Items[0].ServerID != 0 {
				t.Fatalf("metadata failure response = calls %d items %+v", repo.labelCalls, body.Items)
			}
		})
	}
}

func (r *nodeIssueRepoStub) List(_ context.Context, filter ports.NodeAgentIssueFilter) ([]*domain.NodeAgentIssue, int64, error) {
	r.listCalls++
	r.filter = filter
	return []*domain.NodeAgentIssue{{ID: 7, AgentID: "agt_1", Code: "test"}}, 1, nil
}

func TestAdminNodeIssuesListParsesViewWithoutChangingLegacyDefault(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		query string
		view  domain.NodeAgentIssueView
	}{
		{"", ""},
		{"&view=", ""},
		{"&view=all", domain.NodeAgentIssueViewAll},
		{"&view=attention", domain.NodeAgentIssueViewAttention},
		{"&view=diagnostic", domain.NodeAgentIssueViewDiagnostic},
	} {
		t.Run(tc.query, func(t *testing.T) {
			repo := &nodeIssueRepoStub{}
			router := gin.New()
			router.GET("/issues", NewAdminNodeIssuesHandler(repo).List)
			response := httptest.NewRecorder()
			router.ServeHTTP(response, httptest.NewRequest(http.MethodGet,
				"/issues?page=2&page_size=10&keyword=Canada&agent_id=agt_1&code=test&acknowledged=false"+tc.query, nil))
			if response.Code != http.StatusOK || repo.listCalls != 1 || repo.filter.View != tc.view {
				t.Fatalf("status/calls/view = %d/%d/%q, want 200/1/%q", response.Code, repo.listCalls, repo.filter.View, tc.view)
			}
			if repo.filter.Page != 2 || repo.filter.PageSize != 10 || repo.filter.Keyword != "Canada" ||
				repo.filter.AgentID != "agt_1" || repo.filter.Code != "test" || repo.filter.Acknowledged == nil || *repo.filter.Acknowledged {
				t.Fatalf("view dropped other filters: %+v", repo.filter)
			}
		})
	}
}

func TestAdminNodeIssuesRejectsUnknownViewBeforeReadingReports(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, raw := range []string{"future", "ATTENTION", "%20attention", "diagnostics", "attention%20", "%27%20OR%201=1--"} {
		t.Run(raw, func(t *testing.T) {
			repo := &nodeIssueRepoStub{}
			router := gin.New()
			router.GET("/issues", NewAdminNodeIssuesHandler(repo).List)
			response := httptest.NewRecorder()
			router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/issues?view="+raw, nil))
			if response.Code != http.StatusBadRequest || repo.listCalls != 0 {
				t.Fatalf("invalid view status/calls = %d/%d, body=%s", response.Code, repo.listCalls, response.Body.String())
			}
		})
	}
}

func (r *nodeIssueRepoStub) Acknowledge(_ context.Context, id int64, _ time.Time) error {
	r.ackID = id
	return r.ackErr
}

func TestAdminNodeIssuesListParsesReviewFilter(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repo := &nodeIssueRepoStub{}
	router := gin.New()
	handler := NewAdminNodeIssuesHandler(repo)
	router.GET("/issues", handler.List)

	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet,
		"/issues?page=2&page_size=10&keyword=timeout&agent_id=agt_1&code=test&acknowledged=false", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", response.Code, response.Body.String())
	}
	if repo.filter.Page != 2 || repo.filter.PageSize != 10 || repo.filter.Keyword != "timeout" ||
		repo.filter.AgentID != "agt_1" || repo.filter.Code != "test" ||
		repo.filter.Acknowledged == nil || *repo.filter.Acknowledged {
		t.Fatalf("parsed filter = %+v", repo.filter)
	}
	var body struct {
		Total int64                   `json:"total"`
		Items []domain.NodeAgentIssue `json:"items"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil || body.Total != 1 || len(body.Items) != 1 {
		t.Fatalf("response = (%+v, %v)", body, err)
	}
}

func TestAdminNodeIssuesRejectsInvalidInputAndAcknowledges(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repo := &nodeIssueRepoStub{}
	router := gin.New()
	handler := NewAdminNodeIssuesHandler(repo)
	router.GET("/issues", handler.List)
	router.POST("/issues/:id/acknowledge", handler.Acknowledge)

	invalidFilter := httptest.NewRecorder()
	router.ServeHTTP(invalidFilter, httptest.NewRequest(http.MethodGet, "/issues?acknowledged=maybe", nil))
	if invalidFilter.Code != http.StatusBadRequest {
		t.Fatalf("invalid filter status = %d", invalidFilter.Code)
	}
	invalidID := httptest.NewRecorder()
	router.ServeHTTP(invalidID, httptest.NewRequest(http.MethodPost, "/issues/0/acknowledge", nil))
	if invalidID.Code != http.StatusBadRequest {
		t.Fatalf("invalid id status = %d", invalidID.Code)
	}
	ack := httptest.NewRecorder()
	router.ServeHTTP(ack, httptest.NewRequest(http.MethodPost, "/issues/7/acknowledge", nil))
	if ack.Code != http.StatusNoContent || repo.ackID != 7 {
		t.Fatalf("ack status/id = %d/%d", ack.Code, repo.ackID)
	}

	repo.ackErr = domain.ErrNotFound
	missing := httptest.NewRecorder()
	router.ServeHTTP(missing, httptest.NewRequest(http.MethodPost, "/issues/8/acknowledge", nil))
	if missing.Code != http.StatusNotFound {
		t.Fatalf("missing status = %d", missing.Code)
	}
}
