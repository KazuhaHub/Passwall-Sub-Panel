package sqlstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

func TestCurrentUserCursorMatchesCompleteModelsAndResolvedLimits(t *testing.T) {
	port, db := serviceStateFixture(t)
	r := port.(*userRepo)
	clock := time.Date(2026, 10, 15, 3, 4, 5, 123000000, time.UTC)
	groupTraffic, groupIP, groupDevice := int64(9999), 7, 9
	if err := db.Create(&groupRow{ID: 8001, Slug: "cursor-group", Name: "cursor group", TrafficLimitBytes: &groupTraffic, IPLimit: &groupIP, DeviceLimit: &groupDevice}).Error; err != nil {
		t.Fatal(err)
	}
	var models []userRow
	var ids []int64
	for i := int64(1); i <= 601; i++ {
		zeroTraffic, zeroLimit := int64(0), 0
		finiteTraffic, finiteLimit := i+1000, int(i%19)+1
		row := userRow{
			ID: i + 10000, UPN: fmt.Sprintf("cursor-%d@example.test", i), SSOProvider: "oidc:fixture", SSOSubject: fmt.Sprintf("subject-%d", i),
			Email: "private-email", PasswordHash: "private-hash", Role: "user", SubToken: fmt.Sprintf("cursor-token-%d", i), UUID: fmt.Sprintf("00000000-0000-4000-8000-%012d", i),
			GroupID: 8001, EnabledRuleSets: jsonStrings{"one", "two"}, PersonalRules: "preserved-rules", ExpireAt: &clock,
			TrafficResetPeriod: "monthly", TrafficPeriodStart: &clock, LifetimeUpBytes: i * 3, LifetimeDownBytes: i * 5, LifetimeTotalBytes: i * 8,
			PeriodBaselineBytes: i, LifetimeBaselineAt: &clock, DisplayName: "display", Remark: "remark", Enabled: i%2 == 0,
			AutoDisabledReason: "manual", DisableDetail: "disabled", ServiceDisabledReason: "service_manual", ServiceDisableDetail: "service", ServiceDisabledAt: &clock,
			SelfRegistered: true, BlockViolationCount: int(i), LastBlockViolationAt: &clock, EmergencyUsedCount: int(i), EmergencyUntil: &clock,
			EmergencyBaselineBytes: i * 2, TokenVersion: int(i), TOTPSecret: "private-secret", TOTPEnabled: true, RecoveryCodes: jsonStrings{"private-recovery"},
			PermissionOverrides: jsonPermOverrides{}, LastOnlineAt: &clock, CreatedAt: clock, UpdatedAt: clock,
		}
		if i%3 == 0 {
			row.TrafficLimitBytes, row.IPLimit, row.DeviceLimit = &zeroTraffic, &zeroLimit, &zeroLimit
		}
		if i%3 == 1 {
			row.TrafficLimitBytes, row.IPLimit, row.DeviceLimit = &finiteTraffic, &finiteLimit, &finiteLimit
		}
		models = append(models, row)
		ids = append(ids, row.ID)
	}
	if err := db.CreateInBatches(models, 25).Error; err != nil {
		t.Fatal(err)
	}
	// Historic scalar NULLs, pointer NULLs and empty JSON must retain the old
	// model mapping; NULL overrides still inherit instead of becoming zero.
	if err := db.Model(&userRow{}).Where("id = ?", ids[1]).UpdateColumns(map[string]any{
		"email": nil, "password_hash": nil, "personal_rules": nil, "traffic_reset_period": nil,
		"display_name": nil, "remark": nil, "auto_disabled_reason": nil, "disable_detail": nil, "service_disable_detail": nil,
		"lifetime_up_bytes": nil, "lifetime_down_bytes": nil, "lifetime_total_bytes": nil, "period_baseline_bytes": nil,
		"block_violation_count": nil, "emergency_used_count": nil, "emergency_baseline_bytes": nil,
		"expire_at": nil, "traffic_period_start": nil, "lifetime_baseline_at": nil, "service_disabled_at": nil,
		"last_block_violation_at": nil, "emergency_until": nil, "last_online_at": nil, "created_at": nil, "updated_at": nil,
		"enabled_rule_sets": nil, "recovery_codes": nil, "permission_overrides": nil,
	}).Error; err != nil {
		t.Fatal(err)
	}
	var full []userRow
	if err := db.Where("id IN ?", ids).Order("id").Find(&full).Error; err != nil {
		t.Fatal(err)
	}
	var expected []*domain.User
	for i := range full {
		expected = append(expected, full[i].toDomain())
	}
	expected, err := r.resolveAll(t.Context(), expected)
	if err != nil {
		t.Fatal(err)
	}
	statement := &gorm.Statement{DB: db}
	if err := statement.Parse(&userRow{}); err != nil {
		t.Fatal(err)
	}
	wantColumns := slices.Clone(statement.Schema.DBNames)
	slices.Sort(wantColumns)
	queries := 0
	const hook = "current-user-cursor-shape"
	if err := db.Callback().Row().After("gorm:row").Register(hook, func(tx *gorm.DB) {
		if tx.Statement.Table != "users" {
			return
		}
		queries++
		if len(tx.Statement.Vars) > idReadChunk {
			t.Error("current user read exceeded the existing ID chunk")
		}
		rows, ok := tx.Statement.Dest.(*sql.Rows)
		if !ok || tx.Error != nil {
			t.Error("current user read did not use a cursor")
			return
		}
		columns, err := rows.Columns()
		if err != nil {
			t.Error(err)
			return
		}
		columns = slices.Clone(columns)
		slices.Sort(columns)
		if !reflect.DeepEqual(columns, wantColumns) {
			t.Error("current user cursor omitted or duplicated a model column")
		}
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Callback().Row().Remove(hook) })
	requested := append(slices.Clone(ids), ids[0], 999999)
	slices.Reverse(requested)
	got, err := r.ListByIDs(t.Context(), requested)
	if err != nil || queries != 2 || !reflect.DeepEqual(got, expected) {
		t.Fatal("current user cursor changed complete resolved models, order or chunking")
	}
	if got[0].TrafficLimitBytes != 1001 || got[1].TrafficLimitBytes != groupTraffic || got[1].IPLimit != groupIP || got[2].TrafficLimitBytes != 0 || got[2].IPLimit != 0 {
		t.Fatal("current user cursor confused finite, inherited and explicitly unlimited limits")
	}
	if *got[0].Limits.IPLimit == *got[3].Limits.IPLimit {
		t.Fatal("fixture did not create distinct pointer values")
	}
	*got[0].Limits.IPLimit = 999
	got[0].EnabledRuleSets[0] = "changed"
	*got[0].ExpireAt = clock.Add(time.Hour)
	if *got[3].Limits.IPLimit == 999 || got[3].EnabledRuleSets[0] == "changed" || got[3].ExpireAt.Equal(*got[0].ExpireAt) {
		t.Fatal("cursor rows share mutable values")
	}
	if err := db.Model(&userRow{}).Where("id = ?", ids[0]).Updates(map[string]any{"group_id": 0, "traffic_limit_bytes": 4321, "lifetime_total_bytes": 999}).Error; err != nil {
		t.Fatal(err)
	}
	current, err := r.ListByIDs(t.Context(), []int64{ids[0]})
	if err != nil || len(current) != 1 || current[0].GroupID != 0 || current[0].TrafficLimitBytes != 4321 || current[0].LifetimeTotalBytes != 999 || current[0].EnabledRuleSets[0] != "one" {
		t.Fatal("current user read reused prior state or caller mutations")
	}
}

