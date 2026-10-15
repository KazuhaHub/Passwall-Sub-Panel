package sqlstore

import (
	"context"
	"errors"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"gorm.io/gorm"
)

type fleetReadResult struct {
	rows []domain.PSPClientAccounting
	err  error
}

func startFleetRead(r *fleetAccountingReads, ctx context.Context, load func(context.Context) ([]domain.PSPClientAccounting, error)) <-chan fleetReadResult {
	result := make(chan fleetReadResult, 1)
	go func() {
		rows, err := r.read(ctx, load)
		result <- fleetReadResult{rows, err}
	}()
	return result
}

func awaitFleetRead(t *testing.T, result <-chan fleetReadResult) fleetReadResult {
	t.Helper()
	select {
	case got := <-result:
		return got
	case <-time.After(5 * time.Second):
		t.Fatal("fleet read did not finish")
		return fleetReadResult{}
	}
}

func awaitFleetPending(t *testing.T, r *fleetAccountingReads, count int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		r.mu.Lock()
		got := 0
		if r.pending != nil {
			got = len(r.pending.contexts)
		}
		r.mu.Unlock()
		if got == count {
			return
		}
		runtime.Gosched()
	}
	t.Fatal("callers did not join pending batch")
}

func TestFleetReadsBatchOnlyBeforeQueryStartsAndNeverCache(t *testing.T) {
	var reads fleetAccountingReads
	var calls, current atomic.Int64
	current.Store(1)
	started := make(chan struct{})
	release := make(chan struct{})
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})
	load := func(context.Context) ([]domain.PSPClientAccounting, error) {
		value := current.Load()
		if calls.Add(1) == 1 {
			close(started)
			<-release
		}
		return []domain.PSPClientAccounting{{ID: value}}, nil
	}
	first := startFleetRead(&reads, t.Context(), load)
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("first query did not start")
	}
	// A write after the first query's snapshot must be visible to every call
	// that starts afterwards; ordinary in-flight singleflight would fail here.
	current.Store(2)
	const count = 65
	results := make([]<-chan fleetReadResult, count)
	for i := range results {
		results[i] = startFleetRead(&reads, t.Context(), load)
	}
	awaitFleetPending(t, &reads, count)
	close(release)
	if got := awaitFleetRead(t, first); got.err != nil || len(got.rows) != 1 || got.rows[0].ID != 1 {
		t.Fatalf("first read lost its snapshot: %+v", got)
	}
	for _, result := range results {
		got := awaitFleetRead(t, result)
		if got.err != nil || len(got.rows) != 1 || got.rows[0].ID != 2 {
			t.Fatalf("later caller reused an earlier query: %+v", got)
		}
		got.rows[0].ID = -1
	}
	if calls.Load() != 2 {
		t.Fatalf("65 queued callers issued %d queries instead of one following query", calls.Load()-1)
	}
	current.Store(3)
	rows, err := reads.read(t.Context(), load)
	if err != nil || len(rows) != 1 || rows[0].ID != 3 || calls.Load() != 3 {
		t.Fatal("sequential call reused a completed result")
	}
}

func TestFleetReadCancellationDoesNotCancelOtherCallers(t *testing.T) {
	var reads fleetAccountingReads
	started := make(chan struct{})
	release := make(chan struct{})
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})
	var calls atomic.Int64
	load := func(ctx context.Context) ([]domain.PSPClientAccounting, error) {
		if calls.Add(1) == 1 {
			close(started)
			select {
			case <-release:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		return []domain.PSPClientAccounting{{ID: 7}}, nil
	}
	first := startFleetRead(&reads, t.Context(), load)
	<-started
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	canceled := startFleetRead(&reads, ctx, load)
	live := startFleetRead(&reads, t.Context(), load)
	awaitFleetPending(t, &reads, 2)
	cancel()
	if got := awaitFleetRead(t, canceled); !errors.Is(got.err, context.Canceled) || got.rows != nil {
		t.Fatal("canceled queued caller did not leave promptly")
	}
	close(release)
	if got := awaitFleetRead(t, first); got.err != nil {
		t.Fatal(got.err)
	}
	if got := awaitFleetRead(t, live); got.err != nil || len(got.rows) != 1 || got.rows[0].ID != 7 || calls.Load() != 2 {
		t.Fatal("one canceled caller canceled a surviving batch")
	}
}

func TestFleetReadAllCanceledStopsQueryAndDoesNotPoisonNextRead(t *testing.T) {
	var reads fleetAccountingReads
	started, stopped := make(chan struct{}), make(chan struct{})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	first := startFleetRead(&reads, ctx, func(ctx context.Context) ([]domain.PSPClientAccounting, error) {
		close(started)
		<-ctx.Done()
		close(stopped)
		return nil, ctx.Err()
	})
	<-started
	cancel()
	if got := awaitFleetRead(t, first); !errors.Is(got.err, context.Canceled) {
		t.Fatal("canceled caller did not return cancellation")
	}
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("query outlived all callers")
	}
	// Wait for the worker to retire before supplying a different test loader.
	deadline := time.Now().Add(5 * time.Second)
	for {
		reads.mu.Lock()
		idle := !reads.running
		reads.mu.Unlock()
		if idle {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("canceled worker did not retire")
		}
		runtime.Gosched()
	}
	rows, err := reads.read(t.Context(), func(context.Context) ([]domain.PSPClientAccounting, error) {
		return []domain.PSPClientAccounting{{ID: 9}}, nil
	})
	if err != nil || len(rows) != 1 || rows[0].ID != 9 {
		t.Fatal("canceled query poisoned next current read")
	}
}

