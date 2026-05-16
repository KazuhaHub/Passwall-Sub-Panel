package mysql

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"

	"github.com/KazuhaHub/passwall-sub-panel/internal/config"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// samlConfigRow is the single-row persistence of SAMLConfig. ID is pinned
// at 1 so Save() is an upsert of a known primary key — there is exactly
// one SAML config per panel.
//
// Cert and key PEMs are stored inline (TEXT) so the admin UI can paste
// them in and the runtime does not depend on local certificate paths.
type samlConfigRow struct {
	ID      int64 `gorm:"primaryKey"`
	Enabled bool
	Mode    string `gorm:"size:16"`

	SPEntityID string `gorm:"size:255"`
	SPACSURL   string `gorm:"size:255"`
	SPCertPEM  string `gorm:"type:text"`
	SPKeyPEM   string `gorm:"type:text"`

	IDPMetadataURL        string `gorm:"size:255"`
	IDPMetadataRefreshSec int64

	AttrUPN         string `gorm:"size:255"`
	AttrEmail       string `gorm:"size:255"`
	AttrDisplayName string `gorm:"size:255"`
	AttrGroups      string `gorm:"size:255"`

	AdminGroupIDs    jsonStrings
	DefaultGroupSlug string `gorm:"size:64"`
	AllowAutoCreate  bool

	NewUserExpireDays         int
	NewUserTrafficLimitBytes  int64
	NewUserTrafficResetPeriod string `gorm:"size:16"`

	UpdatedAt time.Time
}

func (samlConfigRow) TableName() string { return "saml_config" }

func (r *samlConfigRow) toDomain() (*config.SAMLConfig, error) {
	keyPEM, err := decryptSecret(r.SPKeyPEM)
	if err != nil {
		return nil, err
	}
	c := &config.SAMLConfig{
		Enabled: r.Enabled,
		Mode:    r.Mode,
		SP: config.SPConf{
			EntityID: r.SPEntityID,
			ACSURL:   r.SPACSURL,
			CertPEM:  r.SPCertPEM,
			KeyPEM:   keyPEM,
		},
		IDP: config.IDPConf{
			MetadataURL:             r.IDPMetadataURL,
			MetadataRefreshInterval: time.Duration(r.IDPMetadataRefreshSec) * time.Second,
		},
		AttributeMapping: config.SAMLAttributeMap{
			UPN:         r.AttrUPN,
			Email:       r.AttrEmail,
			DisplayName: r.AttrDisplayName,
			Groups:      r.AttrGroups,
		},
		AdminGroupIDs:    []string(r.AdminGroupIDs),
		DefaultGroupSlug: r.DefaultGroupSlug,
		AllowAutoCreate:  r.AllowAutoCreate,
		NewUserDefaults: config.SAMLNewUserDefaults{
			ExpireDays:         r.NewUserExpireDays,
			TrafficLimitBytes:  r.NewUserTrafficLimitBytes,
			TrafficResetPeriod: r.NewUserTrafficResetPeriod,
		},
	}
	config.ApplySAMLDefaults(c)
	return c, nil
}

func samlConfigFromDomain(c *config.SAMLConfig) (*samlConfigRow, error) {
	keyPEM, err := encryptSecret(c.SP.KeyPEM)
	if err != nil {
		return nil, err
	}
	return &samlConfigRow{
		ID:                        1,
		Enabled:                   c.Enabled,
		Mode:                      c.Mode,
		SPEntityID:                c.SP.EntityID,
		SPACSURL:                  c.SP.ACSURL,
		SPCertPEM:                 c.SP.CertPEM,
		SPKeyPEM:                  keyPEM,
		IDPMetadataURL:            c.IDP.MetadataURL,
		IDPMetadataRefreshSec:     int64(c.IDP.MetadataRefreshInterval / time.Second),
		AttrUPN:                   c.AttributeMapping.UPN,
		AttrEmail:                 c.AttributeMapping.Email,
		AttrDisplayName:           c.AttributeMapping.DisplayName,
		AttrGroups:                c.AttributeMapping.Groups,
		AdminGroupIDs:             jsonStrings(c.AdminGroupIDs),
		DefaultGroupSlug:          c.DefaultGroupSlug,
		AllowAutoCreate:           c.AllowAutoCreate,
		NewUserExpireDays:         c.NewUserDefaults.ExpireDays,
		NewUserTrafficLimitBytes:  c.NewUserDefaults.TrafficLimitBytes,
		NewUserTrafficResetPeriod: c.NewUserDefaults.TrafficResetPeriod,
	}, nil
}

type samlConfigRepo struct{ db *gorm.DB }

func (r *samlConfigRepo) Load(ctx context.Context) (*config.SAMLConfig, error) {
	var row samlConfigRow
	if err := r.db.WithContext(ctx).First(&row, 1).Error; err != nil {
		err = wrapNotFound(err)
		if errors.Is(err, domain.ErrNotFound) {
			c := &config.SAMLConfig{Enabled: false}
			config.ApplySAMLDefaults(c)
			return c, nil
		}
		return nil, err
	}
	return row.toDomain()
}

func (r *samlConfigRepo) Save(ctx context.Context, c *config.SAMLConfig) error {
	row, err := samlConfigFromDomain(c)
	if err != nil {
		return err
	}
	// Upsert by pinned PK=1.
	return r.db.WithContext(ctx).Save(row).Error
}
