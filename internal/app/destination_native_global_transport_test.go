//go:build destination_acceptance && linux

package app

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-protocol/protocol"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// Observe only actual transport timing and HTTP status. No request/report body,
// identity, credential or response is retained, and observation cannot satisfy
// the existing fresh-heartbeat, snapshot or paired-receipt readiness gates.
type nativeGlobalSyncObservation struct {
	mu        sync.Mutex
	attempt   time.Time
	completed time.Time
	duration  time.Duration
	status    int
	inflight  int64
}

func (o *nativeGlobalSyncObservation) handler(original http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		o.mu.Lock()
		o.attempt = started
		o.inflight++
		o.mu.Unlock()
		defer func() { o.mu.Lock(); o.inflight--; o.mu.Unlock() }()
		response := &nativeMixedResponse{ResponseWriter: w}
		original.ServeHTTP(response, r)
		status := response.status
		if status == 0 {
			status = http.StatusOK
		}
		o.mu.Lock()
		o.completed, o.duration, o.status = time.Now(), time.Since(started), status
		o.mu.Unlock()
	})
}

func (o *nativeGlobalSyncObservation) facts() map[string]int64 {
	o.mu.Lock()
	defer o.mu.Unlock()
	now := time.Now()
	facts := map[string]int64{"inflight": o.inflight}
	if !o.attempt.IsZero() {
		facts["attempt_age_ms"] = now.Sub(o.attempt).Milliseconds()
	}
	if !o.completed.IsZero() {
		facts["completion_age_ms"] = now.Sub(o.completed).Milliseconds()
		facts["last_duration_ms"] = o.duration.Milliseconds()
		facts["last_status"] = int64(o.status)
	}
	return facts
}

// Select only a checked cohort's private observation/partition wrapper. Restore
// the original wire bytes and headers before the unchanged authenticated route;
// unknown, malformed or oversized reports retain production rejection behavior.
func nativeGlobalMixedTransport(original http.Handler, peers []*nativeGlobalPeer) (http.Handler, error) {
	return nativeGlobalRoutedTransport(original, peers, func(peer *nativeGlobalPeer, handler http.Handler) http.Handler {
		return peer.sync.handler(peer.probe.handler(handler))
	})
}

// Keep the checked wire router shared, but let byte formation use its response
// fault instead of the batch driver's before-forwarding partition. The latter
// cannot prove that a first packet was already durable before its ACK was lost.
func nativeGlobalByteTransport(original http.Handler, peers []*nativeGlobalPeer, faults []*nativeByteResponseFault) (http.Handler, error) {
	if len(faults) != len(peers) {
		return nil, errors.New("global byte transport fault binding invalid; private content withheld")
	}
	byAgent := make(map[string]*nativeByteResponseFault, len(faults))
	for i, fault := range faults {
		if fault == nil || peers[i] == nil || peers[i].agent == nil || fault.agent == "" || fault.agent != peers[i].agent.AgentID || byAgent[fault.agent] != nil {
			return nil, errors.New("global byte transport fault binding invalid; private content withheld")
		}
		byAgent[fault.agent] = fault
	}
	return nativeGlobalRoutedTransport(original, peers, func(peer *nativeGlobalPeer, handler http.Handler) http.Handler {
		return peer.sync.handler(byAgent[peer.agent.AgentID].handler(handler))
	})
}

