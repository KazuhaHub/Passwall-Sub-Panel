package sqlstore

import (
	"context"
	"fmt"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"gorm.io/gorm"
)

// Parsing is local CPU work inside the definition lock. No network or catalog
// download belongs in edit. Reading source here avoids read/modify/write loss
// when two administrators append different entries concurrently.
func (r *DestDefinitionRepo) EditListEntries(ctx context.Context, id int64, now time.Time, edit func(domain.DestList, bool) (domain.DestList, error)) (domain.DestList, error) {
	if id <= 0 || edit == nil {
		return domain.DestList{}, domain.ErrValidation
	}
	now, err := destWriteTime(now)
	if err != nil {
		return domain.DestList{}, err
	}
	var committed destListRow
	err = r.mutate(ctx, now, func(tx *gorm.DB) (bool, error) {
		var old destListRow
		if err := tx.First(&old, "id = ?", id).Error; err != nil {
			return false, destinationRowError(err)
		}
		if old.Kind != string(domain.DestListCustom) {
			return false, domain.ErrValidation
		}
		allow, err := destListUsedForAllow(tx, id)
		if err != nil {
			return false, err
		}
		var changed bool
		committed, changed, err = editDestinationListRow(tx, old, now, allow, edit)
		return changed, err
	})
	if err != nil {
		return domain.DestList{}, err
	}
	return destListToDomain(committed), nil
}

// Both ordinary entry edits and group exceptions preserve server-owned list
// metadata and commit only parser output from the fresh locked source.
func editDestinationListRow(tx *gorm.DB, old destListRow, now time.Time, allow bool, edit func(domain.DestList, bool) (domain.DestList, error)) (destListRow, bool, error) {
	candidate, err := edit(destListToDomain(old), allow)
	if err != nil {
		return destListRow{}, false, err
	}
	row := old
	row.SourceText = append(destBytes(nil), candidate.SourceText...)
	row.Entries = append(destBytes(nil), candidate.Entries...)
	row.ContentSHA256, row.EntryCount, row.RegexpCount, row.ParseReport = candidate.ContentSHA256, candidate.EntryCount, candidate.RegexpCount, destReportFromDomain(candidate.ParseReport)
	if allow && row.ParseReport != nil && row.ParseReport.IgnoredBroad > 0 {
		return destListRow{}, false, fmt.Errorf("%w: dest_list_too_broad", domain.ErrValidation)
	}
	if equalDestList(row, old) {
		return old, false, nil
	}
	row.UpdatedAt = nextDestRowTime(now, old.UpdatedAt)
	if err := tx.Model(&destListRow{}).Where("id = ?", old.ID).Updates(map[string]any{"source_text": row.SourceText, "entries": row.Entries, "content_sha256": row.ContentSHA256, "entry_count": row.EntryCount, "regexp_count": row.RegexpCount, "parse_report": row.ParseReport, "updated_at": row.UpdatedAt}).Error; err != nil {
		return destListRow{}, false, err
	}
	return row, row.ContentSHA256 != old.ContentSHA256, nil
}
