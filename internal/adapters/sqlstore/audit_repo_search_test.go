package sqlstore

import (
	"context"
	"strings"
	"testing"
	"time"

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
	if err := ensureTestSchema(db); err != nil {
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
	if err := ensureTestSchema(db); err != nil {
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

// TestAuditRepoListExcludesTargetPrefixes pins the filter the audit read
// uses to keep admin-only rows from an operator: a row whose target starts
// with an excluded prefix is left out of the page AND of the total, so the
// pager never counts rows it cannot show; the prefix is literal (its `_`
// is not a wildcard, the same escaping as the keyword search); and a row
// whose target is NULL — the column is nullable, and NOT LIKE on NULL is
// NULL — stays visible, since nobody asked to hide it.
func TestAuditRepoListExcludesTargetPrefixes(t *testing.T) {
	db, err := openTestDB(t)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() {
		if sqlDB, _ := db.DB(); sqlDB != nil {
			_ = sqlDB.Close()
		}
	})
	if err := ensureTestSchema(db); err != nil {
		t.Fatalf("schema: %v", err)
	}
	repo := &auditRepo{db: db}
	ctx := context.Background()
	for _, e := range []*domain.AuditEntry{
		{Actor: "admin@x.org", Action: "create_or_run /api/admin/risk-center/users/:id/dismiss", Target: "/api/admin/risk-center/users/:id/dismiss"},
		{Actor: "admin@x.org", Action: "update /api/admin/risk-center/policy", Target: "/api/admin/risk-center/policy"},
		{Actor: "op@x.org", Action: "update /api/admin/users/:id", Target: "/api/admin/users/:id"},
		{Actor: "op@x.org", Action: "create_or_run /api/admin/riskXcenter/x", Target: "/api/admin/riskXcenter/x"},
	} {
		if err := repo.Insert(ctx, e); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}
	if err := db.Exec("INSERT INTO audit_log (actor, action, at) VALUES (?, ?, ?)",
		"geo-detector", "geo_auto_suspend", time.Now()).Error; err != nil {
		t.Fatalf("insert a row with no target: %v", err)
	}

	targets := func(items []*domain.AuditEntry) []string {
		out := make([]string, len(items))
		for i, it := range items {
			out[i] = it.Target
		}
		return out
	}

	t.Run("the excluded rows leave the page and the total", func(t *testing.T) {
		got, total, err := repo.List(ctx, ports.AuditFilter{
			Pagination:            ports.Pagination{Page: 1, PageSize: 100},
			ExcludeTargetPrefixes: []string{"/api/admin/risk-center/"},
		})
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if total != 3 || len(got) != 3 {
			t.Fatalf("got total=%d targets=%q, want the 3 rows outside /api/admin/risk-center/", total, targets(got))
		}
		for _, it := range got {
			if strings.HasPrefix(it.Target, "/api/admin/risk-center/") {
				t.Fatalf("an excluded row came back: %q", targets(got))
			}
		}
	})

	t.Run("the total counts past a short page", func(t *testing.T) {
		got, total, err := repo.List(ctx, ports.AuditFilter{
			Pagination:            ports.Pagination{Page: 1, PageSize: 1},
			ExcludeTargetPrefixes: []string{"/api/admin/risk-center/"},
		})
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if total != 3 || len(got) != 1 {
			t.Fatalf("got total=%d len=%d, want total 3 on a page of 1", total, len(got))
		}
	})

	t.Run("a search cannot reach an excluded row", func(t *testing.T) {
		got, total, err := repo.List(ctx, ports.AuditFilter{
			Search:                "risk-center",
			ExcludeTargetPrefixes: []string{"/api/admin/risk-center/"},
		})
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if total != 0 || len(got) != 0 {
			t.Fatalf("got total=%d targets=%q, want nothing", total, targets(got))
		}
	})

	t.Run("the prefix is literal", func(t *testing.T) {
		got, total, err := repo.List(ctx, ports.AuditFilter{
			ExcludeTargetPrefixes: []string{"/api/admin/risk_center/"},
		})
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if total != 5 || len(got) != 5 {
			t.Fatalf("got total=%d targets=%q, want all 5: `_` must not match `-` or `X`", total, targets(got))
		}
	})

	t.Run("no exclusion lists every row", func(t *testing.T) {
		got, total, err := repo.List(ctx, ports.AuditFilter{})
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if total != 5 || len(got) != 5 {
			t.Fatalf("got total=%d targets=%q, want all 5", total, targets(got))
		}
	})
}
