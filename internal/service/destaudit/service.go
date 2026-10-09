package destaudit

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	protocol "github.com/KazuhaHub/passwall-protocol/protocol"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/metrics"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// Event contains only bounded diagnostic categories and counts.
type Event struct {
	AgentID, Kind, Outcome     string
	PanelID, ReceivedHour      int64
	Rows, DiagnosticKeys       int64
	NodeDropped, NodeUnmatched uint64
}

type Options struct {
	Now  func() time.Time
	Emit func(Event)
}

type Summary struct {
	Batches, Rows      int
	LossKeys, LossRows int64
}

type Service struct {
	q         *batchQueue
	controls  map[int64]domain.DestAuditControl // protected by q.mu
	loss      *lossBuffer
	worker    *ingestWorker
	repo      ports.DestAuditStore
	now       func() time.Time
	emit      func(Event)
	started   atomic.Bool
	producers sync.RWMutex // StopOffers joins memory-only producers before drain can end
}

// New warms current permissions before HTTP starts. It starts no goroutine;
// the app tracks the single Run owner and keeps its DB alive until it returns.
func New(ctx context.Context, repo ports.DestAuditStore, options Options) (*Service, error) {
	if repo == nil {
		return nil, domain.ErrValidation
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	s := &Service{q: newBatchQueue(), controls: map[int64]domain.DestAuditControl{}, loss: newLossBuffer(), repo: repo, now: options.Now, emit: options.Emit}
	s.q.admit = s.admissionLocked
	s.worker = newIngestWorker(s.q, repo, s.now, s.workerEvent)
	if err := repo.WatchDestinationAuditControls(ctx, s.updateControls); err != nil {
		return nil, domain.ErrUnavailable
	}
	return s, nil
}

// This callback runs under q.mu; it never reads storage or a writer gate.
func (s *Service) admissionLocked(panelID int64, body protocol.AuditObservation) string {
	state, found := s.controls[panelID]
	if !found || state.Collect == domain.AuditCollectOff {
		return "collect_off"
	}
	if state.Revision != body.CollectRevision {
		return "stale_collect_revision"
	}
	if body.Kind == "usage" && state.Collect != domain.AuditCollectHitsAndUsage {
		return "collect_off"
	}
	return ""
}

// Offer has the same short memory boundary as control-cache invalidation.
// Rejections never change the config/roster response or wait for a DB write.
func (s *Service) Offer(agentID string, panelID int64, receivedAt time.Time, body protocol.AuditObservation) {
	s.producers.RLock()
	defer s.producers.RUnlock()
	outcome := s.q.offerReceived(agentID, panelID, receivedAt, body)
	if outcome == "closed" {
		s.publish(Event{AgentID: agentID, Kind: boundedKind(body.Kind), Outcome: "ingest_error"})
		return
	}
	e := Event{AgentID: agentID, PanelID: panelID, Kind: boundedKind(body.Kind), Outcome: outcome, ReceivedHour: receivedAt.UTC().Truncate(time.Hour).UnixMilli()}
	if outcome != "accepted" && outcome != "duplicate_batch" && outcome != "invalid" {
		e.Rows = int64(len(body.Hits) + len(body.Usage))
	}
	s.record(e, false)
}

func boundedKind(kind string) string {
	switch kind {
	case "block", "observe", "trial", "usage":
		return kind
	default:
		return "unknown"
	}
}

func (s *Service) updateControls(states []domain.DestAuditControl) {
	s.producers.RLock()
	defer s.producers.RUnlock()
	type dropped struct {
		b      *queuedBatch
		reason string
	}
	var waiting []dropped
	s.q.mu.Lock()
	for _, state := range states {
		if state.PanelID <= 0 {
			continue
		}
		if !state.Available || !state.Collect.Valid() || state.Revision == 0 {
			state = domain.DestAuditControl{PanelID: state.PanelID}
		}
		if old, found := s.controls[state.PanelID]; found && old == state {
			continue
		}
		if state.Available {
			s.controls[state.PanelID] = state
		} else {
			delete(s.controls, state.PanelID)
		}
		reason := "stale_collect_revision"
		if !state.Available || state.Collect == domain.AuditCollectOff {
			reason = "collect_off"
		}
		removed, _ := s.q.discardPanelLocked(state.PanelID, reason)
		for _, b := range removed {
			waiting = append(waiting, dropped{b, reason})
		}
	}
	s.q.mu.Unlock()
	for _, d := range waiting {
		s.record(Event{AgentID: d.b.agentID, PanelID: d.b.panelID, Kind: d.b.body.Kind, Outcome: d.reason, ReceivedHour: d.b.receivedHour, Rows: int64(len(d.b.body.Hits) + len(d.b.body.Usage))}, false)
	}
}

func (s *Service) workerEvent(e workerEvent) {
	s.record(Event{AgentID: e.agentID, PanelID: e.panelID, Kind: e.kind, Outcome: e.outcome, ReceivedHour: e.receivedHour, Rows: int64(e.rows), NodeDropped: e.nodeDropped, NodeUnmatched: e.nodeUnmatched}, e.persistedLoss)
}

func (s *Service) record(e Event, persisted bool) {
	if !persisted && e.Rows > 0 {
		row := domain.DestAuditLoss{PanelID: e.PanelID, HourMS: e.ReceivedHour, Kind: e.Kind, Reason: e.Outcome, Rows: e.Rows}
		if validBufferedLoss(row) {
			if !s.loss.add(row) {
				s.publish(Event{Kind: e.Kind, Outcome: "loss_buffer_dropped", DiagnosticKeys: 1})
			}
			s.q.signal()
		}
	}
	s.publish(e)
}

// Emit is a diagnostic observer: it must only update memory or fixed-count
// logs, never re-enter collection writers or perform storage/network work.
func (s *Service) publish(e Event) {
	switch e.Outcome {
	case "duplicate_batch", "queue_full", "invalid", "ingest_error":
		metrics.NodeAuditReportTotal.With(boundedKind(e.Kind), e.Outcome).Inc()
	case "stored", "unknown_subject", "out_of_range", "over_budget", "collect_off", "stale_collect_revision":
		if e.Rows > 0 && boundedKind(e.Kind) != "unknown" {
			metrics.DestAuditRowsTotal.With(e.Kind, e.Outcome).AddSaturating(uint64(e.Rows))
		}
	case "node_diagnostics":
		metrics.DestAuditNodeDroppedTotal.AddSaturating(e.NodeDropped)
		metrics.DestAuditNodeUnmatchedTotal.AddSaturating(e.NodeUnmatched)
	case "loss_buffer_dropped":
		if e.DiagnosticKeys > 0 {
			metrics.DestAuditLossBufferDroppedTotal.AddSaturating(uint64(e.DiagnosticKeys))
		}
	case "loss_flush_error":
		metrics.DestAuditLossFlushErrorsTotal.Inc()
	}
	if s.emit != nil {
		s.emit(e)
	}
}

func (s *Service) StopOffers() {
	s.producers.Lock()
	s.q.stop()
	s.producers.Unlock()
}

// Run is called once by the app's tracked worker. Its context is independent
// of ordinary app cancellation; StopOffers drains and a deadline cancels it.
// Data chunks and loss flushes share this one storage owner.
func (s *Service) Run(ctx context.Context) Summary {
	if !s.started.CompareAndSwap(false, true) {
		return Summary{}
	}
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	var retryAfter time.Time
	for {
		if ctx.Err() != nil {
			s.StopOffers()
			payload := s.worker.abandon()
			loss := s.loss.discard()
			if loss.keys > 0 {
				s.publish(Event{Outcome: "loss_buffer_dropped", DiagnosticKeys: int64(loss.keys)})
			}
			return Summary{Batches: payload.batches, Rows: payload.rows, LossKeys: int64(loss.keys), LossRows: loss.rows}
		}
		if s.loss.pendingKeys() > 0 && !time.Now().Before(retryAfter) {
			result, err := s.loss.flush(ctx, s.repo, s.now())
			if result.discarded {
				s.publish(Event{Outcome: "loss_buffer_dropped", DiagnosticKeys: int64(result.keys)})
			}
			if err != nil && !result.discarded {
				s.publish(Event{Outcome: "loss_flush_error"})
				retryAfter = time.Now().Add(time.Second)
			} else {
				retryAfter = time.Time{}
			}
		}
		if ctx.Err() != nil {
			continue
		}
		if s.worker.step(ctx) {
			continue
		}
		if s.q.isClosed() && s.loss.pendingKeys() == 0 {
			return Summary{}
		}
		// Another frozen group can follow a successful flush immediately.
		// Failed storage waits for a tick without spinning.
		if s.loss.pendingKeys() > 0 && retryAfter.IsZero() {
			continue
		}
		select {
		case <-s.q.wake:
		case <-ticker.C:
		case <-ctx.Done():
		}
	}
}
