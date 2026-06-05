package mysql

import (
	"context"
	"time"

	"gorm.io/gorm"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// authTokenRow backs domain.AuthToken — the one-time hashed credential for
// self-service auth flows (password recovery, email verification). The
// (user_id, purpose) index serves OTP lookups and per-user invalidation; the
// token_hash index serves link lookups; the expires_at index serves the
// retention prune.
type authTokenRow struct {
	ID        int64  `gorm:"primaryKey;autoIncrement"`
	UserID    int64  `gorm:"index:idx_authtoken_user_purpose,priority:1;default:0"`
	Purpose   string `gorm:"size:32;not null;index:idx_authtoken_user_purpose,priority:2"`
	TokenHash string `gorm:"size:64;index:idx_authtoken_tokenhash;default:''"`
	CodeHash  string `gorm:"size:64;not null;default:''"`
	Email     string `gorm:"size:255"`
	ExpiresAt time.Time `gorm:"index:idx_authtoken_expires"`
	UsedAt    *time.Time
	// Attempts counts failed OTP guesses against this token. The OTP search
	// space is small (6 digits), so without a per-token cap a distributed
	// attacker could brute it within the TTL regardless of the per-IP limiter.
	Attempts  int `gorm:"not null;default:0"`
	CreatedAt time.Time
}

// otpMaxAttempts is how many wrong OTP guesses a single token tolerates before
// it's burned — the only recourse is a fresh (rate-limited) Forgot request.
const otpMaxAttempts = 5

func (authTokenRow) TableName() string { return "auth_tokens" }

func (r *authTokenRow) toDomain() *domain.AuthToken {
	return &domain.AuthToken{
		ID: r.ID, UserID: r.UserID, Purpose: r.Purpose,
		TokenHash: r.TokenHash, CodeHash: r.CodeHash, Email: r.Email,
		ExpiresAt: r.ExpiresAt, UsedAt: r.UsedAt, CreatedAt: r.CreatedAt,
	}
}

type authTokenRepo struct{ db *gorm.DB }

func (r *authTokenRepo) Create(ctx context.Context, t *domain.AuthToken) error {
	row := authTokenRow{
		UserID: t.UserID, Purpose: t.Purpose,
		TokenHash: t.TokenHash, CodeHash: t.CodeHash, Email: t.Email,
		ExpiresAt: t.ExpiresAt, UsedAt: t.UsedAt,
	}
	if t.CreatedAt.IsZero() {
		row.CreatedAt = time.Now()
	} else {
		row.CreatedAt = t.CreatedAt
	}
	if err := r.db.WithContext(ctx).Create(&row).Error; err != nil {
		return err
	}
	t.ID = row.ID
	t.CreatedAt = row.CreatedAt
	return nil
}

// consume atomically marks the matched row used and returns it. The conditional
// UPDATE (used_at IS NULL) is the race guard: two concurrent consumes can't both
// win, so a one-time token is truly one-time.
func (r *authTokenRepo) consume(ctx context.Context, where string, now time.Time, args ...any) (*domain.AuthToken, error) {
	var row authTokenRow
	q := r.db.WithContext(ctx).Where(where, args...)
	if err := q.First(&row).Error; err != nil {
		return nil, wrapNotFound(err)
	}
	if row.UsedAt != nil || !row.ExpiresAt.After(now) {
		return nil, domain.ErrNotFound
	}
	res := r.db.WithContext(ctx).Model(&authTokenRow{}).
		Where("id = ? AND used_at IS NULL", row.ID).
		Update("used_at", now)
	if res.Error != nil {
		return nil, res.Error
	}
	if res.RowsAffected != 1 {
		// Lost the race — another request consumed it first.
		return nil, domain.ErrNotFound
	}
	row.UsedAt = &now
	return row.toDomain(), nil
}

func (r *authTokenRepo) ConsumeByTokenHash(ctx context.Context, purpose, tokenHash string, now time.Time) (*domain.AuthToken, error) {
	if tokenHash == "" {
		return nil, domain.ErrNotFound
	}
	return r.consume(ctx, "purpose = ? AND token_hash = ? AND token_hash <> ''", now, purpose, tokenHash)
}

// ConsumeByUserCode validates an OTP for a user with a per-token attempt cap:
// it locates the user's single live OTP token (regardless of the submitted
// code), counts the guess, burns the token once the cap is exceeded, and only
// consumes it when the code matches while still under the cap. The cap bounds
// total guesses per issued token no matter how many IPs participate — closing
// the distributed-brute-force window the per-IP limiter can't.
func (r *authTokenRepo) ConsumeByUserCode(ctx context.Context, purpose string, userID int64, codeHash string, now time.Time) (*domain.AuthToken, error) {
	if codeHash == "" || userID == 0 {
		return nil, domain.ErrNotFound
	}
	var row authTokenRow
	q := r.db.WithContext(ctx).
		Where("purpose = ? AND user_id = ? AND code_hash <> '' AND used_at IS NULL", purpose, userID).
		Order("id DESC")
	if err := q.First(&row).Error; err != nil {
		return nil, wrapNotFound(err)
	}
	if !row.ExpiresAt.After(now) {
		return nil, domain.ErrNotFound
	}
	// Count this guess atomically (the race guard doubles as the "already
	// consumed elsewhere" check).
	res := r.db.WithContext(ctx).Model(&authTokenRow{}).
		Where("id = ? AND used_at IS NULL", row.ID).
		Update("attempts", gorm.Expr("attempts + 1"))
	if res.Error != nil {
		return nil, res.Error
	}
	if res.RowsAffected != 1 {
		return nil, domain.ErrNotFound
	}
	row.Attempts++
	if row.Attempts > otpMaxAttempts {
		// Too many guesses — burn it. A new code requires a fresh Forgot.
		_ = r.db.WithContext(ctx).Delete(&authTokenRow{}, row.ID).Error
		return nil, domain.ErrNotFound
	}
	if row.CodeHash != codeHash {
		return nil, domain.ErrNotFound // wrong code; the attempt was counted
	}
	// Correct code under the cap → consume it.
	used := r.db.WithContext(ctx).Model(&authTokenRow{}).
		Where("id = ? AND used_at IS NULL", row.ID).
		Update("used_at", now)
	if used.Error != nil {
		return nil, used.Error
	}
	if used.RowsAffected != 1 {
		return nil, domain.ErrNotFound
	}
	row.UsedAt = &now
	return row.toDomain(), nil
}

func (r *authTokenRepo) DeleteByUserPurpose(ctx context.Context, userID int64, purpose string) error {
	return r.db.WithContext(ctx).
		Where("user_id = ? AND purpose = ?", userID, purpose).
		Delete(&authTokenRow{}).Error
}

func (r *authTokenRepo) DeleteExpired(ctx context.Context, cutoff time.Time) (int64, error) {
	// Prune anything already expired OR already consumed — both are dead.
	res := r.db.WithContext(ctx).
		Where("expires_at < ? OR used_at IS NOT NULL", cutoff).
		Delete(&authTokenRow{})
	return res.RowsAffected, res.Error
}
