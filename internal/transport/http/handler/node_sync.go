package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	nodeprotocol "github.com/KazuhaHub/passwall-protocol/protocol"

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

// NodeRefusalRecorder persists the fact that an AUTHENTICATED report was
// refused. It is deliberately narrow: the handler must not be able to reach the
// agent repository for anything else.
type NodeRefusalRecorder interface {
	RecordProtocolRefusal(ctx context.Context, agentID string, protocolVersion int, reason string, refusedAt time.Time) error
}

// NodeSyncOption configures optional handler collaborators without changing the
// signature every existing construction site uses.
type NodeSyncOption func(*NodeSyncHandler)

// WithNodeRefusalRecorder records refused reports. Without it the handler still
// answers identically; the refusal is simply not persisted, which is the
// behaviour every caller had before this existed.
func WithNodeRefusalRecorder(recorder NodeRefusalRecorder) NodeSyncOption {
	return func(h *NodeSyncHandler) { h.refusals = recorder }
}

// NodeCapabilityReader reads the capabilities an agent last declared. The sync
// handler asks it, before reading a compressed body, whether the agent ever
// declared sync.gzip.v1: a coding is accepted only from a peer that negotiated
// it, and the negotiation is the agent's declaration followed by the panel's
// advertisement, so the stored declaration is what decides.
type NodeCapabilityReader interface {
	GetByAgentID(ctx context.Context, agentID string) (*domain.NodeAgent, error)
}

// WithNodeCapabilityReader enables compressed request bodies. Without it every
// request with a Content-Encoding is refused with 415, so a build that cannot
// check the negotiation never decodes anything.
func WithNodeCapabilityReader(reader NodeCapabilityReader) NodeSyncOption {
	return func(h *NodeSyncHandler) { h.capabilities = reader }
}

// DefaultSyncDecodeConcurrency bounds how many compressed bodies are decoded
// and parsed at once. A decoded body can be 64 MiB, held about twice at the
// join, with the JSON parse on top, so several at once would be an easy way to
// run the panel out of memory.
const DefaultSyncDecodeConcurrency = 4

// syncDecodeWait is how long a compressed request waits for a decode slot
// before it is told to try again.
const syncDecodeWait = 10 * time.Second

// WithSyncDecodeConcurrency sets how many compressed bodies may be decoded at
// once (at least 1).
func WithSyncDecodeConcurrency(n int) NodeSyncOption {
	return func(h *NodeSyncHandler) {
		if n < 1 {
			n = 1
		}
		h.decodeSlots = make(chan struct{}, n)
	}
}

