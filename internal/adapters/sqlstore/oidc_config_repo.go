package sqlstore

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"

	"github.com/KazuhaHub/passwall-sub-panel/internal/config"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// oidcConfigRow is the single-row persistence of OIDCConfig (PK pinned at 1,
// same pattern as samlConfigRow).
//
// ClientSecret is encrypted at rest and is never returned in the admin GET
// response (the handler returns a has_client_secret boolean instead).
type oidcConfigRow struct {
	ID      int64 `gorm:"primaryKey"`
	Enabled bool

	IssuerURL string `gorm:"size:255"`
	ClientID  string `gorm:"size:255"`
	// ClientSecret holds an AES-GCM blob (enc:v1:base64(nonce||ct||tag)), which
	// for a long plaintext secret can exceed 512 chars — a size:512 column
	// silently truncated it (non-strict MySQL) or rejected the insert
	// (Postgres/strict MySQL), corrupting the ciphertext so the next Load failed
	// GCM auth and aborted SSO. type:text matches every other encrypted-secret
	// column (sp_private_key, smtp_password, api_token); AutoMigrate widens it.
	ClientSecret string `gorm:"type:text"`
	RedirectURL  string `gorm:"size:255"`
	Scopes       jsonStrings

	AttrUsername    string `gorm:"size:128"`
	AttrEmail       string `gorm:"size:128"`
	AttrDisplayName string `gorm:"size:128"`
	AttrGroups      string `gorm:"size:128"`

	RoleRules        jsonRoleRules
	DefaultGroupSlug string `gorm:"size:64"`
	AllowAutoCreate  bool

	NewUserExpireDays         int
	NewUserTrafficLimitBytes  int64
	NewUserTrafficResetPeriod string `gorm:"size:16"`

	UpdatedAt time.Time
}

func (oidcConfigRow) TableName() string { return "oidc_settings" }

func (r *oidcConfigRow) toDomain() (*config.OIDCConfig, error) {
	secret, err := decryptSecret(r.ClientSecret)
	if err != nil {
		return nil, err
	}
	c := &config.OIDCConfig{
		Enabled:      r.Enabled,
		IssuerURL:    r.IssuerURL,
		ClientID:     r.ClientID,
		ClientSecret: secret,
		RedirectURL:  r.RedirectURL,
		Scopes:       []string(r.Scopes),
		AttributeMapping: config.OIDCAttributeMap{
			Username:    r.AttrUsername,
			Email:       r.AttrEmail,
			DisplayName: r.AttrDisplayName,
			Groups:      r.AttrGroups,
		},
		RoleRules:        []config.SSORoleRule(r.RoleRules),
		DefaultGroupSlug: r.DefaultGroupSlug,
		AllowAutoCreate:  r.AllowAutoCreate,
		NewUserDefaults: config.SAMLNewUserDefaults{
			ExpireDays:         r.NewUserExpireDays,
			TrafficLimitBytes:  r.NewUserTrafficLimitBytes,
			TrafficResetPeriod: r.NewUserTrafficResetPeriod,
		},
	}
	config.ApplyOIDCDefaults(c)
	return c, nil
}

func oidcConfigFromDomain(c *config.OIDCConfig) (*oidcConfigRow, error) {
	secret, err := encryptSecret(c.ClientSecret)
	if err != nil {
		return nil, err
	}
	return &oidcConfigRow{
		ID:                        1,
		Enabled:                   c.Enabled,
		IssuerURL:                 c.IssuerURL,
		ClientID:                  c.ClientID,
		ClientSecret:              secret,
		RedirectURL:               c.RedirectURL,
		Scopes:                    jsonStrings(c.Scopes),
		AttrUsername:              c.AttributeMapping.Username,
		AttrEmail:                 c.AttributeMapping.Email,
		AttrDisplayName:           c.AttributeMapping.DisplayName,
		AttrGroups:                c.AttributeMapping.Groups,
		RoleRules:        jsonRoleRules(c.RoleRules),
		DefaultGroupSlug: c.DefaultGroupSlug,
		AllowAutoCreate:  c.AllowAutoCreate,
		NewUserExpireDays:         c.NewUserDefaults.ExpireDays,
		NewUserTrafficLimitBytes:  c.NewUserDefaults.TrafficLimitBytes,
		NewUserTrafficResetPeriod: c.NewUserDefaults.TrafficResetPeriod,
	}, nil
}

type oidcConfigRepo struct{ db *gorm.DB }

func (r *oidcConfigRepo) Load(ctx context.Context) (*config.OIDCConfig, error) {
	var row oidcConfigRow
	if err := r.db.WithContext(ctx).First(&row, 1).Error; err != nil {
		err = wrapNotFound(err)
		if errors.Is(err, domain.ErrNotFound) {
			c := &config.OIDCConfig{Enabled: false}
			config.ApplyOIDCDefaults(c)
			return c, nil
		}
		return nil, err
	}
	return row.toDomain()
}

func (r *oidcConfigRepo) Save(ctx context.Context, c *config.OIDCConfig) error {
	row, err := oidcConfigFromDomain(c)
	if err != nil {
		return err
	}
	return r.db.WithContext(ctx).Save(row).Error
}
