package sqlstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// DestDefinitionRepo owns definition writes and their publication generation.
// Every writer acquires the singleton state row before touching definitions.
type DestDefinitionRepo struct{ db *gorm.DB }

func NewDestDefinitionRepo(db *gorm.DB) *DestDefinitionRepo { return &DestDefinitionRepo{db: db} }

func readDestState(db *gorm.DB) (domain.DestPolicyState, error) {
	var rows []destPolicyStateRow
	if err := db.Where("id = ?", 1).Limit(1).Find(&rows).Error; err != nil {
		return domain.DestPolicyState{}, err
	}
	if len(rows) == 0 {
		return domain.DestPolicyState{}, nil
	}
	v := rows[0]
	if v.Generation < 0 || v.PublishedGeneration < 0 || v.PublishedGeneration > v.Generation {
		return domain.DestPolicyState{}, fmt.Errorf("%w: invalid destination publication state", domain.ErrUnavailable)
	}
	s := domain.DestPolicyState{Generation: v.Generation, PublishedGeneration: v.PublishedGeneration,
		FirstUnpublishedAt: v.FirstUnpublishedAt, LastWriteAt: v.LastWriteAt, PublishedAt: v.PublishedAt,
		Paused: v.Paused, PublishErrorAt: v.PublishErrorAt}
	if v.PublishError != nil {
		s.PublishError = &domain.DestPublishError{}
		if err := json.Unmarshal([]byte(*v.PublishError), s.PublishError); err != nil {
			return domain.DestPolicyState{}, fmt.Errorf("%w: corrupt destination publication error", domain.ErrUnavailable)
		}
	}
	return s, nil
}

func (r *DestDefinitionRepo) State(ctx context.Context) (domain.DestPolicyState, error) {
	return readDestState(r.db.WithContext(ctx))
}

func lockDestState(tx *gorm.DB) (destPolicyStateRow, error) {
	state := destPolicyStateRow{ID: 1}
	if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&state).Error; err != nil {
		return destPolicyStateRow{}, err
	}
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&state, "id = ?", 1).Error; err != nil {
		return destPolicyStateRow{}, err
	}
	return state, nil
}

func destWriteTime(now time.Time) (time.Time, error) {
	if now.IsZero() {
		return time.Time{}, fmt.Errorf("%w: destination write requires a timestamp", domain.ErrValidation)
	}
	return now.UTC().Truncate(time.Millisecond), nil
}

func nextDestRowTime(now, previous time.Time) time.Time {
	if !now.After(previous) {
		return previous.UTC().Truncate(time.Millisecond).Add(time.Millisecond)
	}
	return now
}

// mutate also accepts metadata-only writes: change returns false when the
// transaction changed no execution definition, so the generation stays put.
func (r *DestDefinitionRepo) mutate(ctx context.Context, now time.Time, change func(*gorm.DB) (bool, error)) error {
	now, err := destWriteTime(now)
	if err != nil {
		return err
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		state, err := lockDestState(tx)
		if err != nil {
			return err
		}
		changed, err := change(tx)
		if err != nil || !changed {
			return err
		}
		if state.Generation < 0 || state.Generation == math.MaxInt64 {
			return fmt.Errorf("%w: destination generation exhausted", domain.ErrResourceExhausted)
		}
		updates := map[string]any{"generation": gorm.Expr("generation + 1"), "last_write_at": now}
		if state.FirstUnpublishedAt == nil {
			updates["first_unpublished_at"] = now
		}
		return tx.Model(&destPolicyStateRow{}).Where("id = ?", 1).Updates(updates).Error
	})
}

func nextDestPriority(tx *gorm.DB, action domain.DestAction) (int, error) {
	var highest int
	if err := tx.Model(&destPolicyRow{}).Where("action = ?", string(action)).Select("COALESCE(MAX(priority), 0)").Scan(&highest).Error; err != nil {
		return 0, err
	}
	if highest == math.MaxInt {
		return 0, fmt.Errorf("%w: destination priority exhausted", domain.ErrResourceExhausted)
	}
	return highest + 1, nil
}

