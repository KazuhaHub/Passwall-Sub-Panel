package mysql

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// acmeAccountRow stores one registered ACME account, unique per (email,
// directory). AccountKey (the account private key PEM) is encrypted at rest;
// Registration is the lego registration resource JSON.
type acmeAccountRow struct {
	ID           int64  `gorm:"primaryKey;autoIncrement"`
	Email        string `gorm:"column:email;size:255;not null;uniqueIndex:uk_acme_email_dir,priority:1"`
	Directory    string `gorm:"column:directory;size:512;not null;uniqueIndex:uk_acme_email_dir,priority:2"`
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
	return &domain.ACMEAccount{
		ID:           r.ID,
		Email:        r.Email,
		Directory:    r.Directory,
		AccountKey:   key,
		Registration: r.Registration,
		CreatedAt:    r.CreatedAt,
		UpdatedAt:    r.UpdatedAt,
	}, nil
}

func acmeAccountFromDomain(a *domain.ACMEAccount) (*acmeAccountRow, error) {
	key, err := encryptSecret(a.AccountKey)
	if err != nil {
		return nil, err
	}
	return &acmeAccountRow{
		ID:           a.ID,
		Email:        a.Email,
		Directory:    a.Directory,
		AccountKey:   key,
		Registration: a.Registration,
		CreatedAt:    a.CreatedAt,
		UpdatedAt:    a.UpdatedAt,
	}, nil
}

type acmeAccountRepo struct{ db *gorm.DB }

// GetByEmailDirectory returns (nil, nil) when no account is registered for the
// pair — the caller (the ACME service) treats absence as "register on first
// obtain" rather than an error.
func (r *acmeAccountRepo) GetByEmailDirectory(ctx context.Context, email, directory string) (*domain.ACMEAccount, error) {
	var row acmeAccountRow
	err := r.db.WithContext(ctx).Where("email = ? AND directory = ?", email, directory).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return row.toDomain()
}

func (r *acmeAccountRepo) Save(ctx context.Context, a *domain.ACMEAccount) error {
	if a.Email == "" || a.Directory == "" {
		return fmt.Errorf("%w: acme account email and directory required", domain.ErrValidation)
	}
	row, err := acmeAccountFromDomain(a)
	if err != nil {
		return err
	}
	if err := r.db.WithContext(ctx).Save(row).Error; err != nil {
		return err
	}
	a.ID = row.ID
	return nil
}
