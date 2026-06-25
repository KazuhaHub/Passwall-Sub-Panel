package sqlstore

import (
	"context"
	"fmt"
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
// BatchUpdateLastOnline (last_online_at), plus sub.go's
// UpdateBlockViolation. Update() loads the row, the admin mutates
// fields in a dialog, then Save() writes the whole row back; if those
// columns aren't omitted the admin's stale snapshot rolls lifetime
// back, stomps a last-online value the poll just wrote 50ms ago, OR
// rewinds the blocked-client violation counter to whatever it was
// when the dialog opened (defeating the auto-suspend threshold —
// admin "save profile" between a violation increment and the next
// /sub poll resets the counter to its pre-violation value).
// The emergency-access columns are ALSO omitted: emergencyMu only serializes
// the emergency subsystem against itself, not against the broad mutators
// (UpdateProfile, etc.) that call Update WITHOUT the lock. Without omitting them,
// an admin's read-modify-Save that brackets a concurrent UseEmergencyAccess
// grant (or the poll's ClearEmergencyAccess) reverts the just-granted window
// from the dialog's stale snapshot. Emergency columns are written ONLY through
// the targeted GrantEmergencyAccess / ClearEmergencyAccess writers.
var pollOwnedColumns = []string{
	// BatchUpdateTrafficState / UpdateTrafficState
	"lifetime_up_bytes", "lifetime_down_bytes", "lifetime_total_bytes",
	"period_baseline_bytes", "lifetime_baseline_at", "traffic_period_start",
	// BatchUpdateLastOnline
	"last_online_at",
	// AdvanceBlockViolation (sub.go blocked-client path)
	"block_violation_count", "last_block_violation_at",
	// GrantEmergencyAccess / ClearEmergencyAccess (emergency subsystem)
	"emergency_until", "emergency_used_count", "emergency_baseline_bytes",
	// 2FA — written ONLY via SetTOTP / SetRecoveryCodes / ClearTOTP so a stale
	// edit-dialog Save can't re-enable a just-disabled factor (or wipe a secret).
	"totp_secret", "totp_enabled", "recovery_codes",
}

func (r *userRepo) Update(ctx context.Context, u *domain.User) error {
	return r.db.WithContext(ctx).Omit(pollOwnedColumns...).Save(userFromDomain(u)).Error
}

// SetTOTP writes the TOTP secret (encrypted at rest), the enabled flag, and the
// recovery-code hashes in one column-scoped update. Begin passes enabled=false
// with no codes (a not-yet-confirmed secret); Enable passes enabled=true with
// the freshly-generated recovery hashes.
func (r *userRepo) SetTOTP(ctx context.Context, userID int64, secret string, enabled bool, recoveryHashes []string) error {
	if userID == 0 {
		return fmt.Errorf("SetTOTP requires a non-zero user ID")
	}
	enc, err := encryptSecret(secret)
	if err != nil {
		return err
	}
	if recoveryHashes == nil {
		recoveryHashes = []string{}
	}
	return r.db.WithContext(ctx).Model(&userRow{}).Where("id = ?", userID).
		Updates(map[string]any{
			"totp_secret":    enc,
			"totp_enabled":   enabled,
			"recovery_codes": jsonStrings(recoveryHashes),
		}).Error
}

// GetTOTP reads a user's decrypted TOTP secret, enabled flag, and recovery-code
// hashes — the only place the secret is decrypted.
func (r *userRepo) GetTOTP(ctx context.Context, userID int64) (secret string, enabled bool, recoveryHashes []string, err error) {
	var row userRow
	if e := r.db.WithContext(ctx).Select("totp_secret", "totp_enabled", "recovery_codes").
		First(&row, userID).Error; e != nil {
		return "", false, nil, wrapNotFound(e)
	}
	dec, e := decryptSecret(row.TOTPSecret)
	if e != nil {
		return "", false, nil, e
	}
	return dec, row.TOTPEnabled, []string(row.RecoveryCodes), nil
}

// SetRecoveryCodes replaces the stored recovery-code hashes (used to consume a
// redeemed code by writing back the remaining set).
func (r *userRepo) SetRecoveryCodes(ctx context.Context, userID int64, recoveryHashes []string) error {
	if userID == 0 {
		return fmt.Errorf("SetRecoveryCodes requires a non-zero user ID")
	}
	if recoveryHashes == nil {
		recoveryHashes = []string{}
	}
	return r.db.WithContext(ctx).Model(&userRow{}).Where("id = ?", userID).
		Update("recovery_codes", jsonStrings(recoveryHashes)).Error
}

// ConsumeRecoveryCode atomically swaps the stored recovery-code hashes from
// prevHashes to nextHashes, returning true only when this call won (RowsAffected
// == 1). The prior value lives in the WHERE clause (compare-and-swap), so two
// concurrent redemptions of the same one-time code can't both succeed: the first
// changes recovery_codes, and the second's `recovery_codes = <old>` predicate is
// then false (0 rows) — the same pattern AdvanceBlockViolation uses for its
// concurrent-increment guard. recovery_codes is in pollOwnedColumns, so the
// generic Save path can't clobber this between read and swap.
func (r *userRepo) ConsumeRecoveryCode(ctx context.Context, userID int64, prevHashes, nextHashes []string) (bool, error) {
	if userID == 0 {
		return false, fmt.Errorf("ConsumeRecoveryCode requires a non-zero user ID")
	}
	if nextHashes == nil {
		nextHashes = []string{}
	}
	res := r.db.WithContext(ctx).Model(&userRow{}).
		Where("id = ? AND recovery_codes = ?", userID, jsonStrings(prevHashes)).
		Update("recovery_codes", jsonStrings(nextHashes))
	if res.Error != nil {
		return false, res.Error
	}
	return res.RowsAffected == 1, nil
}

// ClearTOTP disables 2FA and wipes the secret + recovery codes.
func (r *userRepo) ClearTOTP(ctx context.Context, userID int64) error {
	if userID == 0 {
		return fmt.Errorf("ClearTOTP requires a non-zero user ID")
	}
	return r.db.WithContext(ctx).Model(&userRow{}).Where("id = ?", userID).
		Updates(map[string]any{
			"totp_secret":    "",
			"totp_enabled":   false,
			"recovery_codes": jsonStrings([]string{}),
		}).Error
}

// CountEnabledAdmins counts enabled admin accounts (last-admin lockout guard).
func (r *userRepo) CountEnabledAdmins(ctx context.Context) (int64, error) {
	var n int64
	err := r.db.WithContext(ctx).Model(&userRow{}).
		Where("role = ? AND enabled = ?", string(domain.RoleAdmin), true).
		Count(&n).Error
	return n, err
}

// CountByStatus tallies the dashboard summary counters with COUNT queries
// instead of loading every user row. Disabled is derived (Total-Enabled) to save
// a query.
func (r *userRepo) CountByStatus(ctx context.Context, now time.Time) (ports.UserStatusCounts, error) {
	var c ports.UserStatusCounts
	if err := r.db.WithContext(ctx).Model(&userRow{}).Count(&c.Total).Error; err != nil {
		return c, err
	}
	if err := r.db.WithContext(ctx).Model(&userRow{}).
		Where("enabled = ?", true).Count(&c.Enabled).Error; err != nil {
		return c, err
	}
	c.Disabled = c.Total - c.Enabled
	if err := r.db.WithContext(ctx).Model(&userRow{}).
		Where("emergency_until IS NOT NULL AND emergency_until > ?", now.UTC()).
		Count(&c.Emergency).Error; err != nil {
		return c, err
	}
	return c, nil
}

// ListExpiringBetween returns up to limit users with ExpireAt in [from, to],
// soonest first — the dashboard "expiring soon" list as one bounded query.
func (r *userRepo) ListExpiringBetween(ctx context.Context, from, to time.Time, limit int) ([]*domain.User, error) {
	if limit <= 0 {
		limit = 5
	}
	var rows []userRow
	err := r.db.WithContext(ctx).
		Where("expire_at IS NOT NULL AND expire_at >= ? AND expire_at <= ?", from.UTC(), to.UTC()).
		Order("expire_at ASC").
		Limit(limit).
		Find(&rows).Error
	if err != nil {
		return nil, err
	}
	out := make([]*domain.User, len(rows))
	for i := range rows {
		out[i] = rows[i].toDomain()
	}
	return out, nil
}

// AdvanceBlockViolation atomically advances the blocked-client violation count
// (see the ports.UserRepo doc). The dedup window lives in the WHERE clause so
// two concurrent /sub requests can't both advance: the first stamps
// last_block_violation_at=at, and the second's "older than notBefore"
// predicate is then false so it updates 0 rows. RowsAffected==1 => advanced;
// the resulting count is read back for the threshold check.
func (r *userRepo) AdvanceBlockViolation(ctx context.Context, userID int64, notBefore, at time.Time, detail string) (int, bool, error) {
	if userID == 0 {
		return 0, false, fmt.Errorf("AdvanceBlockViolation requires a non-zero user ID")
	}
	res := r.db.WithContext(ctx).
		Model(&userRow{}).
		Where("id = ? AND (last_block_violation_at IS NULL OR last_block_violation_at < ?)", userID, notBefore).
		Updates(map[string]any{
			"block_violation_count":   gorm.Expr("block_violation_count + 1"),
			"last_block_violation_at": at,
		})
	if res.Error != nil {
		return 0, false, res.Error
	}
	if res.RowsAffected == 0 {
		return 0, false, nil // inside the dedup window (or no such user) — not advanced
	}
	var count int
	if err := r.db.WithContext(ctx).Model(&userRow{}).Where("id = ?", userID).
		Pluck("block_violation_count", &count).Error; err != nil {
		return 0, true, err
	}
	return count, true, nil
}

// ClearBlockViolation resets the blocked-client tracking columns when a
// user's blocked-client service suspension is restored. Without this, a user
// who was auto-suspended at the
// SubBlockAutoDisableCount threshold (default 5) keeps their count at
// 5 across the admin's manual service restore — the very next /sub fetch
// with a blocked client increments past the threshold and re-suspends
// instantly.
// Column-scoped because pollOwnedColumns omits these from the regular
// userRepo.Update path (to protect sub.go's concurrent increment).
func (r *userRepo) ClearBlockViolation(ctx context.Context, userID int64) error {
	if userID == 0 {
		return fmt.Errorf("ClearBlockViolation requires a non-zero user ID")
	}
	return r.db.WithContext(ctx).
		Model(&userRow{}).
		Where("id = ?", userID).
		Updates(map[string]any{
			"block_violation_count":   0,
			"last_block_violation_at": nil,
		}).Error
}

func (r *userRepo) UpdateServiceState(ctx context.Context, userID int64, reason domain.AutoDisabledReason, detail string, disabledAt *time.Time) error {
	if userID == 0 {
		return fmt.Errorf("UpdateServiceState requires a non-zero user ID")
	}
	return r.db.WithContext(ctx).
		Model(&userRow{}).
		Where("id = ?", userID).
		Updates(map[string]any{
			"service_disabled_reason": string(reason),
			"service_disable_detail":  detail,
			"service_disabled_at":     disabledAt,
		}).Error
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

// GrantEmergencyAccess writes the emergency window for one user via a targeted
// write (the grant counterpart of ClearEmergencyAccess). UseEmergencyAccess
// calls it under the service's emergency lock so the broad Update — which now
// omits the emergency columns — can never revert a just-granted window from a
// concurrent admin edit's stale snapshot.
func (r *userRepo) GrantEmergencyAccess(ctx context.Context, userID int64, until time.Time, usedCount int, baselineBytes int64) error {
	if userID == 0 {
		return fmt.Errorf("GrantEmergencyAccess requires a non-zero user ID")
	}
	return r.db.WithContext(ctx).
		Model(&userRow{}).
		Where("id = ?", userID).
		Updates(map[string]any{
			"emergency_until":          until,
			"emergency_used_count":     usedCount,
			"emergency_baseline_bytes": baselineBytes,
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
	if like := keywordLike(filter.Search); like != "" {
		// Search across the user-facing identifiers admins actually scan
		// the table for: account name, friendly display, email. Remark is
		// intentionally out — it's free-form admin notes; matching on it
		// surfaced "why does this user show up?" results that confused
		// people.
		q = q.Where(likeCols("upn", "display_name", "email"), like, like, like)
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
