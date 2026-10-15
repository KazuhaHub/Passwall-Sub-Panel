//go:build destination_acceptance && linux

package app

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-protocol/protocol"
)

func TestNativeMixedProbePreservesRecoveryFailures(t *testing.T) {
	probe := newNativeMixedProbe("agt_owned_recovery_status")
	// Exercise the post-partition observer without waiting through a real
	// outage. Only the separate pressure acceptance proves that timing.
	probe.start = time.Now().Add(-76 * time.Second)
	body, err := json.Marshal(protocol.NodeReport{AgentID: probe.agent})
	nativeMust(t, err, "encode private recovery status report")
	for i, status := range []int{http.StatusInternalServerError, http.StatusServiceUnavailable, 0, http.StatusOK} {
		calls := 0
		handler := probe.handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls++
			observed, err := io.ReadAll(r.Body)
			if err != nil || !bytes.Equal(observed, body) {
				t.Fatal("failure observation changed the original recovery body")
			}
			w.Header().Set("X-Owned-Recovery", "unchanged")
			if status != 0 {
				w.WriteHeader(status)
				_, _ = w.Write([]byte("owned recovery response"))
			}
		}))
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/v1/node/sync", bytes.NewReader(body)))
		if calls != 1 || recorder.Header().Get("X-Owned-Recovery") != "unchanged" {
			t.Fatal("failure observation suppressed or changed the original handler")
		}
		if status != 0 && (recorder.Code != status || recorder.Body.String() != "owned recovery response") {
			t.Fatal("failure observation changed the original recovery response")
		}
		if status == 0 && recorder.Body.Len() != 0 {
			t.Fatal("failure observation fabricated a recovery response")
		}
		probe.mu.Lock()
		failures, lastStatus := probe.httpFailures, probe.lastHTTPFailureStatus
		invalid, causes, succeeded := probe.invalid, probe.invalidCauses, probe.succeeded
		probe.mu.Unlock()
		wantStatus := status
		if status == http.StatusOK {
			wantStatus = 0 // Preserve the last failure, not a later success.
		}
		if failures != uint64(min(i+1, 3)) || lastStatus != wantStatus || !invalid || causes != 16 || succeeded != uint64(i/3) {
			t.Fatal("recovery observation lost the actual failure or changed its verdict")
		}
	}
}
