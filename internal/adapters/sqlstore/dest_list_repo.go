package sqlstore

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"gorm.io/gorm"
)

func (r *DestDefinitionRepo) SaveList(ctx context.Context, list *domain.DestList, expected, now time.Time) error {
	if list == nil || list.ID < 0 || list.Name == "" || list.OwnerGroupID < 0 || (list.ID > 0 && expected.IsZero()) {
		return domain.ErrValidation
	}
	if list.Kind != domain.DestListCustom && list.Kind != domain.DestListRemote && list.Kind != domain.DestListGeosite {
		return domain.ErrValidation
	}
	now, err := destWriteTime(now)
	if err != nil {
		return err
	}
	row := destListFromDomain(*list)
	err = r.mutate(ctx, now, func(tx *gorm.DB) (bool, error) {
		if row.ID == 0 {
			row.CreatedAt, row.UpdatedAt = now, now
			return true, tx.Create(&row).Error
		}
		var old destListRow
		if err := tx.First(&old, "id = ?", row.ID).Error; err != nil {
			return false, destinationRowError(err)
		}
		if !old.UpdatedAt.Equal(expected) {
			return false, fmt.Errorf("%w: dest_list_stale", domain.ErrConflict)
		}
		row.CreatedAt, row.OwnerGroupID = old.CreatedAt, old.OwnerGroupID
		if equalDestList(row, old) {
			row = old
			return false, nil
		}
		row.UpdatedAt = nextDestRowTime(now, old.UpdatedAt)
		definitionChanged := row.ContentSHA256 != old.ContentSHA256 || row.Kind != old.Kind || row.SourceURL != old.SourceURL || row.GeositeCategory != old.GeositeCategory || row.GeositeAttrs != old.GeositeAttrs
		return definitionChanged, tx.Model(&destListRow{}).Where("id = ?", row.ID).Updates(map[string]any{
			"name": row.Name, "kind": row.Kind, "source_url": row.SourceURL, "geosite_category": row.GeositeCategory,
			"geosite_attrs": row.GeositeAttrs, "entries": row.Entries, "source_text": row.SourceText,
			"entry_count": row.EntryCount, "regexp_count": row.RegexpCount, "content_sha256": row.ContentSHA256,
			"last_fetched_at": row.LastFetchedAt, "last_error": row.LastError, "updated_at": row.UpdatedAt,
		}).Error
	})
	if err == nil {
		*list = destListToDomain(row)
	}
	return err
}

func equalDestList(a, b destListRow) bool {
	return a.Name == b.Name && a.Kind == b.Kind && a.SourceURL == b.SourceURL && a.GeositeCategory == b.GeositeCategory && a.GeositeAttrs == b.GeositeAttrs &&
		bytes.Equal(a.Entries, b.Entries) && bytes.Equal(a.SourceText, b.SourceText) && a.EntryCount == b.EntryCount && a.RegexpCount == b.RegexpCount && a.ContentSHA256 == b.ContentSHA256 && a.LastError == b.LastError && equalDestTime(a.LastFetchedAt, b.LastFetchedAt)
}

func (r *DestDefinitionRepo) CommitListRefresh(ctx context.Context, captured domain.DestList, result domain.DestListRefresh, now time.Time) error {
	if captured.ID <= 0 || captured.UpdatedAt.IsZero() || (captured.Kind != domain.DestListRemote && captured.Kind != domain.DestListGeosite) {
		return domain.ErrValidation
	}
	now, err := destWriteTime(now)
	if err != nil {
		return err
	}
	return r.mutate(ctx, now, func(tx *gorm.DB) (bool, error) {
		var row destListRow
		if err := tx.First(&row, "id = ?", captured.ID).Error; err != nil {
			return false, destinationRowError(err)
		}
		if !row.UpdatedAt.Equal(captured.UpdatedAt) || row.Kind != string(captured.Kind) || row.SourceURL != captured.SourceURL || row.GeositeCategory != captured.GeositeCategory || row.GeositeAttrs != captured.GeositeAttrs {
			return false, fmt.Errorf("%w: dest_list_stale", domain.ErrConflict)
		}
		updates := map[string]any{"last_error": result.LastError, "updated_at": nextDestRowTime(now, row.UpdatedAt)}
		changed := false
		if result.LastError == "" {
			updates["last_fetched_at"] = now
			if result.ContentSHA256 != row.ContentSHA256 {
				updates["entries"] = destBytes(result.Entries)
				updates["entry_count"], updates["regexp_count"], updates["content_sha256"] = result.EntryCount, result.RegexpCount, result.ContentSHA256
				changed = true
			}
		}
		return changed, tx.Model(&destListRow{}).Where("id = ?", row.ID).Updates(updates).Error
	})
}

func (r *DestDefinitionRepo) DeleteList(ctx context.Context, id int64, now time.Time) error {
	return r.mutate(ctx, now, func(tx *gorm.DB) (bool, error) {
		var list destListRow
		if err := tx.First(&list, "id = ?", id).Error; err != nil {
			return false, destinationRowError(err)
		}
		var policies []destPolicyRow
		var groups []destGroupModeRow
		if err := tx.Order("id").Find(&policies).Error; err != nil {
			return false, err
		}
		if err := tx.Order("group_id").Find(&groups).Error; err != nil {
			return false, err
		}
		var refs []domain.DestReference
		for _, p := range policies {
			if slices.Contains(p.ListIDs, id) {
				refs = append(refs, domain.DestReference{Kind: "policy", ID: p.ID, Name: p.Name})
			}
		}
		for _, g := range groups {
			if slices.Contains(g.ListIDs, id) || g.BaseListID == id || g.ExtraListID == id {
				var group groupRow
				if err := tx.First(&group, "id = ?", g.GroupID).Error; err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
					return false, err
				}
				refs = append(refs, domain.DestReference{Kind: "group", ID: g.GroupID, Name: group.Name})
			}
		}
		if len(refs) != 0 {
			return false, &domain.DestListInUseError{UsedBy: refs}
		}
		return true, tx.Delete(&list).Error
	})
}
