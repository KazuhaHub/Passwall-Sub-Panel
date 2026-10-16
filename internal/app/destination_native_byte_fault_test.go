//go:build destination_acceptance && linux

package app

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-protocol/protocol"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

type nativeBytePacket struct {
	id     string
	digest [32]byte
}

type nativeByteFormationFacts struct {
	Suppressed    [4]uint64 `json:"suppressed_success_responses"`
	FrozenRetries [4]uint64 `json:"verified_frozen_retries"`
	Recovered     [4]uint64 `json:"recovered_first_packets"`
	FirstBytes    [4]int    `json:"first_canonical_bytes"`
	FirstRows     [4]int    `json:"first_rows"`
	NewPackets    [4]uint64 `json:"new_packets"`
	MaxNewBytes   [4]int    `json:"max_new_canonical_bytes"`
	MaxNewRows    [4]int    `json:"rows_in_largest_new_packet"`
	OriginalError uint64    `json:"original_http_errors"`
}

// A response-path fault only: the unchanged authenticated handler always sees
// each original request, including retries. A real successful response is
// suppressed until release; no collector ACK, report or SQL result is invented.
type nativeByteResponseFault struct {
	mu       sync.Mutex
	agent    string
	start    time.Time
	released bool
	invalid  bool
	first    [4]nativeBytePacket
	last     [4]nativeBytePacket
	facts    nativeByteFormationFacts
}

func (p *nativeByteResponseFault) arm() (time.Time, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.agent == "" || !p.start.IsZero() || p.released {
		return time.Time{}, false
	}
	p.start = time.Now()
	return p.start, true
}

func (p *nativeByteResponseFault) release() {
	p.mu.Lock()
	p.released = true
	p.mu.Unlock()
}

func (p *nativeByteResponseFault) snapshot() (nativeByteFormationFacts, [4]nativeBytePacket, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.facts, p.first, !p.invalid
}

func (p *nativeByteResponseFault) handler(original http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p.mu.Lock()
		armed := !p.start.IsZero()
		p.mu.Unlock()
		if !armed || r.Method != http.MethodPost || r.URL.Path != "/v1/node/sync" {
			original.ServeHTTP(w, r)
			return
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, protocol.MaxSyncBodyBytes+1))
		_ = r.Body.Close()
		r.Body = io.NopCloser(bytes.NewReader(body))
		if err != nil {
			r.Body = io.NopCloser(io.MultiReader(bytes.NewReader(body), nativeGlobalTransportReadError{err}))
		}
		var report protocol.NodeReport
		if err != nil || int64(len(body)) > protocol.MaxSyncBodyBytes || json.Unmarshal(body, &report) != nil || report.AgentID != p.agent || report.Audit == nil || protocol.ValidateAuditObservation(*report.Audit) != nil {
			original.ServeHTTP(w, r)
			return
		}
		canonical, err := json.Marshal(report.Audit)
		if err != nil {
			original.ServeHTTP(w, r)
			return
		}
		index := -1
		for i, kind := range nativeMixedKinds {
			if report.Audit.Kind == kind {
				index = i
			}
		}
		if index < 0 {
			original.ServeHTTP(w, r)
			return
		}
		packet := nativeBytePacket{report.Audit.BatchID, sha256.Sum256(canonical)}
		response := httptest.NewRecorder()
		original.ServeHTTP(response, r)
		p.mu.Lock()
		suppress := false
		if response.Code != http.StatusOK {
			p.facts.OriginalError++
			p.invalid = true
		} else if !p.released {
			suppress = true
			if p.first[index].id == "" {
				p.first[index] = packet
				p.facts.FirstBytes[index] = len(canonical)
				p.facts.FirstRows[index] = len(report.Audit.Hits) + len(report.Audit.Usage)
			} else if p.first[index] != packet {
				p.invalid = true
			} else {
				p.facts.FrozenRetries[index]++
			}
			p.facts.Suppressed[index]++
		} else if p.facts.Recovered[index] == 0 {
			if p.first[index].id == "" || p.first[index] != packet {
				p.invalid = true
			} else {
				p.facts.Recovered[index]++
			}
		} else if p.first[index] != packet && p.last[index] != packet {
			p.last[index] = packet
			p.facts.NewPackets[index]++
			if len(canonical) > p.facts.MaxNewBytes[index] {
				p.facts.MaxNewBytes[index] = len(canonical)
				p.facts.MaxNewRows[index] = len(report.Audit.Hits) + len(report.Audit.Usage)
			}
		}
		p.mu.Unlock()
		if suppress {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		for key, values := range response.Header() {
			w.Header()[key] = append([]string(nil), values...)
		}
		w.WriteHeader(response.Code)
		_, _ = w.Write(response.Body.Bytes())
	})
}

