package app

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-protocol/protocol"
)

const nativeRejectedMappedCIDR = "::ffff:192.0.2.0/120"

func TestNativeRejectionFixturePassesTheUnmodifiedContract(t *testing.T) {
	policy := &protocol.DestinationPolicy{Rules: []protocol.DestinationRule{{ID: "p1", Action: protocol.RuleBlock, CIDRs: []string{nativeRejectedMappedCIDR}}}}
	if err := protocol.ValidateDestinationPolicy(policy); err != nil {
		t.Fatal("mapped documentation prefix must pass the original contract")
	}
}

func TestNativeRestartProbeDoesNotReuseAnEarlierArmedRequest(t *testing.T) {
	probe := &nativeRestartProbe{}
	probe.armRejected()
	entered, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	body := `{"policy_status":{"state":"rejected","digest":"` + strings.Repeat("a", 64) + `","issue_code":"destination_policy_rejected"}}`
	handler := probe.wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		close(entered)
		<-release
		w.WriteHeader(http.StatusOK)
	}))
	go func() {
		defer close(done)
		handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/v1/node/sync", strings.NewReader(body)))
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		close(release)
		t.Fatal("original HTTP request did not reach its handler")
	}
	probe.armRejected()
	close(release)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("original HTTP request did not finish")
	}
	if _, seen, _ := probe.first(); seen {
		t.Fatal("an earlier armed request satisfied the new attempt")
	}
}

func TestNativeRestartProbeFiltersRealPolicyRejectionsWithoutChangingHTTP(t *testing.T) {
	digest := strings.Repeat("a", 64)
	valid := `{"policy_status":{"state":"rejected","digest":"` + digest + `","issue_code":"destination_policy_rejected"}}`
	for _, code := range []int{http.StatusOK, http.StatusInternalServerError} {
		probe := &nativeRestartProbe{}
		probe.armRejected()
		var consumed []byte
		handler := probe.wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			consumed, _ = io.ReadAll(r.Body)
			w.WriteHeader(code)
		}))
		for _, before := range []string{
			`{"policy_status":{"state":"applied","digest":"` + digest + `"}}`,
			`{"policy_status":{"state":"rejected","digest":"invalid","issue_code":"destination_policy_rejected"}}`,
			`{"policy_status":{"state":"rejected","digest":"` + digest + `","issue_code":"destination_policy_rejected"},"padding":"` + strings.Repeat("x", 1<<20) + `"}`,
		} {
			handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/v1/node/sync", strings.NewReader(before)))
			if !bytes.Equal(consumed, []byte(before)) {
				t.Fatal("probe changed the original request bytes")
			}
			if _, seen, _ := probe.first(); seen {
				t.Fatal("probe accepted an old, invalid or oversized status")
			}
		}
		handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/v1/node/sync", strings.NewReader(valid)))
		status, seen, accepted := probe.first()
		if !seen || status == nil || status.Digest != digest || status.State != "rejected" || accepted != (code == http.StatusOK) || string(consumed) != valid {
			t.Fatal("probe lost the real response acceptance or original rejection")
		}
		// A later report cannot replace the captured rejection.
		handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/v1/node/sync", strings.NewReader(`{"policy_status":{"state":"applied","digest":"`+digest+`"}}`)))
		next, _, _ := probe.first()
		first, _ := json.Marshal(status)
		last, _ := json.Marshal(next)
		if !bytes.Equal(first, last) {
			t.Fatal("a later report replaced the captured rejection")
		}
	}
}
