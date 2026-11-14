// Package destaudit ingests the destination-rule hits a native node reports.
//
// THE SHAPE. A node's report may carry an audit subtree: which rules fired in
// the last hours, for whom, towards what. The sync handler validates it on its
// own (a bad subtree costs the subtree, never the sync) and nodesync hands the
// valid ones to Enqueue AFTER the sync response is built. Enqueue does no
// database work: it de-duplicates the batch id in memory and puts the batch on
// a bounded queue. A worker (Run, started with safego.GoTracked and drained on
// shutdown) maps each batch to stored rows and writes them.
//
// WHY A QUEUE. A sync holds its agent's lock and SQLite has one writer; a batch
// can be thousands of rows. Writing inside Sync would make every node's sync
// wait on it and stall the traffic poll's writes behind it. The price is that
// the node gets its 200 before the rows are stored, so a full queue, a failed
// write or a restart loses the batch. That loss is accepted, bounded and
// counted: the node's own copy is not durable either, and failing the sync to
// retry would block the node's roster and config.
//
// WHAT IS STORED. Hourly aggregates of (rule, account, destination, port) and a
// count, never the client's source address (the protocol has none). Nothing in
// this package may put a destination, an account or an address in a log line or
// a metric label: a counter says that rows were lost, never what they were.
package destaudit

import (
	"container/list"
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/KazuhaHub/passwall-protocol/protocol"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/log"
)

const (
	// maxQueuedPerAgent and maxQueuedTotal bound the queue: a node cannot make
	// the panel hold more than four of its batches, and the panel holds at most
	// 256 in all (a few MiB of rows at the protocol's size bound).
	maxQueuedPerAgent = 4
	maxQueuedTotal    = 256
	// seenBatches is how many recent batch ids the in-memory de-duplication
	// remembers, queued or stored.
	seenBatches = 10000
	// defaultHourlyRowBudget is the most rows one agent may store per hour. A
	// legitimate node is far under it; a node that is not is flooding the table.
	defaultHourlyRowBudget = 20000
	// flushEvery is how often the worker writes the loss counts that Enqueue
	// noted in memory (it does no database work itself).
	flushEvery = 15 * time.Second
	// shutdownDrain is how long the final drain may take.
	shutdownDrain = 15 * time.Second

	// The outcomes the row counter is told. They are the metric's label values
	// (metrics.DestRows*), kept equal by a test.
	metricsStored      = "hits_stored"
	metricsUnknown     = "hits_unknown_subject"
	metricsOutOfRange  = "hits_out_of_range"
	metricsOverBudget  = "hits_over_budget"
	metricsUsageOff    = "usage_collect_off"
	reportAccepted     = "accepted"
	reportDuplicate    = "duplicate_batch"
	reportQueueFull    = "queue_full"
	reportIngestError  = "ingest_error"
	userIDLookupMaxIDs = 4096
)

// Store is the write side (sqlstore.DestHitRepo).
type Store interface {
	StoreBatch(ctx context.Context, agentID, batchID string, receivedAt time.Time, rows []domain.DestHit) (domain.DestBatchResult, error)
	AddDrops(ctx context.Context, hourMS, panelID int64, kind string, n int64) error
	ExistingUserIDs(ctx context.Context, ids []int64) (map[int64]bool, error)
}

// Metrics are the counters the service moves. Every field is optional.
type Metrics struct {
	// Report counts a report's audit subtree once, by outcome.
	Report func(outcome string)
	// Rows counts rows by outcome.
	Rows func(outcome string, n int)
	// NodeDropped and NodeUnmatched add what the node itself reported losing.
	NodeDropped   func(n uint64)
	NodeUnmatched func(n uint64)
}

// Deps are the service's collaborators.
type Deps struct {
	Store   Store
	Metrics Metrics
	// Now is the clock; nil is time.Now.
	Now func() time.Time
	// HourlyRowBudget overrides the per-agent hourly row budget (0: 20000).
	HourlyRowBudget int
}

type queued struct {
	agentID    string
	panelID    int64
	obs        *protocol.AuditObservation
	receivedAt time.Time
}

type lostKey struct {
	hour, panel int64
	kind        string
}

type budget struct {
	hour int64
	used int
}

// Service is the ingest queue and its worker.
type Service struct {
	store   Store
	metrics Metrics
	now     func() time.Time
	budget  int

	mu       sync.Mutex
	queue    []queued
	perAgent map[string]int
	lost     map[lostKey]int64
	budgets  map[string]*budget

	seenMu sync.Mutex
	seen   map[string]*list.Element
	order  *list.List

	wake chan struct{}
}

