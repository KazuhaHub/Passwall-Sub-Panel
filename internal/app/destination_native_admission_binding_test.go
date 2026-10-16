//go:build destination_acceptance && linux

package app

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-protocol/protocol"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/destaudit"
)

// Correlation is valid only while one checked owner's original handler is
// forwarding one canonical observation. Overlap invalidates evidence without
// serializing, delaying or changing either request. No payload is retained.
type nativeAdmissionBinding struct {
	mu      sync.Mutex
	owners  map[string]*nativeAdmissionOwner
	invalid bool
}

type nativeAdmissionOwner struct {
	panel    int64
	inflight int
	current  *nativeAdmissionCandidate
	facts    nativeAdmissionFacts
	digests  [4][32]byte
}

type nativeAdmissionCandidate struct {
	kind, size, rows int
	digest           [32]byte
	events           int
	event            destaudit.Event
}

// Numeric facts only; identities and canonical digests remain private.
type nativeAdmissionFacts struct {
	Accepted      [4]uint64                      `json:"accepted"`
	Rejected      [4]uint64                      `json:"rejected"`
	Retried       [4]uint64                      `json:"same_rejected_candidate_retries"`
	ByteExclusive [4]uint64                      `json:"byte_exclusive_rejections"`
	Last          [4]destaudit.AdmissionPressure `json:"last_rejection"`
}

func (b *nativeAdmissionBinding) observe(e destaudit.Event) {
	// Only these outcomes originate exclusively from synchronous Offer.
	// duplicate_batch can also come from asynchronous durable SQL dedup,
	// so it must never be correlated using the active HTTP slot.
	if e.Outcome != "accepted" && e.Outcome != "queue_full" {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	owner := b.owners[e.AgentID]
	if owner == nil || owner.panel != e.PanelID || owner.inflight != 1 || owner.current == nil {
		b.invalid = true
		return
	}
	c := owner.current
	c.events++
	if c.events != 1 || e.Kind != nativeMixedKinds[c.kind] || e.ReceivedHour <= 0 || e.ReceivedHour%protocol.AuditHourMS != 0 {
		b.invalid = true
		return
	}
	if e.Outcome == "queue_full" {
		p := e.Admission
		if p.CandidateBytes != c.size || e.Rows != int64(c.rows) || p.Batches < 0 || p.Bytes < 0 || p.OwnerBatches < 0 || p.Batches > p.BatchLimit || p.Bytes > p.ByteLimit || p.OwnerBatches > p.OwnerLimit ||
			p.BatchLimit != [4]int{128, 32, 32, 64}[c.kind] || p.ByteLimit != [4]int{32 << 20, 8 << 20, 8 << 20, 16 << 20}[c.kind] || p.OwnerLimit != [4]int{4, 1, 1, 1}[c.kind] ||
			(p.Batches < p.BatchLimit && p.OwnerBatches < p.OwnerLimit && p.CandidateBytes <= p.ByteLimit-p.Bytes) {
			b.invalid = true
			return
		}
	} else if e.Admission != (destaudit.AdmissionPressure{}) {
		b.invalid = true
		return
	}
	c.event = e
}

func (b *nativeAdmissionBinding) handler(original http.Handler, peers []*nativeGlobalPeer) (http.Handler, error) {
	fail := func() (http.Handler, error) {
		return nil, errors.New("admission binding requires unique checked owners; private content withheld")
	}
	if original == nil || len(peers) == 0 || len(peers) > 65 {
		return fail()
	}
	owners := make(map[string]*nativeAdmissionOwner, len(peers))
	panels := make(map[int64]bool, len(peers))
	for _, peer := range peers {
		if peer == nil || peer.agent == nil || peer.agent.AgentID == "" || peer.agent.PanelID <= 0 || owners[peer.agent.AgentID] != nil || panels[peer.agent.PanelID] {
			return fail()
		}
		owners[peer.agent.AgentID] = &nativeAdmissionOwner{panel: peer.agent.PanelID}
		panels[peer.agent.PanelID] = true
	}
	b.mu.Lock()
	if b.owners != nil {
		b.mu.Unlock()
		return fail()
	}
	b.owners = owners
	b.mu.Unlock()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/node/sync" {
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
		if err != nil || int64(len(body)) > protocol.MaxSyncBodyBytes || json.Unmarshal(body, &report) != nil || owners[report.AgentID] == nil || report.Audit == nil || protocol.ValidateAuditObservation(*report.Audit) != nil {
			original.ServeHTTP(w, r)
			return
		}
		kind := -1
		for i, name := range nativeMixedKinds {
			if report.Audit.Kind == name {
				kind = i
			}
		}
		canonical, err := json.Marshal(report.Audit)
		if kind < 0 || err != nil || len(canonical) > protocol.MaxAuditObservationBytes {
			original.ServeHTTP(w, r)
			return
		}
		c := &nativeAdmissionCandidate{kind: kind, size: len(canonical), rows: len(report.Audit.Hits) + len(report.Audit.Usage), digest: sha256.Sum256(canonical)}
		owner := owners[report.AgentID]
		b.mu.Lock()
		owner.inflight++
		if owner.inflight != 1 {
			b.invalid = true
		} else {
			owner.current = c
		}
		b.mu.Unlock()
		response := &nativeMixedResponse{ResponseWriter: w}
		defer func() {
			b.mu.Lock()
			defer b.mu.Unlock()
			owner.inflight--
			if owner.current == c {
				owner.current = nil
			}
			if b.invalid || c.events == 0 {
				return
			}
			if c.events != 1 || response.status != http.StatusOK {
				b.invalid = true
				return
			}
			i := c.kind
			switch c.event.Outcome {
			case "accepted":
				owner.facts.Accepted[i]++
			case "queue_full":
				p := c.event.Admission
				if owner.facts.Rejected[i] != 0 && owner.digests[i] == c.digest {
					owner.facts.Retried[i]++
				}
				owner.facts.Rejected[i]++
				owner.digests[i], owner.facts.Last[i] = c.digest, p
				if p.OwnerBatches == 0 && p.Batches < p.BatchLimit && p.CandidateBytes > p.ByteLimit-p.Bytes {
					owner.facts.ByteExclusive[i]++
				}
			}
		}()
		original.ServeHTTP(response, r)
	}), nil
}

