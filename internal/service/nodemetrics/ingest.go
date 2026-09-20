// Package nodemetrics owns node host telemetry on the panel side: the ingest
// boundary, the retention constants, and the derivation and rollup that read
// what it stores.
package nodemetrics

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sync"
	"time"

	nodeprotocol "github.com/KazuhaHub/passwall-protocol/protocol"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/metrics"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// Retention and cadence constants.
//
// THEY ARE COMPILED VALUES IN THIS VERSION, not settings. The spec defers the
// settings until there is production observation to set them from, and shipping
// several unmeasured retention knobs at once is how a fleet ends up with three
// different answers to "how much history do we keep".
const (
	// DefaultNodeHostReportInterval is what the panel asks a node to report at.
	DefaultNodeHostReportInterval = 60 * time.Second
	// NodeMetricRawWriteInterval is the floor between two history rows. The agent
	// may send far more often; the panel stores what it can chart.
	NodeMetricRawWriteInterval = 60 * time.Second
	NodeMetricRawRetention     = 7 * 24 * time.Hour
	NodeMetricHourlyRetention  = 90 * 24 * time.Hour
	// NodeMetricRollupSafetyLag keeps the rollup off the hour that is still
	// accumulating. A bucket aggregated while its raw rows are still arriving
	// would be wrong, and it would be re-aggregated on the next tick forever.
	NodeMetricRollupSafetyLag = 5 * time.Minute
)

// nodeHostPersistBudget bounds one persistence attempt.
//
// IT IS A HARD REQUIREMENT RATHER THAN A TUNING: telemetry is best-effort, and
// the node is waiting on this same round trip for its roster and its quota. A
// storage stall must cost the panel a metric, never the node a sync.
const nodeHostPersistBudget = 500 * time.Millisecond

// Service ingests node host telemetry.
type Service struct {
	repo ports.NodeHostMetricRepo
	now  func() time.Time
	// refresh holds the on-demand refresh window (spec §11.3). It is in memory
	// on purpose: losing it on a restart is allowed because it has no side
	// effect beyond asking a node for its next sample sooner.
	refreshMu sync.Mutex
	refresh   map[string]refreshWindow
	// lastSampleID is what the panel most recently stored per agent. It is kept
	// here rather than read back so the refresh window costs the request path no
	// query at all.
	lastSampleID map[string]string
}

var errNoRepo = errors.New("nodemetrics: repository is required")

type refreshWindow struct {
	baselineSampleID string
	expiresAt        time.Time
}

type Options struct {
	Repo ports.NodeHostMetricRepo
	Now  func() time.Time
}

func New(options Options) (*Service, error) {
	if options.Repo == nil {
		return nil, errNoRepo
	}
	service := &Service{
		repo: options.Repo, now: options.Now,
		refresh: map[string]refreshWindow{}, lastSampleID: map[string]string{},
	}
	if service.now == nil {
		service.now = time.Now
	}
	return service, nil
}

// IngestResult is what the ingest decided, so the caller can log and count it
// rather than inferring it from side effects.
type IngestResult struct {
	// SampleID is the sample the panel now holds, or the one it kept. It is what
	// the refresh window matches on.
	SampleID string
	// Outcome is the report-level metric label.
	Outcome string
	// HistoryOutcome is the history-level metric label, empty when no history
	// write was attempted.
	HistoryOutcome string
}

