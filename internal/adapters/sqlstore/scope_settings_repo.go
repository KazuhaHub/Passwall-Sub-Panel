package sqlstore

import (
	"context"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// scopeSettingRow is one per-scope override cell: the same (type, name, value)
// shape as settingRow plus a (scope_type, scope_id) key. The table is SPARSE — a
// row exists only for an explicitly-overridden (scope, key); a missing row means
// "inherit the global settings value". An empty table therefore means every
// scope fully inherits, which is what makes this purely additive (AutoMigrate
// picks up the new table; the global `settings` rows stay untouched as the
// "All users" default).
//
// ScopeType is a string (not an enum) so a future "user" scope can be added
// without a schema change; v3.8.0 only ever writes "group". There is no decrypt
// path here: encrypted-at-rest settings are connection secrets, are global-only,
// and SetOverride rejects them, so `encrypted` is always false (kept for
// shape-symmetry with settingRow so the resolver can reuse settingDescriptors).
type scopeSettingRow struct {
	ID        int64  `gorm:"primaryKey;autoIncrement"`
	ScopeType string `gorm:"size:16;not null;uniqueIndex:uk_scope_setting,priority:1"`
	ScopeID   int64  `gorm:"not null;uniqueIndex:uk_scope_setting,priority:2"`
	Type      string `gorm:"size:32;not null;uniqueIndex:uk_scope_setting,priority:3"`
	Name      string `gorm:"size:128;not null;uniqueIndex:uk_scope_setting,priority:4"`
	Value     string `gorm:"type:text"`
	Encrypted bool   `gorm:"not null;default:false"`
	UpdatedAt time.Time
}

func (scopeSettingRow) TableName() string { return "scope_settings" }

// kvScopeSettingsRepo implements ports.ScopeSettingsRepo against scope_settings.
type kvScopeSettingsRepo struct{ db *gorm.DB }

func newKVScopeSettingsRepo(db *gorm.DB) *kvScopeSettingsRepo { return &kvScopeSettingsRepo{db: db} }

func (r *kvScopeSettingsRepo) ListOverrides(ctx context.Context, scopeType string, scopeID int64) ([]ports.ScopeOverride, error) {
	var rows []scopeSettingRow
	if err := r.db.WithContext(ctx).
		Where("scope_type = ? AND scope_id = ?", scopeType, scopeID).
		Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]ports.ScopeOverride, 0, len(rows))
	for _, row := range rows {
		out = append(out, ports.ScopeOverride{
			Type: row.Type, Name: row.Name, Value: row.Value, Encrypted: row.Encrypted,
		})
	}
	return out, nil
}

func (r *kvScopeSettingsRepo) SetOverride(ctx context.Context, scopeType string, scopeID int64, o ports.ScopeOverride) error {
	enc, known := settingKeyEncrypted(o.Type, o.Name)
	if !known {
		// Refuse to strand a value on a dead key — same drift concern as the
		// migrator's copySettingsKV (KnownSettingNames).
		return fmt.Errorf("scope override: unknown setting %s.%s", o.Type, o.Name)
	}
	if enc || o.Encrypted {
		// Encrypted-at-rest settings are global-only (§10-3). Rejecting at the
		// boundary keeps the merge path free of a never-correctly-exercised
		// decrypt branch that a future mis-classified field could silently reach.
		return fmt.Errorf("scope override: %s.%s is encrypted-at-rest and is global-only", o.Type, o.Name)
	}
	row := scopeSettingRow{
		ScopeType: scopeType,
		ScopeID:   scopeID,
		Type:      o.Type,
		Name:      o.Name,
		Value:     o.Value,
		Encrypted: false,
		UpdatedAt: time.Now(),
	}
	// Upsert on the composite unique key — cross-dialect via clause.OnConflict
	// (not raw ON DUPLICATE KEY), same pattern as kvSettingsRepo.Save.
	return r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{
			{Name: "scope_type"}, {Name: "scope_id"}, {Name: "type"}, {Name: "name"},
		},
		DoUpdates: clause.AssignmentColumns([]string{"value", "encrypted", "updated_at"}),
	}).Create(&row).Error
}

func (r *kvScopeSettingsRepo) DeleteOverride(ctx context.Context, scopeType string, scopeID int64, typ, name string) error {
	return r.db.WithContext(ctx).
		Where("scope_type = ? AND scope_id = ? AND type = ? AND name = ?", scopeType, scopeID, typ, name).
		Delete(&scopeSettingRow{}).Error
}

func (r *kvScopeSettingsRepo) DeleteScope(ctx context.Context, scopeType string, scopeID int64) error {
	return r.db.WithContext(ctx).
		Where("scope_type = ? AND scope_id = ?", scopeType, scopeID).
		Delete(&scopeSettingRow{}).Error
}