func (b *nativeAdmissionBinding) snapshot(agent string) (nativeAdmissionFacts, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	owner := b.owners[agent]
	if owner == nil {
		return nativeAdmissionFacts{}, false
	}
	return owner.facts, !b.invalid && owner.inflight == 0
}

// Synthetic callback vectors reject ambiguous evidence. Actual authentication,
// original queue limits and SQL are exercised by the separate App integration.
func TestNativeGlobalAdmissionBindingRejectsAmbiguousEvidence(t *testing.T) {
	agent := &domain.NodeAgent{AgentID: "agt_owned_binding", PanelID: 1}
	now := time.Now().UTC()
	audit := protocol.AuditObservation{BatchID: "00000000000000000000000000000001", Hour: now.Truncate(time.Hour).UnixMilli(), Kind: "block", CollectRevision: 1,
		Hits: []protocol.AuditHit{{Hour: now.Truncate(time.Hour).UnixMilli(), Subject: protocol.NewSubjectKey(1), RuleID: "p12", Action: "block", Dest: "binding.example.test", Port: 443, Count: 1, FirstMS: now.UnixMilli(), LastMS: now.UnixMilli()}}}
	nativeMust(t, protocol.ValidateAuditObservation(audit), "validate binding contract vector")
	canonical, err := json.Marshal(audit)
	nativeMust(t, err, "encode binding canonical vector")
	body, err := json.Marshal(protocol.NodeReport{AgentID: agent.AgentID, Audit: &audit})
	nativeMust(t, err, "encode private binding vector")
	base := destaudit.Event{AgentID: agent.AgentID, PanelID: agent.PanelID, Kind: "block", Outcome: "queue_full", ReceivedHour: audit.Hour, Rows: 1,
		Admission: destaudit.AdmissionPressure{CandidateBytes: len(canonical), Batches: 4, Bytes: 4 * len(canonical), OwnerBatches: 4, BatchLimit: 128, ByteLimit: 32 << 20, OwnerLimit: 4}}
	for _, scenario := range []string{"owner_cap", "byte_cap", "async_notifications", "wrong_bytes", "wrong_panel", "wrong_owner", "wrong_lane", "wrong_limits", "no_capacity_condition", "duplicate_event", "late_event", "failed_response"} {
		t.Run(scenario, func(t *testing.T) {
			binding := &nativeAdmissionBinding{}
			e := base
			switch scenario {
			case "byte_cap":
				e.Admission.OwnerBatches, e.Admission.Batches = 0, 40
				e.Admission.Bytes = e.Admission.ByteLimit - e.Admission.CandidateBytes + 1
			case "wrong_bytes":
				e.Admission.CandidateBytes++
			case "wrong_panel":
				e.PanelID++
			case "wrong_owner":
				e.AgentID = "agt_foreign_binding"
			case "wrong_lane":
				e.Kind = "observe"
			case "wrong_limits":
				e.Admission.ByteLimit--
			case "no_capacity_condition":
				e.Admission.OwnerBatches = 0
			}
			original := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				wire, err := io.ReadAll(r.Body)
				if err != nil || !bytes.Equal(wire, body) || r.Header.Get("X-Owned-Request") != "unchanged" {
					t.Error("admission binding changed original request bytes or headers")
				}
				if scenario == "async_notifications" {
					for _, outcome := range []string{"duplicate_batch", "collect_off", "stale_collect_revision", "stored"} {
						notification := e
						notification.Outcome = outcome
						binding.observe(notification)
					}
				} else if scenario != "late_event" {
					binding.observe(e)
				}
				if scenario == "duplicate_event" {
					binding.observe(e)
				}
				w.Header().Set("X-Owned-Response", "unchanged")
				status := http.StatusOK
				if scenario == "failed_response" {
					status = http.StatusUnauthorized
				}
				w.WriteHeader(status)
				_, _ = w.Write([]byte("original response"))
			})
			handler, err := binding.handler(original, []*nativeGlobalPeer{{agent: agent}})
			nativeMust(t, err, "bind checked vector owner")
			for range 2 {
				r := httptest.NewRequest(http.MethodPost, "/v1/node/sync", bytes.NewReader(body))
				r.Header.Set("X-Owned-Request", "unchanged")
				response := httptest.NewRecorder()
				handler.ServeHTTP(response, r)
				if response.Header().Get("X-Owned-Response") != "unchanged" || response.Body.String() != "original response" || response.Code != map[bool]int{true: http.StatusUnauthorized, false: http.StatusOK}[scenario == "failed_response"] {
					t.Fatal("admission observation changed original response")
				}
			}
			if scenario == "late_event" {
				binding.observe(e)
			}
			facts, valid := binding.snapshot(agent.AgentID)
			wantValid := scenario == "owner_cap" || scenario == "byte_cap" || scenario == "async_notifications"
			if valid != wantValid {
				t.Fatal("ambiguous or contradictory admission evidence was accepted")
			}
			if scenario == "async_notifications" && facts != (nativeAdmissionFacts{}) {
				t.Fatal("asynchronous SQL or permission notifications acquired HTTP attribution")
			}
			if wantValid && scenario != "async_notifications" && (facts.Rejected[0] != 2 || facts.Retried[0] != 1 || facts.ByteExclusive[0] != map[bool]uint64{true: 2, false: 0}[scenario == "byte_cap"]) {
				t.Fatal("canonical retry, owner cap and byte-exclusive facts were conflated")
			}
			if !wantValid && facts != (nativeAdmissionFacts{}) {
				t.Fatal("invalid binding manufactured an accepted capacity witness")
			}
		})
	}
	t.Run("overlap_preserves_both_requests", func(t *testing.T) {
		binding := &nativeAdmissionBinding{}
		entered, release := make(chan struct{}, 2), make(chan struct{})
		var calls sync.WaitGroup
		original := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			entered <- struct{}{}
			<-release
			binding.observe(base)
			w.WriteHeader(http.StatusOK)
		})
		handler, err := binding.handler(original, []*nativeGlobalPeer{{agent: agent}})
		nativeMust(t, err, "bind concurrent vector handler")
		t.Cleanup(func() { close(release); calls.Wait() })
		for range 2 {
			calls.Add(1)
			go func() {
				defer calls.Done()
				handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/v1/node/sync", bytes.NewReader(body)))
			}()
		}
		for range 2 {
			select {
			case <-entered:
			case <-time.After(5 * time.Second):
				t.Fatal("observation serialized overlapping original requests")
			}
		}
		release <- struct{}{}
		release <- struct{}{}
		calls.Wait()
		if facts, valid := binding.snapshot(agent.AgentID); valid || facts != (nativeAdmissionFacts{}) {
			t.Fatal("overlapping requests received an invented canonical attribution")
		}
	})
}
