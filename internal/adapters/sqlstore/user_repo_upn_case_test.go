package sqlstore

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// The upn identity contract, pinned across dialects.
//
// `users.upn` is a plain varchar(255) UNIQUE with no COLLATE anywhere, so the
// three supported backends disagree about what "the same username" means:
// MySQL's default utf8mb4 collation folds case (and accents), while Postgres
// and SQLite compare byte-exact. Verified empirically — on SQLite and Postgres
// `alice@psp.local` and `Alice@psp.local` both insert and become two separate
// identities; on a stock MySQL the second is refused by the index.
//
// These tests exist to make all three answer the SAME thing. They belong in
// this package because only internal/adapters/sqlstore reads PSP_TEST_DB_KIND /
// PSP_TEST_DB_DSN (see testdb_test.go) — service-level tests run against
// in-memory fakes and are dialect-blind, which is exactly why the existing
// suite never caught any of this. Run them on more than SQLite:
//
//	go test ./internal/adapters/sqlstore/...
//	PSP_TEST_DB_KIND=postgres PSP_TEST_DB_DSN='postgres://psp@127.0.0.1:5432/psptest?sslmode=disable' \
//	  go test ./internal/adapters/sqlstore/...

// mkUser builds a user whose OTHER unique columns are distinct, so a rejected
// Create can only ever be the upn index. Learned the hard way: an earlier probe
// concluded "the index folds case" when the real rejection came from the
// sub_token UNIQUE, both rows having been left with the empty default.
func mkUser(upn, tag string) *domain.User {
	return &domain.User{
		UPN:      upn,
		Role:     domain.RoleUser,
		Enabled:  true,
		SubToken: "subtok-" + tag,
		UUID:     "uuid-" + tag,
	}
}

// T1 — the lookup contract. One stored row must be reachable by every spelling
// a human might type: different case, and the leading/trailing whitespace that
// a copy-paste or an autofill leaves behind.
func TestGetByUPNResolvesCaseAndWhitespaceVariants(t *testing.T) {
	db, err := openTestDB(t)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := EnsureSchema(db); err != nil {
		t.Fatalf("schema: %v", err)
	}
	repos := NewRepos(db)
	ctx := context.Background()

	canonical := mkUser("alice@corp.com", "t1")
	if err := repos.User.Create(ctx, canonical); err != nil {
		t.Fatalf("create: %v", err)
	}

	for _, variant := range []string{
		"alice@corp.com",   // exact
		"Alice@corp.com",   // leading capital — what an admin types
		"ALICE@CORP.COM",   // shouting
		" alice@corp.com ", // copy-paste padding
		"\talice@corp.com", // tab
		" Alice@Corp.Com ", // both at once
	} {
		got, err := repos.User.GetByUPN(ctx, variant)
		if err != nil {
			t.Errorf("GetByUPN(%q) = err %v, want the row id=%d", variant, err, canonical.ID)
			continue
		}
		if got.ID != canonical.ID {
			t.Errorf("GetByUPN(%q) resolved id=%d, want id=%d", variant, got.ID, canonical.ID)
		}
	}
}

// T2 — the cross-dialect split, pinned. Creating the same identity twice must
// report domain.ErrAlreadyExists on EVERY backend.
//
// Today this fails differently per dialect, which is the whole problem:
// SQLite/Postgres accept both rows silently, while MySQL's folded index rejects
// the second — but userRepo.Create returns the RAW driver error, so the caller's
// ErrAlreadyExists branch (handler 409) is skipped and staff get a 500 with a
// leaked driver string. registration.go documents that /auth/register SHOULD
// surface ErrAlreadyExists; it cannot until this mapping exists.
//
// This matters more once writes normalize: the DB unique index becomes the real
// backstop for the TOCTOU race between a guard's read and its insert, so its
// failure has to be readable rather than a 500.
func TestCreateDuplicateUPNReportsAlreadyExists(t *testing.T) {
	db, err := openTestDB(t)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := EnsureSchema(db); err != nil {
		t.Fatalf("schema: %v", err)
	}
	repos := NewRepos(db)
	ctx := context.Background()

	if err := repos.User.Create(ctx, mkUser("alice@corp.com", "t2a")); err != nil {
		t.Fatalf("first create: %v", err)
	}
	err = repos.User.Create(ctx, mkUser("alice@corp.com", "t2b"))
	if err == nil {
		t.Fatal("second Create with the same upn succeeded, want domain.ErrAlreadyExists")
	}
	if !errors.Is(err, domain.ErrAlreadyExists) {
		t.Fatalf("second Create = %v, want it to wrap domain.ErrAlreadyExists (callers classify with errors.Is; "+
			"a raw driver error becomes a 500 instead of the intended 409)", err)
	}

	var n int64
	db.Raw("SELECT COUNT(*) FROM users WHERE upn = ?", "alice@corp.com").Scan(&n)
	if n != 1 {
		t.Fatalf("row count = %d, want 1", n)
	}
}

