package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	protocol "github.com/KazuhaHub/passwall-protocol/protocol"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/metrics"
)

func TestNodeSyncIsolatesPolicyStatusFromControl(t *testing.T) {
	valid := `{"state":"applied","digest":"` + strings.Repeat("a", 64) + `"}`
	for _, tc := range []struct {
		name, status         string
		capability, accepted bool
	}{
		{"absent", "", false, false},
		{"null", "null", true, false},
		{"applied", valid, true, true},
		{"without capability", valid, false, false},
		{"scalar", `"bad"`, true, false},
		{"array", `[]`, true, false},
		{"wrong digest type", `{"state":"applied","digest":7}`, true, false},
		{"unknown state", `{"state":"unknown"}`, true, false},
		{"bad digest", `{"state":"applied","digest":"secret-peer-input"}`, true, false},
		{"bad listener type", `{"state":"rejected","listeners":[7]}`, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var got *protocol.PolicyStatus
			called := false
			h, err := NewNodeSyncHandler(nodeSyncServiceFunc(func(_ context.Context, report protocol.NodeReport) (protocol.SyncResponse, error) {
				called, got = true, report.PolicyStatus
				if string(report.Have[protocol.StreamRoster].ETag) != strings.Repeat("b", 64) {
					t.Fatal("control state was lost")
				}
				return protocol.SyncResponse{Envelope: protocol.Envelope{NextPollSeconds: 30}}, nil
			}), NodeAuthenticatorFunc(func(*http.Request) (string, error) { return "agt_policy", nil }))
			if err != nil {
				t.Fatal(err)
			}
			body := policyStatusReport(tc.status, tc.capability)
			before := metrics.NodePolicyStatusDroppedTotal.Value()
			result := httptest.NewRecorder()
			h.ServeHTTP(result, httptest.NewRequest(http.MethodPost, "/v1/node/sync", strings.NewReader(body)))
			if result.Code != http.StatusOK || !called || (got != nil) != tc.accepted {
				t.Fatalf("status=%d called=%v policy_status=%+v body=%s", result.Code, called, got, result.Body.String())
			}
			if tc.accepted && (got.State != "applied" || got.Digest != strings.Repeat("a", 64)) {
				t.Fatalf("status changed: %+v", got)
			}
			wantDropped := int64(1)
			if tc.accepted || tc.status == "" || tc.status == "null" {
				wantDropped = 0
			}
			if delta := metrics.NodePolicyStatusDroppedTotal.Value() - before; delta != wantDropped {
				t.Fatalf("drop count = %d, want %d", delta, wantDropped)
			}
		})
	}
}

func TestNodeSyncPolicyStatusDoesNotHideInvalidControl(t *testing.T) {
	called := false
	h, err := NewNodeSyncHandler(nodeSyncServiceFunc(func(context.Context, protocol.NodeReport) (protocol.SyncResponse, error) {
		called = true
		return protocol.SyncResponse{}, nil
	}), NodeAuthenticatorFunc(func(*http.Request) (string, error) { return "agt_policy", nil }))
	if err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{
		strings.Replace(policyStatusReport(`"bad"`, true), `"`+strings.Repeat("b", 64)+`"`, `7`, 1),
		policyStatusReport(`"bad"`, true) + `{}`,
		strings.Replace(policyStatusReport(`"bad"`, true), `"roster":`, `"other":`, 1),
	} {
		before := metrics.NodePolicyStatusDroppedTotal.Value()
		result := httptest.NewRecorder()
		h.ServeHTTP(result, httptest.NewRequest(http.MethodPost, "/v1/node/sync", strings.NewReader(body)))
		if result.Code != http.StatusBadRequest || called {
			t.Fatalf("status=%d called=%v", result.Code, called)
		}
		if metrics.NodePolicyStatusDroppedTotal.Value() != before {
			t.Fatal("invalid control was counted as a policy drop")
		}
	}
}

func policyStatusReport(status string, capability bool) string {
	body := `{"agent_id":"agt_policy","have":{"config":{"applied":{},"etag":""},"roster":{"applied":{"epoch":1,"version":1},"etag":"old-roster"},"directives":{"applied":{},"etag":""}}`
	if capability {
		body += `,"capabilities":["policy.destination.v1"]`
	}
	if status != "" {
		if !json.Valid([]byte(status)) {
			panic("invalid test JSON")
		}
		body += `,"policy_status":` + status
	}
	return strings.Replace(body+`}`, "old-roster", strings.Repeat("b", 64), 1)
}
