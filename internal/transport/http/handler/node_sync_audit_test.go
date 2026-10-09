package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	protocol "github.com/KazuhaHub/passwall-protocol/protocol"
)

func auditReportFixture() protocol.AuditObservation {
	const hour = int64(1_800_000_000_000)
	return protocol.AuditObservation{BatchID: strings.Repeat("a", 32), Kind: "block", Hour: hour, CollectRevision: 1,
		Hits: []protocol.AuditHit{{Hour: hour, RuleID: "p1", Action: "block", Subject: "usr_7", Dest: "example.com", Port: 443, Count: 1, FirstMS: hour + 1, LastMS: hour + 2}}}
}

func nodeAuditReport(raw string, capabilities []string, partial bool) string {
	var body map[string]any
	if err := json.Unmarshal([]byte(policyStatusReport("", false)), &body); err != nil {
		panic(err)
	}
	if raw != "" {
		body["audit"] = json.RawMessage(raw)
	}
	if len(capabilities) > 0 {
		body["capabilities"] = capabilities
	}
	if partial {
		body["partial"] = true
	}
	wire, err := json.Marshal(body)
	if err != nil {
		panic(err)
	}
	return string(wire)
}

func TestNodeSyncAuditDecodeAndCapabilityIsolation(t *testing.T) {
	valid := auditReportFixture()
	encoded, err := json.Marshal(valid)
	if err != nil {
		t.Fatal(err)
	}
	validJSON := string(encoded)
	usage := valid
	usage.Kind, usage.Hits, usage.Dropped = "usage", nil, 1
	usageWire, _ := json.Marshal(usage)
	trial := valid
	trial.Kind, trial.Hits[0].RuleID, trial.Hits[0].Action = "trial", "g1", "observe"
	trial.Hits[0].Subject, trial.Hits[0].Port = "", 0
	trialWire, _ := json.Marshal(trial)
	for _, tc := range []struct {
		name, raw             string
		hits, usage, accepted bool
	}{
		{"absent", "", false, false, false}, {"null", "null", true, false, false},
		{"valid", validJSON, true, false, true}, {"without hits capability", validJSON, false, false, false},
		{"counter-only usage missing capability", string(usageWire), true, false, false},
		{"counter-only usage", string(usageWire), true, true, true}, {"usage capability alone", string(usageWire), false, true, false},
		{"anonymous trial", string(trialWire), true, false, true},
		{"scalar", `"bad"`, true, false, false}, {"array", `[]`, true, false, false},
		{"overflow port", strings.Replace(validJSON, `"port":443`, `"port":70000`, 1), true, false, false},
		{"count type", strings.Replace(validJSON, `"count":1`, `"count":"bad"`, 1), true, false, false},
		{"invalid count", strings.Replace(validJSON, `"count":1`, `"count":0`, 1), true, false, false},
		{"invalid kind", strings.Replace(validJSON, `"kind":"block"`, `"kind":"peer-private-kind"`, 1), true, false, false},
		{"invalid revision", strings.Replace(validJSON, `"collect_revision":1`, `"collect_revision":0`, 1), true, false, false},
		{"oversized ignored extension", strings.TrimSuffix(validJSON, "}") + `,"extra":"` + strings.Repeat("x", protocol.MaxAuditObservationBytes) + `"}`, true, false, false},
	} {
		for _, partial := range []bool{false, true} {
			name := tc.name + "/full"
			if partial {
				name = tc.name + "/partial"
			}
			t.Run(name, func(t *testing.T) {
				called := false
				var got *protocol.AuditObservation
				handler, err := NewNodeSyncHandler(nodeSyncServiceFunc(func(_ context.Context, report protocol.NodeReport) (protocol.SyncResponse, error) {
					called, got = true, report.Audit
					if report.Partial != partial || string(report.Have[protocol.StreamRoster].ETag) != strings.Repeat("b", 64) {
						t.Fatal("audit changed control state")
					}
					return protocol.SyncResponse{Envelope: protocol.Envelope{NextPollSeconds: 30}}, nil
				}), NodeAuthenticatorFunc(func(*http.Request) (string, error) { return "agt_policy", nil }))
				if err != nil {
					t.Fatal(err)
				}
				var caps []string
				if tc.hits {
					caps = append(caps, protocol.CapabilityAuditHits)
				}
				if tc.usage {
					caps = append(caps, protocol.CapabilityAuditUsage)
				}
				response := httptest.NewRecorder()
				handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/v1/node/sync", strings.NewReader(nodeAuditReport(tc.raw, caps, partial))))
				if response.Code != 200 || !called || (got != nil) != tc.accepted {
					t.Fatalf("control status=%d called=%v audit retained=%v; want retained=%v", response.Code, called, got != nil, tc.accepted)
				}
			})
		}
	}
}

func TestNodeSyncAuditDoesNotHideInvalidControl(t *testing.T) {
	called := false
	handler, err := NewNodeSyncHandler(nodeSyncServiceFunc(func(context.Context, protocol.NodeReport) (protocol.SyncResponse, error) {
		called = true
		return protocol.SyncResponse{}, nil
	}), NodeAuthenticatorFunc(func(*http.Request) (string, error) { return "agt_policy", nil }))
	if err != nil {
		t.Fatal(err)
	}
	body := nodeAuditReport(`"bad-audit"`, []string{protocol.CapabilityAuditHits}, false)
	for _, invalid := range []string{body + `{}`, strings.Replace(body, `"roster":`, `"other":`, 1), strings.Replace(body, `"`+strings.Repeat("b", 64)+`"`, `7`, 1)} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/v1/node/sync", strings.NewReader(invalid)))
		if response.Code != 400 || called {
			t.Fatalf("invalid control status=%d called=%v", response.Code, called)
		}
	}
}