func (r *DestDefinitionRepo) SavePolicy(ctx context.Context, p *domain.DestPolicy, expected time.Time, now time.Time) error {
	if p == nil || p.ID < 0 || p.Name == "" || (p.ID > 0 && expected.IsZero()) {
		return fmt.Errorf("%w: destination policy identity or edit version", domain.ErrValidation)
	}
	now, err := destWriteTime(now)
	if err != nil {
		return err
	}
	row := destPolicyFromDomain(*p)
	err = r.mutate(ctx, now, func(tx *gorm.DB) (bool, error) {
		if err := destinationListReferences(tx, row.ListIDs, 0, false); err != nil {
			return false, err
		}
		if row.ID == 0 {
			row.Priority, err = nextDestPriority(tx, p.Action)
			if err != nil {
				return false, err
			}
			row.CreatedAt, row.UpdatedAt = now, now
			err = tx.Create(&row).Error
		} else {
			var old destPolicyRow
			if err := tx.First(&old, "id = ?", row.ID).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					return false, domain.ErrNotFound
				}
				return false, err
			}
			if !old.UpdatedAt.Equal(expected) {
				return false, fmt.Errorf("%w: dest_policy_stale", domain.ErrConflict)
			}
			if equalDestPolicy(row, old) {
				row = old
				return false, nil
			}
			row.CreatedAt, row.UpdatedAt, row.Priority = old.CreatedAt, nextDestRowTime(now, old.UpdatedAt), old.Priority
			if row.Action != old.Action {
				row.Priority, err = nextDestPriority(tx, p.Action)
				if err != nil {
					return false, err
				}
			}
			err = tx.Model(&destPolicyRow{}).Where("id = ?", row.ID).Updates(map[string]any{
				"name": row.Name, "action": row.Action, "list_ids": row.ListIDs, "inline": row.Inline,
				"scope": row.Scope, "group_ids": row.GroupIDs, "priority": row.Priority, "enabled": row.Enabled,
				"counts_as_risk": row.CountsAsRisk, "template_key": row.TemplateKey, "updated_at": row.UpdatedAt,
			}).Error
		}
		if isUniqueViolationErr(err) {
			return false, fmt.Errorf("%w: dest_name_taken", domain.ErrAlreadyExists)
		}
		return true, err
	})
	if err == nil {
		*p = destPolicyToDomain(row)
	}
	return err
}

func (r *DestDefinitionRepo) DeletePolicy(ctx context.Context, id int64, now time.Time) error {
	return r.mutate(ctx, now, func(tx *gorm.DB) (bool, error) {
		result := tx.Where("id = ?", id).Delete(&destPolicyRow{})
		if result.Error != nil {
			return false, result.Error
		}
		if result.RowsAffected == 0 {
			return false, domain.ErrNotFound
		}
		return true, nil
	})
}

func equalDestPolicy(a, b destPolicyRow) bool {
	return a.Name == b.Name && a.Action == b.Action && a.Scope == b.Scope &&
		a.Enabled == b.Enabled && a.CountsAsRisk == b.CountsAsRisk && a.TemplateKey == b.TemplateKey &&
		slices.Equal(a.ListIDs, b.ListIDs) && slices.Equal(a.GroupIDs, b.GroupIDs) &&
		a.Inline.Ports == b.Inline.Ports && a.Inline.Network == b.Inline.Network && a.Inline.Private == b.Inline.Private &&
		slices.Equal(a.Inline.CIDRs, b.Inline.CIDRs) && slices.Equal(a.Inline.Protocols, b.Inline.Protocols)
}

func (r *DestDefinitionRepo) readTransaction(ctx context.Context, read func(*gorm.DB) error) error {
	var options *sql.TxOptions
	if r.db.Dialector.Name() != "sqlite" {
		options = &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true}
	}
	return r.db.WithContext(ctx).Transaction(read, options)
}

func (r *DestDefinitionRepo) ReadDefinitions(ctx context.Context) (domain.DestDefinitions, error) {
	var defs domain.DestDefinitions
	err := r.readTransaction(ctx, func(tx *gorm.DB) error {
		var err error
		defs, err = readDestinationDefinitions(tx)
		return err
	})
	if err != nil {
		return domain.DestDefinitions{}, err
	}
	return defs, err
}

func readDestinationDefinitions(tx *gorm.DB) (domain.DestDefinitions, error) {
	var defs domain.DestDefinitions
	err := func() error {
		var err error
		// The generation read establishes the snapshot before definitions.
		defs.State, err = readDestState(tx)
		if err != nil {
			return err
		}
		var lists []destListRow
		var policies []destPolicyRow
		var exemptions []destExemptionRow
		var groups []destGroupModeRow
		if err := tx.Order("id").Find(&lists).Error; err != nil {
			return err
		}
		if err := tx.Order("priority, id").Find(&policies).Error; err != nil {
			return err
		}
		if err := tx.Order("user_id").Find(&exemptions).Error; err != nil {
			return err
		}
		if err := tx.Order("group_id").Find(&groups).Error; err != nil {
			return err
		}
		for _, row := range lists {
			defs.Lists = append(defs.Lists, destListToDomain(row))
		}
		for _, row := range policies {
			defs.Policies = append(defs.Policies, destPolicyToDomain(row))
		}
		for _, row := range exemptions {
			defs.Exemptions = append(defs.Exemptions, domain.DestExemption{UserID: row.UserID, Reason: row.Reason, CreatedBy: row.CreatedBy, CreatedAt: row.CreatedAt, ExpiresAt: row.ExpiresAt})
		}
		for _, row := range groups {
			defs.Groups = append(defs.Groups, domain.DestGroupMode{GroupID: row.GroupID, Mode: row.Mode, Stage: row.Stage, ListIDs: append([]int64(nil), row.ListIDs...), BaseListID: row.BaseListID, ExtraListID: row.ExtraListID, StageChangedAt: row.StageChangedAt, UpdatedAt: row.UpdatedAt})
		}
		return nil
	}()
	if err != nil {
		return domain.DestDefinitions{}, err
	}
	return defs, nil
}