// T3 — the lockout-regression guard. DO NOT DELETE THIS TEST.
//
// Installs created before normalization carry non-canonical upns: CreateLocal
// stored exactly what an admin typed (only trimmed), and EnsureSSO stored the
// IdP's assertion value verbatim, untrimmed. If the fix folds the lookup input
// and probes only once, every one of those rows becomes instantly unreachable —
// a hard lockout of real users, possibly including an admin.
//
// The raw insert is deliberate: it bypasses the service layer so the row is
// non-canonical no matter how the write path behaves. A test that created the
// row through the normal path would normalize it and silently stop testing
// anything.
func TestGetByUPNStillResolvesLegacyMixedCaseRow(t *testing.T) {
	db, err := openTestDB(t)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := EnsureSchema(db); err != nil {
		t.Fatalf("schema: %v", err)
	}
	repos := NewRepos(db)
	ctx := context.Background()

	// A row exactly as a pre-normalization install stores it.
	if err := db.Exec(
		"INSERT INTO users (upn, role, enabled, sub_token, uuid, group_id, token_version) VALUES (?, ?, ?, ?, ?, ?, ?)",
		"Alice@Corp.Com", string(domain.RoleUser), true, "subtok-t3", "uuid-t3", 0, 0,
	).Error; err != nil {
		t.Fatalf("raw insert: %v", err)
	}

	got, err := repos.User.GetByUPN(ctx, "Alice@Corp.Com")
	if err != nil {
		t.Fatalf("GetByUPN(%q) = %v — a legacy row became unreachable; this is a hard lockout of existing users",
			"Alice@Corp.Com", err)
	}
	if got.UPN != "Alice@Corp.Com" {
		t.Fatalf("resolved upn = %q, want the stored value preserved", got.UPN)
	}
}

// T4 — concurrency. Two requests racing to claim the same identity must settle
// as exactly one row plus one ErrAlreadyExists.
//
// The point is that the DB unique index — not the read-then-write guard in the
// service, which is inherently TOCTOU-racy — is what enforces this. Normalizing
// at write is what lets the plain existing index do that job on all three
// dialects with no DDL.
func TestConcurrentCreateSameUPNYieldsOneRow(t *testing.T) {
	db, err := openTestDB(t)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := EnsureSchema(db); err != nil {
		t.Fatalf("schema: %v", err)
	}
	repos := NewRepos(db)
	ctx := context.Background()

	var wg sync.WaitGroup
	errs := make([]error, 2)
	start := make(chan struct{})
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			errs[i] = repos.User.Create(ctx, mkUser("race@corp.com", string(rune('a'+i))))
		}(i)
	}
	close(start)
	wg.Wait()

	var okCount int
	for _, e := range errs {
		if e == nil {
			okCount++
			continue
		}
		if !errors.Is(e, domain.ErrAlreadyExists) {
			t.Errorf("loser got %v, want domain.ErrAlreadyExists (a raw driver error surfaces as a 500)", e)
		}
	}
	if okCount != 1 {
		t.Fatalf("%d creates succeeded, want exactly 1", okCount)
	}

	var n int64
	db.Raw("SELECT COUNT(*) FROM users WHERE upn = ?", "race@corp.com").Scan(&n)
	if n != 1 {
		t.Fatalf("row count = %d, want 1", n)
	}
}
