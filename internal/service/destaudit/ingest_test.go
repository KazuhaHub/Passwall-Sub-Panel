package destaudit

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/KazuhaHub/passwall-protocol/protocol"

	"github.com/KazuhaHub/passwall-sub-panel/internal/adapters/sqlstore"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/metrics"
)

// The ingest worker takes the audit subtree a node reported (already validated
// by the sync handler), maps it to stored rows and writes it. What these tests
// hold it to: a row is mapped syntactically (subject to user, rule to source),
// rows that collide after mapping are merged, anything the worker cannot or may
// not store is counted and never silently lost, a duplicate batch counts once,
// and nothing a destination or an account could be recovered from reaches a
// log line.

const hourMS = int64(3600000)

type counters struct {
	mu            sync.Mutex
	reports       map[string]int
	rows          map[string]int
	nodeDropped   uint64
	nodeUnmatched uint64
}

func (c *counters) metrics() Metrics {
	c.reports, c.rows = map[string]int{}, map[string]int{}
	return Metrics{
		Report:        func(o string) { c.mu.Lock(); c.reports[o]++; c.mu.Unlock() },
		Rows:          func(o string, n int) { c.mu.Lock(); c.rows[o] += n; c.mu.Unlock() },
		NodeDropped:   func(n uint64) { c.mu.Lock(); c.nodeDropped += n; c.mu.Unlock() },
		NodeUnmatched: func(n uint64) { c.mu.Lock(); c.nodeUnmatched += n; c.mu.Unlock() },
	}
}

type fixture struct {
	t     *testing.T
	db    *gorm.DB
	store *sqlstore.DestHitRepo
	svc   *Service
	ctr   *counters
	now   time.Time
	users []int64
}

// failStore wraps the real store to inject a failure.
type failStore struct {
	Store
	failStore bool
	err       error
	// failLookup and panicStore make ExistingUserIDs fail and StoreBatch panic.
	failLookup bool
	panicStore bool
}

func (f *failStore) ExistingUserIDs(ctx context.Context, ids []int64) (map[int64]bool, error) {
	if f.failLookup {
		return nil, f.err
	}
	return f.Store.ExistingUserIDs(ctx, ids)
}

func (f *failStore) StoreBatch(ctx context.Context, agent, batch string, at time.Time, rows []domain.DestHit) (domain.DestBatchResult, error) {
	if f.panicStore {
		panic("nil dereference in the store")
	}
	if f.failStore {
		return domain.DestBatchResult{}, f.err
	}
	return f.Store.StoreBatch(ctx, agent, batch, at, rows)
}

