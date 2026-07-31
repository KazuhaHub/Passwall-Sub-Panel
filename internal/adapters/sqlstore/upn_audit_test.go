package sqlstore

import (
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// The boot audit must survive a users table that isn't there — a very old
// install runs `psp migrate` first, and the audit runs unconditionally at boot
// before anyone knows which schema state they're in.
//
// It must NOT reach that conclusion through db.Migrator().HasTable, which
// ownership_repo.go:26 documents as swallowing ALL errors and returning false:
// trusting that bool means one transient blip silently skips the audit, and a
// diagnostic that skips itself on a bad day is worse than useless because its
// silence reads as "clean".
func TestAuditUPNCanonicalizationSurvivesMissingTable(t *testing.T) {
	db, err := openTestDB(t)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	// Deliberately NO EnsureSchema — there is no users table at all.
	AuditUPNCanonicalization(db) // must not panic
}

func TestAuditUPNCanonicalizationHandlesNilDB(t *testing.T) {
	AuditUPNCanonicalization(nil) // must not panic
}

// The audit is the prerequisite for the R2 backfill, so what it FINDS has to be
// right, not merely non-crashing. Pinned here because a silent regression in it
// would make an operator believe their install is safe to fold when it is not.
func TestAuditUPNCanonicalizationCountsCollisionsAndNonCanonical(t *testing.T) {
	db, err := openTestDB(t)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := EnsureSchema(db); err != nil {
		t.Fatalf("schema: %v", err)
	}
	repos := NewRepos(db)
	ctx := t.Context()

	// A colliding pair (same folded name, two rows) plus a lone non-canonical
	// row. Created through the repo so the fixture cannot drift from the real
	// insert path.
	for i, upn := range []string{"alice@corp.com", "Alice@Corp.Com", "Bob@Corp.Com"} {
		u := mkUser(upn, string(rune('x'+i)))
		if err := repos.User.Create(ctx, u); err != nil {
			t.Fatalf("create %q: %v", upn, err)
		}
	}

	collisions, nonCanonical, err := auditUPNState(db)
	if err != nil {
		t.Fatalf("auditUPNState: %v", err)
	}
	if len(collisions) != 1 || collisions[0].N != 2 {
		t.Fatalf("collisions = %+v, want exactly one group of 2 (alice@corp.com)", collisions)
	}
	if collisions[0].Folded != "alice@corp.com" {
		t.Fatalf("colliding group = %q, want alice@corp.com", collisions[0].Folded)
	}
	// Alice@Corp.Com and Bob@Corp.Com are both stored non-canonically.
	if nonCanonical != 2 {
		t.Fatalf("nonCanonical = %d, want 2", nonCanonical)
	}
}

// A clean install must report nothing at all — no collisions, no non-canonical
// rows — so an operator can read silence as a genuine all-clear.
func TestAuditUPNCanonicalizationSilentOnCleanInstall(t *testing.T) {
	db, err := openTestDB(t)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := EnsureSchema(db); err != nil {
		t.Fatalf("schema: %v", err)
	}
	repos := NewRepos(db)
	if err := repos.User.Create(t.Context(), &domain.User{
		UPN: "admin", Role: domain.RoleAdmin, Enabled: true,
		SubToken: "tok-clean", UUID: "uuid-clean",
	}); err != nil {
		t.Fatalf("create: %v", err)
	}

	collisions, nonCanonical, err := auditUPNState(db)
	if err != nil {
		t.Fatalf("auditUPNState: %v", err)
	}
	if len(collisions) != 0 || nonCanonical != 0 {
		t.Fatalf("clean install reported collisions=%+v nonCanonical=%d, want none", collisions, nonCanonical)
	}
}
