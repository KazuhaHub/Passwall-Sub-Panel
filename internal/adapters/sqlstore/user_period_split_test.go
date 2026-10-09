package sqlstore

import (
	"context"
	"fmt"
	"reflect"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// TestUserTrafficStatePersistsPeriodSplitBaselines pins the per-direction
// period baselines (users.period_baseline_up_bytes / _down_bytes) on BOTH
// traffic-state writers. They share userTrafficStateMap precisely so they
// cannot diverge: a writer that drops the pair either loses a rollover's split
// or leaves a fresh period_baseline_bytes beside stale per-direction ones. The
// values are negative on purpose — a manual SetPeriodUsage legitimately stores
// signed baselines, and a non-negative column or a clamp would corrupt them.
func TestUserTrafficStatePersistsPeriodSplitBaselines(t *testing.T) {
	const gb = int64(1) << 30
	writers := []struct {
		name  string
		write func(ctx context.Context, repo ports.UserRepo, u *domain.User) error
	}{
		{"single row update", func(ctx context.Context, repo ports.UserRepo, u *domain.User) error {
			return repo.UpdateTrafficState(ctx, u)
		}},
		{"batched update", func(ctx context.Context, repo ports.UserRepo, u *domain.User) error {
			return repo.BatchUpdateTrafficState(ctx, []*domain.User{u})
		}},
	}
	for i, w := range writers {
		t.Run(w.name, func(t *testing.T) {
			db, err := openTestDB(t)
			if err != nil {
				t.Fatalf("open db: %v", err)
			}
			if err := ensureTestSchema(db); err != nil {
				t.Fatalf("schema: %v", err)
			}
			repo := NewRepos(db).User
			ctx := context.Background()
			u := &domain.User{
				UPN: fmt.Sprintf("split-%d@example.test", i), Role: domain.RoleUser, SubToken: fmt.Sprintf("sub-split-%d", i),
				UUID: fmt.Sprintf("00000000-0000-0000-0000-%012d", 500+i), GroupID: 1,
				TrafficResetPeriod: domain.ResetMonthly, Enabled: true,
			}
			if err := repo.Create(ctx, u); err != nil {
				t.Fatalf("create: %v", err)
			}
			state := &domain.User{
				ID:              u.ID,
				LifetimeUpBytes: 2 * gb, LifetimeDownBytes: 5 * gb, LifetimeTotalBytes: 7 * gb,
				PeriodBaselineBytes:   4 * gb,
				PeriodBaselineUpBytes: 3 * gb, PeriodBaselineDownBytes: -2 * gb,
			}
			if err := w.write(ctx, repo, state); err != nil {
				t.Fatalf("write traffic state: %v", err)
			}
			got, err := repo.GetByID(ctx, u.ID)
			if err != nil {
				t.Fatalf("get: %v", err)
			}
			if got.PeriodBaselineBytes != 4*gb || got.PeriodBaselineUpBytes != 3*gb || got.PeriodBaselineDownBytes != -2*gb {
				t.Fatalf("period baselines = (total %d, up %d, down %d), want (%d, %d, %d)",
					got.PeriodBaselineBytes, got.PeriodBaselineUpBytes, got.PeriodBaselineDownBytes, 4*gb, 3*gb, -2*gb)
			}
			if got.LifetimeUpBytes != 2*gb || got.LifetimeDownBytes != 5*gb || got.LifetimeTotalBytes != 7*gb {
				t.Fatalf("lifetime = (%d, %d, %d), want (%d, %d, %d)",
					got.LifetimeUpBytes, got.LifetimeDownBytes, got.LifetimeTotalBytes, 2*gb, 5*gb, 7*gb)
			}
			// The stored state must read back as the split it encodes:
			// up = 2-3 → clamped 0, down = PeriodUsed() = 3 GiB.
			if up, down := got.PeriodUsedSplit(); up != 0 || down != 3*gb {
				t.Fatalf("PeriodUsedSplit() = (%d, %d), want (0, %d)", up, down, 3*gb)
			}
		})
	}
}

// TestUpdateOmitsPeriodSplitBaselines pins pollOwnedColumns for the
// per-direction baselines: an admin profile Save (UpdateProfile's GetByID →
// mutate → Update) that brackets a rollover or a manual usage edit must not
// write its stale snapshot's baselines back. Without the omit the total
// baseline (omitted) and the per-direction ones (clobbered) would describe
// two different period starts.
func TestUpdateOmitsPeriodSplitBaselines(t *testing.T) {
	db, err := openTestDB(t)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := ensureTestSchema(db); err != nil {
		t.Fatalf("schema: %v", err)
	}
	repo := NewRepos(db).User
	ctx := context.Background()
	u := &domain.User{
		UPN: "split-omit@example.test", Role: domain.RoleUser, SubToken: "sub-split-omit",
		UUID: "00000000-0000-0000-0000-000000000510", GroupID: 1,
		TrafficResetPeriod: domain.ResetMonthly, Enabled: true,
	}
	if err := repo.Create(ctx, u); err != nil {
		t.Fatalf("create: %v", err)
	}
	adminSnapshot, err := repo.GetByID(ctx, u.ID)
	if err != nil {
		t.Fatalf("GetByID (admin snapshot): %v", err)
	}
	// A rollover lands between the dialog's load and its Save.
	if err := repo.UpdateTrafficState(ctx, &domain.User{
		ID: u.ID, LifetimeUpBytes: 40, LifetimeDownBytes: 60, LifetimeTotalBytes: 100,
		PeriodBaselineBytes: 90, PeriodBaselineUpBytes: 37, PeriodBaselineDownBytes: -11,
	}); err != nil {
		t.Fatalf("UpdateTrafficState: %v", err)
	}
	adminSnapshot.Remark = "admin edited the remark"
	if err := repo.Update(ctx, adminSnapshot); err != nil {
		t.Fatalf("Update: %v", err)
	}
	got, err := repo.GetByID(ctx, u.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.PeriodBaselineUpBytes != 37 || got.PeriodBaselineDownBytes != -11 {
		t.Fatalf("stale profile Save clobbered per-direction baselines: up %d down %d, want 37 / -11",
			got.PeriodBaselineUpBytes, got.PeriodBaselineDownBytes)
	}
	if got.Remark != "admin edited the remark" {
		t.Fatalf("admin remark edit lost: %q", got.Remark)
	}
}

// periodSplitBackfillCase is one users row before the per-direction columns
// existed, and what user_period_baseline_split_v1 must seed for it.
type periodSplitBackfillCase struct {
	name                      string
	up, down, total, baseline int64
	wantBU, wantBD            int64
	wantUp, wantDown          int64 // User.PeriodUsedSplit() right after the backfill
}

// periodSplitBackfillCases: PB > 0 rows reproduce the pre-split header
// (upload 0, download = PeriodUsed()); PB <= 0 rows are measured exactly
// (0/0 baselines, the whole lifetime is the period).
func periodSplitBackfillCases() []periodSplitBackfillCase {
	const gb = int64(1) << 30
	return []periodSplitBackfillCase{
		{"rolled period reads as all download", 30 * gb, 70 * gb, 100 * gb, 90 * gb, 30 * gb, 60 * gb, 0, 10 * gb},
		// 2^53+1 is not representable in float64: integer-only SQL or bust.
		// The down baseline goes negative (42 - 1035) and must not be clamped.
		{"rolled period beyond float precision", 9007199254740993, 42, 9007199254741035, 9007199254740000, 9007199254740993, -993, 0, 1035},
		{"rolled period with drifted lifetime total", 10 * gb, 20 * gb, 50 * gb, 40 * gb, 10 * gb, 10 * gb, 0, 10 * gb},
		{"baseline above lifetime", 5 * gb, 5 * gb, 10 * gb, 20 * gb, 5 * gb, 5 * gb, 0, 0},
		{"never rolled is measured exactly", 7 * gb, 11 * gb, 18 * gb, 0, 0, 0, 7 * gb, 11 * gb},
		{"never rolled with drifted total absorbs residual in download", 10 * gb, 20 * gb, 50 * gb, 0, 0, 0, 10 * gb, 40 * gb},
		{"fresh user", 0, 0, 0, 0, 0, 0, 0, 0},
	}
}

// assertPeriodSplitBackfill checks every users row against its case (rows were
// inserted in case order, so id order matches) and that the marker is single.
func assertPeriodSplitBackfill(t *testing.T, rows []userRow, cases []periodSplitBackfillCase) {
	t.Helper()
	if len(rows) != len(cases) {
		t.Fatalf("got %d users rows, want %d", len(rows), len(cases))
	}
	for i, tc := range cases {
		row := rows[i]
		if row.PeriodBaselineUpBytes != tc.wantBU || row.PeriodBaselineDownBytes != tc.wantBD {
			t.Errorf("%s: backfilled baselines = (up %d, down %d), want (%d, %d)",
				tc.name, row.PeriodBaselineUpBytes, row.PeriodBaselineDownBytes, tc.wantBU, tc.wantBD)
		}
		if row.PeriodBaselineBytes != tc.baseline || row.LifetimeTotalBytes != tc.total {
			t.Errorf("%s: backfill rewrote its inputs: total %d baseline %d", tc.name, row.LifetimeTotalBytes, row.PeriodBaselineBytes)
		}
		if up, down := row.toDomain().PeriodUsedSplit(); up != tc.wantUp || down != tc.wantDown {
			t.Errorf("%s: PeriodUsedSplit() = (%d, %d), want (%d, %d)", tc.name, up, down, tc.wantUp, tc.wantDown)
		}
	}
}

func assertPeriodSplitMarkerOnce(t *testing.T, db *gorm.DB) {
	t.Helper()
	var applied int64
	if err := db.Table("schema_migrations").Where("id = ?", userPeriodBaselineSplitMigrationID).Count(&applied).Error; err != nil || applied != 1 {
		t.Fatalf("%s marker count = %d (%v), want exactly 1", userPeriodBaselineSplitMigrationID, applied, err)
	}
}

// TestUserPeriodBaselineSplitBackfillFromV3 drives the backfill through the
// real V3 → V4 boot (frozen v3.9.2-beta.20 users table, no per-direction
// columns) and pins: the exact formula for PB > 0 and PB <= 0 rows, that the
// raw UPDATE leaves every pre-existing cell (updated_at included) untouched,
// one marker row, and that it never re-runs — a second boot after the
// lifetimes moved must not re-seed (that would silently move the period
// start of every rolled-over user's split).
func TestUserPeriodBaselineSplitBackfillFromV3(t *testing.T) {
	db, err := openIsolatedTestDB(t)
	if err != nil {
		t.Fatal(err)
	}
	seedV3Baseline(t, db)
	cases := periodSplitBackfillCases()
	when := time.Date(2026, 7, 1, 2, 3, 4, 0, time.UTC)
	for i, tc := range cases {
		row := &v392Beta20UserRow{
			UPN: fmt.Sprintf("split-v3-%d", i), Role: "user", SubToken: fmt.Sprintf("split-v3-token-%d", i),
			UUID: fmt.Sprintf("00000000-0000-0000-0000-%012d", 600+i), GroupID: 1, Enabled: true,
			TrafficResetPeriod: "month", TrafficPeriodStart: &when,
			LifetimeUpBytes: tc.up, LifetimeDownBytes: tc.down, LifetimeTotalBytes: tc.total, PeriodBaselineBytes: tc.baseline,
			CreatedAt: when, UpdatedAt: when,
		}
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	beforeFrozen := v392ReadRows[v392Beta20UserRow](t, db)
	if err := EnsureSchema(db); err != nil {
		t.Fatalf("V4 boot: %v", err)
	}
	if !reflect.DeepEqual(v392ReadRows[v392Beta20UserRow](t, db), beforeFrozen) {
		t.Fatal("backfill rewrote a pre-existing users cell (updated_at bump?)")
	}
	assertPeriodSplitBackfill(t, v392ReadRows[userRow](t, db), cases)
	assertPeriodSplitMarkerOnce(t, db)

	// Traffic moves on; a later boot must leave the seeded baselines alone.
	if err := db.Exec("UPDATE users SET lifetime_up_bytes = lifetime_up_bytes + 1000, lifetime_down_bytes = lifetime_down_bytes + 2000, lifetime_total_bytes = lifetime_total_bytes + 3000").Error; err != nil {
		t.Fatal(err)
	}
	if err := EnsureSchema(db); err != nil {
		t.Fatalf("second V4 boot: %v", err)
	}
	rows := v392ReadRows[userRow](t, db)
	for i, tc := range cases {
		if rows[i].PeriodBaselineUpBytes != tc.wantBU || rows[i].PeriodBaselineDownBytes != tc.wantBD {
			t.Errorf("%s: second boot re-seeded baselines to (up %d, down %d), want unchanged (%d, %d)",
				tc.name, rows[i].PeriodBaselineUpBytes, rows[i].PeriodBaselineDownBytes, tc.wantBU, tc.wantBD)
		}
	}
	assertPeriodSplitMarkerOnce(t, db)
}

// TestUserPeriodBaselineSplitBackfillOnCompletedV4Install pins where the
// backfill is dispatched: an existing V4 install already carries
// v3_to_v4_baseline_v1, so migrateV3ToV4 returns early there. A backfill
// wired inside that bridge would never run for the deployments that actually
// need it and leave every rolled-over user's header counting lifetime upload.
func TestUserPeriodBaselineSplitBackfillOnCompletedV4Install(t *testing.T) {
	db, err := openIsolatedTestDB(t)
	if err != nil {
		t.Fatal(err)
	}
	if err := EnsureSchema(db); err != nil {
		t.Fatalf("fresh V4 boot: %v", err)
	}
	// A fresh install stamps the marker over an empty table.
	assertPeriodSplitMarkerOnce(t, db)
	cases := periodSplitBackfillCases()
	for i, tc := range cases {
		row := &userRow{
			UPN: fmt.Sprintf("split-v4-%d", i), Role: "user", SubToken: fmt.Sprintf("split-v4-token-%d", i),
			UUID: fmt.Sprintf("00000000-0000-0000-0000-%012d", 700+i), GroupID: 1, Enabled: true,
			TrafficResetPeriod: "month",
			LifetimeUpBytes:    tc.up, LifetimeDownBytes: tc.down, LifetimeTotalBytes: tc.total, PeriodBaselineBytes: tc.baseline,
		}
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	// Rewind to a completed V4 install that predates the columns.
	for _, column := range []string{"period_baseline_up_bytes", "period_baseline_down_bytes"} {
		if err := db.Migrator().DropColumn(&userRow{}, column); err != nil {
			t.Fatalf("drop %s: %v", column, err)
		}
	}
	if err := db.Where("id = ?", userPeriodBaselineSplitMigrationID).Delete(&schemaMigrationRow{}).Error; err != nil {
		t.Fatal(err)
	}
	if err := EnsureSchema(db); err != nil {
		t.Fatalf("upgrade boot: %v", err)
	}
	assertPeriodSplitBackfill(t, v392ReadRows[userRow](t, db), cases)
	assertPeriodSplitMarkerOnce(t, db)
}
