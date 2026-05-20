package mysql

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

func TestCreateUsersWithUPN(t *testing.T) {
	db, err := Open("sqlite", filepath.Join(t.TempDir(), "panel.db"))
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := EnsureSchema(db); err != nil {
		t.Fatalf("schema: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("unwrap db: %v", err)
	}
	defer func() {
		if err := sqlDB.Close(); err != nil {
			t.Fatalf("close db: %v", err)
		}
	}()

	repo := NewRepos(db).User
	ctx := context.Background()
	users := []*domain.User{
		{
			UPN:                "alice@example.test",
			PasswordHash:       "hash",
			Role:               domain.RoleUser,
			SubToken:           "sub-token-alice",
			UUID:               "00000000-0000-0000-0000-000000000001",
			GroupID:            1,
			TrafficResetPeriod: domain.ResetMonthly,
			Enabled:            true,
		},
		{
			UPN:                "bob@example.test",
			PasswordHash:       "hash",
			Role:               domain.RoleUser,
			SubToken:           "sub-token-bob",
			UUID:               "00000000-0000-0000-0000-000000000002",
			GroupID:            1,
			TrafficResetPeriod: domain.ResetMonthly,
			Enabled:            true,
		},
	}

	for _, u := range users {
		if err := repo.Create(ctx, u); err != nil {
			t.Fatalf("create %s: %v", u.UPN, err)
		}
	}
}

// TestListSearchIsCaseInsensitive locks the contract that the admin user
// search ignores case. It passes trivially on SQLite (whose LIKE is already
// ASCII-case-insensitive); its real job is to pin the LOWER()-based query so a
// future edit can't silently drop it and regress on Postgres, where LIKE is
// case-sensitive. The same SQL runs verbatim on all three backends.
func TestListSearchIsCaseInsensitive(t *testing.T) {
	db, err := Open("sqlite", filepath.Join(t.TempDir(), "panel.db"))
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := EnsureSchema(db); err != nil {
		t.Fatalf("schema: %v", err)
	}
	sqlDB, _ := db.DB()
	defer sqlDB.Close()

	repo := NewRepos(db).User
	ctx := context.Background()
	u := &domain.User{
		UPN:                "Carol@Example.Test",
		DisplayName:        "Carol Danvers",
		Email:              "Carol@Example.Test",
		PasswordHash:       "hash",
		Role:               domain.RoleUser,
		SubToken:           "sub-token-carol",
		UUID:               "00000000-0000-0000-0000-000000000003",
		GroupID:            1,
		TrafficResetPeriod: domain.ResetMonthly,
		Enabled:            true,
	}
	if err := repo.Create(ctx, u); err != nil {
		t.Fatalf("create: %v", err)
	}

	// Each query uses a different case than the stored value.
	for _, q := range []string{"carol", "CAROL", "example.test", "DANVERS"} {
		got, total, err := repo.List(ctx, ports.UserFilter{Search: q})
		if err != nil {
			t.Fatalf("list search %q: %v", q, err)
		}
		if total != 1 || len(got) != 1 {
			t.Fatalf("search %q: got total=%d len=%d, want exactly 1 match", q, total, len(got))
		}
	}
}
