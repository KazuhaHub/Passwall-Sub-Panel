package mysql

import (
	"context"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

type userRepo struct{ db *gorm.DB }

func (r *userRepo) Create(ctx context.Context, u *domain.User) error {
	row := userFromDomain(u)
	if err := r.db.WithContext(ctx).Create(row).Error; err != nil {
		return err
	}
	u.ID = row.ID
	u.CreatedAt = row.CreatedAt
	u.UpdatedAt = row.UpdatedAt
	return nil
}

// pollOwnedColumns lists user columns owned by a non-Update writer that
// runs concurrently with admin edits — traffic poll's
// BatchUpdateTrafficState (lifetime counters + period baseline) and
// BatchUpdateLastOnline (last_online_at). Update() loads the row, the
// admin mutates fields in a dialog, then Save() writes the whole row
// back; if those columns aren't omitted the admin's stale snapshot
// rolls lifetime back or stomps a last-online value the poll just
// wrote 50ms ago. Emergency-access columns are intentionally NOT in
// this list — UseEmergencyAccess goes through Update too, and the
// race with admin editing the SAME user concurrently is narrowed by
// emergencyMu at the service layer rather than the repo guard.
var pollOwnedColumns = []string{
	// BatchUpdateTrafficState / UpdateTrafficState
	"lifetime_up_bytes", "lifetime_down_bytes", "lifetime_total_bytes",
	"period_baseline_bytes", "lifetime_baseline_at", "traffic_period_start",
	// BatchUpdateLastOnline
	"last_online_at",
}

func (r *userRepo) Update(ctx context.Context, u *domain.User) error {
	return r.db.WithContext(ctx).Omit(pollOwnedColumns...).Save(userFromDomain(u)).Error
}

// UpdateTrafficState writes only the columns the traffic poll owns, via a
// map so zero-values (e.g. resetting period_baseline_bytes to 0) are persisted.
// Keeps a slow poll cycle from clobbering concurrent admin / self-service edits
// to other columns. The emergency-access columns are intentionally NOT written
// here — see ClearEmergencyAccess and the interface doc.
func (r *userRepo) UpdateTrafficState(ctx context.Context, u *domain.User) error {
	if u == nil || u.ID == 0 {
		return fmt.Errorf("UpdateTrafficState requires a non-zero user ID; got %+v", u)
	}
	return r.db.WithContext(ctx).
		Model(&userRow{}).
		Where("id = ?", u.ID).
		Updates(map[string]any{
			"lifetime_up_bytes":     u.LifetimeUpBytes,
			"lifetime_down_bytes":   u.LifetimeDownBytes,
			"lifetime_total_bytes":  u.LifetimeTotalBytes,
			"period_baseline_bytes": u.PeriodBaselineBytes,
			"lifetime_baseline_at":  u.LifetimeBaselineAt,
			"traffic_period_start":  u.TrafficPeriodStart,
		}).Error
}

// BatchUpdateTrafficState runs N UpdateTrafficState writes wrapped in one
// transaction. The win is SQLite-specific: each per-row UPDATE in auto-commit
// mode is its own ~5–10ms WAL fsync, so PollOnce's hot loop (one write per
// user, plus per-client BatchUpdateCounters below) used to spend most of its
// wall time waiting on commits rather than doing real work. Wrapping the N
// statements in a single transaction collapses them to a single commit at
// the end. MySQL/Postgres get the smaller round-trip win.
//
// Column scope and emergency-column skip are identical to UpdateTrafficState;
// see that method's doc for the rationale on why the narrow write matters.
// No-op on an empty slice so callers don't need to guard the no-users path.
func (r *userRepo) BatchUpdateTrafficState(ctx context.Context, users []*domain.User) error {
	if len(users) == 0 {
		return nil
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for _, u := range users {
			if u == nil || u.ID == 0 {
				return fmt.Errorf("BatchUpdateTrafficState requires a non-zero user ID; got %+v", u)
			}
			err := tx.Model(&userRow{}).
				Where("id = ?", u.ID).
				Updates(map[string]any{
					"lifetime_up_bytes":     u.LifetimeUpBytes,
					"lifetime_down_bytes":   u.LifetimeDownBytes,
					"lifetime_total_bytes":  u.LifetimeTotalBytes,
					"period_baseline_bytes": u.PeriodBaselineBytes,
					"lifetime_baseline_at":  u.LifetimeBaselineAt,
					"traffic_period_start":  u.TrafficPeriodStart,
				}).Error
			if err != nil {
				return err
			}
		}
		return nil
	})
}

