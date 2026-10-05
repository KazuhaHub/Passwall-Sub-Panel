package sqlstore

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"gorm.io/gorm"
)

func validDestinationMode(mode, stage string) bool {
	return mode == "open" && stage == "" || mode == "allowlist" && (stage == "trial" || stage == "enforce")
}

func groupModeDomain(row destGroupModeRow) domain.DestGroupMode {
	return domain.DestGroupMode{GroupID: row.GroupID, Mode: row.Mode, Stage: row.Stage,
		ListIDs: append([]int64(nil), row.ListIDs...), BaseListID: row.BaseListID,
		ExtraListID: row.ExtraListID, StageChangedAt: runtimeTimeCopy(row.StageChangedAt), UpdatedAt: row.UpdatedAt}
}

func readGroupMode(tx *gorm.DB, id int64) (destGroupModeRow, bool, error) {
	var row destGroupModeRow
	err := tx.First(&row, "group_id = ?", id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return destGroupModeRow{GroupID: id, Mode: "open"}, false, nil
	}
	if err != nil {
		return destGroupModeRow{}, false, err
	}
	if !validDestinationMode(row.Mode, row.Stage) || row.BaseListID < 0 || row.ExtraListID < 0 {
		return destGroupModeRow{}, false, fmt.Errorf("%w: corrupt destination group mode", domain.ErrUnavailable)
	}
	return row, true, nil
}

func destinationGroupExists(tx *gorm.DB, id int64) error {
	var group groupRow
	return destinationRowError(tx.Select("id").First(&group, "id = ?", id).Error)
}

func (r *DestDefinitionRepo) GetGroupMode(ctx context.Context, id int64) (domain.DestGroupMode, error) {
	if id <= 0 {
		return domain.DestGroupMode{}, domain.ErrValidation
	}
	if r == nil || r.db == nil {
		return domain.DestGroupMode{}, domain.ErrUnavailable
	}
	var row destGroupModeRow
	err := r.readTransaction(ctx, func(tx *gorm.DB) error {
		if err := destinationGroupExists(tx, id); err != nil {
			return err
		}
		var err error
		row, _, err = readGroupMode(tx, id)
		return err
	})
	if err != nil {
		return domain.DestGroupMode{}, err
	}
	return groupModeDomain(row), nil
}

// Read only reference metadata, never the potentially large list bodies.
// owner=0 rejects all private lists for ordinary policies.
func destinationListReferences(tx *gorm.DB, ids []int64, owner int64, ready bool) error {
	for _, id := range ids {
		if id <= 0 {
			return domain.ErrValidation
		}
	}
	if len(ids) == 0 {
		return nil
	}
	ids = slices.Clone(ids)
	slices.Sort(ids)
	ids = slices.Compact(ids)
	var lists []destListRow
	if err := tx.Select("id", "owner_group_id", "kind", "last_fetched_at").Where("id IN ?", ids).Find(&lists).Error; err != nil {
		return err
	}
	if len(lists) != len(ids) {
		return fmt.Errorf("%w: dest_list_missing", domain.ErrNotFound)
	}
	for _, list := range lists {
		if list.OwnerGroupID != 0 && list.OwnerGroupID != owner {
			return fmt.Errorf("%w: dest_list_group_owned", domain.ErrValidation)
		}
		switch domain.DestListKind(list.Kind) {
		case domain.DestListCustom:
		case domain.DestListRemote, domain.DestListGeosite:
			if ready && list.LastFetchedAt == nil {
				return fmt.Errorf("%w: dest_list_not_ready", domain.ErrConflict)
			}
		default:
			return fmt.Errorf("%w: corrupt destination list kind", domain.ErrUnavailable)
		}
	}
	return nil
}

