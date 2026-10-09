package sqlstore

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type legalConsentRow struct {
	UserID         int64     `gorm:"primaryKey;autoIncrement:false"`
	ConsentVersion int64     `gorm:"primaryKey;autoIncrement:false"`
	AcceptedAt     time.Time `gorm:"not null"`
	Method         string    `gorm:"size:16;not null"`
}

func (legalConsentRow) TableName() string { return "legal_consents" }

var _ ports.RegisteredUserWriter = (*userRepo)(nil)

// Writers take the same global row lock as publication before checking the
// version. The insert-only seed covers the first-publication race, even when
// legal is currently disabled. Read-only callers use SHARE without creating it.
func readLegalState(tx *gorm.DB, write bool) (domain.LegalConsentStatus, error) {
	strength := "SHARE"
	if write {
		seed := settingRow{Type: "legal", Name: "consent_version", Value: "0", UpdatedAt: time.Now().UTC()}
		if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "type"}, {Name: "name"}}, DoNothing: true}).Create(&seed).Error; err != nil {
			return domain.LegalConsentStatus{}, err
		}
		strength = "UPDATE"
	}
	var state settingRow
	err := tx.Clauses(clause.Locking{Strength: strength}).Where("type = ? AND name = ?", "legal", "consent_version").First(&state).Error
	status := domain.LegalConsentStatus{}
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return status, err
	}
	if err == nil {
		status.ConsentVersion, err = strconv.ParseInt(state.Value, 10, 64)
		if err != nil || status.ConsentVersion < 0 || state.Encrypted {
			return status, fmt.Errorf("%w: invalid stored legal consent version", domain.ErrValidation)
		}
	}
	var enabled settingRow
	err = tx.Where("type = ? AND name = ?", "legal", "enabled").First(&enabled).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return status, nil
	}
	if err != nil {
		return status, err
	}
	status.Enabled, err = strconv.ParseBool(enabled.Value)
	if err != nil || enabled.Encrypted {
		return status, fmt.Errorf("%w: invalid stored legal enablement", domain.ErrValidation)
	}
	return status, nil
}

func validateRegisteredConsent(state domain.LegalConsentStatus, accepted int64) error {
	if state.Enabled && state.ConsentVersion > 0 && accepted != state.ConsentVersion {
		return domain.ErrLegalConsentOutdated
	}
	return nil
}

func recordLegalConsent(tx *gorm.DB, userID, version int64, method string) error {
	row := legalConsentRow{UserID: userID, ConsentVersion: version, AcceptedAt: time.Now().UTC(), Method: method}
	return tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "user_id"}, {Name: "consent_version"}}, DoNothing: true}).Create(&row).Error
}

func (r *legalRepo) Accept(ctx context.Context, userID, accepted int64) error {
	if userID <= 0 {
		return domain.ErrNotFound
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		state, err := readLegalState(tx, true)
		if err != nil {
			return err
		}
		if !state.Enabled || state.ConsentVersion == 0 {
			return domain.ErrNotFound
		}
		var u userRow
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&u, userID).Error; err != nil {
			return wrapNotFound(err)
		}
		if u.Role != string(domain.RoleUser) {
			return domain.ErrForbidden
		}
		if err := validateRegisteredConsent(state, accepted); err != nil {
			return err
		}
		return recordLegalConsent(tx, userID, accepted, "prompt")
	})
}

func (r *legalRepo) Status(ctx context.Context, userID int64) (domain.LegalConsentStatus, error) {
	var state domain.LegalConsentStatus
	if userID <= 0 {
		return state, domain.ErrNotFound
	}
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var err error
		state, err = readLegalState(tx, false)
		if err != nil {
			return err
		}
		var u userRow
		if err := tx.Select("id", "role").First(&u, userID).Error; err != nil {
			return wrapNotFound(err)
		}
		if u.Role != string(domain.RoleUser) || !state.Enabled || state.ConsentVersion == 0 {
			return nil
		}
		var count int64
		if err := tx.Model(&legalConsentRow{}).Where("user_id = ? AND consent_version >= ?", userID, state.ConsentVersion).Count(&count).Error; err != nil {
			return err
		}
		state.Pending = count == 0
		return nil
	})
	return state, err
}

// A new major publication affects all ordinary accounts, including accounts
// that have already accepted the current version. Disabled accounts also count.
func (r *legalRepo) AffectedUsers(ctx context.Context) (int64, error) {
	var n int64
	err := r.db.WithContext(ctx).Model(&userRow{}).Where("role = ?", string(domain.RoleUser)).Count(&n).Error
	return n, err
}

func (r *legalRepo) PurgeOrphans(ctx context.Context) (int64, error) {
	live := r.db.WithContext(ctx).Model(&userRow{}).Select("id")
	result := r.db.WithContext(ctx).Where("user_id NOT IN (?)", live).Delete(&legalConsentRow{})
	return result.RowsAffected, result.Error
}

func (r *userRepo) CreateRegistered(ctx context.Context, u *domain.User, accepted int64) error {
	if u == nil || !u.SelfRegistered || u.Role != domain.RoleUser {
		return domain.ErrValidation
	}
	created := *u
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		state, err := readLegalState(tx, true)
		if err != nil {
			return err
		}
		if err := validateRegisteredConsent(state, accepted); err != nil {
			return err
		}
		writer := *r
		writer.db = tx
		if err := writer.Create(ctx, &created); err != nil {
			return err
		}
		if state.Enabled && state.ConsentVersion > 0 {
			return recordLegalConsent(tx, created.ID, state.ConsentVersion, "register")
		}
		return nil
	})
	if err == nil {
		*u = created
	}
	return err
}

func (r *userRepo) ResumeRegistration(ctx context.Context, userID int64, passwordHash string, accepted int64) error {
	if userID <= 0 || passwordHash == "" {
		return domain.ErrValidation
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		state, err := readLegalState(tx, true)
		if err != nil {
			return err
		}
		if err := validateRegisteredConsent(state, accepted); err != nil {
			return err
		}
		var u userRow
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&u, userID).Error; err != nil {
			return wrapNotFound(err)
		}
		if u.Enabled || u.AutoDisabledReason != string(domain.DisabledPendingEmailVerify) || u.Role != string(domain.RoleUser) || u.PasswordHash == "" {
			return domain.ErrAlreadyExists
		}
		if err := tx.Model(&userRow{}).Where("id = ?", userID).Updates(map[string]any{"password_hash": passwordHash, "token_version": gorm.Expr("token_version + 1")}).Error; err != nil {
			return err
		}
		if state.Enabled && state.ConsentVersion > 0 {
			return recordLegalConsent(tx, userID, state.ConsentVersion, "register")
		}
		return nil
	})
}
