package sqlstore

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

func TestUserAccountAccessCountsAndFiltersLegacyServiceRowsAsActive(t *testing.T) {
	db, err := openTestDB(t)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := EnsureSchema(db); err != nil {
		t.Fatalf("schema: %v", err)
	}
	if sqlDB, err := db.DB(); err == nil {
		t.Cleanup(func() { _ = sqlDB.Close() })
	}

	repo := NewRepos(db).User
	ctx := context.Background()
	users := []*domain.User{
		{UPN: "active@example.test", Enabled: true},
		{UPN: "legacy-expired@example.test", Enabled: false, AutoDisabledReason: domain.DisabledExpired},
		{UPN: "manual-disabled@example.test", Enabled: false, AutoDisabledReason: domain.DisabledManual},
		{UPN: "legacy-null-reason@example.test", Enabled: false},
		{UPN: "service-paused@example.test", Enabled: true, ServiceDisabledReason: domain.DisabledServiceManual},
	}
	for i, u := range users {
		u.Role = domain.RoleUser
		u.GroupID = 1
		u.SubToken = fmt.Sprintf("access-scope-token-%d", i)
		u.UUID = fmt.Sprintf("00000000-0000-0000-0000-%012d", i+1)
		u.TrafficResetPeriod = domain.ResetMonthly
		if err := repo.Create(ctx, u); err != nil {
			t.Fatalf("create %s: %v", u.UPN, err)
		}
	}
	if err := db.Model(&userRow{}).
		Where("upn = ?", "legacy-null-reason@example.test").
		UpdateColumn("auto_disabled_reason", nil).Error; err != nil {
		t.Fatalf("set historical NULL disable reason: %v", err)
	}

	counts, err := repo.CountByStatus(ctx, time.Now())
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if counts.Total != 5 || counts.Enabled != 3 || counts.Disabled != 2 {
		t.Fatalf("counts = %+v, want total=5 enabled=3 disabled=2", counts)
	}

	active := true
	rows, total, err := repo.List(ctx, ports.UserFilter{Enabled: &active})
	if err != nil {
		t.Fatalf("list active: %v", err)
	}
	if total != 3 || len(rows) != 3 {
		t.Fatalf("active total=%d len=%d, want 3", total, len(rows))
	}

	active = false
	rows, total, err = repo.List(ctx, ports.UserFilter{Enabled: &active})
	if err != nil {
		t.Fatalf("list disabled: %v", err)
	}
	if total != 2 || len(rows) != 2 {
		t.Fatalf("disabled rows=%+v total=%d, want manual and historical NULL account suspensions", rows, total)
	}
}
