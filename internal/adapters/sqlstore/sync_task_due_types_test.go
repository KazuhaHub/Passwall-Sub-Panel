package sqlstore

import (
	"context"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// A burst of due mail tasks at the head of the queue used to fill the whole
// ListDue window, so the user processor never saw the one user task behind
// them. Filtering by type in SQL makes each consumer's window its own.
func TestSyncTaskListDueFiltersByType(t *testing.T) {
	repo := retiredSyncTaskTestRepo(t)
	ctx := context.Background()
	base := time.Now().Add(-time.Hour)
	for i := 0; i < 30; i++ {
		if err := repo.Create(ctx, &domain.SyncTask{
			Type: domain.SyncTaskMailNotify, Status: domain.SyncTaskPending,
			TargetType: "user:mail", TargetID: int64(i + 1), NextRunAt: base.Add(time.Duration(i) * time.Second),
		}); err != nil {
			t.Fatal(err)
		}
	}
	// Later than every mail row: under the untyped query it sits at position 31.
	if err := repo.Create(ctx, &domain.SyncTask{
		Type: domain.SyncTaskUserResync, Status: domain.SyncTaskPending,
		TargetType: "user", TargetID: 1, NextRunAt: base.Add(time.Minute),
	}); err != nil {
		t.Fatal(err)
	}

	user, err := repo.ListDue(ctx, time.Now(), []domain.SyncTaskType{domain.SyncTaskUserResync, domain.SyncTaskUserDelete}, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(user) != 1 || user[0].Type != domain.SyncTaskUserResync {
		t.Fatalf("user window = %+v, want exactly the user_resync task", user)
	}

	mail, err := repo.ListDue(ctx, time.Now(), []domain.SyncTaskType{domain.SyncTaskMailNotify}, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(mail) != 20 {
		t.Fatalf("mail window = %d rows, want 20", len(mail))
	}
	for i, task := range mail {
		if task.Type != domain.SyncTaskMailNotify {
			t.Fatalf("mail window row %d has foreign type %s", i, task.Type)
		}
		if i > 0 && task.NextRunAt.Before(mail[i-1].NextRunAt) {
			t.Fatalf("mail window not ordered by next_run_at at row %d", i)
		}
	}

	// No owned types means "owns nothing", never "owns everything".
	none, err := repo.ListDue(ctx, time.Now(), nil, 20)
	if err != nil || len(none) != 0 {
		t.Fatalf("ListDue(no types) = %v, %v, want empty", none, err)
	}
}