func TestFleetBatchCancellationAndErrorsPreserveIndependentCallers(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	sentinel := errors.New("database read failed")
	rows, err := readFleetAccountingBatch([]context.Context{ctx, t.Context()}, func(query context.Context) ([]domain.PSPClientAccounting, error) {
		cancel()
		if query.Err() != nil {
			t.Fatal("one caller canceled shared query")
		}
		return nil, sentinel
	})
	if rows != nil || !errors.Is(err, sentinel) {
		t.Fatal("database error was swallowed")
	}
	var reads fleetAccountingReads
	if rows, err := reads.read(ctx, func(context.Context) ([]domain.PSPClientAccounting, error) {
		t.Fatal("already canceled call issued a query")
		return nil, nil
	}); rows != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("early cancellation lost")
	}
}

func TestFleetReadsCoalesceActualCurrentSQLAndReturnOwnedRows(t *testing.T) {
	r, _ := inboundBatchFixture(t, 0)
	var clients []pspClientRow
	for id := int64(1); id <= 601; id++ {
		clients = append(clients, pspClientRow{ID: id, UserID: id % 200, PanelID: id % 65, LastRawUpBytes: id})
	}
	if err := r.db.CreateInBatches(clients, 100).Error; err != nil {
		t.Fatal(err)
	}
	started, release := make(chan struct{}), make(chan struct{})
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})
	var queries atomic.Int64
	const hook = "fleet-coalesced-current-read"
	if err := r.db.Callback().Row().Before("gorm:row").Register(hook, func(tx *gorm.DB) {
		if tx.Statement.Table == "psp_clients" && queries.Add(1) == 1 {
			close(started)
			<-release
		}
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.db.Callback().Row().Remove(hook) })
	start := func() <-chan fleetReadResult {
		result := make(chan fleetReadResult, 1)
		go func() { rows, err := r.ListFleetAccounting(t.Context()); result <- fleetReadResult{rows, err} }()
		return result
	}
	first := start()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("SQL read did not start")
	}
	if err := r.db.Model(&pspClientRow{}).Where("id = ?", 1).Update("last_raw_up_bytes", 999).Error; err != nil {
		t.Fatal(err)
	}
	results := make([]<-chan fleetReadResult, 65)
	for i := range results {
		results[i] = start()
	}
	awaitFleetPending(t, &r.fleetReads, len(results))
	close(release)
	results = append(results, first)
	for _, result := range results {
		got := awaitFleetRead(t, result)
		if got.err != nil || len(got.rows) != len(clients) {
			t.Fatalf("incomplete current fleet: %d, %v", len(got.rows), got.err)
		}
		found := false
		for _, row := range got.rows {
			if row.ID == 1 {
				found = row.LastRawUpBytes == 999
			}
		}
		if !found {
			t.Fatal("coalesced query lost completed baseline write")
		}
		for i := range got.rows {
			got.rows[i] = domain.PSPClientAccounting{}
		}
	}
	if queries.Load() != 2 {
		t.Fatalf("expected two current SQL queries, got %d", queries.Load())
	}
}

func TestFleetBatchAllCallerCancellationReachesSQLContext(t *testing.T) {
	first, cancelFirst := context.WithCancel(t.Context())
	defer cancelFirst()
	second, cancelSecond := context.WithCancel(t.Context())
	defer cancelSecond()
	_, err := readFleetAccountingBatch([]context.Context{first, second}, func(query context.Context) ([]domain.PSPClientAccounting, error) {
		cancelFirst()
		cancelSecond()
		select {
		case <-query.Done():
			return nil, query.Err()
		case <-time.After(5 * time.Second):
			t.Fatal("SQL context outlived all batch callers")
			return nil, nil
		}
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatal("all-caller cancellation did not reach SQL")
	}
}

func TestFleetReadErrorsFanOutWithoutPartialRowsOrCaching(t *testing.T) {
	var reads fleetAccountingReads
	var calls atomic.Int64
	started, release := make(chan struct{}), make(chan struct{})
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})
	sentinel := errors.New("current SQL cursor failed")
	load := func(context.Context) ([]domain.PSPClientAccounting, error) {
		switch calls.Add(1) {
		case 1:
			close(started)
			<-release
		case 2:
			return []domain.PSPClientAccounting{{ID: 999}}, sentinel
		}
		return []domain.PSPClientAccounting{{ID: 3}}, nil
	}
	first := startFleetRead(&reads, t.Context(), load)
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("first query did not start")
	}
	second := startFleetRead(&reads, t.Context(), load)
	third := startFleetRead(&reads, t.Context(), load)
	awaitFleetPending(t, &reads, 2)
	close(release)
	if got := awaitFleetRead(t, first); got.err != nil {
		t.Fatal(got.err)
	}
	for _, result := range []<-chan fleetReadResult{second, third} {
		if got := awaitFleetRead(t, result); got.rows != nil || !errors.Is(got.err, sentinel) {
			t.Fatal("batch lost error or leaked partial rows")
		}
	}
	rows, err := reads.read(t.Context(), load)
	if err != nil || len(rows) != 1 || rows[0].ID != 3 || calls.Load() != 3 {
		t.Fatal("failed batch poisoned current read")
	}
}
