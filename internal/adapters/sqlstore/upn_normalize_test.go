package sqlstore

import (
	"testing"

	"gorm.io/gorm"
)

// The R2 backfill. It is the only thing that can fix the ONE direction R1
// deliberately could not: a row stored as `Alice@Corp.Com` is unreachable by the
// canonical spelling `alice@corp.com`, because GetByUPN's probes compare the
// input against the stored column and neither probe can rewrite storage.
//
// It is a separate, operator-invoked step rather than a boot migration on
// purpose: folding can violate the unique index, and both aborting at boot and
// silently rewriting an admin's login name are unacceptable outcomes for a
// panel that is coming up.
//
// Run on more than SQLite — folding behaviour and the unique index are exactly
// what differs between backends:
//
//	go test ./internal/adapters/sqlstore/...
//	PSP_TEST_DB_KIND=postgres PSP_TEST_DB_DSN='...' go test ./internal/adapters/sqlstore/...

func seedUPNs(t *testing.T, upns ...string) (*gorm.DB, func() []string) {
	t.Helper()
	db, err := openTestDB(t)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := EnsureSchema(db); err != nil {
		t.Fatalf("schema: %v", err)
	}
	for i, upn := range upns {
		// Raw insert: the service layer would canonicalize these, and the whole
		// point of the fixture is rows that predate that.
		if err := db.Exec(
			"INSERT INTO users (upn, role, enabled, sub_token, uuid, group_id, token_version) VALUES (?, ?, ?, ?, ?, ?, ?)",
			upn, "user", true, "tok-"+string(rune('a'+i)), "uuid-"+string(rune('a'+i)), 0, 0,
		).Error; err != nil {
			t.Fatalf("seed %q: %v", upn, err)
		}
	}
	readBack := func() []string {
		var got []string
		db.Raw(`SELECT upn FROM users ORDER BY id`).Scan(&got)
		return got
	}
	return db, readBack
}

// N1 — a clean install has nothing to do, and says so.
func TestNormalizeStoredUPNsCleanInstallIsNoOp(t *testing.T) {
	db, readBack := seedUPNs(t, "admin", "alice@corp.com")
	rep, err := NormalizeStoredUPNs(db, true)
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if rep.Changed != 0 || len(rep.Collisions) != 0 {
		t.Fatalf("clean install: changed=%d collisions=%+v, want 0/none", rep.Changed, rep.Collisions)
	}
	if got := readBack(); got[0] != "admin" || got[1] != "alice@corp.com" {
		t.Fatalf("rows mutated on a clean install: %v", got)
	}
}

// N2 — non-canonical rows fold, and the report names each change so an operator
// can see exactly which login strings are about to change under their users.
func TestNormalizeStoredUPNsFoldsNonCanonical(t *testing.T) {
	db, readBack := seedUPNs(t, "Alice@Corp.Com", " bob@corp.com ", "carol@corp.com")
	rep, err := NormalizeStoredUPNs(db, true)
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if rep.Changed != 2 {
		t.Fatalf("changed = %d, want 2", rep.Changed)
	}
	got := readBack()
	want := []string{"alice@corp.com", "bob@corp.com", "carol@corp.com"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("row %d = %q, want %q (all: %v)", i, got[i], want[i], got)
		}
	}
}

// N3 — the safety property. A colliding pair must be REFUSED and left exactly as
// it was: merging two accounts is a business decision (whose sub token, whose
// quota, whose traffic history survives) and a migration must never guess it.
// Critically, the non-colliding rows in the same run still get folded.
func TestNormalizeStoredUPNsRefusesCollidingGroups(t *testing.T) {
	db, readBack := seedUPNs(t, "alice@corp.com", "Alice@Corp.Com", "Dave@Corp.Com")
	rep, err := NormalizeStoredUPNs(db, true)
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if len(rep.Collisions) != 1 || rep.Collisions[0].Folded != "alice@corp.com" {
		t.Fatalf("collisions = %+v, want the alice@corp.com group", rep.Collisions)
	}
	got := readBack()
	if got[0] != "alice@corp.com" || got[1] != "Alice@Corp.Com" {
		t.Fatalf("colliding rows were modified: %v", got)
	}
	if got[2] != "dave@corp.com" {
		t.Fatalf("non-colliding row was not folded: %v (a refused group must not block the rest)", got)
	}
	if rep.Changed != 1 {
		t.Fatalf("changed = %d, want 1 (only Dave)", rep.Changed)
	}
}

// N4 — dry run reports precisely what apply would do, and writes nothing.
func TestNormalizeStoredUPNsDryRunMutatesNothing(t *testing.T) {
	db, readBack := seedUPNs(t, "Alice@Corp.Com", "Bob@Corp.Com")
	rep, err := NormalizeStoredUPNs(db, false)
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if rep.Changed != 2 {
		t.Fatalf("dry-run reported changed=%d, want it to report the 2 it WOULD change", rep.Changed)
	}
	got := readBack()
	if got[0] != "Alice@Corp.Com" || got[1] != "Bob@Corp.Com" {
		t.Fatalf("dry run mutated rows: %v", got)
	}
}

// N5 — idempotent. An operator who runs it twice, or a script that retries,
// must not see a second round of changes.
func TestNormalizeStoredUPNsIsIdempotent(t *testing.T) {
	db, readBack := seedUPNs(t, "Alice@Corp.Com")
	if _, err := NormalizeStoredUPNs(db, true); err != nil {
		t.Fatalf("first: %v", err)
	}
	rep, err := NormalizeStoredUPNs(db, true)
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if rep.Changed != 0 {
		t.Fatalf("second run changed=%d, want 0", rep.Changed)
	}
	if got := readBack(); got[0] != "alice@corp.com" {
		t.Fatalf("row = %q after two runs, want alice@corp.com", got[0])
	}
}
