package handler

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	nodeprotocol "github.com/KazuhaHub/passwall-protocol/protocol"

	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/metrics"
)

// A node's policy status is a control input but never worth failing a sync
// for. The report is decoded through a shadow struct whose policy_status is
// raw bytes, so a value that does not decode (a number where a string goes, a
// state the protocol does not know, an oversized list) only drops that
// subtree: the sync still answers 200, the roster and config still flow, and
// the drop is counted.

const syncHave = `"have":{"config":{"applied":{},"etag":""},"roster":{"applied":{},"etag":""},"directives":{"applied":{},"etag":""}}`

func postPolicyReport(t *testing.T, statusJSON string, caps string) (code int, got *nodeprotocol.NodeReport) {
	t.Helper()
	h, err := NewNodeSyncHandler(nodeSyncServiceFunc(func(_ context.Context, report nodeprotocol.NodeReport) (nodeprotocol.SyncResponse, error) {
		got = &report
		return nodeprotocol.SyncResponse{Envelope: nodeprotocol.Envelope{NextPollSeconds: 30}}, nil
	}), NodeAuthenticatorFunc(func(*http.Request) (string, error) { return "agt_p", nil }))
	if err != nil {
		t.Fatal(err)
	}
	body := fmt.Sprintf(`{"agent_id":"agt_p","partial":false,%s,"capabilities":[%s]`, syncHave, caps)
	if statusJSON != "" {
		body += `,"policy_status":` + statusJSON
	}
	body += `}`
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/node/sync", strings.NewReader(body)))
	return rec.Code, got
}

func TestNodeSyncHandler_PolicyStatusIsDecodedInIsolation(t *testing.T) {
	cap := `"` + nodeprotocol.CapabilityDestinationPolicy + `"`
	digest := strings.Repeat("a", 64)
	for _, c := range []struct {
		name, status, caps string
		wantKept           bool
		wantDropped        int64
	}{
		{"valid", `{"digest":"` + digest + `","state":"applied"}`, cap, true, 0},
		{"valid rejection", `{"digest":"` + digest + `","state":"rejected","issue_code":"destination_policy_rejected"}`, cap, true, 0},
		{"absent", ``, cap, false, 0},
		{"null", `null`, cap, false, 0},
		{"wrong type", `{"digest":7,"state":"applied"}`, cap, false, 1},
		{"not an object", `"applied"`, cap, false, 1},
		{"unknown state", `{"digest":"` + digest + `","state":"limbo"}`, cap, false, 1},
		{"bad digest", `{"digest":"nothex","state":"applied"}`, cap, false, 1},
		{"applied with an issue", `{"digest":"` + digest + `","state":"applied","issue_code":"destination_policy_rejected"}`, cap, false, 1},
		{"no capability", `{"digest":"` + digest + `","state":"applied"}`, ``, false, 1},
		{"oversized", `{"digest":"` + digest + `","state":"rejected","issue_code":"destination_policy_sniffing_insufficient","listeners":[` + strings.Repeat(`"lst_1",`, 4000) + `"lst_2"]}`, cap, false, 1},
	} {
		t.Run(c.name, func(t *testing.T) {
			before := metrics.NodePolicyStatusDroppedTotal.Value()
			code, got := postPolicyReport(t, c.status, c.caps)
			if code != http.StatusOK {
				t.Fatalf("status %d: a bad policy_status must never fail the sync", code)
			}
			if got == nil {
				t.Fatal("the report never reached the service")
			}
			if kept := got.PolicyStatus != nil; kept != c.wantKept {
				t.Fatalf("kept = %v, want %v", kept, c.wantKept)
			}
			if dropped := metrics.NodePolicyStatusDroppedTotal.Value() - before; dropped != c.wantDropped {
				t.Fatalf("dropped counter moved by %d, want %d", dropped, c.wantDropped)
			}
		})
	}
}

// The rest of the report is untouched by the shadow decode.
func TestNodeSyncHandler_ShadowDecodeKeepsTheOtherFields(t *testing.T) {
	cap := `"` + nodeprotocol.CapabilityDestinationPolicy + `"`
	_, got := postPolicyReport(t, `{"digest":7}`, cap)
	if got == nil || got.AgentID != "agt_p" || len(got.Capabilities) != 1 || got.Capabilities[0] != nodeprotocol.CapabilityDestinationPolicy {
		t.Fatalf("report = %+v", got)
	}
}
