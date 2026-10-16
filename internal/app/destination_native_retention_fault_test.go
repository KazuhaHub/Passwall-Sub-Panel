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
	"time"

	"github.com/KazuhaHub/passwall-protocol/protocol"
)

const (
	nativeRetentionInactive = iota
	nativeRetentionCapture
	nativeRetentionOffline
	nativeRetentionBlockRecovery
)

// All report bytes, subjects, batch IDs and credentials remain private. These
// fixed-size counters describe transport only, never SQL commit or a Node ACK.
type nativeRetentionFaultFacts struct {
	Phase          int       `json:"phase"`
	CapturedRows   [2]int    `json:"captured_rows"`
	CanonicalBytes [2]int    `json:"canonical_bytes"`
	Suppressed     [2]uint64 `json:"suppressed_success_responses"`
	Retries        [2]uint64 `json:"canonical_retries"`
	Offline        uint64    `json:"offline_requests"`
	Recovered      uint64    `json:"original_block_success_responses"`
	OriginalErrors uint64    `json:"original_http_errors"`
	Replacements   int       `json:"handler_replacements"`
	IsolatedMS     int64     `json:"isolated_unix_ms"`
	ReplacedMS     int64     `json:"replaced_unix_ms"`
	ResumedMS      int64     `json:"resumed_unix_ms"`
}

type nativeRetentionPacket struct {
	id      string
	digest  [32]byte
	request []byte
}

// Capture one immutable real block and observe batch, suppress their successful
// responses, then partition sync before the production handler. The caller must
// independently prove both original SQL commits before isolate. Replacement is
// available only while isolated; rebuilding/shutting down the App remains the
// driver's responsibility. No clock, receipt or database result is fabricated.
type nativeRetentionResponseFault struct {
	mu       sync.Mutex
	agent    string
	original http.Handler
	packets  [2]nativeRetentionPacket
	facts    nativeRetentionFaultFacts
	isolated time.Time
	invalid  bool
}

func (p *nativeRetentionResponseFault) arm() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.agent == "" || p.original == nil || p.facts.Phase != nativeRetentionInactive || p.invalid {
		return false
	}
	p.facts.Phase = nativeRetentionCapture
	return true
}

func (p *nativeRetentionResponseFault) isolate() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.invalid || p.facts.Phase != nativeRetentionCapture || p.packets[0].id == "" || p.packets[1].id == "" {
		return false
	}
	p.isolated = time.Now()
	p.facts.IsolatedMS = p.isolated.UnixMilli()
	p.facts.Phase = nativeRetentionOffline
	return true
}

func (p *nativeRetentionResponseFault) replace(original http.Handler) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if original == nil || p.invalid || p.facts.Phase != nativeRetentionOffline || p.facts.Replacements != 0 {
		return false
	}
	p.original = original
	p.facts.Replacements = 1
	p.facts.ReplacedMS = time.Now().UnixMilli()
	return true
}

func (p *nativeRetentionResponseFault) resumeBlock() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.invalid || p.facts.Phase != nativeRetentionOffline || p.facts.Replacements != 1 || p.isolated.IsZero() || time.Since(p.isolated) < 26*time.Hour {
		return false
	}
	p.facts.ResumedMS = time.Now().UnixMilli()
	p.facts.Phase = nativeRetentionBlockRecovery
	return true
}

func (p *nativeRetentionResponseFault) snapshot() (nativeRetentionFaultFacts, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.facts, !p.invalid
}

// A defensive copy allows a later explicitly labelled authenticated boundary
// fixture without mutating the original Node packet kept for replay identity.
func (p *nativeRetentionResponseFault) privateRequest(lane int) []byte {
	p.mu.Lock()
	defer p.mu.Unlock()
	if lane < 0 || lane >= len(p.packets) {
		return nil
	}
	return bytes.Clone(p.packets[lane].request)
}

func (p *nativeRetentionResponseFault) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Serialize the short handler boundary with phase changes and replacement,
	// so isolate cannot return with an old production request still in flight.
	p.mu.Lock()
	defer p.mu.Unlock()
	original := p.original
	if original == nil {
		p.invalid = true
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	if p.facts.Phase == nativeRetentionInactive || r.Method != http.MethodPost || r.URL.Path != "/v1/node/sync" {
		original.ServeHTTP(w, r)
		return
	}
	incoming := r.Body
	defer incoming.Close()
	body, err := io.ReadAll(io.LimitReader(incoming, protocol.MaxSyncBodyBytes+1))
	// Keep an oversized tail and a read error visible to the unchanged handler.
	// A malformed request must never become a successful captured original.
	r.Body = io.NopCloser(io.MultiReader(bytes.NewReader(body), incoming))
	if err != nil {
		r.Body = io.NopCloser(io.MultiReader(bytes.NewReader(body), nativeGlobalTransportReadError{err}))
	}
	var report protocol.NodeReport
	if err != nil || int64(len(body)) > protocol.MaxSyncBodyBytes || json.Unmarshal(body, &report) != nil || report.AgentID != p.agent {
		original.ServeHTTP(w, r)
		return
	}
	index := -1
	if report.Audit != nil {
		if protocol.ValidateAuditObservation(*report.Audit) != nil {
			p.invalid = true
			original.ServeHTTP(w, r)
			return
		}
		switch report.Audit.Kind {
		case "block":
			index = 0
		case "observe":
			index = 1
		default:
			p.invalid = true
			original.ServeHTTP(w, r)
			return
		}
		canonical, marshalErr := json.Marshal(report.Audit)
		if marshalErr != nil {
			p.invalid = true
			original.ServeHTTP(w, r)
			return
		}
		packet := &p.packets[index]
		if packet.id != "" {
			if packet.id != report.Audit.BatchID || packet.digest != sha256.Sum256(canonical) {
				p.invalid = true
			} else {
				p.facts.Retries[index]++
			}
		}
	}
	if p.facts.Phase == nativeRetentionOffline || (p.facts.Phase == nativeRetentionBlockRecovery && index == 1) {
		p.facts.Offline++
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	response := httptest.NewRecorder()
	original.ServeHTTP(response, r)
	if response.Code != http.StatusOK {
		p.facts.OriginalErrors++
		p.invalid = true
	} else if p.facts.Phase == nativeRetentionCapture && index >= 0 {
		packet := &p.packets[index]
		if packet.id == "" {
			canonical, _ := json.Marshal(report.Audit)
			*packet = nativeRetentionPacket{report.Audit.BatchID, sha256.Sum256(canonical), bytes.Clone(body)}
			p.facts.CapturedRows[index] = len(report.Audit.Hits)
			p.facts.CanonicalBytes[index] = len(canonical)
		}
		p.facts.Suppressed[index]++
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	} else if p.facts.Phase == nativeRetentionBlockRecovery && index == 0 && !p.invalid {
		p.facts.Recovered++
	}
	for key, values := range response.Header() {
		w.Header()[key] = append([]string(nil), values...)
	}
	w.WriteHeader(response.Code)
	_, _ = w.Write(response.Body.Bytes())
}