// New builds the service. A nil store makes Enqueue a no-op, which is what a
// build without hit recording is.
func New(d Deps) *Service {
	now := d.Now
	if now == nil {
		now = time.Now
	}
	b := d.HourlyRowBudget
	if b <= 0 {
		b = defaultHourlyRowBudget
	}
	return &Service{
		store: d.Store, metrics: d.Metrics, now: now, budget: b,
		perAgent: map[string]int{}, lost: map[lostKey]int64{}, budgets: map[string]*budget{},
		seen: map[string]*list.Element{}, order: list.New(), wake: make(chan struct{}, 1),
	}
}

func (s *Service) report(outcome string) {
	if s.metrics.Report != nil {
		s.metrics.Report(outcome)
	}
}

func (s *Service) rows(outcome string, n int) {
	if n > 0 && s.metrics.Rows != nil {
		s.metrics.Rows(outcome, n)
	}
}

// remember records a batch id and reports whether it was already known. The
// least recently used id is forgotten past seenBatches.
func (s *Service) remember(key string) (known bool) {
	s.seenMu.Lock()
	defer s.seenMu.Unlock()
	if el, ok := s.seen[key]; ok {
		s.order.MoveToFront(el)
		return true
	}
	s.seen[key] = s.order.PushFront(key)
	for s.order.Len() > seenBatches {
		oldest := s.order.Back()
		s.order.Remove(oldest)
		delete(s.seen, oldest.Value.(string))
	}
	return false
}

func (s *Service) forget(key string) {
	s.seenMu.Lock()
	defer s.seenMu.Unlock()
	if el, ok := s.seen[key]; ok {
		s.order.Remove(el)
		delete(s.seen, key)
	}
}

// Enqueue offers one validated observation. It does no database work and
// never blocks, so the sync that calls it is not slowed by the store.
//
// A batch id already seen (queued or stored) counts as duplicate_batch and is
// dropped: the node sends a batch again until it gets a 200, and counting its
// rows twice would double every hit of a flaky link. The id is remembered BEFORE
// the batch is queued, so a re-send that arrives while the first is still
// waiting is not queued a second time either.
func (s *Service) Enqueue(agentID string, panelID int64, o *protocol.AuditObservation) {
	if s == nil || s.store == nil || o == nil {
		return
	}
	key := agentID + "/" + o.BatchID
	if s.remember(key) {
		s.report(reportDuplicate)
		return
	}
	s.mu.Lock()
	if s.perAgent[agentID] >= maxQueuedPerAgent || len(s.queue) >= maxQueuedTotal {
		s.mu.Unlock()
		// Lost for good: forget the id, so a re-send (should there be one) is
		// not mistaken for a stored batch, and count the rows for the range
		// question ("this hour has N rows missing").
		s.forget(key)
		s.report(reportQueueFull)
		s.noteLost(panelID, o.Hits, domain.DestDropQueueFull)
		s.signal()
		return
	}
	s.queue = append(s.queue, queued{agentID: agentID, panelID: panelID, obs: o, receivedAt: s.now().UTC()})
	s.perAgent[agentID]++
	s.mu.Unlock()
	s.report(reportAccepted)
	s.signal()
}

func (s *Service) signal() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

// noteLost counts rows that did not become stored rows, in memory, against the
// hour each belongs to. The worker writes the counts; Enqueue must not.
func (s *Service) noteLost(panelID int64, hits []protocol.AuditHit, kind string) {
	if len(hits) == 0 {
		return
	}
	lo, hi := s.window()
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, h := range hits {
		if h.Hour < lo || h.Hour > hi {
			continue
		}
		s.lost[lostKey{h.Hour, panelID, kind}]++
	}
}

func (s *Service) window() (lo, hi int64) {
	now := s.now()
	return now.Add(-domain.AuditHourPastWindow).UnixMilli(), now.Add(domain.AuditHourFutureWindow).UnixMilli()
}

func (s *Service) pop() (queued, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.queue) == 0 {
		return queued{}, false
	}
	q := s.queue[0]
	s.queue = s.queue[1:]
	if s.perAgent[q.agentID]--; s.perAgent[q.agentID] <= 0 {
		delete(s.perAgent, q.agentID)
	}
	return q, true
}

// Drain processes everything queued now, then writes the noted loss counts.
func (s *Service) Drain(ctx context.Context) {
	if s == nil || s.store == nil {
		return
	}
	for {
		q, ok := s.pop()
		if !ok {
			break
		}
		s.process(ctx, q)
	}
	s.flushLost(ctx)
}

