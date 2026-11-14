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

// A node's audit subtree is hit data and never worth failing a sync for. Like
// the policy status it is held back as raw bytes by the shadow struct, measured
// raw, decoded and validated on its own and capability-checked; any failure
// drops the subtree (counted), and the sync, the roster and the config go on.

const hitsCap = `"` + nodeprotocol.CapabilityAuditHits + `"`
const usageCap = `"` + nodeprotocol.CapabilityAuditUsage + `"`

func postAuditReport(t *testing.T, auditJSON string, caps string) (code int, got *nodeprotocol.NodeReport) {
	t.Helper()
	h, err := NewNodeSyncHandler(nodeSyncServiceFunc(func(_ context.Context, report nodeprotocol.NodeReport) (nodeprotocol.SyncResponse, error) {
		got = &report
		return nodeprotocol.SyncResponse{Envelope: nodeprotocol.Envelope{NextPollSeconds: 30}}, nil
	}), NodeAuthenticatorFunc(func(*http.Request) (string, error) { return "agt_a", nil }))
	if err != nil {
		t.Fatal(err)
	}
	body := fmt.Sprintf(`{"agent_id":"agt_a","partial":false,%s,"capabilities":[%s]`, syncHave, caps)
	if auditJSON != "" {
		body += `,"audit":` + auditJSON
	}
	body += `}`
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/node/sync", strings.NewReader(body)))
	return rec.Code, got
}

const batch = `"batch_id":"00000000000000000000000000000001"`

func validHit(count string) string {
	return `{"hour":` + hourAligned() + `,"rule_id":"p12x1","action":"block","subject":"usr_7","dest":"bad.example","port":443,"count":` + count + `,"first_ms":` + hourAligned() + `,"last_ms":` + hourAligned() + `}`
}

func hourAligned() string { return fmt.Sprint(int64(1763000000000) / 3600000 * 3600000) }

func TestNodeSyncHandler_AuditIsDecodedInIsolation(t *testing.T) {
	good := `{` + batch + `,"hits":[` + validHit("3") + `],"dropped_hits":0,"dropped_trial":0,"dropped_usage":0,"unmatched":0}`
	for _, c := range []struct {
		name, audit, caps string
		wantKept          bool
		wantOutcome       string
	}{
		{"valid", good, hitsCap, true, ""},
		{"absent", ``, hitsCap, false, ""},
		{"null", `null`, hitsCap, false, ""},
		{"no capability", good, ``, false, metrics.NodeAuditNoCapability},
		{"only the usage capability", good, usageCap, false, metrics.NodeAuditNoCapability},
		{"a port out of range", `{` + batch + `,"hits":[` + strings.Replace(validHit("1"), `"port":443`, `"port":70000`, 1) + `]}`, hitsCap, false, metrics.NodeAuditInvalid},
		{"a count that is a string", `{` + batch + `,"hits":[` + strings.Replace(validHit("1"), `"count":1`, `"count":"many"`, 1) + `]}`, hitsCap, false, metrics.NodeAuditInvalid},
		{"not an object", `"hits"`, hitsCap, false, metrics.NodeAuditInvalid},
		{"a bad batch id", `{"batch_id":"short","hits":[` + validHit("1") + `]}`, hitsCap, false, metrics.NodeAuditInvalid},
		{"an empty observation", `{` + batch + `}`, hitsCap, false, metrics.NodeAuditInvalid},
		{"an hour off the grid", `{` + batch + `,"hits":[` + strings.Replace(validHit("1"), `"hour":`+hourAligned(), `"hour":`+fmt.Sprint(int64(1763000000001)), 1) + `]}`, hitsCap, false, metrics.NodeAuditInvalid},
		{"an uppercase destination", `{` + batch + `,"hits":[` + strings.Replace(validHit("1"), "bad.example", "BAD.example", 1) + `]}`, hitsCap, false, metrics.NodeAuditInvalid},
		{"oversized", `{` + batch + `,"hits":[` + validHit("1") + `],"pad":"` + strings.Repeat("a", nodeprotocol.MaxAuditObservationBytes) + `"}`, hitsCap, false, metrics.NodeAuditOversized},
	} {
		t.Run(c.name, func(t *testing.T) {
			counters := map[string]int64{}
			for _, o := range []string{metrics.NodeAuditNoCapability, metrics.NodeAuditInvalid, metrics.NodeAuditOversized} {
				counters[o] = metrics.NodeAuditReportTotal.With(o).Value()
			}
			code, got := postAuditReport(t, c.audit, c.caps)
			if code != http.StatusOK {
				t.Fatalf("status %d: a bad audit subtree must never fail the sync", code)
			}
			if got == nil {
				t.Fatal("the report never reached the service")
			}
			if kept := got.Audit != nil; kept != c.wantKept {
				t.Fatalf("kept = %v, want %v", kept, c.wantKept)
			}
			for o, before := range counters {
				moved := metrics.NodeAuditReportTotal.With(o).Value() - before
				want := int64(0)
				if o == c.wantOutcome {
					want = 1
				}
				if moved != want {
					t.Errorf("%s moved by %d, want %d", o, moved, want)
				}
			}
		})
	}
}

// Usage rows without audit.usage.v1 are dropped; the hits beside them are kept.
func TestNodeSyncHandler_AuditUsageNeedsItsOwnCapability(t *testing.T) {
	both := `{` + batch + `,"hits":[` + validHit("1") + `],"usage":[{"hour":` + hourAligned() + `,"subject":"usr_7","site":"bad.example","count":1}]}`
	_, got := postAuditReport(t, both, hitsCap)
	if got == nil || got.Audit == nil || len(got.Audit.Hits) != 1 || len(got.Audit.Usage) != 0 {
		t.Fatalf("audit = %+v: the usage rows must go and the hits stay", got)
	}
	_, got = postAuditReport(t, both, hitsCap+","+usageCap)
	if got == nil || got.Audit == nil || len(got.Audit.Usage) != 1 {
		t.Fatalf("audit = %+v: with both capabilities the usage rows are kept for the ingest to refuse", got)
	}
}

// The rest of the report is untouched by the shadow decode.
func TestNodeSyncHandler_AuditShadowDecodeKeepsTheOtherFields(t *testing.T) {
	_, got := postAuditReport(t, `{"hits":"not rows"}`, hitsCap)
	if got == nil || got.AgentID != "agt_a" || len(got.Capabilities) != 1 || got.Capabilities[0] != nodeprotocol.CapabilityAuditHits {
		t.Fatalf("report = %+v", got)
	}
}
