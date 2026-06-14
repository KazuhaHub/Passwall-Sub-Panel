package mysql

import (
	"context"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// TestSyncTaskMarkRunningReportsClaim locks the claim semantics that stop a
// just-canceled task from still executing its 3X-UI side effect. MarkRunning
// must report claimed=true for a Pending task (flipping it to Running) and
// claimed=false for a task Canceled in the window between ListDue and the
// claim — in which case the loop skips the (irreversible) side effect rather
// than running a task the admin already canceled.
func TestSyncTaskMarkRunningReportsClaim(t *testing.T) {
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
	repo := &syncTaskRepo{db: db}
	ctx := context.Background()

	// Pending task → claimed, and flipped to Running.
	pending := &domain.SyncTask{Type: domain.SyncTaskUserResync, TargetType: "user", TargetID: 1}
	if err := repo.Create(ctx, pending); err != nil {
		t.Fatalf("create pending: %v", err)
	}
	claimed, err := repo.MarkRunning(ctx, pending.ID)
	if err != nil {
		t.Fatalf("MarkRunning(pending): %v", err)
	}
	if !claimed {
		t.Fatalf("MarkRunning on a Pending task must report claimed=true")
	}
	if got, _ := repo.GetByID(ctx, pending.ID); got.Status != domain.SyncTaskRunning {
		t.Fatalf("status after claim = %q, want Running", got.Status)
	}

	// Task canceled before the claim → NOT claimed, status stays Canceled.
	canceled := &domain.SyncTask{Type: domain.SyncTaskUserResync, TargetType: "user", TargetID: 2}
	if err := repo.Create(ctx, canceled); err != nil {
		t.Fatalf("create canceled: %v", err)
	}
	if err := repo.Cancel(ctx, canceled.ID); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	claimed, err = repo.MarkRunning(ctx, canceled.ID)
	if err != nil {
		t.Fatalf("MarkRunning(canceled): %v", err)
	}
	if claimed {
		t.Fatalf("MarkRunning on a Canceled task must report claimed=false (so the loop skips its side effect)")
	}
	if got, _ := repo.GetByID(ctx, canceled.ID); got.Status != domain.SyncTaskCanceled {
		t.Fatalf("status = %q, want Canceled (claim must not resurrect a canceled task)", got.Status)
	}
}
