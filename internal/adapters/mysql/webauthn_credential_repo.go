package mysql

import (
	"context"
	"time"

	"gorm.io/gorm"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// webauthnCredentialRow backs domain.PasskeyCredential — one registered WebAuthn
// passkey. Credential stores the full opaque webauthn.Credential record as JSON
// (type:text, cross-dialect-portable, not encrypted — a public key is not a
// secret); credential_id is the globally-unique discoverable-login lookup key;
// sign_count is denormalized out of the record so the anti-clone update can gate
// on it. The user_id index serves per-user listing.
type webauthnCredentialRow struct {
	ID           int64  `gorm:"primaryKey;autoIncrement"`
	UserID       int64  `gorm:"not null;index:idx_webauthn_user"`
	CredentialID string `gorm:"column:credential_id;size:512;not null;uniqueIndex:uk_webauthn_credid"`
	Credential   string `gorm:"column:credential;type:text;not null"`
	SignCount    int64  `gorm:"column:sign_count;not null;default:0"`
	Name         string `gorm:"column:name;size:255;default:''"`
	CreatedAt    time.Time
	LastUsedAt   *time.Time `gorm:"column:last_used_at"`
}

func (webauthnCredentialRow) TableName() string { return "webauthn_credentials" }

func (r *webauthnCredentialRow) toDomain() *domain.PasskeyCredential {
	return &domain.PasskeyCredential{
		ID: r.ID, UserID: r.UserID, CredentialID: r.CredentialID,
		Credential: []byte(r.Credential), SignCount: r.SignCount, Name: r.Name,
		CreatedAt: r.CreatedAt, LastUsedAt: r.LastUsedAt,
	}
}

type webauthnCredentialRepo struct{ db *gorm.DB }

func (r *webauthnCredentialRepo) Save(ctx context.Context, c *domain.PasskeyCredential) error {
	row := webauthnCredentialRow{
		UserID: c.UserID, CredentialID: c.CredentialID,
		Credential: string(c.Credential), SignCount: c.SignCount, Name: c.Name,
		LastUsedAt: c.LastUsedAt,
	}
	if c.CreatedAt.IsZero() {
		row.CreatedAt = time.Now()
	} else {
		row.CreatedAt = c.CreatedAt
	}
	if err := r.db.WithContext(ctx).Create(&row).Error; err != nil {
		return err
	}
	c.ID = row.ID
	c.CreatedAt = row.CreatedAt
	return nil
}

func (r *webauthnCredentialRepo) FindByUserID(ctx context.Context, userID int64) ([]*domain.PasskeyCredential, error) {
	var rows []webauthnCredentialRow
	if err := r.db.WithContext(ctx).Where("user_id = ?", userID).Order("id ASC").Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]*domain.PasskeyCredential, len(rows))
	for i := range rows {
		out[i] = rows[i].toDomain()
	}
	return out, nil
}

func (r *webauthnCredentialRepo) FindByCredentialID(ctx context.Context, credentialID string) (*domain.PasskeyCredential, error) {
	if credentialID == "" {
		return nil, domain.ErrNotFound
	}
	var row webauthnCredentialRow
	if err := r.db.WithContext(ctx).Where("credential_id = ?", credentialID).First(&row).Error; err != nil {
		return nil, wrapNotFound(err)
	}
	return row.toDomain(), nil
}

// UpdateAfterLogin writes the refreshed credential record + sign count, gated on
// the new count not regressing. A cloned/replayed authenticator presents a
// SignCount <= the stored one; the WHERE then matches 0 rows and the write is
// refused (returns false) without corrupting the stored record.
func (r *webauthnCredentialRepo) UpdateAfterLogin(ctx context.Context, id int64, credential []byte, newSignCount int64, lastUsed time.Time) (bool, error) {
	res := r.db.WithContext(ctx).Model(&webauthnCredentialRow{}).
		Where("id = ? AND sign_count <= ?", id, newSignCount).
		Updates(map[string]any{
			"credential":   string(credential),
			"sign_count":   newSignCount,
			"last_used_at": lastUsed,
		})
	if res.Error != nil {
		return false, res.Error
	}
	return res.RowsAffected == 1, nil
}

func (r *webauthnCredentialRepo) Rename(ctx context.Context, id, userID int64, name string) error {
	return r.db.WithContext(ctx).Model(&webauthnCredentialRow{}).
		Where("id = ? AND user_id = ?", id, userID).
		Update("name", name).Error
}

func (r *webauthnCredentialRepo) Delete(ctx context.Context, id, userID int64) error {
	return r.db.WithContext(ctx).
		Where("id = ? AND user_id = ?", id, userID).
		Delete(&webauthnCredentialRow{}).Error
}

// CountByUserIDs returns the number of credentials per user for the given ids in
// ONE grouped query (avoids an N+1 when enriching a user list). Users with no
// passkeys are simply absent from the returned map.
func (r *webauthnCredentialRepo) CountByUserIDs(ctx context.Context, userIDs []int64) (map[int64]int, error) {
	out := make(map[int64]int, len(userIDs))
	if len(userIDs) == 0 {
		return out, nil
	}
	var rows []struct {
		UserID int64
		N      int64
	}
	if err := r.db.WithContext(ctx).
		Model(&webauthnCredentialRow{}).
		Select("user_id, COUNT(*) AS n").
		Where("user_id IN ?", userIDs).
		Group("user_id").
		Scan(&rows).Error; err != nil {
		return nil, err
	}
	for _, x := range rows {
		out[x.UserID] = int(x.N)
	}
	return out, nil
}

// DeleteAllByUserID removes every credential owned by a user and returns the
// count deleted. Used by the admin "revoke all passkeys" break-glass; the
// user_id scope keeps it from touching anyone else's credentials.
func (r *webauthnCredentialRepo) DeleteAllByUserID(ctx context.Context, userID int64) (int, error) {
	res := r.db.WithContext(ctx).
		Where("user_id = ?", userID).
		Delete(&webauthnCredentialRow{})
	if res.Error != nil {
		return 0, res.Error
	}
	return int(res.RowsAffected), nil
}
