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
	"testing"

	"github.com/KazuhaHub/passwall-protocol/protocol"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// Select only a checked cohort's private observation/partition wrapper. Restore
// the original wire bytes and headers before the unchanged authenticated route;
// unknown, malformed or oversized reports retain production rejection behavior.
func nativeGlobalMixedTransport(original http.Handler, peers []*nativeGlobalPeer) (http.Handler, error) {
	fail := func() (http.Handler, error) {
		return nil, errors.New("global transport peer binding invalid; private content withheld")
	}
	if original == nil || len(peers) == 0 {
		return fail()
	}
	handlers := make(map[string]http.Handler, len(peers))
	for _, peer := range peers {
		if peer == nil || peer.agent == nil || peer.agent.AgentID == "" || peer.probe == nil || peer.probe.agent != peer.agent.AgentID || handlers[peer.agent.AgentID] != nil {
			return fail()
		}
		handlers[peer.agent.AgentID] = peer.probe.handler(original)
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

func TestNativeGlobalTransportKeepsProductionAuthenticationAndExactBodies(t *testing.T) {
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
		if send(body, "wrong-owned-fixture-credential") == http.StatusOK {
			t.Fatal("cohort observer bypassed production credential validation")
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
