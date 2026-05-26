package mysql

import (
	"context"
	"time"

	"gorm.io/gorm"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

type syncTaskRepo struct{ db *gorm.DB }

func (r *syncTaskRepo) Create(ctx context.Context, task *domain.SyncTask) error {
	row := syncTaskFromDomain(task)
	if row.Status == "" {
		row.Status = string(domain.SyncTaskPending)
	}
	if row.NextRunAt.IsZero() {
		row.NextRunAt = time.Now()
	}
	if err := r.db.WithContext(ctx).Create(row).Error; err != nil {
		return err
	}
	*task = *row.toDomain()
	return nil
}

func (r *syncTaskRepo) GetByID(ctx context.Context, id int64) (*domain.SyncTask, error) {
	var row syncTaskRow
	if err := r.db.WithContext(ctx).First(&row, id).Error; err != nil {
		return nil, wrapNotFound(err)
	}
	return row.toDomain(), nil
}

func (r *syncTaskRepo) GetActiveByTarget(ctx context.Context, typ domain.SyncTaskType, targetType string, targetID int64) (*domain.SyncTask, error) {
	var row syncTaskRow
	err := r.db.WithContext(ctx).
		Where("type = ? AND target_type = ? AND target_id = ? AND status IN ?",
			string(typ), targetType, targetID,
			[]string{string(domain.SyncTaskPending), string(domain.SyncTaskRunning)}).
		Order("id DESC").
		First(&row).Error
	if err != nil {
		return nil, wrapNotFound(err)
	}
	return row.toDomain(), nil
}

func (r *syncTaskRepo) HasActiveByTargetAny(ctx context.Context, types []domain.SyncTaskType, targetType string, targetID int64) (bool, error) {
	if len(types) == 0 {
		return false, nil
	}
	typeStrings := make([]string, len(types))
	for i, t := range types {
		typeStrings[i] = string(t)
	}
	var n int64
	err := r.db.WithContext(ctx).Model(&syncTaskRow{}).
		Where("target_type = ? AND target_id = ? AND type IN ? AND status IN ?",
			targetType, targetID,
			typeStrings,
			[]string{string(domain.SyncTaskPending), string(domain.SyncTaskRunning)}).
		Limit(1).
		Count(&n).Error
	return n > 0, err
}

func (r *syncTaskRepo) List(ctx context.Context, filter ports.SyncTaskFilter) ([]*domain.SyncTask, int64, error) {
	q := r.db.WithContext(ctx).Model(&syncTaskRow{})
	if filter.Status != nil {
		q = q.Where("status = ?", string(*filter.Status))
	}
	if filter.Type != nil {
		q = q.Where("type = ?", string(*filter.Type))
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if filter.SortDir == "" {
		filter.SortDir = "desc"
	}
	var rows []syncTaskRow
	if err := applyPagination(q, filter.Pagination, syncTaskSortAllowlist, "id").Find(&rows).Error; err != nil {
		return nil, 0, err
	}
	out := make([]*domain.SyncTask, len(rows))
	for i := range rows {
		out[i] = rows[i].toDomain()
	}
	return out, total, nil
}

var syncTaskSortAllowlist = map[string]string{
	"id":          "id",
	"type":        "type",
	"status":      "status",
	"attempts":    "attempts",
	"next_run_at": "next_run_at",
	"created_at":  "created_at",
}

func (r *syncTaskRepo) ListDue(ctx context.Context, now time.Time, limit int) ([]*domain.SyncTask, error) {
	if limit <= 0 {
		limit = 20
	}
	var rows []syncTaskRow
	if err := r.db.WithContext(ctx).
		Where("status = ? AND next_run_at <= ?", string(domain.SyncTaskPending), now).
		Order("next_run_at ASC, id ASC").
		Limit(limit).
		Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]*domain.SyncTask, len(rows))
	for i := range rows {
		out[i] = rows[i].toDomain()
	}
	return out, nil
}

func (r *syncTaskRepo) MarkRunning(ctx context.Context, id int64) error {
	return r.db.WithContext(ctx).Model(&syncTaskRow{}).Where("id = ? AND status = ?", id, string(domain.SyncTaskPending)).
		Updates(map[string]any{"status": string(domain.SyncTaskRunning)}).Error
}

func (r *syncTaskRepo) MarkSucceeded(ctx context.Context, id int64) error {
	now := time.Now()
	return r.db.WithContext(ctx).Model(&syncTaskRow{}).Where("id = ? AND status <> ?", id, string(domain.SyncTaskCanceled)).
		Updates(map[string]any{
			"status":      string(domain.SyncTaskSucceeded),
			"last_error":  "",
			"finished_at": &now,
		}).Error
}

func (r *syncTaskRepo) MarkRetry(ctx context.Context, id int64, lastError string, nextRunAt time.Time) error {
	return r.db.WithContext(ctx).Model(&syncTaskRow{}).Where("id = ? AND status <> ?", id, string(domain.SyncTaskCanceled)).
		Updates(map[string]any{
			"status":      string(domain.SyncTaskPending),
			"last_error":  lastError,
			"attempts":    gorm.Expr("attempts + 1"),
			"next_run_at": nextRunAt,
		}).Error
}

func (r *syncTaskRepo) Cancel(ctx context.Context, id int64) error {
	now := time.Now()
	return r.db.WithContext(ctx).Model(&syncTaskRow{}).Where("id = ? AND status IN ?", id,
		[]string{string(domain.SyncTaskPending), string(domain.SyncTaskRunning)}).
		Updates(map[string]any{
			"status":      string(domain.SyncTaskCanceled),
			"finished_at": &now,
		}).Error
}

func (r *syncTaskRepo) RetryNow(ctx context.Context, id int64) error {
	return r.db.WithContext(ctx).Model(&syncTaskRow{}).Where("id = ?", id).
		Updates(map[string]any{
			"status":      string(domain.SyncTaskPending),
			"last_error":  "",
			"next_run_at": time.Now(),
			"finished_at": nil,
		}).Error
}

func (r *syncTaskRepo) ResetRunning(ctx context.Context) error {
	return r.db.WithContext(ctx).Model(&syncTaskRow{}).Where("status = ?", string(domain.SyncTaskRunning)).
		Updates(map[string]any{
			"status":      string(domain.SyncTaskPending),
			"next_run_at": time.Now(),
		}).Error
}

func (r *syncTaskRepo) DeleteSucceededBefore(ctx context.Context, cutoff time.Time) (int64, error) {
	res := r.db.WithContext(ctx).
		Where("status = ? AND finished_at IS NOT NULL AND finished_at < ?",
			string(domain.SyncTaskSucceeded), cutoff).
		Delete(&syncTaskRow{})
	return res.RowsAffected, res.Error
}

func (r *syncTaskRepo) DeleteFinished(ctx context.Context) (int64, error) {
	// Anything not currently pending/running is fair game for the
	// one-click clear — succeeded, canceled, and any future "failed".
	res := r.db.WithContext(ctx).
		Where("status NOT IN ?", []string{
			string(domain.SyncTaskPending),
			string(domain.SyncTaskRunning),
		}).
		Delete(&syncTaskRow{})
	return res.RowsAffected, res.Error
}