// Run is the worker: it processes batches as they arrive and, when ctx is
// cancelled, drains what is left on a context that is not, so a shutdown does
// not throw away the last batches. It returns when that is done.
func (s *Service) Run(ctx context.Context) {
	if s == nil || s.store == nil {
		<-ctx.Done()
		return
	}
	tick := time.NewTicker(flushEvery)
	defer tick.Stop()
	// THE WORK RUNS ON A CONTEXT THAT IS NOT CANCELLED WITH THE WORKER's. A batch
	// popped while the panel is shutting down must still be written: cancelling
	// its context would make the write fail and the batch, already off the queue,
	// would be lost. The worker stops by finishing the queue, then returning.
	work := context.WithoutCancel(ctx)
	for {
		s.Drain(work)
		select {
		case <-ctx.Done():
			final, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownDrain)
			s.Drain(final)
			cancel()
			return
		case <-s.wake:
		case <-tick.C:
		}
	}
}

func (s *Service) flushLost(ctx context.Context) {
	s.mu.Lock()
	if len(s.lost) == 0 {
		s.mu.Unlock()
		return
	}
	pending := s.lost
	s.lost = map[lostKey]int64{}
	s.mu.Unlock()
	for k, n := range pending {
		if err := s.store.AddDrops(ctx, k.hour, k.panel, k.kind, n); err != nil {
			// Put it back: a lost count must not be lost twice.
			s.mu.Lock()
			s.lost[k] += n
			s.mu.Unlock()
			log.Warn("destination hit loss counts not written", "rows", n, "err", err)
		}
	}
}

// sourceOf maps a rule id to the stored source: the x<k> suffix of a split
// policy is dropped, so p12x1 and p12x2 are both p12. It is a syntactic map
// only: the protocol has already checked the id's form.
func sourceOf(ruleID string) string {
	if i := strings.IndexByte(ruleID, 'x'); i > 0 {
		return ruleID[:i]
	}
	return ruleID
}

// process stores one batch. A panic in it costs the batch, never the worker: it
// is recovered, counted as an ingest error, recorded as dropped rows, and the
// budget the batch had reserved is given back.
func (s *Service) process(ctx context.Context, q queued) {
	reserved := 0
	defer func() {
		if r := recover(); r != nil {
			// Nothing of the batch is logged: only its size and the panic's type.
			log.Error("destination hit batch panicked; dropped", "agent_id", q.agentID, "rows", len(q.obs.Hits), "panic", fmt.Sprintf("%T", r))
			s.refund(q.agentID, reserved)
			s.failBatch(q, domain.DestDropIngest)
		}
	}()
	s.processBatch(ctx, q, &reserved)
}

// failBatch accounts for a batch that was lost whole: an ingest error, its rows
// in the hours they belong to, and its id forgotten so a re-send is not taken
// for a stored batch.
func (s *Service) failBatch(q queued, kind string) {
	s.report(reportIngestError)
	s.noteLost(q.panelID, q.obs.Hits, kind)
	s.forget(q.agentID + "/" + q.obs.BatchID)
}

