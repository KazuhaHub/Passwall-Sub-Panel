package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	nodeprotocol "github.com/KazuhaHub/passwall-node/protocol"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/log"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/metrics"
)

var ErrNodeAuthentication = errors.New("node authentication failed")

type NodeSyncService interface {
	Sync(context.Context, nodeprotocol.NodeReport) (nodeprotocol.SyncResponse, error)
}

// NodeAuthenticator keeps credential verification outside the stable C1/C2
// protocol body and coordinator. Production uses NodeBearerAuthenticator;
// tests may inject a focused function implementation.
type NodeAuthenticator interface {
	Authenticate(*http.Request) (agentID string, err error)
}

type NodeAuthenticatorFunc func(*http.Request) (string, error)

func (f NodeAuthenticatorFunc) Authenticate(request *http.Request) (string, error) {
	return f(request)
}

type NodeSyncHandler struct {
	service NodeSyncService
	auth    NodeAuthenticator
	// drops rate-limits the log line for agents whose telemetry keeps failing.
	// The METRIC is not rate-limited — it is the durable record — but a node with
	// a permanently broken collector would otherwise emit one warning per poll
	// forever, which is how a real signal gets filtered out of a log.
	drops hostDropLog
}

// hostDropLogInterval is how often one agent's telemetry failure may be logged.
const hostDropLogInterval = time.Minute

type hostDropLog struct {
	mu   sync.Mutex
	last map[string]time.Time
}

func (l *hostDropLog) shouldLog(agentID string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.last == nil {
		l.last = map[string]time.Time{}
	}
	if last, exists := l.last[agentID]; exists && now.Sub(last) < hostDropLogInterval {
		return false
	}
	l.last[agentID] = now
	return true
}

func NewNodeSyncHandler(service NodeSyncService, auth NodeAuthenticator) (*NodeSyncHandler, error) {
	if service == nil || auth == nil {
		return nil, errors.New("node sync service and authenticator are required")
	}
	return &NodeSyncHandler{service: service, auth: auth}, nil
}

// sanitizeHost validates the telemetry subtree and drops it when it is unusable.
//
// DROPPING IS THE WHOLE POINT: the caller keeps the request and answers it
// normally. A malformed sample must cost the panel a metric and nothing else,
// which is the one failure mode this feature is built to be incapable of
// producing.
func (h *NodeSyncHandler) sanitizeHost(body []byte, host *nodeprotocol.HostObservation, agentID string) *nodeprotocol.HostObservation {
	if host == nil {
		return nil
	}
	// THE RAW SUBTREE IS MEASURED BEFORE THE STRUCT IS TRUSTED, and this is the
	// only layer that can do it: the bound exists for what a future agent might
	// add, which a decoded struct cannot see. The whole body is already in memory
	// — capped by MaxBytesReader — so reaching the raw bytes costs one cheap
	// decode into a one-field envelope.
	var envelope struct {
		Host json.RawMessage `json:"host"`
	}
	if err := json.Unmarshal(body, &envelope); err == nil &&
		int64(len(envelope.Host)) > nodeprotocol.MaxHostObservationBytes {
		h.recordHostDrop(agentID, "oversized")
		return nil
	}
	if err := nodeprotocol.ValidateHostObservation(*host); err != nil {
		h.recordHostDrop(agentID, "invalid")
		return nil
	}
	return host
}

// recordHostDrop counts and, at most once a minute per agent, logs.
//
// The count and the log answer different questions: the metric is "is this
// happening", which must never be sampled, and the log is "what exactly", which
// is only useful at a rate a person can read.
func (h *NodeSyncHandler) recordHostDrop(agentID, reason string) {
	metrics.NodeHostReportTotal.With(metrics.NodeHostOutcomeInvalid).Inc()
	if !h.drops.shouldLog(agentID, time.Now()) {
		return
	}
	log.Warn("node host telemetry dropped", "agent_id", agentID, "reason", reason)
}

func (h *NodeSyncHandler) ServeHTTP(w http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost || request.URL.Path != "/v1/node/sync" {
		http.NotFound(w, request)
		return
	}
	agentID, err := h.auth.Authenticate(request)
	if err != nil || agentID == "" {
		writeNodeError(w, http.StatusUnauthorized, ErrNodeAuthentication)
		return
	}
	limited := http.MaxBytesReader(w, request.Body, nodeprotocol.MaxSyncBodyBytes)
	defer limited.Close()
	// Read in full rather than streaming into a decoder. The host subtree's RAW
	// size has to be measured, and a decoder that produced the struct has already
	// discarded the bytes that would say how large an unknown additive section
	// was — which is the only thing the size bound exists to catch.
	body, err := io.ReadAll(limited)
	if err != nil {
		status := http.StatusBadRequest
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			status = http.StatusRequestEntityTooLarge
		}
		writeNodeError(w, status, fmt.Errorf("read node report: %w", err))
		return
	}
	var report nodeprotocol.NodeReport
	// Unmarshal rather than a streaming decoder: this makes a trailing second
	// document a syntax error, instead of something a following Decode has to be
	// remembered to notice.
	if err := json.Unmarshal(body, &report); err != nil {
		writeNodeError(w, http.StatusBadRequest, fmt.Errorf("decode node report: %w", err))
		return
	}
	if report.AgentID != agentID {
		writeNodeError(w, http.StatusUnauthorized, ErrNodeAuthentication)
		return
	}
	// The telemetry subtree is validated SEPARATELY, and failing it costs the
	// sample rather than the request. The control half is validated first below,
	// so a report that is wrong about its roster is still rejected outright.
	if err := nodeprotocol.ValidateNodeReportBase(report); err != nil {
		writeNodeError(w, http.StatusBadRequest, fmt.Errorf("validate node report: %w", err))
		return
	}
	report.Host = h.sanitizeHost(body, report.Host, agentID)
	response, err := h.service.Sync(request.Context(), report)
	if err != nil {
		// The untrusted shape was already validated above. Everything after this
		// boundary must leave the node's immutable outbox retryable. In particular,
		// evidence capacity cannot be acknowledged until durable receipt succeeds.
		// Never echo repository detail or untrusted result content to the caller.
		log.Error("native node sync failed", "agent_id", agentID, "err", err)
		status := http.StatusInternalServerError
		if errors.Is(err, domain.ErrResourceExhausted) {
			status = http.StatusTooManyRequests
		}
		writeNodeError(w, status, errors.New("node sync failed"))
		return
	}
	payload, err := json.Marshal(response)
	if err != nil || int64(len(payload)) > nodeprotocol.MaxSyncBodyBytes {
		log.Error("native node sync response encoding failed", "agent_id", agentID, "bytes", len(payload), "err", err)
		writeNodeError(w, http.StatusInternalServerError, errors.New("node sync failed"))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(payload)
}

func writeNodeError(w http.ResponseWriter, status int, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
}

var _ http.Handler = (*NodeSyncHandler)(nil)