// Ingest persists one report's host subtree.
//
// IT NEVER RETURNS AN ERROR, and that is the whole design. Telemetry is
// best-effort observation on a request that also carries the node's roster and
// its quota; a malformed sample, a storage stall or a disk error must cost the
// panel a metric and nothing else. Returning an error here would be the one way
// this feature could break the data plane, which is the outcome it exists to be
// incapable of.
func (s *Service) Ingest(ctx context.Context, agentID string, observation *nodeprotocol.HostObservation, receivedAt time.Time) IngestResult {
	if observation == nil {
		metrics.NodeHostReportTotal.With(metrics.NodeHostOutcomeNoHost).Inc()
		return IngestResult{Outcome: metrics.NodeHostOutcomeNoHost}
	}
	// Re-validated here as well as at the wire boundary: this is the entry point
	// a non-HTTP caller reaches, and a sample the contract refuses must not reach
	// SQL from any direction.
	if err := nodeprotocol.ValidateHostObservation(*observation); err != nil {
		metrics.NodeHostReportTotal.With(metrics.NodeHostOutcomeInvalid).Inc()
		return IngestResult{Outcome: metrics.NodeHostOutcomeInvalid}
	}
	canonical, err := json.Marshal(observation)
	if err != nil {
		metrics.NodeHostReportTotal.With(metrics.NodeHostOutcomeInvalid).Inc()
		return IngestResult{Outcome: metrics.NodeHostOutcomeInvalid}
	}
	digest := sha256.Sum256(canonical)
	observationForStorage := domain.NodeHostObservation{
		AgentID: agentID, SampleID: observation.SampleID,
		PayloadDigest: hex.EncodeToString(digest[:]),
		CollectedAt:   time.UnixMilli(observation.CollectedAtMS).UTC(),
		ReceivedAt:    receivedAt.UTC(),
		BootID:        observation.BootID,
		ResourceScope: string(observation.Scope.ResourceScope),
		SnapshotJSON:  canonical,
	}

	// The predecessor is read ONCE and used twice: for the write throttle below,
	// and for the two figures the server list renders. Reading it here rather than
	// at read time is what keeps that list to one query — a rate needs two
	// samples, and the list cannot fetch a predecessor per row.
	previous, hasPrevious := s.previousSample(ctx, agentID, receivedAt)
	if hasPrevious {
		rates := Derive(previous, sampleFromObservation(agentID, &observationForStorage, observation))
		observationForStorage.CPUPercent = rates.CPUPercent
		observationForStorage.MemoryPercent = rates.MemoryUsedPercent
	}

	// The whole persistence attempt runs under its own deadline, so a slow disk
	// cannot hold the node's sync open.
	bounded, cancel := context.WithTimeout(ctx, nodeHostPersistBudget)
	defer cancel()
	started := time.Now()
	defer func() {
		metrics.NodeHostPersistMS.Observe(float64(time.Since(started).Milliseconds()))
		metrics.NodeHostSnapshotBytes.Observe(float64(len(canonical)))
	}()

	request := domain.NodeHostPersistRequest{Observation: &observationForStorage}
	if isHistoryDue(previous, hasPrevious, observation, receivedAt) {
		sample := sampleFromObservation(agentID, &observationForStorage, observation)
		request.Sample = sample
		request.Interfaces = interfacesFromObservation(agentID, observation.SampleID, receivedAt, observation)
	}

	result, err := s.repo.Persist(bounded, request)
	if err != nil {
		// A storage error is invisible to the node by design, so it has to be
		// visible HERE or it is visible nowhere.
		metrics.NodeHostReportTotal.With(metrics.NodeHostOutcomeStorageError).Inc()
		if request.Sample != nil {
			metrics.NodeHostHistoryTotal.With(metrics.NodeHostHistoryStorageError).Inc()
		}
		return IngestResult{SampleID: observation.SampleID, Outcome: metrics.NodeHostOutcomeStorageError}
	}
	// The refresh window matches on what the panel now holds, so it is recorded
	// here where the agent id is in hand rather than inferred later.
	s.RememberSample(agentID, observation.SampleID)
	return s.classify(observation, request.Sample != nil, result)
}

func (s *Service) classify(observation *nodeprotocol.HostObservation, wroteHistory bool, result domain.NodeHostPersistResult) IngestResult {
	// A late request that changed nothing is still HANDLED, so it is not an
	// error — the only outcome worth separating out is the identity conflict,
	// because that one means the agent built two different samples under one id.
	outcome := metrics.NodeHostOutcomeAccepted
	if result.IdentityConflict {
		outcome = metrics.NodeHostOutcomeIdentityConflict
	}
	metrics.NodeHostReportTotal.With(outcome).Inc()

	historyOutcome := ""
	if wroteHistory {
		switch {
		case result.IdentityConflict, result.Duplicate:
			historyOutcome = metrics.NodeHostHistoryDuplicate
		case result.HistoryInserted:
			historyOutcome = metrics.NodeHostHistoryInserted
		default:
			historyOutcome = metrics.NodeHostHistoryThrottled
		}
		metrics.NodeHostHistoryTotal.With(historyOutcome).Inc()
	}
	return IngestResult{SampleID: observation.SampleID, Outcome: outcome, HistoryOutcome: historyOutcome}
}

