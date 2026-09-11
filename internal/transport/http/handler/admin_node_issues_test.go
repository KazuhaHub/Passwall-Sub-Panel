package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

type nodeIssueRepoStub struct {
	ports.NodeAgentIssueRepo
	filter ports.NodeAgentIssueFilter
	ackID  int64
	ackErr error
}

func (r *nodeIssueRepoStub) List(_ context.Context, filter ports.NodeAgentIssueFilter) ([]*domain.NodeAgentIssue, int64, error) {
	r.filter = filter
	return []*domain.NodeAgentIssue{{ID: 7, AgentID: "agt_1", Code: "test"}}, 1, nil
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
