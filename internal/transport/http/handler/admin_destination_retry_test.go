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

type destinationRetryStub struct {
	changed bool
	err     error
	calls   int
	agent   string
}

func (s *destinationRetryStub) RetryDestinationPolicy(_ context.Context, id string) (bool, error) {
	s.calls++
	s.agent = id
	return s.changed, s.err
}

func TestDestinationRetryHandlerDependencyAndErrorBoundaries(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, sample := range []struct {
		service *destinationRetryStub
		want    int
		payload string
	}{
		{nil, 503, "unavailable"},
		{&destinationRetryStub{changed: true}, 200, `"retry_requested":true`},
		{&destinationRetryStub{}, 200, `"retry_requested":false`},
		{&destinationRetryStub{err: domain.ErrNotFound}, 404, "Not found"},
		{&destinationRetryStub{err: domain.ErrValidation}, 400, "validation"},
		{&destinationRetryStub{err: errors.New("private database path")}, 500, "Internal server error"},
	} {
		var service DestinationPolicyRetrier
		if sample.service != nil {
			service = sample.service
		}
		r := gin.New()
		r.POST("/dest/agents/:agent_id/retry", NewAdminDestinationRetryHandler(service).Retry)
		response := httptest.NewRecorder()
		r.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/dest/agents/agt_fixture/retry", nil))
		if response.Code != sample.want || !strings.Contains(response.Body.String(), sample.payload) || strings.Contains(response.Body.String(), "private database path") {
			t.Fatalf("retry error boundary=%d body=%s", response.Code, response.Body.String())
		}
		if sample.service != nil && (sample.service.calls != 1 || sample.service.agent != "agt_fixture") {
			t.Fatal("retry changed request identity or repeated persistence")
		}
	}
}