func newFixture(t *testing.T, mut func(*Deps)) *fixture {
	t.Helper()
	db, err := sqlstore.OpenQuiet("sqlite", filepath.Join(t.TempDir(), "audit.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := sqlstore.EnsureSchema(db); err != nil {
		t.Fatal(err)
	}
	repos := sqlstore.NewRepos(db)
	f := &fixture{t: t, db: db, store: sqlstore.NewDestRepos(db).Hits, ctr: &counters{}, now: time.Date(2026, 11, 13, 10, 30, 0, 0, time.UTC)}
	for _, upn := range []string{"alice", "bob"} {
		u := &domain.User{UPN: upn, SubToken: "t-" + upn, UUID: "u-" + upn, Role: domain.RoleUser, Enabled: true}
		if err := repos.User.Create(context.Background(), u); err != nil {
			t.Fatal(err)
		}
		f.users = append(f.users, u.ID)
	}
	deps := Deps{Store: f.store, Now: func() time.Time { return f.now }, Metrics: f.ctr.metrics()}
	if mut != nil {
		mut(&deps)
	}
	f.svc = New(deps)
	return f
}

func (f *fixture) hour() int64 { return f.now.Truncate(time.Hour).UnixMilli() }

func (f *fixture) subject(i int) protocol.SubjectKey { return protocol.NewSubjectKey(f.users[i]) }

func hitRow(hour int64, rule, action string, subject protocol.SubjectKey, dest string, port uint16, count uint32) protocol.AuditHit {
	return protocol.AuditHit{Hour: hour, RuleID: rule, Action: action, Subject: subject, Dest: dest, Port: port, Count: count, FirstMS: hour + 1000, LastMS: hour + 2000}
}

func obs(id string, hits ...protocol.AuditHit) *protocol.AuditObservation {
	return &protocol.AuditObservation{BatchID: id, Hits: hits}
}

func batchID(n int) string { return fmt.Sprintf("%032x", n) }

func (f *fixture) enqueueAndDrain(agent string, panel int64, o *protocol.AuditObservation) {
	f.t.Helper()
	f.svc.Enqueue(agent, panel, o)
	f.svc.Drain(context.Background())
}

func (f *fixture) stored() []domain.DestHitRow {
	f.t.Helper()
	rows, _, err := f.store.List(context.Background(), domain.DestHitFilter{PageSize: 200, IncludeTrial: true})
	if err != nil {
		f.t.Fatal(err)
	}
	return rows
}

func TestIngest_MapsSubjectsAndRulesAndMergesWhatCollides(t *testing.T) {
	f := newFixture(t, nil)
	h := f.hour()
	f.enqueueAndDrain("agt_1", 5, obs(batchID(1),
		// p12x1 and p12x2 are one policy: they collide on the stored key.
		hitRow(h, "p12x1", "block", f.subject(0), "bad.example", 443, 2),
		hitRow(h, "p12x2", "block", f.subject(0), "bad.example", 443, 3),
		hitRow(h, "p13", "observe", f.subject(1), "watched.example", 80, 4),
		hitRow(h, "s4", "observe", f.subject(1), "src.example", 443, 1),
		// A trial row: the node already folded it, so it has no subject and no port.
		protocol.AuditHit{Hour: h, RuleID: "g3", Action: "observe", Dest: "trial.example", Count: 9, FirstMS: h + 1, LastMS: h + 2},
	))
	rows := f.stored()
	by := map[string]domain.DestHitRow{}
	for _, r := range rows {
		by[r.Source+"/"+r.Action] = r
	}
	if len(rows) != 4 {
		t.Fatalf("stored %d rows: %+v", len(rows), rows)
	}
	if r := by["p12/block"]; r.Count != 5 || r.UserID != f.users[0] || r.PanelID != 5 || r.Dest != "bad.example" || r.Port != 443 {
		t.Fatalf("p12 = %+v (p12x1 and p12x2 are one policy)", r)
	}
	if r := by["p13/observe"]; r.Count != 4 || r.Action != "observe" {
		t.Fatalf("a report-only hit must be stored as observe: %+v", r)
	}
	if r := by["s4/observe"]; r.Count != 1 || r.UserID != f.users[1] {
		t.Fatalf("source rule hit = %+v", r)
	}
	if r := by["g3/observe"]; r.UserID != 0 || r.Port != 0 || r.Dest != "trial.example" {
		t.Fatalf("trial row = %+v, want user 0 and port 0", r)
	}
	if f.ctr.rows[metricsStored] != 4 || f.ctr.reports["accepted"] != 1 {
		t.Fatalf("counters = %+v %+v", f.ctr.rows, f.ctr.reports)
	}
}

func TestIngest_HoursOutsideTheWindowAreDroppedAndCounted(t *testing.T) {
	f := newFixture(t, nil)
	h := f.hour()
	f.enqueueAndDrain("agt_1", 5, obs(batchID(1),
		hitRow(h-47*hourMS, "p1", "block", f.subject(0), "ok.example", 1, 1),    // inside
		hitRow(h-49*hourMS, "p1", "block", f.subject(0), "old.example", 1, 1),   // too old
		hitRow(h+hourMS, "p1", "block", f.subject(0), "next.example", 1, 1),     // the next hour is inside
		hitRow(h+2*hourMS, "p1", "block", f.subject(0), "future.example", 1, 1), // too far ahead
	))
	if rows := f.stored(); len(rows) != 2 {
		t.Fatalf("stored %d rows, want 2: %+v", len(rows), rows)
	}
	if f.ctr.rows[metricsOutOfRange] != 2 {
		t.Fatalf("out_of_range = %d", f.ctr.rows[metricsOutOfRange])
	}
}

func TestIngest_AnAccountThatNoLongerExistsIsNotStored(t *testing.T) {
	f := newFixture(t, nil)
	h := f.hour()
	f.enqueueAndDrain("agt_1", 5, obs(batchID(1),
		hitRow(h, "p1", "block", f.subject(0), "a.example", 1, 1),
		hitRow(h, "p1", "block", protocol.NewSubjectKey(f.users[1]+500), "b.example", 1, 1),
	))
	rows := f.stored()
	if len(rows) != 1 || rows[0].Dest != "a.example" {
		t.Fatalf("stored = %+v", rows)
	}
	if f.ctr.rows[metricsUnknown] != 1 {
		t.Fatalf("unknown_subject = %d", f.ctr.rows[metricsUnknown])
	}
}

func TestIngest_UsageRowsAreNotCollectedAndAreCounted(t *testing.T) {
	f := newFixture(t, nil)
	h := f.hour()
	o := obs(batchID(1), hitRow(h, "p1", "block", f.subject(0), "a.example", 1, 1))
	o.Usage = []protocol.AuditUsage{{Hour: h, Subject: f.subject(0), Site: "a.example", Count: 3}, {Hour: h, Subject: f.subject(1), Site: "(ip)", Count: 1}}
	f.enqueueAndDrain("agt_1", 5, o)
	if f.ctr.rows[metricsUsageOff] != 2 {
		t.Fatalf("usage_collect_off = %d, want 2", f.ctr.rows[metricsUsageOff])
	}
	if rows := f.stored(); len(rows) != 1 {
		t.Fatalf("stored = %+v", rows)
	}
}

func TestIngest_ADuplicateBatchCountsOnce(t *testing.T) {
	f := newFixture(t, nil)
	h := f.hour()
	o := obs(batchID(1), hitRow(h, "p1", "block", f.subject(0), "a.example", 1, 7))
	f.enqueueAndDrain("agt_1", 5, o)
	// The node re-sends the same batch (it never saw the 200).
	f.enqueueAndDrain("agt_1", 5, o)
	rows := f.stored()
	if len(rows) != 1 || rows[0].Count != 7 {
		t.Fatalf("rows = %+v, want one row counted once", rows)
	}
	if f.ctr.reports["duplicate_batch"] != 1 || f.ctr.reports["accepted"] != 1 {
		t.Fatalf("reports = %+v", f.ctr.reports)
	}
}

// After a restart the in-memory memory of batch ids is gone: the marker row is
// what stops a re-sent batch from counting twice.
func TestIngest_ADuplicateAfterARestartIsCaughtByTheStore(t *testing.T) {
	f := newFixture(t, nil)
	h := f.hour()
	o := obs(batchID(1), hitRow(h, "p1", "block", f.subject(0), "a.example", 1, 7))
	f.enqueueAndDrain("agt_1", 5, o)

	restarted := New(Deps{Store: f.store, Now: func() time.Time { return f.now }, Metrics: f.ctr.metrics()})
	restarted.Enqueue("agt_1", 5, o)
	restarted.Drain(context.Background())
	if rows := f.stored(); len(rows) != 1 || rows[0].Count != 7 {
		t.Fatalf("rows = %+v", rows)
	}
	if f.ctr.reports["duplicate_batch"] != 1 {
		t.Fatalf("reports = %+v", f.ctr.reports)
	}
	// The node's own loss counters are not counted a second time either.
	o2 := obs(batchID(2), hitRow(f.hour(), "p1", "block", f.subject(0), "a.example", 1, 1))
	o2.DroppedHits, o2.Unmatched = 5, 6
	f.enqueueAndDrain("agt_1", 5, o2)
	restarted2 := New(Deps{Store: f.store, Now: func() time.Time { return f.now }, Metrics: f.ctr.metrics()})
	restarted2.Enqueue("agt_1", 5, o2)
	restarted2.Drain(context.Background())
	if f.ctr.nodeDropped != 5 || f.ctr.nodeUnmatched != 6 {
		t.Fatalf("node counters = %d %d, want 5 6 once", f.ctr.nodeDropped, f.ctr.nodeUnmatched)
	}
}

func TestIngest_TheQueueIsBoundedPerAgentAndInAll(t *testing.T) {
	f := newFixture(t, nil)
	h := f.hour()
	mk := func(i int) *protocol.AuditObservation {
		o := obs(batchID(i), hitRow(h, "p1", "block", f.subject(0), fmt.Sprintf("h%d.example", i), 1, 1))
		return o
	}
	// No worker drains: 4 fit for one agent, the fifth is lost.
	for i := 1; i <= 5; i++ {
		f.svc.Enqueue("agt_1", 5, mk(i))
	}
	if f.ctr.reports["accepted"] != 4 || f.ctr.reports["queue_full"] != 1 {
		t.Fatalf("per-agent bound: %+v", f.ctr.reports)
	}
	// 256 in all: 64 agents of 4 fill it, the next agent is refused.
	for a := 0; a < 63; a++ {
		for i := 0; i < 4; i++ {
			f.svc.Enqueue(fmt.Sprintf("agt_x%d", a), int64(100+a), mk(1000+a*10+i))
		}
	}
	f.svc.Enqueue("agt_last", 999, mk(9999))
	if f.ctr.reports["queue_full"] != 2 {
		t.Fatalf("global bound: %+v", f.ctr.reports)
	}
	// What was lost is in the database as a count, per hour, for the range question.
	f.svc.Drain(context.Background())
	got, err := f.store.DroppedInRange(context.Background(), h, h+hourMS)
	if err != nil || got != 2 {
		t.Fatalf("dropped in range = %d, %v; want the two lost rows", got, err)
	}
}

func TestIngest_AnAgentsHourlyBudgetIsEnforced(t *testing.T) {
	f := newFixture(t, func(d *Deps) { d.HourlyRowBudget = 3 })
	h := f.hour()
	rows := []protocol.AuditHit{}
	for i := 0; i < 5; i++ {
		rows = append(rows, hitRow(h, "p1", "block", f.subject(0), fmt.Sprintf("h%d.example", i), 1, 1))
	}
	f.enqueueAndDrain("agt_1", 5, obs(batchID(1), rows...))
	if got := len(f.stored()); got != 3 {
		t.Fatalf("stored %d, want the budget of 3", got)
	}
	if f.ctr.rows[metricsOverBudget] != 2 {
		t.Fatalf("over_budget = %d", f.ctr.rows[metricsOverBudget])
	}
	// The budget is per agent: another node still has its own.
	f.enqueueAndDrain("agt_2", 6, obs(batchID(2), hitRow(h, "p1", "block", f.subject(0), "z.example", 1, 1)))
	if got := len(f.stored()); got != 4 {
		t.Fatalf("stored %d after another agent's batch", got)
	}
	// And the next hour starts a fresh one.
	f.now = f.now.Add(time.Hour)
	f.enqueueAndDrain("agt_1", 5, obs(batchID(3), hitRow(f.hour(), "p1", "block", f.subject(0), "n.example", 1, 1)))
	if got := len(f.stored()); got != 5 {
		t.Fatalf("stored %d in the next hour", got)
	}
	if dropped, _ := f.store.DroppedInRange(context.Background(), h, h+hourMS); dropped != 2 {
		t.Fatalf("over-budget rows are recorded for the range question: %d", dropped)
	}
}

func TestIngest_TheDefaultBudgetIsTwentyThousandRows(t *testing.T) {
	if defaultHourlyRowBudget != 20000 {
		t.Fatalf("budget = %d", defaultHourlyRowBudget)
	}
}

func TestIngest_NodeLossCountersAreStoredAndCounted(t *testing.T) {
	f := newFixture(t, nil)
	h := f.hour()
	o := obs(batchID(1), hitRow(h, "p1", "block", f.subject(0), "a.example", 1, 1))
	o.DroppedHits, o.DroppedTrial, o.DroppedUsage, o.Unmatched = 5, 1000, 7, 3
	f.enqueueAndDrain("agt_1", 5, o)
	if f.ctr.nodeDropped != 5+1000+7 || f.ctr.nodeUnmatched != 3 {
		t.Fatalf("node counters = %d %d", f.ctr.nodeDropped, f.ctr.nodeUnmatched)
	}
	// A lost BLOCK hit is a gap in the record; trial overflow is not.
	if got, _ := f.store.DroppedInRange(context.Background(), h, h+hourMS); got != 5 {
		t.Fatalf("dropped in range = %d, want 5 (trial and usage overflow are not gaps)", got)
	}
}

// A batch of only counters (every row dropped on the node) still reaches the
// counters.
func TestIngest_ABatchWithoutHitsStillCarriesItsCounters(t *testing.T) {
	f := newFixture(t, nil)
	o := &protocol.AuditObservation{BatchID: batchID(1), Usage: []protocol.AuditUsage{{Hour: f.hour(), Subject: f.subject(0), Site: "a.example", Count: 1}}, DroppedHits: 9}
	f.enqueueAndDrain("agt_1", 5, o)
	if f.ctr.nodeDropped != 9 || f.ctr.rows[metricsUsageOff] != 1 {
		t.Fatalf("counters = %d %+v", f.ctr.nodeDropped, f.ctr.rows)
	}
}

func TestIngest_AFailedWriteIsCountedAndDoesNotStopTheWorker(t *testing.T) {
	boom := errors.New("disk on fire")
	fs := &failStore{err: boom}
	f := newFixture(t, func(d *Deps) { fs.Store = d.Store; d.Store = fs })
	h := f.hour()
	fs.failStore = true
	f.enqueueAndDrain("agt_1", 5, obs(batchID(1),
		hitRow(h, "p1", "block", f.subject(0), "a.example", 1, 1), hitRow(h, "p1", "block", f.subject(0), "b.example", 1, 1)))
	if f.ctr.reports["ingest_error"] != 1 {
		t.Fatalf("reports = %+v", f.ctr.reports)
	}
	fs.failStore = false
	f.enqueueAndDrain("agt_1", 5, obs(batchID(2), hitRow(h, "p1", "block", f.subject(0), "c.example", 1, 1)))
	if rows := f.stored(); len(rows) != 1 || rows[0].Dest != "c.example" {
		t.Fatalf("the worker did not carry on: %+v", rows)
	}
	if got, _ := f.store.DroppedInRange(context.Background(), h, h+hourMS); got != 2 {
		t.Fatalf("the unstored rows are recorded as dropped: %d", got)
	}
}

// A panic while a batch is processed costs that batch, never the worker: it is
// counted as an ingest error, its in-range rows are recorded as dropped, the
// budget it reserved is given back, and the next batch is stored.
func TestIngest_APanicInABatchIsRecoveredAndTheWorkerCarriesOn(t *testing.T) {
	fs := &failStore{err: errors.New("unused")}
	f := newFixture(t, func(d *Deps) { fs.Store = d.Store; d.Store = fs; d.HourlyRowBudget = 2 })
	h := f.hour()
	fs.panicStore = true
	f.enqueueAndDrain("agt_1", 5, obs(batchID(1),
		hitRow(h, "p1", "block", f.subject(0), "a.example", 1, 1), hitRow(h, "p1", "block", f.subject(0), "b.example", 1, 1)))
	if f.ctr.reports["ingest_error"] != 1 {
		t.Fatalf("reports = %+v", f.ctr.reports)
	}
	if got, _ := f.store.DroppedInRange(context.Background(), h, h+hourMS); got != 2 {
		t.Fatalf("the panicked batch's rows are recorded as dropped: %d", got)
	}
	// The budget (2 rows) was refunded, so a batch of two still fits.
	fs.panicStore = false
	f.enqueueAndDrain("agt_1", 5, obs(batchID(2),
		hitRow(h, "p1", "block", f.subject(0), "c.example", 1, 1), hitRow(h, "p1", "block", f.subject(0), "d.example", 1, 1)))
	if rows := f.stored(); len(rows) != 2 {
		t.Fatalf("the worker did not carry on or the budget was not refunded: %+v", rows)
	}
}

// An account lookup that fails loses the batch like a failed write does: counted,
// recorded as dropped, and the budget untouched.
func TestIngest_AFailedAccountLookupIsRecordedAsDropped(t *testing.T) {
	fs := &failStore{err: errors.New("lookup failed")}
	f := newFixture(t, func(d *Deps) { fs.Store = d.Store; d.Store = fs; d.HourlyRowBudget = 2 })
	h := f.hour()
	fs.failLookup = true
	f.enqueueAndDrain("agt_1", 5, obs(batchID(1),
		hitRow(h, "p1", "block", f.subject(0), "a.example", 1, 1), hitRow(h, "p1", "block", f.subject(1), "b.example", 1, 1)))
	if f.ctr.reports["ingest_error"] != 1 {
		t.Fatalf("reports = %+v", f.ctr.reports)
	}
	if got, _ := f.store.DroppedInRange(context.Background(), h, h+hourMS); got != 2 {
		t.Fatalf("the lost rows are recorded as dropped: %d", got)
	}
	fs.failLookup = false
	f.enqueueAndDrain("agt_1", 5, obs(batchID(2),
		hitRow(h, "p1", "block", f.subject(0), "c.example", 1, 1), hitRow(h, "p1", "block", f.subject(1), "d.example", 1, 1)))
	if rows := f.stored(); len(rows) != 2 {
		t.Fatalf("budget was consumed by the failed batch: %+v", rows)
	}
}

// Run drains what is queued when it is told to stop, so a shutdown does not
// throw away the last batches.
func TestRun_DrainsTheQueueOnShutdown(t *testing.T) {
	f := newFixture(t, nil)
	h := f.hour()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { f.svc.Run(ctx); close(done) }()
	for i := 1; i <= 3; i++ {
		f.svc.Enqueue("agt_1", 5, obs(batchID(i), hitRow(h, "p1", "block", f.subject(0), fmt.Sprintf("h%d.example", i), 1, 1)))
	}
	cancel()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not return after cancel")
	}
	if got := len(f.stored()); got != 3 {
		t.Fatalf("stored %d after shutdown, want 3: the queue must be drained", got)
	}
}