func (r *DestDefinitionRepo) Publish(ctx context.Context, generation, previousPublished int64, body []byte, now time.Time) error {
	if generation <= previousPublished || previousPublished < 0 || !json.Valid(body) || len(body) == 0 || body[0] != '{' {
		return fmt.Errorf("%w: destination publication candidate", domain.ErrValidation)
	}
	now, err := destWriteTime(now)
	if err != nil {
		return err
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&destPolicyStateRow{}).Where("id = ? AND generation = ? AND published_generation = ?", 1, generation, previousPublished).
			Updates(map[string]any{"published_generation": generation, "published_at": now, "first_unpublished_at": nil, "publish_error": nil, "publish_error_at": nil})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return fmt.Errorf("%w: destination publication changed", domain.ErrConflict)
		}
		row := destPolicySnapshotRow{Generation: generation, Body: append(destBytes(nil), body...), CreatedAt: now}
		if err := tx.Create(&row).Error; err != nil {
			return err
		}
		return tx.Where("generation < ?", generation).Delete(&destPolicySnapshotRow{}).Error
	})
}

func (r *DestDefinitionRepo) RecordPublishError(ctx context.Context, generation, previousPublished int64, issue domain.DestPublishError, now time.Time) error {
	if generation <= previousPublished || previousPublished < 0 || issue.Kind == "" {
		return domain.ErrValidation
	}
	now, err := destWriteTime(now)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(issue)
	if err != nil {
		return err
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		state, err := lockDestState(tx)
		if err != nil {
			return err
		}
		if state.Generation != generation || state.PublishedGeneration != previousPublished {
			return fmt.Errorf("%w: destination publication changed", domain.ErrConflict)
		}
		if state.PublishError != nil && *state.PublishError == string(raw) {
			return nil
		}
		return tx.Model(&destPolicyStateRow{}).Where("id = ?", 1).Updates(map[string]any{"publish_error": string(raw), "publish_error_at": now}).Error
	})
}

func (r *DestDefinitionRepo) Published(ctx context.Context) (domain.DestPolicySnapshot, bool, error) {
	_, snapshot, found, err := r.PublishedState(ctx)
	return snapshot, found, err
}

// PublishedState returns the live pause flag and its selected published body
// from one consistent read, so an older pause read cannot override a new one.
func (r *DestDefinitionRepo) PublishedState(ctx context.Context) (domain.DestPolicyState, domain.DestPolicySnapshot, bool, error) {
	var state domain.DestPolicyState
	var snapshot domain.DestPolicySnapshot
	found := false
	err := r.readTransaction(ctx, func(tx *gorm.DB) error {
		var err error
		state, err = readDestState(tx)
		if err != nil {
			return err
		}
		snapshot, found, err = readDestPublishedSnapshot(tx, state)
		return err
	})
	if err != nil {
		return domain.DestPolicyState{}, domain.DestPolicySnapshot{}, false, err
	}
	return state, snapshot, found, nil
}

func readDestPublishedSnapshot(tx *gorm.DB, state domain.DestPolicyState) (domain.DestPolicySnapshot, bool, error) {
	if state.PublishedGeneration == 0 {
		return domain.DestPolicySnapshot{}, false, nil
	}
	var rows []destPolicySnapshotRow
	if err := tx.Where("generation = ?", state.PublishedGeneration).Find(&rows).Error; err != nil {
		return domain.DestPolicySnapshot{}, false, err
	}
	if len(rows) != 1 || !json.Valid(rows[0].Body) || len(rows[0].Body) == 0 || rows[0].Body[0] != '{' {
		return domain.DestPolicySnapshot{}, false, fmt.Errorf("%w: missing or corrupt destination snapshot", domain.ErrUnavailable)
	}
	return domain.DestPolicySnapshot{Generation: rows[0].Generation, Body: append([]byte(nil), rows[0].Body...), CreatedAt: rows[0].CreatedAt}, true, nil
}