func nativeGlobalRoutedTransport(original http.Handler, peers []*nativeGlobalPeer, wrap func(*nativeGlobalPeer, http.Handler) http.Handler) (http.Handler, error) {
	fail := func() (http.Handler, error) {
		return nil, errors.New("global transport peer binding invalid; private content withheld")
	}
	if original == nil || len(peers) == 0 || len(peers) > 65 || wrap == nil {
		return fail()
	}
	handlers := make(map[string]http.Handler, len(peers))
	for _, peer := range peers {
		if peer == nil || peer.agent == nil || peer.agent.AgentID == "" || peer.probe == nil || peer.probe.agent != peer.agent.AgentID || handlers[peer.agent.AgentID] != nil {
			return fail()
		}
		handlers[peer.agent.AgentID] = wrap(peer, original)
		if handlers[peer.agent.AgentID] == nil {
			return fail()
		}
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/node/sync" || r.Method != http.MethodPost {
			original.ServeHTTP(w, r)
			return
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, protocol.MaxSyncBodyBytes+1))
		_ = r.Body.Close()
		r.Body = io.NopCloser(bytes.NewReader(body))
		if err != nil {
			r.Body = io.NopCloser(io.MultiReader(bytes.NewReader(body), nativeGlobalTransportReadError{err}))
		}
		var envelope struct {
			Agent string `json:"agent_id"`
		}
		if err == nil && int64(len(body)) <= protocol.MaxSyncBodyBytes && json.Unmarshal(body, &envelope) == nil {
			if handler := handlers[envelope.Agent]; handler != nil {
				handler.ServeHTTP(w, r)
				return
			}
		}
		original.ServeHTTP(w, r)
	}), nil
}

type nativeGlobalTransportReadError struct{ err error }

func (r nativeGlobalTransportReadError) Read([]byte) (int, error) { return 0, r.err }

func TestNativeGlobalByteTransportKeepsIndependentResponseFaultsAndAuthentication(t *testing.T) {
	f := buildDestinationPolicyFixture(t)
	secret := "fixture-global-byte-independent-credential"
	digest := sha256.Sum256([]byte(secret))
	other := &domain.NodeAgent{AgentID: "agt_global_byte_transport", Epoch: 1, CredentialSHA256: hex.EncodeToString(digest[:]), DesiredCoreEngine: domain.NodeCoreXray, DesiredCoreVersion: "26.6.27"}
	panel := &domain.Panel{Name: "Owned byte transport peer", Kind: domain.PanelKindPSP, URL: "psp://" + other.AgentID}
	nativeMust(t, f.a.repos.NativeAgentProvisioning.Create(t.Context(), panel, other), "provision independent byte transport identity")
	nativeMust(t, f.a.xuiPool.Add(panel), "register actual byte transport adapter")
	peers := []*nativeGlobalPeer{{agent: f.agent, probe: newNativeMixedProbe(f.agent.AgentID)}, {agent: other, probe: newNativeMixedProbe(other.AgentID)}}
	faults := []*nativeByteResponseFault{{agent: f.agent.AgentID}, {agent: other.AgentID}}
	var observed, expected []byte
	calls := 0
	original := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var err error
		observed, err = io.ReadAll(r.Body)
		nativeMust(t, err, "read private original byte transport body")
		if !bytes.Equal(observed, expected) {
			t.Fatal("global byte response fault changed the original request")
		}
		r.Body = io.NopCloser(bytes.NewReader(observed))
		f.a.server.Handler.ServeHTTP(w, r)
	})
	handler, err := nativeGlobalByteTransport(original, peers, faults)
	nativeMust(t, err, "bind independent original byte response faults")
	frozenAudit := fixtureAudit(t, f, 1)
	send := func(owner int, credential string, change bool) *httptest.ResponseRecorder {
		t.Helper()
		report := f.report
		report.AgentID = peers[owner].agent.AgentID
		// Repeated original wire reports must preserve event timestamps too.
		// Only the explicit next-packet vector changes the immutable batch ID.
		audit := *frozenAudit
		report.Audit = &audit
		if change {
			report.Audit.BatchID = strings.Repeat("f", 32)
		}
		wire, err := json.Marshal(report)
		nativeMust(t, err, "encode original byte transport report")
		expected = append(append([]byte(" \n"), wire...), '\n')
		r := httptest.NewRequest(http.MethodPost, "/v1/node/sync", bytes.NewReader(expected))
		r.Header.Set("Authorization", "Bearer "+credential)
		r.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, r)
		return response
	}
	credentials := []string{f.credential, secret}
	for i, fault := range faults {
		// The old batch partition is armed deliberately: byte routing must never
		// enter it, or the real authenticated original handler would not run.
		peers[i].probe.start = time.Now()
		if _, valid := fault.arm(); !valid || send(i, credentials[i], false).Code != http.StatusServiceUnavailable {
			t.Fatal("global byte transport failed to suppress the actual success response")
		}
	}
	if calls != 2 {
		t.Fatal("byte transport reused the before-forwarding batch partition")
	}
	faults[0].release()
	if send(0, credentials[0], false).Code != http.StatusOK || send(1, credentials[1], false).Code != http.StatusServiceUnavailable {
		t.Fatal("releasing a carrier also released the independently reserved owner")
	}
	first, _, valid := faults[0].snapshot()
	fresh, _, freshValid := faults[1].snapshot()
	if !valid || !freshValid || first.Recovered[0] != 1 || fresh.Recovered[0] != 0 || fresh.FrozenRetries[0] != 1 {
		t.Fatal("global response routing attributed canonical recovery to the wrong owner")
	}
	faults[1].release()
	if send(1, credentials[1], false).Code != http.StatusOK || send(1, credentials[1], true).Code != http.StatusOK {
		t.Fatal("reserved owner could not recover its original canonical report")
	}
	fresh, _, freshValid = faults[1].snapshot()
	if !freshValid || fresh.Recovered[0] != 1 || fresh.NewPackets[0] != 1 || fresh.OriginalError != 0 {
		t.Fatal("reserved owner's original recovery/new-packet facts are incomplete")
	}
	// Original authentication failures remain failures even while a different
	// response fault is still active; they cannot be recorded as suppressed 200s.
	if response := send(0, "invalid-owned-credential", false); response.Code == http.StatusOK || response.Code == http.StatusServiceUnavailable {
		t.Fatal("global byte transport hid original credential rejection")
	}
	if calls != 7 {
		t.Fatal("global byte transport did not forward each exact original invocation")
	}
	for _, invalid := range [][]*nativeByteResponseFault{nil, {nil, faults[1]}, {faults[1], faults[0]}, {faults[0], faults[0]}} {
		if _, err := nativeGlobalByteTransport(original, peers, invalid); err == nil {
			t.Fatal("global byte transport accepted an absent, duplicate or mismatched fault")
		}
	}
}