func TestNativeByteResponseFaultPreservesOriginalHandlerAndCanonicalRetries(t *testing.T) {
	for _, scenario := range []string{"canonical_recovery", "changed_frozen_packet", "original_authentication_failure"} {
		t.Run(scenario, func(t *testing.T) {
			fault := &nativeByteResponseFault{agent: "agt_owned_byte_fault"}
			audit := fixtureAudit(t, destinationPolicyFixture{user: &domain.User{ID: 1}}, 1)
			report := protocol.NodeReport{AgentID: fault.agent, Audit: audit}
			var body []byte
			calls := 0
			original := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				wire, err := io.ReadAll(r.Body)
				if err != nil || !bytes.Equal(wire, body) || r.Header.Get("X-Owned-Original") != "unchanged" {
					t.Fatal("response fault changed request bytes or headers")
				}
				w.Header().Add("X-Owned-Reply", "one")
				w.Header().Add("X-Owned-Reply", "two")
				if scenario == "original_authentication_failure" {
					w.WriteHeader(http.StatusUnauthorized)
				}
				_, _ = io.WriteString(w, "original response")
			})
			handler := fault.handler(original)
			send := func() *httptest.ResponseRecorder {
				t.Helper()
				var err error
				body, err = json.Marshal(report)
				nativeMust(t, err, "encode original response-fault vector")
				r := httptest.NewRequest(http.MethodPost, "/v1/node/sync", bytes.NewReader(body))
				r.Header.Set("X-Owned-Original", "unchanged")
				response := httptest.NewRecorder()
				handler.ServeHTTP(response, r)
				return response
			}
			// Inactive faults do not buffer or change an original response.
			before := send()
			if before.Body.String() != "original response" || len(before.Header().Values("X-Owned-Reply")) != 2 {
				t.Fatal("unarmed fault changed original response")
			}
			if _, ok := fault.arm(); !ok {
				t.Fatal("could not arm the original response boundary")
			}
			if _, ok := fault.arm(); ok {
				t.Fatal("response fault reset its actual start time")
			}
			first := send()
			if scenario == "original_authentication_failure" {
				facts, packets, valid := fault.snapshot()
				if first.Code != http.StatusUnauthorized || first.Body.String() != "original response" || len(first.Header().Values("X-Owned-Reply")) != 2 || valid || facts.OriginalError != 1 || packets[0].id != "" || facts.Suppressed != [4]uint64{} {
					t.Fatal("response fault hid an original authentication rejection")
				}
				return
			}
			if first.Code != http.StatusServiceUnavailable || first.Body.Len() != 0 {
				t.Fatal("fault delivered an acknowledgement instead of suppressing it")
			}
			if scenario == "changed_frozen_packet" {
				report.Audit.Hits[0].Dest = "changed.example.test"
			}
			second := send()
			if second.Code != http.StatusServiceUnavailable {
				t.Fatal("fault fabricated a successful frozen retry")
			}
			facts, packets, valid := fault.snapshot()
			if valid != (scenario == "canonical_recovery") || facts.Suppressed[0] != 2 || packets[0].id != report.Audit.BatchID || calls != 3 {
				t.Fatal("response partition lost original forwarding or changed frozen evidence")
			}
			if !valid {
				return
			}
			if facts.FrozenRetries[0] != 1 {
				t.Fatal("same real canonical retry was not counted")
			}
			fault.release()
			fault.release()
			recovered := send()
			if recovered.Code != http.StatusOK || recovered.Body.String() != "original response" || len(recovered.Header().Values("X-Owned-Reply")) != 2 {
				t.Fatal("recovery changed the original successful response")
			}
			report.Audit = fixtureAudit(t, destinationPolicyFixture{user: &domain.User{ID: 1}}, 2)
			_ = send()
			facts, _, valid = fault.snapshot()
			if !valid || facts.Recovered[0] != 1 || facts.NewPackets[0] != 1 || facts.MaxNewBytes[0] <= 0 || facts.MaxNewRows[0] != 1 || calls != 5 {
				t.Fatal("recovery lost the next independently formed canonical packet")
			}
			if _, ok := fault.arm(); ok {
				t.Fatal("released fault was silently rearmed")
			}
		})
	}
}
