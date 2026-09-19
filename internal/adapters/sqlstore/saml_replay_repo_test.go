package sqlstore

import (
	"context"
	"sync"
	"testing"
	"time"
)

func newSAMLReplayRepo(t *testing.T) *samlReplayRepo {
	t.Helper()
	db, err := openTestDB(t)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := ensureTestSchema(db); err != nil {
		t.Fatalf("schema: %v", err)
	}
	t.Cleanup(func() {
		if sqlDB, derr := db.DB(); derr == nil {
			_ = sqlDB.Close()
		}
	})
	return &samlReplayRepo{db: db}
}

// The core security property: a second submission of the same assertion, still
// inside its window, must be reported as already consumed.
func TestSAMLReplayRepo_SecondSubmissionIsReplay(t *testing.T) {
	r := newSAMLReplayRepo(t)
	ctx := context.Background()
	now := time.Now()
	exp := now.Add(5 * time.Minute)

	seen, err := r.SeenOrAdd(ctx, "assertion-1", exp, now)
	if err != nil {
		t.Fatalf("first SeenOrAdd: %v", err)
	}
	if seen {
		t.Fatal("first submission reported as replay")
	}

	seen, err = r.SeenOrAdd(ctx, "assertion-1", exp, now.Add(time.Second))
	if err != nil {
		t.Fatalf("second SeenOrAdd: %v", err)
	}
	if !seen {
		t.Fatal("REPLAY NOT DETECTED: the same assertion was accepted twice inside its window")
	}
}

// This is the window the in-memory cache cannot close: a fresh process (new repo
// instance over the same database) must still refuse an assertion the previous
// one consumed.
func TestSAMLReplayRepo_SurvivesProcessRestart(t *testing.T) {
	db, err := openTestDB(t)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := ensureTestSchema(db); err != nil {
		t.Fatalf("schema: %v", err)
	}
	t.Cleanup(func() {
		if sqlDB, derr := db.DB(); derr == nil {
			_ = sqlDB.Close()
		}
	})
	ctx := context.Background()
	now := time.Now()
	exp := now.Add(5 * time.Minute)

	before := &samlReplayRepo{db: db}
	if seen, err := before.SeenOrAdd(ctx, "assertion-restart", exp, now); err != nil || seen {
		t.Fatalf("first consume: seen=%v err=%v", seen, err)
	}

	// A different repo value standing in for a restarted process / second
	// instance: no shared in-memory state, only the table.
	after := &samlReplayRepo{db: db}
	seen, err := after.SeenOrAdd(ctx, "assertion-restart", exp, now.Add(time.Second))
	if err != nil {
		t.Fatalf("post-restart SeenOrAdd: %v", err)
	}
	if !seen {
		t.Fatal("REPLAY NOT DETECTED after restart: durable set did not carry the consumed ID")
	}
}

// A row whose window has closed is not a replay — the assertion would be
// rejected as expired anyway, and a recycled ID must not lock a user out.
func TestSAMLReplayRepo_ExpiredRowIsNotAReplay(t *testing.T) {
	r := newSAMLReplayRepo(t)
	ctx := context.Background()
	now := time.Now()

	if seen, err := r.SeenOrAdd(ctx, "assertion-old", now.Add(time.Minute), now); err != nil || seen {
		t.Fatalf("first consume: seen=%v err=%v", seen, err)
	}
	later := now.Add(10 * time.Minute) // past the stored expiry
	seen, err := r.SeenOrAdd(ctx, "assertion-old", later.Add(5*time.Minute), later)
	if err != nil {
		t.Fatalf("SeenOrAdd after expiry: %v", err)
	}
	if seen {
		t.Fatal("expired row reported as a replay")
	}
	// The window must have been taken over, so an immediate re-submit is again
	// a replay rather than being permanently accepted.
	if seen, err := r.SeenOrAdd(ctx, "assertion-old", later.Add(5*time.Minute), later.Add(time.Second)); err != nil || !seen {
		t.Fatalf("row was not refreshed: seen=%v err=%v", seen, err)
	}
}

