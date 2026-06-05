package mysql

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"gorm.io/gorm"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// dnsCredentialRow stores one DNS-provider credential set for ACME DNS-01.
// Credentials is a JSON-encoded map[string]string, encrypted at rest (the
// provider tokens are secrets, same trust boundary as xui_panels.api_token).
type dnsCredentialRow struct {
	ID          int64  `gorm:"primaryKey;autoIncrement"`
	Name        string `gorm:"column:name;size:128;uniqueIndex;not null"`
	Provider    string `gorm:"column:provider;size:64;not null"`
	Credentials string `gorm:"column:credentials;type:text"`
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

func (dnsCredentialRow) TableName() string { return "dns_credentials" }

func (r *dnsCredentialRow) toDomain() (*domain.DNSCredential, error) {
	dec, err := decryptSecret(r.Credentials)
	if err != nil {
		return nil, fmt.Errorf("decrypt dns credentials (id=%d): %w", r.ID, err)
	}
	creds := map[string]string{}
	if dec != "" {
		if err := json.Unmarshal([]byte(dec), &creds); err != nil {
			return nil, fmt.Errorf("unmarshal dns credentials (id=%d): %w", r.ID, err)
		}
	}
	return &domain.DNSCredential{
		ID:          r.ID,
		Name:        r.Name,
		Provider:    r.Provider,
		Credentials: creds,
		CreatedAt:   r.CreatedAt,
		UpdatedAt:   r.UpdatedAt,
	}, nil
}

func dnsCredentialFromDomain(c *domain.DNSCredential) (*dnsCredentialRow, error) {
	raw := "{}"
	if len(c.Credentials) > 0 {
		b, err := json.Marshal(c.Credentials)
		if err != nil {
			return nil, err
		}
		raw = string(b)
	}
	enc, err := encryptSecret(raw)
	if err != nil {
		return nil, err
	}
	return &dnsCredentialRow{
		ID:          c.ID,
		Name:        c.Name,
		Provider:    c.Provider,
		Credentials: enc,
		CreatedAt:   c.CreatedAt,
		UpdatedAt:   c.UpdatedAt,
	}, nil
}

type dnsCredentialRepo struct{ db *gorm.DB }

func (r *dnsCredentialRepo) Create(ctx context.Context, c *domain.DNSCredential) error {
	if c.Name == "" {
		return fmt.Errorf("%w: dns credential name required", domain.ErrValidation)
	}
	if c.Provider == "" {
		return fmt.Errorf("%w: dns credential provider required", domain.ErrValidation)
	}
	row, err := dnsCredentialFromDomain(c)
	if err != nil {
		return err
	}
	row.ID = 0
	if err := r.db.WithContext(ctx).Create(row).Error; err != nil {
		return err
	}
	c.ID = row.ID
	return nil
}

func (r *dnsCredentialRepo) Update(ctx context.Context, c *domain.DNSCredential) error {
	if c.ID == 0 {
		return fmt.Errorf("%w: dns credential id required", domain.ErrValidation)
	}
	row, err := dnsCredentialFromDomain(c)
	if err != nil {
		return err
	}
	return r.db.WithContext(ctx).Save(row).Error
}

func (r *dnsCredentialRepo) Delete(ctx context.Context, id int64) error {
	return r.db.WithContext(ctx).Delete(&dnsCredentialRow{}, id).Error
}

func (r *dnsCredentialRepo) GetByID(ctx context.Context, id int64) (*domain.DNSCredential, error) {
	var row dnsCredentialRow
	if err := r.db.WithContext(ctx).First(&row, id).Error; err != nil {
		return nil, wrapNotFound(err)
	}
	return row.toDomain()
}

func (r *dnsCredentialRepo) List(ctx context.Context) ([]*domain.DNSCredential, error) {
	var rows []dnsCredentialRow
	if err := r.db.WithContext(ctx).Order("name ASC").Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]*domain.DNSCredential, len(rows))
	for i := range rows {
		c, err := rows[i].toDomain()
		if err != nil {
			return nil, err
		}
		out[i] = c
	}
	return out, nil
}
