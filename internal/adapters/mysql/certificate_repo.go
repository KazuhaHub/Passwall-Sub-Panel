package mysql

import (
	"context"
	"fmt"
	"time"

	"gorm.io/gorm"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// tlsCertificateRow stores one PSP-managed ACME certificate. CertPEM/KeyPEM are
// encrypted at rest. Explicit column tags pin the DB names so the column-scoped
// UpdateIssued map below can't drift from GORM's acronym naming heuristics.
type tlsCertificateRow struct {
	ID              int64       `gorm:"primaryKey;autoIncrement"`
	Name            string      `gorm:"column:name;size:255;not null"`
	Domains         jsonStrings `gorm:"column:domains"`
	ACMEAccountID   int64       `gorm:"column:acme_account_id;index"`
	DNSCredentialID int64       `gorm:"column:dns_credential_id;index"`
	CertPEM         string      `gorm:"column:cert_pem;type:text"`
	KeyPEM          string      `gorm:"column:key_pem;type:text"`
	Status          string      `gorm:"column:status;size:16;not null;default:'pending';index"`
	LastError       string      `gorm:"column:last_error;type:text"`
	NotBefore       *time.Time  `gorm:"column:not_before"`
	NotAfter        *time.Time  `gorm:"column:not_after"`
	Fingerprint     string      `gorm:"column:fingerprint;size:128;default:''"`
	AutoRenew       bool        `gorm:"column:auto_renew;default:true"`
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

func (tlsCertificateRow) TableName() string { return "tls_certificates" }

func (r *tlsCertificateRow) toDomain() (*domain.TLSCertificate, error) {
	certPEM, err := decryptSecret(r.CertPEM)
	if err != nil {
		return nil, fmt.Errorf("decrypt cert_pem (id=%d): %w", r.ID, err)
	}
	keyPEM, err := decryptSecret(r.KeyPEM)
	if err != nil {
		return nil, fmt.Errorf("decrypt key_pem (id=%d): %w", r.ID, err)
	}
	return &domain.TLSCertificate{
		ID:              r.ID,
		Name:            r.Name,
		Domains:         []string(r.Domains),
		ACMEAccountID:   r.ACMEAccountID,
		DNSCredentialID: r.DNSCredentialID,
		CertPEM:         certPEM,
		KeyPEM:          keyPEM,
		Status:          domain.CertStatus(r.Status),
		LastError:       r.LastError,
		NotBefore:       r.NotBefore,
		NotAfter:        r.NotAfter,
		Fingerprint:     r.Fingerprint,
		AutoRenew:       r.AutoRenew,
		CreatedAt:       r.CreatedAt,
		UpdatedAt:       r.UpdatedAt,
	}, nil
}

func tlsCertificateFromDomain(c *domain.TLSCertificate) (*tlsCertificateRow, error) {
	certPEM, err := encryptSecret(c.CertPEM)
	if err != nil {
		return nil, err
	}
	keyPEM, err := encryptSecret(c.KeyPEM)
	if err != nil {
		return nil, err
	}
	status := string(c.Status)
	if status == "" {
		status = string(domain.CertStatusPending)
	}
	return &tlsCertificateRow{
		ID:              c.ID,
		Name:            c.Name,
		Domains:         jsonStrings(c.Domains),
		ACMEAccountID:   c.ACMEAccountID,
		DNSCredentialID: c.DNSCredentialID,
		CertPEM:         certPEM,
		KeyPEM:          keyPEM,
		Status:          status,
		LastError:       c.LastError,
		NotBefore:       c.NotBefore,
		NotAfter:        c.NotAfter,
		Fingerprint:     c.Fingerprint,
		AutoRenew:       c.AutoRenew,
		CreatedAt:       c.CreatedAt,
		UpdatedAt:       c.UpdatedAt,
	}, nil
}

type certificateRepo struct{ db *gorm.DB }

func (r *certificateRepo) Create(ctx context.Context, c *domain.TLSCertificate) error {
	if c.Name == "" {
		return fmt.Errorf("%w: certificate name required", domain.ErrValidation)
	}
	if len(c.Domains) == 0 {
		return fmt.Errorf("%w: certificate needs at least one domain", domain.ErrValidation)
	}
	row, err := tlsCertificateFromDomain(c)
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

func (r *certificateRepo) Update(ctx context.Context, c *domain.TLSCertificate) error {
	if c.ID == 0 {
		return fmt.Errorf("%w: certificate id required", domain.ErrValidation)
	}
	row, err := tlsCertificateFromDomain(c)
	if err != nil {
		return err
	}
	return r.db.WithContext(ctx).Save(row).Error
}

// UpdateIssued writes ONLY the issuance-owned columns. The issuance/renewal
// worker uses this so a concurrent admin edit (name / domains / auto_renew /
// binding) that landed after the worker loaded its snapshot is not reverted.
// Mirrors nodes.UpdateHealth / xui_panels.UpdateVersion.
func (r *certificateRepo) UpdateIssued(ctx context.Context, c *domain.TLSCertificate) error {
	if c.ID == 0 {
		return fmt.Errorf("%w: certificate id required", domain.ErrValidation)
	}
	certPEM, err := encryptSecret(c.CertPEM)
	if err != nil {
		return err
	}
	keyPEM, err := encryptSecret(c.KeyPEM)
	if err != nil {
		return err
	}
	updates := map[string]any{
		"cert_pem":    certPEM,
		"key_pem":     keyPEM,
		"status":      string(c.Status),
		"not_before":  c.NotBefore,
		"not_after":   c.NotAfter,
		"fingerprint": c.Fingerprint,
		"last_error":  c.LastError,
	}
	return r.db.WithContext(ctx).Model(&tlsCertificateRow{}).Where("id = ?", c.ID).Updates(updates).Error
}

func (r *certificateRepo) Delete(ctx context.Context, id int64) error {
	return r.db.WithContext(ctx).Delete(&tlsCertificateRow{}, id).Error
}

func (r *certificateRepo) GetByID(ctx context.Context, id int64) (*domain.TLSCertificate, error) {
	var row tlsCertificateRow
	if err := r.db.WithContext(ctx).First(&row, id).Error; err != nil {
		return nil, wrapNotFound(err)
	}
	return row.toDomain()
}

func (r *certificateRepo) List(ctx context.Context) ([]*domain.TLSCertificate, error) {
	return r.listWhere(ctx, r.db.WithContext(ctx).Order("id ASC"))
}

func (r *certificateRepo) ListByStatus(ctx context.Context, status domain.CertStatus) ([]*domain.TLSCertificate, error) {
	return r.listWhere(ctx, r.db.WithContext(ctx).Where("status = ?", string(status)).Order("id ASC"))
}

func (r *certificateRepo) listWhere(ctx context.Context, q *gorm.DB) ([]*domain.TLSCertificate, error) {
	var rows []tlsCertificateRow
	if err := q.Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]*domain.TLSCertificate, len(rows))
	for i := range rows {
		c, err := rows[i].toDomain()
		if err != nil {
			return nil, err
		}
		out[i] = c
	}
	return out, nil
}
