package handler

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	nodeprotocol "github.com/KazuhaHub/passwall-node/protocol"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

type nodeSyncServiceFunc func(context.Context, nodeprotocol.NodeReport) (nodeprotocol.SyncResponse, error)

func (f nodeSyncServiceFunc) Sync(ctx context.Context, report nodeprotocol.NodeReport) (nodeprotocol.SyncResponse, error) {
	return f(ctx, report)
}

func TestNodeSyncHandlerBindsAuthenticatedIdentityToBody(t *testing.T) {
	called := false
	handler, err := NewNodeSyncHandler(nodeSyncServiceFunc(func(_ context.Context, report nodeprotocol.NodeReport) (nodeprotocol.SyncResponse, error) {
		called = true
		return nodeprotocol.SyncResponse{Envelope: nodeprotocol.Envelope{NextPollSeconds: 30}}, nil
	}), NodeAuthenticatorFunc(func(*http.Request) (string, error) { return "agt_expected", nil }))
	if err != nil {
		t.Fatal(err)
	}

	mismatch := httptest.NewRequest(http.MethodPost, "/v1/node/sync", strings.NewReader(`{"agent_id":"agt_other","partial":false,"have":{}}`))
	mismatchResult := httptest.NewRecorder()
	handler.ServeHTTP(mismatchResult, mismatch)
	if mismatchResult.Code != http.StatusUnauthorized || called {
		t.Fatalf("identity mismatch = status %d called %v", mismatchResult.Code, called)
	}

	match := httptest.NewRequest(http.MethodPost, "/v1/node/sync", strings.NewReader(`{"agent_id":"agt_expected","partial":false,"have":{"config":{"applied":{},"etag":""},"roster":{"applied":{},"etag":""},"directives":{"applied":{},"etag":""}}}`))
	matchResult := httptest.NewRecorder()
	handler.ServeHTTP(matchResult, match)
	if matchResult.Code != http.StatusOK || !called {
		t.Fatalf("matching identity = status %d called %v body %s", matchResult.Code, called, matchResult.Body.String())
	}
}

func TestNodeSyncHandlerRejectsAuthenticationAndTrailingJSON(t *testing.T) {
	service := nodeSyncServiceFunc(func(context.Context, nodeprotocol.NodeReport) (nodeprotocol.SyncResponse, error) {
		return nodeprotocol.SyncResponse{}, nil
	})
	unauthorized, err := NewNodeSyncHandler(service, NodeAuthenticatorFunc(func(*http.Request) (string, error) {
		return "", errors.New("bad credential")
	}))
	if err != nil {
		t.Fatal(err)
	}
	result := httptest.NewRecorder()
	unauthorized.ServeHTTP(result, httptest.NewRequest(http.MethodPost, "/v1/node/sync", strings.NewReader(`{}`)))
	if result.Code != http.StatusUnauthorized {
		t.Fatalf("auth failure status = %d", result.Code)
	}

	authorized, err := NewNodeSyncHandler(service, NodeAuthenticatorFunc(func(*http.Request) (string, error) {
		return "agt_expected", nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	result = httptest.NewRecorder()
	authorized.ServeHTTP(result, httptest.NewRequest(http.MethodPost, "/v1/node/sync", strings.NewReader(`{"agent_id":"agt_expected"} {}`)))
	if result.Code != http.StatusBadRequest {
		t.Fatalf("trailing JSON status = %d body=%s", result.Code, result.Body.String())
	}

	result = httptest.NewRecorder()
	authorized.ServeHTTP(result, httptest.NewRequest(http.MethodPost, "/v1/node/sync", strings.NewReader(`{"agent_id":"agt_expected","partial":false,"have":{}}`)))
	if result.Code != http.StatusBadRequest || !strings.Contains(result.Body.String(), "have.config is required") {
		t.Fatalf("invalid report status = %d body=%s", result.Code, result.Body.String())
	}
}

func TestNodeSyncHandlerHidesCoordinatorFailuresAndMarksThemRetryable(t *testing.T) {
	handler, err := NewNodeSyncHandler(nodeSyncServiceFunc(func(context.Context, nodeprotocol.NodeReport) (nodeprotocol.SyncResponse, error) {
		return nodeprotocol.SyncResponse{}, errors.New("private driver detail")
	}), NodeAuthenticatorFunc(func(*http.Request) (string, error) { return "agt_expected", nil }))
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/v1/node/sync", strings.NewReader(
		`{"agent_id":"agt_expected","partial":false,"have":{"config":{"applied":{},"etag":""},"roster":{"applied":{},"etag":""},"directives":{"applied":{},"etag":""}}}`,
	))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusInternalServerError || strings.Contains(response.Body.String(), "private") {
		t.Fatalf("coordinator failure = status %d body %s", response.Code, response.Body.String())
	}
}

func TestNodeSyncHandlerMarksDurableReceiptCapacityExhaustionRetryable(t *testing.T) {
	handler, err := NewNodeSyncHandler(nodeSyncServiceFunc(func(context.Context, nodeprotocol.NodeReport) (nodeprotocol.SyncResponse, error) {
		return nodeprotocol.SyncResponse{}, errors.Join(domain.ErrResourceExhausted, errors.New("private quota detail"))
	}), NodeAuthenticatorFunc(func(*http.Request) (string, error) { return "agt_http_receipts", nil }))
	if err != nil {
		t.Fatal(err)
	}
	response := postHTTPReceiptReport(t, handler, time.Date(2026, 9, 12, 15, 0, 0, 0, time.UTC))
	if response.Code != http.StatusTooManyRequests || strings.Contains(response.Body.String(), "private") {
		t.Fatalf("receipt capacity failure = status %d body %s", response.Code, response.Body.String())
	}
}

func TestNodeSyncHandlerRejectsOversizedCoordinatorResponse(t *testing.T) {
	handler, err := NewNodeSyncHandler(nodeSyncServiceFunc(func(context.Context, nodeprotocol.NodeReport) (nodeprotocol.SyncResponse, error) {
		return nodeprotocol.SyncResponse{
			Config: nodeprotocol.Segment[nodeprotocol.ConfigBody]{
				ETag: nodeprotocol.ETag(strings.Repeat("a", int(nodeprotocol.MaxSyncBodyBytes))),
			},
		}, nil
	}), NodeAuthenticatorFunc(func(*http.Request) (string, error) { return "agt_expected", nil }))
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/v1/node/sync", strings.NewReader(
		`{"agent_id":"agt_expected","partial":false,"have":{"config":{"applied":{},"etag":""},"roster":{"applied":{},"etag":""},"directives":{"applied":{},"etag":""}}}`,
	))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusInternalServerError || strings.Contains(response.Body.String(), strings.Repeat("a", 64)) {
		t.Fatalf("oversized coordinator response = status %d body %q", response.Code, response.Body.String())
	}
}
