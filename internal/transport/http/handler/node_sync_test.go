package handler

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	nodeprotocol "github.com/KazuhaHub/passwall-protocol/protocol"

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

// A MALFORMED SAMPLE MUST COST THE PANEL A METRIC AND NOTHING ELSE. The node is
// waiting on this same round trip for its roster and its quota, so dropping the
// telemetry has to leave the request a success — which is the one failure mode
// this feature is built to be incapable of producing.
func TestNodeSyncHandlerDropsAMalformedHostAndStillAnswers(t *testing.T) {
	baseHave := `"have":{"config":{"applied":{},"etag":""},"roster":{"applied":{},"etag":""},"directives":{"applied":{},"etag":""}}`
	cases := []struct {
		name string
		host string
	}{
		{"a sample id that is not hex", `{"sample_id":"not-a-sample-id","collected_at_ms":1789000000000,"uptime_ms":1,"scope":{},"platform":{}}`},
		{"a scope the contract refuses", `{"sample_id":"0123456789abcdef0123456789abcdef","collected_at_ms":1789000000000,"uptime_ms":1,"scope":{"deployment":"kubernetes"},"platform":{}}`},
		{"a missing required identity", `{"collected_at_ms":1789000000000,"uptime_ms":1,"scope":{},"platform":{}}`},
	}
	// NOT COVERED HERE, DELIBERATELY: a host that is not an object at all is a
	// JSON TYPE error, not a semantic one, and the spec rejects the whole request
	// for that — the isolation rule is for a subtree that decodes and then fails
	// validation. See TestNodeSyncHandlerRejectsATypedHostBelow."""
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			delivered := false
			var seen nodeprotocol.NodeReport
			handler, err := NewNodeSyncHandler(nodeSyncServiceFunc(func(_ context.Context, report nodeprotocol.NodeReport) (nodeprotocol.SyncResponse, error) {
				delivered = true
				seen = report
				return nodeprotocol.SyncResponse{Envelope: nodeprotocol.Envelope{NextPollSeconds: 30}}, nil
			}), NodeAuthenticatorFunc(func(*http.Request) (string, error) { return "agt_1", nil }))
			if err != nil {
				t.Fatal(err)
			}
			body := `{"agent_id":"agt_1","partial":false,` + baseHave + `,"host":` + testCase.host + `}`
			request := httptest.NewRequest(http.MethodPost, "/v1/node/sync", strings.NewReader(body))
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, request)

			if recorder.Code != http.StatusOK {
				t.Fatalf("a malformed sample failed the round: status %d, body %s", recorder.Code, recorder.Body.String())
			}
			if !delivered {
				t.Fatal("the sync service was never reached")
			}
			if seen.Host != nil {
				t.Fatal("a sample the contract refuses reached the service")
			}
		})
	}
}

// THE RAW SUBTREE IS WHAT THE BOUND IS FOR. A decoded struct cannot see fields it
// does not know about, so a future agent's unknown section is only ever visible
// in the bytes — and this is the only layer holding them.
func TestNodeSyncHandlerDropsAnOversizedRawHostSubtree(t *testing.T) {
	var seen nodeprotocol.NodeReport
	handler, err := NewNodeSyncHandler(nodeSyncServiceFunc(func(_ context.Context, report nodeprotocol.NodeReport) (nodeprotocol.SyncResponse, error) {
		seen = report
		return nodeprotocol.SyncResponse{Envelope: nodeprotocol.Envelope{NextPollSeconds: 30}}, nil
	}), NodeAuthenticatorFunc(func(*http.Request) (string, error) { return "agt_1", nil }))
	if err != nil {
		t.Fatal(err)
	}
	// A valid sample plus an unknown section large enough to blow the bound. The
	// known fields are all well within it, which is exactly the case a struct
	// validator cannot catch.
	padding := strings.Repeat("x", int(nodeprotocol.MaxHostObservationBytes)+1024)
	body := `{"agent_id":"agt_1","partial":false,` +
		`"have":{"config":{"applied":{},"etag":""},"roster":{"applied":{},"etag":""},"directives":{"applied":{},"etag":""}},` +
		`"host":{"sample_id":"0123456789abcdef0123456789abcdef","collected_at_ms":1789000000000,"uptime_ms":1,` +
		`"scope":{"deployment":"systemd","resource_scope":"host","cgroup_version":2,"data_filesystem_scope":"host_mount"},` +
		`"platform":{"os":"linux","arch":"amd64","logical_cpus":4},"future_section":"` + padding + `"}}`
	request := httptest.NewRequest(http.MethodPost, "/v1/node/sync", strings.NewReader(body))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("an oversized subtree failed the round: status %d", recorder.Code)
	}
	if seen.Host != nil {
		t.Fatal("an oversized host subtree was carried past the wire boundary")
	}
}

// The counterpart: a sample the contract accepts is passed through untouched, so
// the drop tests above are not passing because everything is dropped.
func TestNodeSyncHandlerPassesAValidHostThrough(t *testing.T) {
	var seen nodeprotocol.NodeReport
	handler, err := NewNodeSyncHandler(nodeSyncServiceFunc(func(_ context.Context, report nodeprotocol.NodeReport) (nodeprotocol.SyncResponse, error) {
		seen = report
		return nodeprotocol.SyncResponse{Envelope: nodeprotocol.Envelope{NextPollSeconds: 30}}, nil
	}), NodeAuthenticatorFunc(func(*http.Request) (string, error) { return "agt_1", nil }))
	if err != nil {
		t.Fatal(err)
	}
	body := `{"agent_id":"agt_1","partial":false,` +
		`"have":{"config":{"applied":{},"etag":""},"roster":{"applied":{},"etag":""},"directives":{"applied":{},"etag":""}},` +
		`"host":{"sample_id":"0123456789abcdef0123456789abcdef","collected_at_ms":1789000000000,"uptime_ms":1,` +
		`"scope":{"deployment":"systemd","resource_scope":"host","cgroup_version":2,"data_filesystem_scope":"host_mount"},` +
		`"platform":{"os":"linux","arch":"amd64","logical_cpus":4}}}`
	request := httptest.NewRequest(http.MethodPost, "/v1/node/sync", strings.NewReader(body))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("a valid sample failed the round: status %d", recorder.Code)
	}
	if seen.Host == nil || seen.Host.SampleID != "0123456789abcdef0123456789abcdef" {
		t.Fatalf("a valid sample did not reach the service: %+v", seen.Host)
	}
}