// Concurrent submissions of one stolen assertion must yield exactly one
// "not seen" — this is why SeenOrAdd is an atomic insert-if-absent rather than
// a read-then-write.
func TestSAMLReplayRepo_ConcurrentSubmissionsAdmitExactlyOne(t *testing.T) {
	r := newSAMLReplayRepo(t)
	ctx := context.Background()
	now := time.Now()
	exp := now.Add(5 * time.Minute)

	const racers = 8
	var (
		wg       sync.WaitGroup
		mu       sync.Mutex
		accepted int
	)
	for i := 0; i < racers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			seen, err := r.SeenOrAdd(ctx, "assertion-race", exp, now)
			if err != nil {
				return // a busy-DB error is not an acceptance
			}
			if !seen {
				mu.Lock()
				accepted++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if accepted != 1 {
		t.Fatalf("accepted %d concurrent submissions of the same assertion, want exactly 1", accepted)
	}
}

func TestSAMLReplayRepo_DeleteExpiredOnlyRemovesClosedWindows(t *testing.T) {
	r := newSAMLReplayRepo(t)
	ctx := context.Background()
	now := time.Now()

	if _, err := r.SeenOrAdd(ctx, "expired", now.Add(-time.Minute), now.Add(-2*time.Minute)); err != nil {
		t.Fatalf("seed expired: %v", err)
	}
	if _, err := r.SeenOrAdd(ctx, "live", now.Add(10*time.Minute), now); err != nil {
		t.Fatalf("seed live: %v", err)
	}

	deleted, err := r.DeleteExpired(ctx, now)
	if err != nil {
		t.Fatalf("DeleteExpired: %v", err)
	}
	if deleted != 1 {
		t.Fatalf("deleted %d rows, want 1", deleted)
	}
	// The still-valid entry must survive the sweep, or the GC would itself
	// re-open the replay window it exists to keep closed.
	if seen, err := r.SeenOrAdd(ctx, "live", now.Add(10*time.Minute), now.Add(time.Second)); err != nil || !seen {
		t.Fatalf("live entry did not survive DeleteExpired: seen=%v err=%v", seen, err)
	}
}

// A blank ID is never recorded; the caller rejects those outright.
func TestSAMLReplayRepo_BlankIDIsIgnored(t *testing.T) {
	r := newSAMLReplayRepo(t)
	seen, err := r.SeenOrAdd(context.Background(), "", time.Now().Add(time.Minute), time.Now())
	if err != nil || seen {
		t.Fatalf("blank id: seen=%v err=%v", seen, err)
	}
}

// Takeover of a closed-window row must be conditional on the row STILL being
// closed. Both calls below carry the same stale observation "this row was
// expired"; only the first may win. An unconditional UPDATE would report
// RowsAffected == 1 to both, so two concurrent recyclers would both be told
// "first" — the same defect the insert path avoids via the primary key
// (ADR 0036 D5).
func TestSAMLReplayRepo_TakeoverIsConditionalOnTheRowStillBeingExpired(t *testing.T) {
	r := newSAMLReplayRepo(t)
	ctx := context.Background()
	now := time.Now()

	// Seed a row whose window has already closed.
	if _, err := r.SeenOrAdd(ctx, "recycled", now.Add(-time.Minute), now.Add(-2*time.Minute)); err != nil {
		t.Fatalf("seed: %v", err)
	}

	ok, err := r.takeOverExpiredRow(ctx, "recycled", now.Add(5*time.Minute), now)
	if err != nil {
		t.Fatalf("first takeover: %v", err)
	}
	if !ok {
		t.Fatal("first takeover was refused; a recycled ID would lock the user out")
	}

	ok, err = r.takeOverExpiredRow(ctx, "recycled", now.Add(5*time.Minute), now)
	if err != nil {
		t.Fatalf("second takeover: %v", err)
	}
	if ok {
		t.Fatal("a second takeover of the same row was admitted; concurrent recyclers would both be told \"first\"")
	}
}

// Concurrent recyclers of one expired ID: exactly one is admitted. This is the
// queue-level counterpart of the deterministic test above, and it is the one
// that would catch a regression on a database that actually runs the racers in
// parallel (MySQL, PostgreSQL).
func TestSAMLReplayRepo_ConcurrentTakeoverAdmitsExactlyOne(t *testing.T) {
	r := newSAMLReplayRepo(t)
	ctx := context.Background()
	now := time.Now()

	if _, err := r.SeenOrAdd(ctx, "recycled-race", now.Add(-time.Minute), now.Add(-2*time.Minute)); err != nil {
		t.Fatalf("seed: %v", err)
	}

	later := now.Add(10 * time.Minute)
	exp := later.Add(5 * time.Minute)

	const racers = 8
	var (
		wg       sync.WaitGroup
		mu       sync.Mutex
		accepted int
	)
	for i := 0; i < racers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			seen, err := r.SeenOrAdd(ctx, "recycled-race", exp, later)
			if err != nil {
				return // a busy-DB error is not an acceptance
			}
			if !seen {
				mu.Lock()
				accepted++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if accepted != 1 {
		t.Fatalf("takeover admitted %d concurrent submissions, want exactly 1", accepted)
	}
}

// The read-back after a conflict has three outcomes with three different
// meanings, and conflating them is a real bug: a vanished row is a replay (the
// safe direction), a read failure is an infrastructure fault that must be
// reportable, and neither may be reported as the other.
func TestSAMLReplayRepo_ExistingRowReadDistinguishesMissingFromBroken(t *testing.T) {
	r := newSAMLReplayRepo(t)
	ctx := context.Background()

	if _, found, err := readExistingRow(ctx, r.db, "absent"); err != nil || found {
		t.Fatalf("absent row: found=%v err=%v, want found=false err=nil", found, err)
	}

	sqlDB, err := r.db.DB()
	if err != nil {
		t.Fatalf("db handle: %v", err)
	}
	if err := sqlDB.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if _, found, err := readExistingRow(ctx, r.db, "absent"); err == nil {
		t.Fatalf("a broken database was reported as found=%v err=nil; a read failure must not look like a missing row", found)
	}

	// The public contract too: an unusable store must never be presented as
	// "already consumed" or as an acceptance, only as an error.
	if seen, err := r.SeenOrAdd(ctx, "x", time.Now().Add(time.Minute), time.Now()); err == nil {
		t.Fatalf("SeenOrAdd on a broken store returned seen=%v with no error", seen)
	}
}
