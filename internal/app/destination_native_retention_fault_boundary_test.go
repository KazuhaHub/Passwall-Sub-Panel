//go:build destination_acceptance && linux

package app

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-protocol/protocol"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

func TestNativeRetentionFaultKeepsOriginalPacketsAndRestartBoundary(t *testing.T) {
	newFault := func(t *testing.T) (*nativeRetentionResponseFault, [2]protocol.NodeReport) {
		t.Helper()
		p := &nativeRetentionResponseFault{agent: "agt_owned_retention"}
		var reports [2]protocol.NodeReport
		for i, kind := range []string{"block", "observe"} {
			audit := fixtureAudit(t, destinationPolicyFixture{user: &domain.User{ID: 1}}, i+1)
			audit.Kind, audit.Hits[0].Action = kind, kind
			nativeMust(t, protocol.ValidateAuditObservation(*audit), "validate retention packet fixture")
			reports[i] = protocol.NodeReport{AgentID: p.agent, Audit: audit}
		}
		return p, reports
	}
	request := func(t *testing.T, report protocol.NodeReport) *http.Request {
		t.Helper()
		body, err := json.Marshal(report)
		nativeMust(t, err, "encode private retention report")
		r := httptest.NewRequest(http.MethodPost, "/v1/node/sync", bytes.NewReader(body))
		r.Header.Set("Authorization", "Bearer owned-retention-credential")
		return r
	}
	send := func(t *testing.T, p *nativeRetentionResponseFault, report protocol.NodeReport) *httptest.ResponseRecorder {
		t.Helper()
		response := httptest.NewRecorder()
		p.ServeHTTP(response, request(t, report))
		return response
	}
	accepted := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer owned-retention-credential" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Add("X-Owned-Response", "first")
		w.Header().Add("X-Owned-Response", "second")
		_, _ = io.WriteString(w, "original response")
	})
	capture := func(t *testing.T, p *nativeRetentionResponseFault, reports [2]protocol.NodeReport) {
		t.Helper()
		p.original = accepted
		if !p.arm() {
			t.Fatal("could not arm capture")
		}
		for _, report := range reports {
			if response := send(t, p, report); response.Code != http.StatusServiceUnavailable || response.Body.Len() != 0 {
				t.Fatal("capture returned an original success response")
			}
		}
	}
	t.Run("unarmed_and_foreign_requests_keep_original_bytes_and_headers", func(t *testing.T) {
		p, reports := newFault(t)
		r := request(t, reports[0])
		expected, err := io.ReadAll(r.Body)
		nativeMust(t, err, "read request vector")
		calls := 0
		p.original = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls++
			body, err := io.ReadAll(r.Body)
			if err != nil || !bytes.Equal(body, expected) {
				t.Error("fault changed original report bytes")
			}
			accepted.ServeHTTP(w, r)
		})
		before := send(t, p, reports[0])
		if before.Code != 200 || before.Body.String() != "original response" || len(before.Header().Values("X-Owned-Response")) != 2 || !p.arm() {
			t.Fatal("inactive endpoint altered the original response")
		}
		p.agent = "agt_other_retention"
		response := send(t, p, reports[0])
		facts, valid := p.snapshot()
		if calls != 2 || response.Code != 200 || response.Body.String() != "original response" || !valid || facts.Suppressed != [2]uint64{} {
			t.Fatal("fault captured another agent")
		}
	})
	t.Run("capture_is_bounded_immutable_and_defensively_copied", func(t *testing.T) {
		p, reports := newFault(t)
		capture(t, p, reports)
		for range 20 {
			for _, report := range reports {
				_ = send(t, p, report)
			}
		}
		facts, valid := p.snapshot()
		if !valid || facts.Suppressed != [2]uint64{21, 21} || facts.Retries != [2]uint64{20, 20} || facts.CapturedRows != [2]int{1, 1} || facts.CanonicalBytes[0] <= 0 || facts.CanonicalBytes[1] <= 0 || p.arm() {
			t.Fatal("capture lost frozen identity or reset its scope")
		}
		original := p.privateRequest(0)
		changed := p.privateRequest(0)
		changed[0] ^= 1
		if !bytes.Equal(original, p.privateRequest(0)) || p.privateRequest(-1) != nil || p.privateRequest(2) != nil {
			t.Fatal("private packet copy aliased the frozen original")
		}
		public, err := json.Marshal(facts)
		nativeMust(t, err, "encode numeric transport facts")
		for _, private := range []string{p.agent, reports[0].Audit.BatchID, "owned-retention-credential", "example.test"} {
			if bytes.Contains(public, []byte(private)) {
				t.Fatal("numeric transport facts leaked a private packet field")
			}
		}
	})
	t.Run("authentication_failure_is_not_captured_or_hidden", func(t *testing.T) {
		p, reports := newFault(t)
		p.original = accepted
		if !p.arm() {
			t.Fatal("could not arm authentication vector")
		}
		r := request(t, reports[0])
		r.Header.Set("Authorization", "Bearer incorrect")
		response := httptest.NewRecorder()
		p.ServeHTTP(response, r)
		facts, valid := p.snapshot()
		if valid || response.Code != 401 || facts.OriginalErrors != 1 || facts.Suppressed != [2]uint64{} || p.privateRequest(0) != nil || p.isolate() {
			t.Fatal("authentication rejection became a captured success")
		}
	})
	t.Run("changed_original_invalidates_evidence_without_replacing_it", func(t *testing.T) {
		p, reports := newFault(t)
		capture(t, p, reports)
		original := p.privateRequest(0)
		reports[0].Audit.Hits[0].Count++
		_ = send(t, p, reports[0])
		_, valid := p.snapshot()
		if valid || p.isolate() || !bytes.Equal(original, p.privateRequest(0)) {
			t.Fatal("changed retry replaced original packet evidence")
		}
	})
	t.Run("offline_partition_blocks_even_sync_without_audit", func(t *testing.T) {
		p, reports := newFault(t)
		p.original = accepted
		if p.isolate() || p.replace(accepted) || p.resumeBlock() {
			t.Fatal("retention phase skipped original capture")
		}
		capture(t, p, reports)
		if !p.isolate() || p.isolate() || p.resumeBlock() {
			t.Fatal("partition reset its start or skipped restart")
		}
		p.original = http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("offline sync reached production") })
		for _, report := range []protocol.NodeReport{reports[0], reports[1], {AgentID: p.agent}} {
			if send(t, p, report).Code != 503 {
				t.Fatal("offline sync received an acknowledgement")
			}
		}
		facts, valid := p.snapshot()
		if !valid || facts.Offline != 3 || facts.IsolatedMS <= 0 || facts.ResumedMS != 0 {
			t.Fatal("partition did not retain its original time and request counts")
		}
	})
	t.Run("replacement_and_real_26h_guard_precede_independent_lane_recovery", func(t *testing.T) {
		p, reports := newFault(t)
		capture(t, p, reports)
		if !p.isolate() || p.replace(nil) || !p.replace(accepted) || p.replace(accepted) || p.resumeBlock() {
			t.Fatal("restart endpoint accepted nil, repeated or premature recovery")
		}
		// Synthetic boundary state exercises routing only. This is explicitly not
		// evidence of real elapsed hours, a production restart or SQL durability.
		p.isolated = time.Now().Add(-26 * time.Hour)
		if !p.resumeBlock() || p.resumeBlock() || p.replace(accepted) {
			t.Fatal("recovery phase reset or replaced an active endpoint")
		}
		block := send(t, p, reports[0])
		if block.Code != 200 || block.Body.String() != "original response" || len(block.Header().Values("X-Owned-Response")) != 2 || send(t, p, reports[1]).Code != 503 || send(t, p, protocol.NodeReport{AgentID: p.agent}).Code != 200 {
			t.Fatal("block recovery released observe or changed original response")
		}
		facts, valid := p.snapshot()
		if !valid || facts.Recovered != 1 || facts.Offline != 1 || facts.Replacements != 1 || facts.ReplacedMS < facts.IsolatedMS || facts.ResumedMS < facts.ReplacedMS {
			t.Fatal("restart routing facts do not describe the actual transitions")
		}
	})
	t.Run("read_errors_and_oversized_tails_reach_original_handler", func(t *testing.T) {
		for _, oversized := range []bool{false, true} {
			p, _ := newFault(t)
			body := "{\"agent_id\":\"agt_owned_retention\","
			if oversized {
				body += strings.Repeat("x", int(protocol.MaxSyncBodyBytes)+128)
			}
			p.original = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				actual, err := io.ReadAll(r.Body)
				if string(actual) != body || (!oversized && err == nil) || (oversized && err != nil) {
					t.Error("fault swallowed an input error or oversized request tail")
				}
				w.WriteHeader(413)
			})
			if !p.arm() {
				t.Fatal("could not arm malformed vector")
			}
			r := httptest.NewRequest(http.MethodPost, "/v1/node/sync", strings.NewReader(body))
			if !oversized {
				r.Body = io.NopCloser(io.MultiReader(strings.NewReader(body), nativeGlobalTransportReadError{errors.New("owned read error")}))
			}
			response := httptest.NewRecorder()
			p.ServeHTTP(response, r)
			if response.Code != 413 || p.privateRequest(0) != nil {
				t.Fatal("malformed report became a captured packet")
			}
		}
	})
	t.Run("isolate_joins_the_inflight_original_handler", func(t *testing.T) {
		p, reports := newFault(t)
		capture(t, p, reports)
		entered, release := make(chan struct{}), make(chan struct{})
		p.original = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			close(entered)
			<-release
			accepted.ServeHTTP(w, r)
		})
		requestDone := make(chan struct{})
		r := request(t, reports[0])
		go func() {
			p.ServeHTTP(httptest.NewRecorder(), r)
			close(requestDone)
		}()
		<-entered
		isolated := make(chan bool, 1)
		go func() { isolated <- p.isolate() }()
		select {
		case <-isolated:
			t.Error("isolate returned before the original request completed")
		case <-time.After(20 * time.Millisecond):
		}
		close(release)
		<-requestDone
		select {
		case ok := <-isolated:
			if !ok {
				t.Fatal("could not isolate after joining original request")
			}
		case <-time.After(3 * time.Second):
			t.Fatal("partition did not join its original handler")
		}
	})
}