func TestNativeGlobalTransportKeepsProductionAuthenticationAndExactBodies(t *testing.T) {
	var observation nativeGlobalSyncObservation
	if facts := observation.facts(); len(facts) != 1 || facts["inflight"] != 0 {
		t.Fatal("unused transport observer fabricated a completion or status")
	}
	entered, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	observedResponse := httptest.NewRecorder()
	observer := observation.handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-release
		w.Header().Set("X-Owned-Observation", "unchanged")
		w.WriteHeader(http.StatusTeapot)
		_, _ = w.Write([]byte("owned response"))
	}))
	go func() {
		defer close(done)
		observer.ServeHTTP(observedResponse, httptest.NewRequest(http.MethodPost, "/v1/node/sync", nil))
	}()
	<-entered
	pendingFacts := observation.facts()
	_, inventedCompletion := pendingFacts["completion_age_ms"]
	_, inventedStatus := pendingFacts["last_status"]
	close(release)
	<-done
	if pendingFacts["inflight"] != 1 || inventedCompletion || inventedStatus {
		t.Fatal("in-flight transport observation fabricated a completed response")
	}
	finished := observation.facts()
	if finished["inflight"] != 0 || finished["last_status"] != http.StatusTeapot || observedResponse.Code != http.StatusTeapot || observedResponse.Header().Get("X-Owned-Observation") != "unchanged" || observedResponse.Body.String() != "owned response" {
		t.Fatal("transport observation changed the original response or completion")
	}
	// Actual production authentication/storage with protocol vectors. This
	// preflight does not claim real daemons, a timed partition or native pressure.
	f := buildDestinationPolicyFixture(t)
	secret := "fixture-global-transport-independent-credential"
	digest := sha256.Sum256([]byte(secret))
	other := &domain.NodeAgent{AgentID: "agt_global_transport", Epoch: 1, CredentialSHA256: hex.EncodeToString(digest[:]), DesiredCoreEngine: domain.NodeCoreXray, DesiredCoreVersion: "26.6.27"}
	panel := &domain.Panel{Name: "Owned transport peer", Kind: domain.PanelKindPSP, URL: "psp://" + other.AgentID}
	nativeMust(t, f.a.repos.NativeAgentProvisioning.Create(t.Context(), panel, other), "provision actual transport identity")
	nativeMust(t, f.a.xuiPool.Add(panel), "register actual transport adapter")
	peers := []*nativeGlobalPeer{{agent: f.agent, probe: newNativeMixedProbe(f.agent.AgentID)}, {agent: other, probe: newNativeMixedProbe(other.AgentID)}}
	var observed []byte
	original := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		nativeMust(t, err, "observe private original sync wire bytes")
		observed = append([]byte(nil), body...)
		r.Body = io.NopCloser(bytes.NewReader(body))
		f.a.server.Handler.ServeHTTP(w, r)
	})
	handler, err := nativeGlobalMixedTransport(original, peers)
	nativeMust(t, err, "bind two independent transport observers")
	send := func(body []byte, credential string) int {
		request := httptest.NewRequest(http.MethodPost, "/v1/node/sync", bytes.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Authorization", "Bearer "+credential)
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		if !bytes.Equal(observed, body) {
			t.Fatal("cohort transport changed the original authenticated body")
		}
		return recorder.Code
	}
	for i, credential := range []string{f.credential, secret} {
		report := f.report
		report.AgentID = peers[i].agent.AgentID
		body, err := json.Marshal(report)
		nativeMust(t, err, "encode private actual transport report")
		body = append(append([]byte(" \n"), body...), '\n')
		if send(body, credential) != http.StatusOK {
			t.Fatal("cohort observer suppressed an authenticated production sync")
		}
		rejected := send(body, "wrong-owned-fixture-credential")
		if rejected == http.StatusOK {
			t.Fatal("cohort observer bypassed production credential validation")
		}
		facts := peers[i].sync.facts()
		if facts["inflight"] != 0 || facts["last_status"] != int64(rejected) {
			t.Fatal("transport observer changed or lost the actual production rejection")
		}
	}
	for _, body := range [][]byte{[]byte("{"), []byte(`{"agent_id":"agt_foreign"}`), []byte(strings.Repeat(" ", int(protocol.MaxSyncBodyBytes)+1))} {
		if send(body, f.credential) == http.StatusOK {
			t.Fatal("cohort observer admitted malformed, foreign or oversized input")
		}
	}
	for _, invalid := range [][]*nativeGlobalPeer{nil, {nil}, {{agent: f.agent}}, {peers[0], peers[0]}, {{agent: f.agent, probe: newNativeMixedProbe(other.AgentID)}}} {
		if _, err := nativeGlobalMixedTransport(original, invalid); err == nil {
			t.Fatal("cohort observer accepted an unbound or duplicated identity")
		}
	}
	for _, peer := range peers {
		peer.probe.mu.Lock()
		invalid, frozen, recovered := peer.probe.invalid, peer.probe.frozen, peer.probe.recovered
		peer.probe.mu.Unlock()
		if invalid || frozen != [4]uint64{} || recovered != [4]uint64{} {
			t.Fatal("untimed transport preflight fabricated a canonical retry")
		}
	}
	readFailure := errors.New("owned transport read failure")
	var receivedErr error
	failedReader := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, receivedErr = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusBadRequest)
	})
	failedTransport, err := nativeGlobalMixedTransport(failedReader, peers)
	nativeMust(t, err, "bind read-error preservation boundary")
	request := httptest.NewRequest(http.MethodPost, "/v1/node/sync", io.MultiReader(strings.NewReader("{"), nativeGlobalTransportReadError{readFailure}))
	failedTransport.ServeHTTP(httptest.NewRecorder(), request)
	if !errors.Is(receivedErr, readFailure) {
		t.Fatal("cohort observer discarded the original body read failure")
	}
}