type NodeSyncHandler struct {
	service NodeSyncService
	auth    NodeAuthenticator
	// capabilities answers whether an agent declared sync.gzip.v1. Optional: nil
	// refuses every compressed body.
	capabilities NodeCapabilityReader
	// decodeSlots is the decode semaphore; a slot is held only while a compressed
	// body that is already in memory is inflated.
	decodeSlots chan struct{}
	// decoding holds the agents that have a compressed body being inflated.
	decodingMu sync.Mutex
	decoding   map[string]struct{}
	// decodeWait is how long a compressed request waits for a slot.
	decodeWait time.Duration
	// refusals persists a refused report. Optional: a nil recorder costs the
	// record, never the response.
	refusals NodeRefusalRecorder
	// refusalLog throttles the refusal warning per agent, the same way drops does
	// for telemetry. A node retrying every thirty seconds must not be able to
	// push other lines out of the log; the metric is the unthrottled record.
	refusalLog hostDropLog
	// drops rate-limits the log line for agents whose telemetry keeps failing.
	// The METRIC is not rate-limited — it is the durable record — but a node with
	// a permanently broken collector would otherwise emit one warning per poll
	// forever, which is how a real signal gets filtered out of a log.
	drops hostDropLog
	// policyDrops is the same throttle for the policy status.
	policyDrops hostDropLog
	// auditDrops is the same throttle for the audit (hit) subtree.
	auditDrops hostDropLog
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

func NewNodeSyncHandler(service NodeSyncService, auth NodeAuthenticator, options ...NodeSyncOption) (*NodeSyncHandler, error) {
	if service == nil || auth == nil {
		return nil, errors.New("node sync service and authenticator are required")
	}
	handler := &NodeSyncHandler{service: service, auth: auth}
	for _, option := range options {
		if option != nil {
			option(handler)
		}
	}
	if handler.decodeSlots == nil {
		handler.decodeSlots = make(chan struct{}, DefaultSyncDecodeConcurrency)
	}
	if handler.decodeWait <= 0 {
		handler.decodeWait = syncDecodeWait
	}
	return handler, nil
}

// recordRefusal persists, counts and logs a refused report.
//
// IT NEVER CHANGES THE RESPONSE. The status code and body are exactly what they
// were before this existed, deliberately: the node collapses every non-200 into
// one untyped error, so no agent in the field can tell the difference, and
// changing them would be PSP altering wire behaviour outside the contract module
// for no reachable benefit.
//
// The write runs on a context detached from the request, because the request's
// is about to be abandoned by the very refusal being recorded.
func (h *NodeSyncHandler) recordRefusal(agentID string, reportedProtocol int, reason string, err error) {
	if h.refusals == nil || agentID == "" {
		return
	}
	now := time.Now().UTC()
	metrics.NodeSyncRefusedTotal.With(reason).Inc()
	if h.refusalLog.shouldLog(agentID, now) {
		generations := domain.SupportedNodeProtocolGenerations()
		log.Warn("native node sync refused",
			"agent_id", agentID, "reason", reason, "reported_protocol", reportedProtocol,
			"panel_min", generations.Min, "panel_max", generations.Max, "err", err)
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(context.Background()), 3*time.Second)
	defer cancel()
	if recordErr := h.refusals.RecordProtocolRefusal(ctx, agentID, reportedProtocol, reason, now); recordErr != nil {
		log.Error("recording a refused node report failed", "agent_id", agentID, "err", recordErr)
	}
}

// refusalReason classifies a rejected report.
//
// The generation is separated from everything else because only it names a
// PAIRING an operator can act on; the rest is one bucket by design. The detail
// belongs in the log, not in a column whose values a peer would get to choose.
func refusalReason(report nodeprotocol.NodeReport) string {
	effective := nodeprotocol.EffectiveProtocolVersion(report.ProtocolVersion)
	generations := domain.SupportedNodeProtocolGenerations()
	if report.ProtocolVersion < 0 || effective < generations.Min || effective > generations.Max {
		return domain.NodeRefusalProtocolGeneration
	}
	return domain.NodeRefusalReportInvalid
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

// nodeReportWire is the report as it is decoded off the wire: the typed
// NodeReport with the policy status and the audit subtree held back as raw
// bytes.
//
// Each field is named like the typed one it shadows, and encoding/json resolves
// the SHALLOWER one, so the typed fields of the embedded NodeReport are never
// reached by the decoder and a subtree that is the wrong shape (a number for a
// digest, a port of 70000, a count that is a string) cannot fail the decode of
// the whole report. The raw bytes are then measured, decoded, validated and
// capability-checked on their own, in sanitizePolicyStatus and sanitizeAudit,
// where any failure costs the subtree and nothing else.
type nodeReportWire struct {
	nodeprotocol.NodeReport
	PolicyStatus json.RawMessage `json:"policy_status,omitempty"`
	Audit        json.RawMessage `json:"audit,omitempty"`
}

// maxPolicyStatusBytes bounds the raw status. The protocol's own bounds
// (MaxPolicyStatusListeners listener keys of at most a few dozen bytes) keep a
// real one under ~16 KiB; the measure is on the raw bytes because a decoded
// struct has already forgotten what an unknown additive field weighed.
const maxPolicyStatusBytes = 16 << 10

// sanitizePolicyStatus decodes and validates the policy-status subtree and
// returns nil for one that is absent or unusable.
//
// DROPPING IS THE WHOLE POINT, as for the host telemetry: the caller keeps the
// request and answers it normally. A node whose status the panel cannot read
// costs the panel a counter and nothing else. (It does leave the panel without
// the node's confirmation, which is the safe direction: no confirmation never
// records a last-known-good and never clears a refusal.)
func (h *NodeSyncHandler) sanitizePolicyStatus(raw json.RawMessage, capabilities []string, agentID string) *nodeprotocol.PolicyStatus {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	drop := func(reason string) *nodeprotocol.PolicyStatus {
		metrics.NodePolicyStatusDroppedTotal.Inc()
		if h.policyDrops.shouldLog(agentID, time.Now()) {
			log.Warn("node policy status dropped", "agent_id", agentID, "reason", reason)
		}
		return nil
	}
	if len(raw) > maxPolicyStatusBytes {
		return drop("oversized")
	}
	var status nodeprotocol.PolicyStatus
	if err := json.Unmarshal(raw, &status); err != nil {
		return drop("decode")
	}
	if err := nodeprotocol.ValidatePolicyStatus(status); err != nil {
		return drop("invalid")
	}
	if !slices.Contains(capabilities, nodeprotocol.CapabilityDestinationPolicy) {
		return drop("no_capability")
	}
	return &status
}

// sanitizeAudit decodes and validates the audit (hit) subtree and returns nil
// for one that is absent or unusable.
//
// DROPPING IS THE WHOLE POINT, as for the status and the host telemetry: a
// subtree that is oversized, does not decode, fails ValidateAuditObservation or
// arrives without audit.hits.v1 costs the panel a counter and the node one
// batch, and the sync answers normally with the roster and config intact.
//
// The raw size is measured BEFORE the struct is trusted (a decoded struct has
// forgotten what an unknown additive field weighed). Usage rows additionally
// need audit.usage.v1 and are removed without it, the hits beside them kept.
//
// Nothing the subtree held is logged: a drop is a counter and an agent id.
func (h *NodeSyncHandler) sanitizeAudit(raw json.RawMessage, capabilities []string, agentID string) *nodeprotocol.AuditObservation {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	drop := func(outcome string) *nodeprotocol.AuditObservation {
		metrics.NodeAuditReportTotal.With(outcome).Inc()
		if h.auditDrops.shouldLog(agentID, time.Now()) {
			log.Warn("node audit batch dropped", "agent_id", agentID, "reason", outcome)
		}
		return nil
	}
	if len(raw) > nodeprotocol.MaxAuditObservationBytes {
		return drop(metrics.NodeAuditOversized)
	}
	var observation nodeprotocol.AuditObservation
	if err := json.Unmarshal(raw, &observation); err != nil {
		return drop(metrics.NodeAuditInvalid)
	}
	if err := nodeprotocol.ValidateAuditObservation(observation); err != nil {
		return drop(metrics.NodeAuditInvalid)
	}
	if !slices.Contains(capabilities, nodeprotocol.CapabilityAuditHits) {
		return drop(metrics.NodeAuditNoCapability)
	}
	if !slices.Contains(capabilities, nodeprotocol.CapabilityAuditUsage) {
		observation.Usage = nil
	}
	return &observation
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
	// The coding is read before the body. Only gzip, from an agent that declared
	// sync.gzip.v1, is taken: anything else with a Content-Encoding is 415 and
	// nothing is read. A request with no coding is what it always was.
	encoding, err := nodeprotocol.SyncEncodingFromHeaders(request.Header.Values(nodeprotocol.SyncContentEncodingHeader))
	if err != nil {
		metrics.NodeSyncGzipRefusedTotal.With("unknown_coding").Inc()
		writeNodeError(w, http.StatusUnsupportedMediaType, fmt.Errorf("sync body coding: %w", err))
		return
	}
	limited := http.MaxBytesReader(w, request.Body, nodeprotocol.MaxSyncWireBytes)
	defer limited.Close()
	var body []byte
	if encoding == nodeprotocol.SyncEncodingGzip {
		if declared, err := h.declaredGzip(request.Context(), agentID); err != nil {
			log.Error("native node sync capability lookup failed", "agent_id", agentID, "err", err)
			writeNodeError(w, http.StatusInternalServerError, errors.New("node sync failed"))
			return
		} else if !declared {
			metrics.NodeSyncGzipRefusedTotal.With("not_negotiated").Inc()
			writeNodeError(w, http.StatusUnsupportedMediaType, errors.New("compressed sync body was not negotiated"))
			return
		}
		// THE WIRE BODY IS READ BEFORE ANY SLOT IS TAKEN. A slot pays for
		// inflating, which is CPU and memory; a slow uploader would hold one for as
		// long as it likes if it were taken first. The read is bounded by the wire
		// limit.
		compressed, err := io.ReadAll(limited)
		if err != nil {
			h.refuseBody(w, err)
			return
		}
		release, ok := h.acquireDecode(request.Context(), agentID)
		if !ok {
			// 503, not 429: nothing is wrong with the request. The agent keeps
			// its state on a 5xx and sends the same request again on its next
			// poll; a 400 or 415 would have switched compression off for an hour.
			metrics.NodeSyncDecodeBusyTotal.Inc()
			w.Header().Set("Retry-After", "5")
			writeNodeError(w, http.StatusServiceUnavailable, errors.New("node sync busy"))
			return
		}
		// Both limits are enforced while it inflates: a bomb costs about the
		// decoded limit, never its size.
		body, err = nodeprotocol.DecodeSyncBody(bytes.NewReader(compressed), encoding)
		release()
		if err == nil {
			metrics.NodeSyncBytesTotal.With("request_wire").Add(int64(len(compressed)))
			metrics.NodeSyncBytesTotal.With("request_decoded").Add(int64(len(body)))
		}
		compressed = nil
		if err != nil {
			h.refuseBody(w, err)
			return
		}
	} else if body, err = io.ReadAll(limited); err != nil {
		h.refuseBody(w, err)
		return
	}
	var wire nodeReportWire
	// Unmarshal rather than a streaming decoder: this makes a trailing second
	// document a syntax error, instead of something a following Decode has to be
	// remembered to notice.
	if err := json.Unmarshal(body, &wire); err != nil {
		writeNodeError(w, http.StatusBadRequest, fmt.Errorf("decode node report: %w", err))
		return
	}
	report := wire.NodeReport
	report.PolicyStatus, report.Audit = nil, nil
	if report.AgentID != agentID {
		writeNodeError(w, http.StatusUnauthorized, ErrNodeAuthentication)
		return
	}
	// The telemetry subtree is validated SEPARATELY, and failing it costs the
	// sample rather than the request. The control half is validated first below,
	// so a report that is wrong about its roster is still rejected outright.
	// THE PANEL'S OWN RANGE IS CHECKED FIRST, and separately from the contract's
	// validator. ValidateNodeReportBase bounds the generation by the SHARED
	// package's constant, which is the dependency's opinion; which generations
	// this panel admits is PSP's declaration. They agree today and the check is
	// here so that they cannot silently stop agreeing — the shared literal is
	// what a parallel /v2/node/sync would be built beside, so it is not the thing
	// to widen.
	if effective := nodeprotocol.EffectiveProtocolVersion(report.ProtocolVersion); report.ProtocolVersion >= 0 {
		if generations := domain.SupportedNodeProtocolGenerations(); effective < generations.Min || effective > generations.Max {
			err := fmt.Errorf("protocol_version %d is outside the reviewed range %d..%d",
				effective, generations.Min, generations.Max)
			h.recordRefusal(agentID, effective, domain.NodeRefusalProtocolGeneration, err)
			writeNodeError(w, http.StatusBadRequest, fmt.Errorf("validate node report: %w", err))
			return
		}
	}
	if err := nodeprotocol.ValidateNodeReportBase(report); err != nil {
		h.recordRefusal(agentID, nodeprotocol.EffectiveProtocolVersion(report.ProtocolVersion), refusalReason(report), err)
		writeNodeError(w, http.StatusBadRequest, fmt.Errorf("validate node report: %w", err))
		return
	}
	report.Host = h.sanitizeHost(body, report.Host, agentID)
	// The policy status is validated AFTER the control half, and on its own:
	// a report that is wrong about its roster is still refused outright, and
	// one whose status is wrong keeps everything but the status.
	report.PolicyStatus = h.sanitizePolicyStatus(wire.PolicyStatus, report.Capabilities, agentID)
	// The audit subtree the same way: after the control half, on its own.
	report.Audit = h.sanitizeAudit(wire.Audit, report.Capabilities, agentID)
	// The agent's declaration is read from the report that was just validated.
	declaredGzip := slices.Contains(report.Capabilities, nodeprotocol.CapabilitySyncGzip)
	if encoding == nodeprotocol.SyncEncodingGzip && !declaredGzip {
		metrics.NodeSyncGzipRefusedTotal.With("not_negotiated").Inc()
		writeNodeError(w, http.StatusUnsupportedMediaType, errors.New("compressed sync body without the sync.gzip.v1 capability"))
		return
	}
	body, wire = nil, nodeReportWire{}
	if declaredGzip {
		// Every response to an agent that declared the capability varies with
		// what it asked for, so a cache between the two must not mix them.
		w.Header().Add("Vary", nodeprotocol.SyncAcceptEncodingHeader)
	}
	acceptGzip := declaredGzip && nodeprotocol.AcceptsSyncGzip(strings.Join(request.Header.Values(nodeprotocol.SyncAcceptEncodingHeader), ","))
	var response nodeprotocol.SyncResponse
	if service, ok := h.service.(interface {
		SyncWithResponseEncoding(context.Context, nodeprotocol.NodeReport, string) (nodeprotocol.SyncResponse, error)
	}); ok && acceptGzip {
		response, err = service.SyncWithResponseEncoding(request.Context(), report, nodeprotocol.SyncEncodingGzip)
	} else {
		response, err = h.service.Sync(request.Context(), report)
	}
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
	if err != nil || int64(len(payload)) > nodeprotocol.MaxSyncDecodedBytes {
		log.Error("native node sync response encoding failed", "agent_id", agentID, "bytes", len(payload), "err", err)
		writeNodeError(w, http.StatusInternalServerError, errors.New("node sync failed"))
		return
	}
	if acceptGzip {
		if compressed, err := nodeprotocol.EncodeSyncBody(payload); err == nil {
			w.Header().Set(nodeprotocol.SyncContentEncodingHeader, nodeprotocol.SyncEncodingGzip)
			metrics.NodeSyncBytesTotal.With("response_plain").Add(int64(len(payload)))
			metrics.NodeSyncBytesTotal.With("response_wire").Add(int64(len(compressed)))
			payload = compressed
		}
	}
	if int64(len(payload)) > nodeprotocol.MaxSyncWireBytes {
		log.Error("native node sync response exceeds wire limit", "agent_id", agentID, "bytes", len(payload))
		writeNodeError(w, http.StatusInternalServerError, errors.New("node sync failed"))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if declaredGzip {
		w.Header().Set(nodeprotocol.SyncAcceptEncodingHeader, nodeprotocol.SyncEncodingGzip)
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(payload)
}

// declaredGzip reports whether the agent ever declared sync.gzip.v1. A handler
// built without a capability reader never decodes anything.
func (h *NodeSyncHandler) declaredGzip(ctx context.Context, agentID string) (bool, error) {
	if h.capabilities == nil {
		return false, nil
	}
	agent, err := h.capabilities.GetByAgentID(ctx, agentID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return false, nil
		}
		return false, err
	}
	return agent != nil && slices.Contains(agent.ObservedCapabilities, nodeprotocol.CapabilitySyncGzip), nil
}

// refuseBody answers a body that could not be read or decoded.
func (h *NodeSyncHandler) refuseBody(w http.ResponseWriter, err error) {
	status := http.StatusBadRequest
	var tooLarge *http.MaxBytesError
	switch {
	case errors.As(err, &tooLarge), errors.Is(err, nodeprotocol.ErrSyncWireTooLarge), errors.Is(err, nodeprotocol.ErrSyncDecodedTooLarge):
		status = http.StatusRequestEntityTooLarge
	case errors.Is(err, nodeprotocol.ErrSyncUnknownEncoding):
		status = http.StatusUnsupportedMediaType
	}
	writeNodeError(w, status, fmt.Errorf("read node report: %w", err))
}

// acquireDecode takes a decode slot for one agent, waiting a bounded time. An
// agent may hold one slot at a time: a second compressed request of the same
// agent while its first inflates is refused at once, so one agent cannot take
// every slot. The returned release is safe to call more than once.
func (h *NodeSyncHandler) acquireDecode(ctx context.Context, agentID string) (release func(), ok bool) {
	h.decodingMu.Lock()
	if h.decoding == nil {
		h.decoding = map[string]struct{}{}
	}
	if _, busy := h.decoding[agentID]; busy {
		h.decodingMu.Unlock()
		return nil, false
	}
	h.decoding[agentID] = struct{}{}
	h.decodingMu.Unlock()
	forget := func() {
		h.decodingMu.Lock()
		delete(h.decoding, agentID)
		h.decodingMu.Unlock()
	}
	timer := time.NewTimer(h.decodeWait)
	defer timer.Stop()
	select {
	case h.decodeSlots <- struct{}{}:
	case <-timer.C:
		forget()
		return nil, false
	case <-ctx.Done():
		forget()
		return nil, false
	}
	var once sync.Once
	return func() { once.Do(func() { <-h.decodeSlots; forget() }) }, true
}

func writeNodeError(w http.ResponseWriter, status int, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
}

var _ http.Handler = (*NodeSyncHandler)(nil)