// BatchUpdateLastOnline writes per-user last_online_at via a single
// transaction with N column-scoped UPDATEs — same batching rationale as
// BatchUpdateTrafficState. Each entry overwrites the row's last_online_at
// unconditionally; on a transient panel outage where the new max may be
// older than what we previously stored, this can produce a brief backward
// step until the next poll cycle re-reads the missing panel. Acceptable
// for an advisory "last seen" display at self-hosted scale; if the value
// ever drives policy (auto-disable on inactivity etc.) revisit and add a
// "WHERE last_online_at IS NULL OR last_online_at < ?" guard.
func (r *userRepo) BatchUpdateLastOnline(ctx context.Context, lastOnline map[int64]time.Time) error {
	if len(lastOnline) == 0 {
		return nil
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for uid, ts := range lastOnline {
			if uid == 0 {
				return fmt.Errorf("BatchUpdateLastOnline requires non-zero user IDs; got %d", uid)
			}
			if err := tx.Model(&userRow{}).
				Where("id = ?", uid).
				Updates(map[string]any{"last_online_at": ts}).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

// ClearEmergencyAccess nulls the emergency window for one user via a targeted
// write (map so the zero/NULL values land). Used by the traffic poll under the
// emergency lock; keeps emergency clearing out of UpdateTrafficState's stale
// per-cycle write.
func (r *userRepo) ClearEmergencyAccess(ctx context.Context, userID int64) error {
	if userID == 0 {
		return fmt.Errorf("ClearEmergencyAccess requires a non-zero user ID")
	}
	return r.db.WithContext(ctx).
		Model(&userRow{}).
		Where("id = ?", userID).
		Updates(map[string]any{
			"emergency_until":          nil,
			"emergency_baseline_bytes": 0,
		}).Error
}

func (r *userRepo) Delete(ctx context.Context, id int64) error {
	return r.db.WithContext(ctx).Delete(&userRow{}, id).Error
}

func (r *userRepo) GetByID(ctx context.Context, id int64) (*domain.User, error) {
	var row userRow
	if err := r.db.WithContext(ctx).First(&row, id).Error; err != nil {
		return nil, wrapNotFound(err)
	}
	return row.toDomain(), nil
}

func (r *userRepo) GetByUPN(ctx context.Context, upn string) (*domain.User, error) {
	var row userRow
	if err := r.db.WithContext(ctx).Where("upn = ?", upn).First(&row).Error; err != nil {
		return nil, wrapNotFound(err)
	}
	return row.toDomain(), nil
}

func (r *userRepo) GetBySSO(ctx context.Context, provider, subject string) (*domain.User, error) {
	if provider == "" || subject == "" {
		return nil, domain.ErrNotFound
	}
	var row userRow
	if err := r.db.WithContext(ctx).
		Where("sso_provider = ? AND sso_subject = ?", provider, subject).
		First(&row).Error; err != nil {
		return nil, wrapNotFound(err)
	}
	return row.toDomain(), nil
}

func (r *userRepo) GetBySubToken(ctx context.Context, token string) (*domain.User, error) {
	var row userRow
	if err := r.db.WithContext(ctx).Where("sub_token = ?", token).First(&row).Error; err != nil {
		return nil, wrapNotFound(err)
	}
	return row.toDomain(), nil
}

func (r *userRepo) List(ctx context.Context, filter ports.UserFilter) ([]*domain.User, int64, error) {
	q := r.db.WithContext(ctx).Model(&userRow{})
	if filter.Search != "" {
		// Escape LIKE wildcards before wrapping so a search string like
		// "%" doesn't turn into a full-table scan, and "_" doesn't match
		// every single-character UPN. Order matters: replace backslash
		// first so the subsequent escapes don't double-up.
		s := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(filter.Search)
		// Lowercase the pattern and LOWER() the columns so search is
		// case-insensitive on every backend. SQLite and MySQL LIKE already
		// ignore ASCII case, but Postgres LIKE is case-sensitive (it reserves
		// ILIKE for that) — without this, "john" would miss "John" on PG.
		// These columns are matched with a leading-% pattern that can't use a
		// B-tree index anyway, so wrapping them in LOWER() costs nothing.
		like := "%" + strings.ToLower(s) + "%"
		// Search across the user-facing identifiers admins actually scan
		// the table for: account name, friendly display, email. Remark is
		// intentionally out — it's free-form admin notes; matching on it
		// surfaced "why does this user show up?" results that confused
		// people.
		q = q.Where("LOWER(upn) LIKE ? OR LOWER(display_name) LIKE ? OR LOWER(email) LIKE ?", like, like, like)
	}
	if filter.GroupID != nil {
		q = q.Where("group_id = ?", *filter.GroupID)
	}
	if filter.Role != nil {
		q = q.Where("role = ?", string(*filter.Role))
	}
	if filter.Enabled != nil {
		q = q.Where("enabled = ?", *filter.Enabled)
	}

	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	var rows []userRow
	if err := applyPagination(q, filter.Pagination, userSortAllowlist, "id").Find(&rows).Error; err != nil {
		return nil, 0, err
	}
	out := make([]*domain.User, len(rows))
	for i := range rows {
		out[i] = rows[i].toDomain()
	}
	return out, total, nil
}

var userSortAllowlist = map[string]string{
	"id":             "id",
	"upn":            "upn",
	"email":          "email",
	"display_name":   "display_name",
	"role":           "role",
	"group_id":       "group_id",
	"enabled":        "enabled",
	"created_at":     "created_at",
	"expire_at":      "expire_at",
	"last_online_at": "last_online_at",
}

func (r *userRepo) ListByGroup(ctx context.Context, groupID int64) ([]*domain.User, error) {
	var rows []userRow
	if err := r.db.WithContext(ctx).Where("group_id = ?", groupID).Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]*domain.User, len(rows))
	for i := range rows {
		out[i] = rows[i].toDomain()
	}
	return out, nil
}
