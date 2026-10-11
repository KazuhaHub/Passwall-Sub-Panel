package app

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/KazuhaHub/passwall-protocol/protocol"
)

type nativeLegacySnapshot struct {
	Sequence                                         uint64
	AgentVersion, CoreEngine, CoreVersion, CoreState string
	PolicyCapable, PolicyPresent                     bool
	HaveConfig, HaveRoster                           protocol.StreamState
	Config                                           protocol.Segment[protocol.ConfigBody]
	Roster                                           protocol.Segment[protocol.RosterBody]
	ListenerApplied                                  bool
}

type nativeLegacyProbe struct {
	mu       sync.Mutex
	latest   nativeLegacySnapshot
	listener protocol.ListenerKey
}

func (p *nativeLegacyProbe) snapshot() nativeLegacySnapshot {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.latest
}

func (p *nativeLegacyProbe) wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/v1/node/sync" {
			next.ServeHTTP(w, r)
			return
		}
		input, output := &nativeBoundedReport{}, &nativeBoundedReport{}
		r.Body = &nativeTeeBody{Reader: io.TeeReader(r.Body, input), Closer: r.Body}
		response := &nativeLegacyResponse{nativeProbeResponse: nativeProbeResponse{ResponseWriter: w}, output: output}
		next.ServeHTTP(response, r)
		var report protocol.NodeReport
		var reply protocol.SyncResponse
		if input.truncated || output.truncated || response.code != 200 || json.Unmarshal(input.buf.Bytes(), &report) != nil || json.Unmarshal(output.buf.Bytes(), &reply) != nil {
			return
		}
		s := nativeLegacySnapshot{AgentVersion: report.AgentVersion, CoreEngine: report.CoreEngine, CoreVersion: report.CoreVersion, CoreState: report.CoreState, HaveConfig: report.Have[protocol.StreamConfig], HaveRoster: report.Have[protocol.StreamRoster]}
		for _, capability := range report.Capabilities {
			s.PolicyCapable = s.PolicyCapable || capability == protocol.CapabilityDestinationPolicy
		}
		s.PolicyPresent = report.PolicyStatus != nil || reply.Config.Body != nil && reply.Config.Body.Policy != nil
		for _, object := range report.Objects {
			s.ListenerApplied = s.ListenerApplied || object.Stream == protocol.StreamConfig && object.Key == string(p.listener) && object.State == protocol.ObjectApplied
		}
		// Retain stream validators, never listener credentials or roster bodies.
		s.Config = protocol.Segment[protocol.ConfigBody]{Version: reply.Config.Version, ETag: reply.Config.ETag, Unchanged: reply.Config.Unchanged}
		s.Roster = protocol.Segment[protocol.RosterBody]{Version: reply.Roster.Version, ETag: reply.Roster.ETag, Unchanged: reply.Roster.Unchanged}
		p.mu.Lock()
		s.Sequence = p.latest.Sequence + 1
		p.latest = s
		p.mu.Unlock()
	})
}

type nativeLegacyResponse struct {
	nativeProbeResponse
	output *nativeBoundedReport
}

func (w *nativeLegacyResponse) Write(data []byte) (int, error) {
	if w.code == 0 {
		w.code = 200
	}
	n, err := w.ResponseWriter.Write(data)
	_, _ = w.output.Write(data[:n])
	return n, err
}

func TestNativeLegacyProbePreservesWireAndIgnoresRejectedOrOversizedSync(t *testing.T) {
	const input = `{"agent_version":"v4.0.1.8","core_engine":"xray","core_version":"26.6.27","core_state":"running","have":{"config":{"applied":{"epoch":1,"version":2},"etag":"config"}},"objects":[{"stream":"config","key":"lst_1","state":"applied"}]}`
	const output = `{"config":{"version":{"epoch":1,"version":2},"etag":"config","unchanged":true},"roster":{"etag":"roster","unchanged":true}}`
	for _, tc := range []struct {
		name, body string
		code       int
		want       uint64
	}{{"accepted", input, 200, 1}, {"rejected", input, 401, 0}, {"malformed", "private invalid", 200, 0}, {"oversized", `{"private":"` + strings.Repeat("x", 2<<20) + `"}`, 200, 0}} {
		t.Run(tc.name, func(t *testing.T) {
			p := &nativeLegacyProbe{listener: protocol.NewListenerKey(1)}
			response := httptest.NewRecorder()
			p.wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				data, err := io.ReadAll(r.Body)
				if err != nil || string(data) != tc.body {
					t.Fatal("legacy probe changed original input")
				}
				w.WriteHeader(tc.code)
				_, _ = io.WriteString(w, output)
			})).ServeHTTP(response, httptest.NewRequest("POST", "/v1/node/sync", strings.NewReader(tc.body)))
			if response.Code != tc.code || response.Body.String() != output {
				t.Fatal("legacy probe changed original output")
			}
			s := p.snapshot()
			if s.Sequence != tc.want {
				t.Fatal("legacy probe accepted invalid exchange")
			}
			if tc.want == 1 && (!s.ListenerApplied || s.PolicyCapable || s.PolicyPresent || s.Config.Body != nil || s.HaveConfig.ETag != "config") {
				t.Fatal("legacy probe lost actual convergence or retained private bodies")
			}
		})
	}
}