func ensureModeOwnedList(tx *gorm.DB, id, groupID int64, initial domain.DestList, now time.Time) (int64, error) {
	if id != 0 {
		var list destListRow
		if err := tx.Select("id", "owner_group_id", "kind").First(&list, "id = ?", id).Error; err != nil {
			return 0, destinationRowError(err)
		}
		if list.OwnerGroupID != groupID || list.Kind != string(domain.DestListCustom) {
			return 0, fmt.Errorf("%w: corrupt destination owned list", domain.ErrUnavailable)
		}
		return id, nil
	}
	if initial.ID != 0 || initial.Kind != domain.DestListCustom || strings.TrimSpace(initial.Name) == "" || utf8.RuneCountInString(initial.Name) > 128 || initial.SourceURL != "" || initial.GeositeCategory != "" || initial.GeositeAttrs != "" {
		return 0, domain.ErrValidation
	}
	row := destListFromDomain(initial)
	row.OwnerGroupID, row.CreatedAt, row.UpdatedAt = groupID, now, now
	if err := tx.Create(&row).Error; err != nil {
		return 0, err
	}
	return row.ID, nil
}

func (r *DestDefinitionRepo) SaveGroupMode(ctx context.Context, mode *domain.DestGroupMode, expected time.Time, initial [2]domain.DestList, now time.Time) error {
	if mode == nil || mode.GroupID <= 0 {
		return domain.ErrValidation
	}
	if r == nil || r.db == nil {
		return domain.ErrUnavailable
	}
	now, err := destWriteTime(now)
	if err != nil {
		return err
	}
	row := destGroupModeRow{GroupID: mode.GroupID, Mode: mode.Mode, Stage: mode.Stage, ListIDs: append(jsonInt64s(nil), mode.ListIDs...)}
	slices.Sort(row.ListIDs)
	row.ListIDs = slices.Compact(row.ListIDs)
	err = r.mutate(ctx, now, func(tx *gorm.DB) (bool, error) {
		if err := destinationGroupExists(tx, row.GroupID); err != nil {
			return false, err
		}
		old, found, err := readGroupMode(tx, row.GroupID)
		if err != nil {
			return false, err
		}
		if !old.UpdatedAt.Equal(expected) {
			return false, fmt.Errorf("%w: dest_mode_stale", domain.ErrConflict)
		}
		if !validDestinationMode(row.Mode, row.Stage) || old.Mode == "open" && row.Mode == "allowlist" && row.Stage != "trial" {
			return false, fmt.Errorf("%w: dest_mode_invalid_transition", domain.ErrConflict)
		}
		if err := destinationListReferences(tx, row.ListIDs, row.GroupID, row.Stage == "enforce"); err != nil {
			return false, err
		}
		row.BaseListID, row.ExtraListID, row.StageChangedAt = old.BaseListID, old.ExtraListID, runtimeTimeCopy(old.StageChangedAt)
		if row.Mode == "allowlist" {
			row.BaseListID, err = ensureModeOwnedList(tx, row.BaseListID, row.GroupID, initial[0], now)
			if err != nil {
				return false, err
			}
			row.ExtraListID, err = ensureModeOwnedList(tx, row.ExtraListID, row.GroupID, initial[1], now)
			if err != nil {
				return false, err
			}
			if row.BaseListID == row.ExtraListID {
				return false, fmt.Errorf("%w: duplicate destination owned lists", domain.ErrUnavailable)
			}
		}
		if row.Mode == old.Mode && row.Stage == old.Stage && slices.Equal(row.ListIDs, old.ListIDs) && row.BaseListID == old.BaseListID && row.ExtraListID == old.ExtraListID {
			row = old
			return false, nil
		}
		if row.Mode != old.Mode || row.Stage != old.Stage {
			stageAt := now
			if old.StageChangedAt != nil {
				stageAt = nextDestRowTime(now, *old.StageChangedAt)
			}
			row.StageChangedAt = &stageAt
		}
		row.UpdatedAt = nextDestRowTime(now, old.UpdatedAt)
		if !found {
			return true, tx.Create(&row).Error
		}
		return true, tx.Model(&destGroupModeRow{}).Where("group_id = ?", row.GroupID).Updates(map[string]any{
			"mode": row.Mode, "stage": row.Stage, "list_ids": row.ListIDs,
			"base_list_id": row.BaseListID, "extra_list_id": row.ExtraListID,
			"stage_changed_at": row.StageChangedAt, "updated_at": row.UpdatedAt,
		}).Error
	})
	if err == nil {
		*mode = groupModeDomain(row)
	}
	return err
}

var _ ports.DestinationGroupModeRepo = (*DestDefinitionRepo)(nil)