// previousSample finds the newest history row before this report, which is what
// the write throttle measures against.
//
// It reads through the range query with the report's own instant as the window
// start and the predecessor asked for, so the throttle survives a panel restart
// without any in-memory state: the answer comes from the database every time.
func (s *Service) previousSample(ctx context.Context, agentID string, receivedAt time.Time) (*domain.NodeHostMetricSample, bool) {
	samples, err := s.repo.RawRange(ctx, agentID, receivedAt.UTC(), receivedAt.UTC(), true)
	if err != nil || len(samples) == 0 {
		return nil, false
	}
	return &samples[0], true
}

// isHistoryDue applies the write throttle.
//
// A BREAK IS ALWAYS DUE. The raw interval exists to bound storage, not to hide
// discontinuities: a reboot, a scope change or a core restart is exactly the
// event a chart has to mark, and letting the interval suppress it would draw a
// continuous line through a restart.
func isHistoryDue(previous *domain.NodeHostMetricSample, hasPrevious bool, observation *nodeprotocol.HostObservation, receivedAt time.Time) bool {
	if !hasPrevious {
		return true
	}
	if !receivedAt.Before(previous.ReceivedAt.Add(NodeMetricRawWriteInterval)) {
		return true
	}
	if previous.BootID != observation.BootID {
		return true
	}
	if previous.ResourceScope != string(observation.Scope.ResourceScope) {
		return true
	}
	return processStartChanged(previous, observation)
}

func processStartChanged(previous *domain.NodeHostMetricSample, observation *nodeprotocol.HostObservation) bool {
	if observation.Processes == nil {
		return false
	}
	current := time.UnixMilli(observation.Processes.Agent.StartedAtMS).UTC()
	if previous.AgentStartedAt == nil {
		return true
	}
	return !previous.AgentStartedAt.Equal(current)
}

// RequestRefresh opens the on-demand refresh window for one agent (spec §11.3).
//
// It records the sample the panel currently holds as the baseline, so the window
// closes when something DIFFERENT arrives rather than when anything arrives —
// otherwise a sample already in flight would close it immediately and the
// operator's refresh would appear to do nothing.
func (s *Service) RequestRefresh(agentID string) {
	s.refreshMu.Lock()
	defer s.refreshMu.Unlock()
	// The baseline is read from the map directly rather than through
	// currentSampleID: that helper takes the same lock, and a re-entrant call
	// would deadlock the request path rather than merely be inconvenient.
	baseline := s.lastSampleID[agentID]
	window, exists := s.refresh[agentID]
	now := s.now().UTC()
	// Coalesced within the window, so a double-click is one request.
	if exists && now.Before(window.expiresAt) {
		window.expiresAt = now.Add(refreshWindowLifetime)
		s.refresh[agentID] = window
		return
	}
	s.refresh[agentID] = refreshWindow{
		baselineSampleID: baseline,
		expiresAt:        now.Add(refreshWindowLifetime),
	}
}

// refreshWindowLifetime is thirty seconds: long enough for a node polling at the
// default cadence to answer, short enough that a node which never answers does
// not leave the panel asking forever.
const refreshWindowLifetime = 30 * time.Second

// WantsHostReport reports whether the next envelope for this agent should ask
// for a sample, and closes the window once the one it was waiting for arrives.
//
// The panel's own record of the current sample decides it, so the caller cannot
// pass the wrong one — and the window closes because a DIFFERENT sample arrived,
// not because any sample did. A sample already in flight when the operator hit
// refresh would otherwise close it immediately, and the refresh would look like
// it did nothing.
func (s *Service) WantsHostReport(agentID string) bool {
	s.refreshMu.Lock()
	defer s.refreshMu.Unlock()
	window, exists := s.refresh[agentID]
	if !exists {
		return false
	}
	if s.now().UTC().After(window.expiresAt) {
		delete(s.refresh, agentID)
		return false
	}
	if current := s.lastSampleID[agentID]; current != "" && current != window.baselineSampleID {
		delete(s.refresh, agentID)
		return false
	}
	return true
}

// RememberSample records the sample the panel now holds for an agent.
func (s *Service) RememberSample(agentID, sampleID string) {
	s.refreshMu.Lock()
	defer s.refreshMu.Unlock()
	s.lastSampleID[agentID] = sampleID
}