func TestCurrentUserCursorErrorsNeverReturnPartialAccountsOrRetainConnections(t *testing.T) {
	port, db := serviceStateFixture(t)
	r := port.(*userRepo)
	first, second := createServiceStateUser(t, port, 91), createServiceStateUser(t, port, 92)
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	before := sqlDB.Stats().InUse
	for _, column := range []string{"enabled_rule_sets", "recovery_codes", "permission_overrides"} {
		if err := db.Model(&userRow{}).Where("id = ?", second.ID).Update(column, "{").Error; err != nil {
			t.Fatal(err)
		}
		if got, err := r.ListByIDs(t.Context(), []int64{first.ID, second.ID}); got != nil || err == nil {
			t.Fatal("malformed later JSON row returned partial users or hid its error")
		}
		if sqlDB.Stats().InUse != before {
			t.Fatal("failed user scan retained a connection")
		}
		if err := db.Model(&userRow{}).Where("id = ?", second.ID).Update(column, "[]").Error; err != nil {
			t.Fatal(err)
		}
		if got, err := r.ListByIDs(t.Context(), []int64{first.ID, second.ID}); err != nil || len(got) != 2 {
			t.Fatal("healthy read did not recover after failed scan")
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if got, err := r.ListByIDs(ctx, []int64{first.ID}); got != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("canceled user read became an empty success")
	}
	if sqlDB.Stats().InUse != before {
		t.Fatal("canceled user scan retained a connection")
	}
}