// The worker's logs never carry a destination or an account, even when the
// store's error text does.
func TestRedactErr_WithholdsAnyErrorThatNamesADestination(t *testing.T) {
	rows := []domain.DestHit{{Dest: "secret.example"}, {Dest: "other.example"}}
	if got := redactErr(errors.New(`insert failed: Key (dest)=(secret.example) already exists`), rows); strings.Contains(got, "secret.example") {
		t.Fatalf("leaked: %q", got)
	}
	if got := redactErr(errors.New("database is locked"), rows); got != "database is locked" {
		t.Fatalf("an ordinary error was withheld: %q", got)
	}
}

// The service's outcome strings are the metric's label values; they are
// written twice (the metrics package cannot be imported by a service's logic)
// and held equal here.
func TestOutcomeStringsMatchTheMetricLabels(t *testing.T) {
	for got, want := range map[string]string{
		metricsStored: metrics.DestRowsHitsStored, metricsUnknown: metrics.DestRowsHitsUnknown,
		metricsOutOfRange: metrics.DestRowsHitsOutOfRange, metricsOverBudget: metrics.DestRowsHitsOverBudget,
		metricsUsageOff: metrics.DestRowsUsageCollOff,
		reportAccepted:  metrics.NodeAuditAccepted, reportDuplicate: metrics.NodeAuditDuplicate,
		reportQueueFull: metrics.NodeAuditQueueFull, reportIngestError: metrics.NodeAuditIngestError,
	} {
		if got != want {
			t.Errorf("%q != %q", got, want)
		}
	}
}
