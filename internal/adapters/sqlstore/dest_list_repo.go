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

func (r *DestDefinitionRepo) GetList(ctx context.Context, id int64) (domain.DestList, error) {
	if id <= 0 {
		return domain.DestList{}, domain.ErrValidation
	}
	var row destListRow
	if err := r.db.WithContext(ctx).First(&row, "id = ?", id).Error; err != nil {
		return domain.DestList{}, destinationRowError(err)
	}
	return destListToDomain(row), nil
}

func (r *DestDefinitionRepo) ListRefreshTargets(ctx context.Context) ([]domain.DestList, error) {
	var rows []destListRow
	if err := r.db.WithContext(ctx).Select("id", "name", "kind", "source_url", "geosite_category", "geosite_attrs", "last_fetched_at", "last_error", "updated_at").Where("kind IN ?", []string{string(domain.DestListRemote), string(domain.DestListGeosite)}).Order("id").Find(&rows).Error; err != nil {
		return nil, err
	}
	result := make([]domain.DestList, 0, len(rows))
	for _, row := range rows {
		result = append(result, destListToDomain(row))
	}
	return result, nil
}

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
		if row.Kind == string(domain.DestListCustom) && row.ParseReport != nil && row.ParseReport.IgnoredBroad > 0 {
			allow, err := destListUsedForAllow(tx, row.ID)
			if err != nil {
				return false, err
			}
			if allow {
				return false, fmt.Errorf("%w: dest_list_too_broad", domain.ErrValidation)
			}
		}
		if row.OwnerGroupID != 0 && row.Kind != string(domain.DestListCustom) {
			return false, fmt.Errorf("%w: dest_list_group_owned", domain.ErrValidation)
		}
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
			"parse_report": row.ParseReport,
		}).Error
	})
	if err == nil {
		*list = destListToDomain(row)
	}
	return err
}

func equalDestList(a, b destListRow) bool {
	return a.Name == b.Name && a.Kind == b.Kind && a.SourceURL == b.SourceURL && a.GeositeCategory == b.GeositeCategory && a.GeositeAttrs == b.GeositeAttrs &&
		bytes.Equal(a.Entries, b.Entries) && bytes.Equal(a.SourceText, b.SourceText) && equalDestReport(a.ParseReport, b.ParseReport) && a.EntryCount == b.EntryCount && a.RegexpCount == b.RegexpCount && a.ContentSHA256 == b.ContentSHA256 && a.LastError == b.LastError && equalDestTime(a.LastFetchedAt, b.LastFetchedAt)
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
			updates["parse_report"] = destReportFromDomain(result.ParseReport)
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
	if id <= 0 {
		return domain.ErrValidation
	}
	now, err := destWriteTime(now)
	if err != nil {
		return err
	}
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
		var closed []destGroupModeRow
		for _, p := range policies {
			if slices.Contains(p.ListIDs, id) {
				refs = append(refs, domain.DestReference{Kind: "policy", ID: p.ID, Name: p.Name})
			}
		}
		for _, g := range groups {
			if slices.Contains(g.ListIDs, id) || g.BaseListID == id || g.ExtraListID == id {
				if !validDestinationMode(g.Mode, g.Stage) {
					return false, fmt.Errorf("%w: corrupt destination group mode", domain.ErrUnavailable)
				}
				if g.Mode == "open" {
					closed = append(closed, g)
					continue
				}
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
		for _, g := range closed {
			g.ListIDs = slices.DeleteFunc(g.ListIDs, func(v int64) bool { return v == id })
			if g.BaseListID == id {
				g.BaseListID = 0
			}
			if g.ExtraListID == id {
				g.ExtraListID = 0
			}
			if err := tx.Model(&destGroupModeRow{}).Where("group_id = ?", g.GroupID).Updates(map[string]any{
				"list_ids": g.ListIDs, "base_list_id": g.BaseListID, "extra_list_id": g.ExtraListID,
				"updated_at": nextDestRowTime(now, g.UpdatedAt),
			}).Error; err != nil {
				return false, err
			}
		}
		return true, tx.Delete(&list).Error
	})
}
