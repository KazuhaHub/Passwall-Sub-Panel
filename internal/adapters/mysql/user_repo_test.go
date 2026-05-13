package mysql

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
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
