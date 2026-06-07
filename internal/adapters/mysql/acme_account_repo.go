package mysql

import (
	"context"
	"fmt"
	"time"

	"gorm.io/gorm"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// acmeAccountRow is an admin-managed ACME CA account profile. (email, directory)
// is unique — the same contact on the same CA is the same ACME account. Name /
// EABKeyID / KeyType are admin config; AccountKey (account private key PEM) and
// EABHMACKey are encrypted at rest; Registration is the lego registration JSON.
// AccountKey/Registration are filled lazily on first issuance and reused.
type acmeAccountRow struct {
	ID           int64  `gorm:"primaryKey;autoIncrement"`
	Name         string `gorm:"column:name;size:255;not null;default:''"`
	Email        string `gorm:"column:email;size:255;not null;uniqueIndex:uk_acme_email_dir,priority:1"`
	Directory    string `gorm:"column:directory;size:512;not null;uniqueIndex:uk_acme_email_dir,priority:2"`
	EABKeyID     string `gorm:"column:eab_key_id;size:255;not null;default:''"`
	EABHMACKey   string `gorm:"column:eab_hmac;type:text"`
	KeyType      string `gorm:"column:key_type;size:32;not null;default:''"`
	AccountKey   string `gorm:"column:account_key;type:text"`
	Registration string `gorm:"column:registration;type:text"`
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

func (acmeAccountRow) TableName() string { return "acme_accounts" }

func (r *acmeAccountRow) toDomain() (*domain.ACMEAccount, error) {
	key, err := decryptSecret(r.AccountKey)
	if err != nil {
		return nil, fmt.Errorf("decrypt acme account key (id=%d): %w", r.ID, err)
	}
	hmac, err := decryptSecret(r.EABHMACKey)
	if err != nil {
		return nil, fmt.Errorf("decrypt acme eab hmac (id=%d): %w", r.ID, err)
	}
	return &domain.ACMEAccount{
		ID:           r.ID,
		Name:         r.Name,
		Email:        r.Email,
		Directory:    r.Directory,
		EABKeyID:     r.EABKeyID,
		EABHMACKey:   hmac,
		KeyType:      r.KeyType,
		AccountKey:   key,
		Registration: r.Registration,
		CreatedAt:    r.CreatedAt,
		UpdatedAt:    r.UpdatedAt,
	}, nil
}

type acmeAccountRepo struct{ db *gorm.DB }

func (r *acmeAccountRepo) Create(ctx context.Context, a *domain.ACMEAccount) error {
	if a.Email == "" || a.Directory == "" {
		return fmt.Errorf("%w: acme account email and directory required", domain.ErrValidation)
	}
	key, err := encryptSecret(a.AccountKey)
	if err != nil {
		return err
	}
	hmac, err := encryptSecret(a.EABHMACKey)
	if err != nil {
		return err
	}
	row := &acmeAccountRow{
		Name: a.Name, Email: a.Email, Directory: a.Directory,
		EABKeyID: a.EABKeyID, EABHMACKey: hmac, KeyType: a.KeyType,
		AccountKey: key, Registration: a.Registration,
	}
	if err := r.db.WithContext(ctx).Create(row).Error; err != nil {
		return err
	}
	a.ID = row.ID
	return nil
}

// Update persists ONLY the admin-editable config columns — it never touches
// account_key / registration (those are the lazily-filled machine fields; the
// service clears them via ClearRegistration when the identity changes).
func (r *acmeAccountRepo) Update(ctx context.Context, a *domain.ACMEAccount) error {
	if a.Email == "" || a.Directory == "" {
		return fmt.Errorf("%w: acme account email and directory required", domain.ErrValidation)
	}
	hmac, err := encryptSecret(a.EABHMACKey)
	if err != nil {
		return err
	}
	res := r.db.WithContext(ctx).Model(&acmeAccountRow{}).Where("id = ?", a.ID).Updates(map[string]any{
		"name":       a.Name,
		"email":      a.Email,
		"directory":  a.Directory,
		"eab_key_id": a.EABKeyID,
		"eab_hmac":   hmac,
		"key_type":   a.KeyType,
	})
	if res.Error != nil {
		return res.Error
	}
	return nil
}

func (r *acmeAccountRepo) Delete(ctx context.Context, id int64) error {
	return r.db.WithContext(ctx).Where("id = ?", id).Delete(&acmeAccountRow{}).Error
}

func (r *acmeAccountRepo) GetByID(ctx context.Context, id int64) (*domain.ACMEAccount, error) {
	var row acmeAccountRow
	if err := r.db.WithContext(ctx).Where("id = ?", id).First(&row).Error; err != nil {
		return nil, wrapNotFound(err)
	}
	return row.toDomain()
}

func (r *acmeAccountRepo) List(ctx context.Context) ([]*domain.ACMEAccount, error) {
	var rows []acmeAccountRow
	if err := r.db.WithContext(ctx).Order("id ASC").Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]*domain.ACMEAccount, 0, len(rows))
	for i := range rows {
		a, err := rows[i].toDomain()
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, nil
}

// UpdateRegistration writes back the registered account key + registration JSON
// (the lazy machine fields) after a successful first registration.
func (r *acmeAccountRepo) UpdateRegistration(ctx context.Context, id int64, accountKeyPEM, registrationJSON string) error {
	key, err := encryptSecret(accountKeyPEM)
	if err != nil {
		return err
	}
	return r.db.WithContext(ctx).Model(&acmeAccountRow{}).Where("id = ?", id).Updates(map[string]any{
		"account_key":  key,
		"registration": registrationJSON,
	}).Error
}

// ClearRegistration drops the account key + registration so the next issuance
// re-registers (used when the admin changes the account's registered identity).
func (r *acmeAccountRepo) ClearRegistration(ctx context.Context, id int64) error {
	return r.db.WithContext(ctx).Model(&acmeAccountRow{}).Where("id = ?", id).Updates(map[string]any{
		"account_key":  "",
		"registration": "",
	}).Error
}
