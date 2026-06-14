package sqlstore

import (
	"context"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// TestAuditRepoSearch covers the v3.3.0 keyword search: a single case-
// insensitive substring matched across actor / action / target. The sub-log
// and mail-log repos use the same LOWER(...) LIKE construction (plus a users
// join), so this exercises the shared matching contract.
func TestAuditRepoSearch(t *testing.T) {
	db, err := openTestDB(t)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() {
		if sqlDB, _ := db.DB(); sqlDB != nil {
			_ = sqlDB.Close()
		}
	})
	if err := EnsureSchema(db); err != nil {
		t.Fatalf("schema: %v", err)
	}
	repo := &auditRepo{db: db}
	ctx := context.Background()

	for _, e := range []*domain.AuditEntry{
		{Actor: "admin@x.org", Action: "user.create", Target: "u123"},
		{Actor: "op@x.org", Action: "user.disable", Target: "u456"},
		{Actor: "admin@x.org", Action: "node.delete", Target: "n7"},
	} {
		if err := repo.Insert(ctx, e); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}

	cases := []struct {
		name, search string
		wantTotal    int64
	}{
		{"by actor substring", "op@", 1},
		{"by action prefix, case-insensitive", "USER.", 2},
		{"by target", "n7", 1},
		{"no match", "nope", 0},
		{"empty returns all", "", 3},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, total, err := repo.List(ctx, ports.AuditFilter{Search: tc.search})
			if err != nil {
				t.Fatalf("List: %v", err)
			}
			if total != tc.wantTotal || int64(len(got)) != tc.wantTotal {
				t.Fatalf("search %q: got total=%d len=%d, want %d", tc.search, total, len(got), tc.wantTotal)
			}
		})
	}
}

// TestAuditRepoSearchEscapesLikeMeta locks the LIKE-escape fix (likeCols'
// ESCAPE '!' clause): a keyword containing an underscore must match the
// LITERAL underscore, not act as a single-char wildcard. Without the explicit
// ESCAPE, SQLite (the default backend) ignores keywordLike's wildcard-escaping,
// so "user_5" would ALSO match "userX5" — returning 2 rows instead of 1. The
// other 7 keyword-search repos share the same likeCols construction.
//
// The escape char is `!`, NOT backslash: `ESCAPE '\'` is a 1064 syntax error on
// MySQL (backslash escapes the closing quote in a MySQL string literal) — the
// beta.7 regression that broke every keyword search on MySQL deployments.
// TestLikeColsEscapeIsPortable guards the generated-SQL form directly.
func TestAuditRepoSearchEscapesLikeMeta(t *testing.T) {
	db, err := openTestDB(t)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() {
		if sqlDB, _ := db.DB(); sqlDB != nil {
			_ = sqlDB.Close()
		}
	})
	if err := EnsureSchema(db); err != nil {
		t.Fatalf("schema: %v", err)
	}
	repo := &auditRepo{db: db}
	ctx := context.Background()
	for _, e := range []*domain.AuditEntry{
		{Actor: "user_5@x.org", Action: "login", Target: "t1"},
		{Actor: "userX5@x.org", Action: "login", Target: "t2"},
	} {
		if err := repo.Insert(ctx, e); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}
	got, total, err := repo.List(ctx, ports.AuditFilter{Search: "user_5"})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if total != 1 || len(got) != 1 {
		t.Fatalf("search 'user_5': got total=%d len=%d, want exactly 1 — underscore must be literal, not a wildcard", total, len(got))
	}
	if got[0].Actor != "user_5@x.org" {
		t.Fatalf("matched the wrong row: %q", got[0].Actor)
	}
}
