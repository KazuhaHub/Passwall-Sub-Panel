package user

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// The task types a sync-status read covers. Fixed, not caller-supplied: the
// answer is only meaningful next to the set it was computed from, and widening
// the set changes the meaning of "no active tasks".
//
// This is deliberately NOT the same set HasPendingSync checks — that one omits
// user_migrate, so it can report "nothing pending" while a shared-client
// migration is still running.
var syncStatusTaskTypes = []domain.SyncTaskType{
	domain.SyncTaskUserDelete,
	domain.SyncTaskUserResync,
	domain.SyncTaskUserPushConfig,
	domain.SyncTaskUserMigrate,
}

const (
	// Response caps, not business limits. The extra row is only read to detect
	// truncation; it is never returned.
	syncStatusActiveLimit   = 20
	syncStatusTerminalLimit = 10
)

// SyncStatusState is what a read observed about local tasks. There is
// deliberately no value meaning "upstream is in sync": no active task is
// evidence about the queue, not about the panel. See ADR 0034.
type SyncStatusState string

const (
	SyncStatusActiveTasks   SyncStatusState = "active_tasks"
	SyncStatusNoActiveTasks SyncStatusState = "no_active_tasks"
)

// SyncStatusHistoryScope marks the terminal list as what is still stored, so a
// purged history is never read as "nothing ever ran".
const SyncStatusHistoryScopeRetained = "retained_only"

// SyncTaskView is the subset of a task a status read may expose. Payload,
// Summary and LastError are excluded: a task payload can carry subscription
// credentials, and an upstream error text can carry an endpoint or a token.
type SyncTaskView struct {
	ID         int64
	Type       domain.SyncTaskType
	Status     domain.SyncTaskStatus
	Attempts   int
	NextRunAt  time.Time
	CreatedAt  time.Time
	UpdatedAt  time.Time
	FinishedAt *time.Time
	// HasError reports that an error was recorded, not what it was. It does not
	// distinguish "still retrying" from "gave up" — Status does that.
	HasError bool
}

// SyncStatus is one observation of a user's sync tasks.
type SyncStatus struct {
	TargetType   string
	TargetID     int64
	TargetExists bool
	ObservedAt   time.Time

	CoveredTaskTypes []domain.SyncTaskType
	State            SyncStatusState

	ActiveTasks          []SyncTaskView
	ActiveTasksTruncated bool

	RecentTerminalTasks []SyncTaskView
	HistoryTruncated    bool
	HistoryScope        string
}

func syncTaskView(t *domain.SyncTask) SyncTaskView {
	return SyncTaskView{
		ID: t.ID, Type: t.Type, Status: t.Status,
		Attempts: t.Attempts, NextRunAt: t.NextRunAt,
		CreatedAt: t.CreatedAt, UpdatedAt: t.UpdatedAt, FinishedAt: t.FinishedAt,
		HasError: t.LastError != "",
	}
}

// SyncStatus reports what local sync work is observable for one user.
//
// It never fails because the target is missing: an admin looking at a deleted
// user still needs to see what happened to its retained tasks, so a missing row
// is reported as TargetExists=false rather than as an error. It DOES fail when
// the task store cannot answer — reporting that as "no active tasks" is the
// behaviour ADR 0034 exists to stop.
//
// The read touches no upstream panel and changes no task state.
func (s *Service) SyncStatus(ctx context.Context, userID int64) (*SyncStatus, error) {
	if s.tasks == nil {
		return nil, fmt.Errorf("sync status: task store not configured: %w", domain.ErrUnavailable)
	}

	out := &SyncStatus{
		TargetType:       "user",
		TargetID:         userID,
		ObservedAt:       time.Now().UTC(),
		CoveredTaskTypes: append([]domain.SyncTaskType(nil), syncStatusTaskTypes...),
		HistoryScope:     SyncStatusHistoryScopeRetained,
	}

	switch _, err := s.users.GetByID(ctx, userID); {
	case err == nil:
		out.TargetExists = true
	case errors.Is(err, domain.ErrNotFound):
		out.TargetExists = false
	default:
		return nil, fmt.Errorf("sync status: resolve target: %w", domain.ErrUnavailable)
	}

	// One extra row per query is how truncation is detected without a count.
	active, err := s.tasks.ListActiveByTarget(ctx, syncStatusTaskTypes, "user", userID, syncStatusActiveLimit+1)
	if err != nil {
		return nil, fmt.Errorf("sync status: list active tasks: %w", domain.ErrUnavailable)
	}
	if len(active) > syncStatusActiveLimit {
		out.ActiveTasksTruncated = true
		active = active[:syncStatusActiveLimit]
	}
	out.ActiveTasks = make([]SyncTaskView, 0, len(active))
	for _, t := range active {
		out.ActiveTasks = append(out.ActiveTasks, syncTaskView(t))
	}

	terminal, err := s.tasks.ListTerminalByTarget(ctx, syncStatusTaskTypes, "user", userID, syncStatusTerminalLimit+1)
	if err != nil {
		return nil, fmt.Errorf("sync status: list terminal tasks: %w", domain.ErrUnavailable)
	}
	if len(terminal) > syncStatusTerminalLimit {
		out.HistoryTruncated = true
		terminal = terminal[:syncStatusTerminalLimit]
	}
	out.RecentTerminalTasks = make([]SyncTaskView, 0, len(terminal))
	for _, t := range terminal {
		out.RecentTerminalTasks = append(out.RecentTerminalTasks, syncTaskView(t))
	}

	if len(out.ActiveTasks) > 0 {
		out.State = SyncStatusActiveTasks
	} else {
		out.State = SyncStatusNoActiveTasks
	}
	return out, nil
}