func (s *Service) processBatch(ctx context.Context, q queued, reserved *int) {
	o := q.obs
	now := s.now()
	lo, hi := s.window()
	nowHour := now.Truncate(time.Hour).UnixMilli()

	// Per-row dispositions are tallied and reported only once the batch is known
	// not to be a duplicate, or a re-send would count its rejects twice.
	var outOfRange, unknown, overBudget int
	lostRows := map[lostKey]int64{}
	lose := func(hour int64, kind string) { lostRows[lostKey{hour, q.panelID, kind}]++ }

	var ids []int64
	idSeen := map[int64]bool{}
	type prepared struct {
		hit  protocol.AuditHit
		user int64
		ok   bool
	}
	cands := make([]prepared, 0, len(o.Hits))
	for _, h := range o.Hits {
		if h.Hour < lo || h.Hour > hi {
			outOfRange++
			continue
		}
		p := prepared{hit: h, ok: true}
		if h.Subject != "" {
			id, err := h.Subject.RowID()
			if err != nil || id <= 0 {
				unknown++
				continue
			}
			p.user = id
			if !idSeen[id] && len(ids) < userIDLookupMaxIDs {
				idSeen[id] = true
				ids = append(ids, id)
			}
		}
		cands = append(cands, p)
	}
	var existing map[int64]bool
	if len(ids) > 0 {
		var err error
		if existing, err = s.store.ExistingUserIDs(ctx, ids); err != nil {
			// Nothing was reserved yet, so there is no budget to give back.
			s.failBatch(q, domain.DestDropIngest)
			log.Warn("destination hit batch not stored: account lookup failed", "agent_id", q.agentID, "rows", len(o.Hits), "err", err)
			return
		}
	}
	mapped := make([]domain.DestHit, 0, len(cands))
	for _, c := range cands {
		if c.user != 0 && !existing[c.user] {
			unknown++
			continue
		}
		h := c.hit
		mapped = append(mapped, domain.DestHit{
			HourMS: h.Hour, PanelID: q.panelID, UserID: c.user, Source: sourceOf(h.RuleID), Action: h.Action,
			Dest: h.Dest, Port: int(h.Port), Count: int64(h.Count), FirstMS: h.FirstMS, LastMS: h.LastMS,
		})
	}
	merged := domain.MergeDestHits(mapped)

	// The agent's hourly budget, in stored rows. Counted per wall-clock hour of
	// ingestion: what it protects is the write rate, not the data's own hour.
	s.mu.Lock()
	b := s.budgets[q.agentID]
	if b == nil || b.hour != nowHour {
		b = &budget{hour: nowHour}
		s.budgets[q.agentID] = b
	}
	room := max(s.budget-b.used, 0)
	if len(merged) > room {
		for _, r := range merged[room:] {
			lose(r.HourMS, domain.DestDropOverBudget)
		}
		overBudget = len(merged) - room
		merged = merged[:room]
	}
	b.used += len(merged)
	*reserved = len(merged)
	if len(s.budgets) > 4096 {
		for k, v := range s.budgets {
			if v.hour != nowHour {
				delete(s.budgets, k)
			}
		}
	}
	s.mu.Unlock()

	res, err := s.store.StoreBatch(ctx, q.agentID, o.BatchID, q.receivedAt, merged)
	if res.Duplicate {
		// A batch the store already holds (the process restarted since it was
		// first sent, so the in-memory id was gone). Nothing is counted again.
		s.refund(q.agentID, len(merged))
		*reserved = 0
		s.report(reportDuplicate)
		return
	}
	s.rows(metricsStored, res.Stored)
	s.rows(metricsOutOfRange, outOfRange)
	s.rows(metricsUnknown, unknown)
	s.rows(metricsOverBudget, overBudget)
	s.rows(metricsUsageOff, len(o.Usage))
	if err != nil {
		s.report(reportIngestError)
		log.Warn("destination hits not (fully) stored", "agent_id", q.agentID, "rows", len(merged)-res.Stored, "err", redactErr(err, merged))
		// What was not stored, by hour, for the range question.
		for _, r := range merged[res.Stored:] {
			lose(r.HourMS, domain.DestDropIngest)
		}
	}
	// What the node itself lost, attributed to the hour it was noticed in: the
	// node's counters carry no hour. A lost BLOCK hit is a gap in the record;
	// trial overflow is recorded apart (it is expected on a busy node).
	if o.DroppedHits > 0 {
		lostRows[lostKey{nowHour, q.panelID, domain.DestDropNodeHits}] += int64(o.DroppedHits)
	}
	if o.DroppedTrial > 0 {
		lostRows[lostKey{nowHour, q.panelID, domain.DestDropNodeTrial}] += int64(o.DroppedTrial)
	}
	if s.metrics.NodeDropped != nil && o.DroppedHits+o.DroppedTrial+o.DroppedUsage > 0 {
		s.metrics.NodeDropped(o.DroppedHits + o.DroppedTrial + o.DroppedUsage)
	}
	if s.metrics.NodeUnmatched != nil && o.Unmatched > 0 {
		s.metrics.NodeUnmatched(o.Unmatched)
	}
	s.mu.Lock()
	for k, n := range lostRows {
		s.lost[k] += n
	}
	s.mu.Unlock()
}

// refund gives back the budget a duplicate batch reserved.
func (s *Service) refund(agentID string, n int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if b := s.budgets[agentID]; b != nil {
		b.used = max(b.used-n, 0)
	}
}

// redactErr is an error's text for a log line, withheld when it names any
// destination of the batch: a database error may quote the key it refused, and
// a destination must not reach a log.
func redactErr(err error, rows []domain.DestHit) string {
	msg := err.Error()
	for _, r := range rows {
		if r.Dest != "" && strings.Contains(msg, r.Dest) {
			return fmt.Sprintf("(error text withheld: it names a destination; %T)", err)
		}
	}
	return msg
}
