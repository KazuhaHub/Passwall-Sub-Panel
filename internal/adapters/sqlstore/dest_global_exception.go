package sqlstore

import (
	"context"
	"fmt"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"gorm.io/gorm"
)

func globalExceptionConflict() error {
	return fmt.Errorf("%w: dest_exception_conflict", domain.ErrConflict)
}
func validGlobalExceptionPolicy(p destPolicyRow) bool {
	return p.Action == string(domain.DestAllow) && p.Scope == string(domain.DestScopeAll) && p.Enabled && len(p.ListIDs) == 1 && len(p.GroupIDs) == 0 && len(p.Inline.CIDRs) == 0 && p.Inline.Ports == "" && p.Inline.Network == "" && len(p.Inline.Protocols) == 0 && !p.Inline.Private
}
func globalExceptionPolicyName(tx *gorm.DB) (string, error) {
	for suffix := 1; ; suffix++ {
		name := "放行例外"
		if suffix > 1 {
			name = fmt.Sprintf("放行例外 (%d)", suffix)
		}
		var count int64
		if err := tx.Model(&destPolicyRow{}).Where("name = ?", name).Count(&count).Error; err != nil {
			return "", err
		}
		if count == 0 {
			return name, nil
		}
	}
}

// First use commits the list, leading allow policy, shifted priorities and one
// generation together. Later calls parse only the fresh linked custom source
// under the same definition lock. No nested repository or network I/O in edit.
func (r *DestDefinitionRepo) AddGlobalException(ctx context.Context, now time.Time, edit func(domain.DestList) (domain.DestList, error)) (domain.DestGlobalExceptionCommit, error) {
	if edit == nil {
		return domain.DestGlobalExceptionCommit{}, domain.ErrValidation
	}
	now, err := destWriteTime(now)
	if err != nil {
		return domain.DestGlobalExceptionCommit{}, err
	}
	var result domain.DestGlobalExceptionCommit
	err = r.mutate(ctx, now, func(tx *gorm.DB) (bool, error) {
		var policies []destPolicyRow
		if err := tx.Where("template_key = ?", domain.DestGlobalExceptionTemplateKey).Limit(2).Find(&policies).Error; err != nil {
			return false, err
		}
		if len(policies) > 1 {
			return false, globalExceptionConflict()
		}
		var old destListRow
		if len(policies) == 1 {
			p := policies[0]
			if !validGlobalExceptionPolicy(p) {
				return false, globalExceptionConflict()
			}
			if err := tx.First(&old, "id = ?", p.ListIDs[0]).Error; err != nil {
				if destinationRowError(err) == domain.ErrNotFound {
					return false, globalExceptionConflict()
				}
				return false, err
			}
			if old.Kind != string(domain.DestListCustom) || old.OwnerGroupID != 0 {
				return false, globalExceptionConflict()
			}
			result = domain.DestGlobalExceptionCommit{ListID: old.ID, PolicyID: p.ID}
		} else {
			old = destListRow{Name: "放行例外", Kind: string(domain.DestListCustom), CreatedAt: now, UpdatedAt: now}
		}
		candidate, err := edit(destListToDomain(old))
		if err != nil {
			return false, err
		}
		row := old
		row.SourceText = append(destBytes(nil), candidate.SourceText...)
		row.Entries = append(destBytes(nil), candidate.Entries...)
		row.ContentSHA256, row.EntryCount, row.RegexpCount, row.ParseReport = candidate.ContentSHA256, candidate.EntryCount, candidate.RegexpCount, destReportFromDomain(candidate.ParseReport)
		if row.EntryCount == 0 || row.ParseReport != nil && row.ParseReport.IgnoredBroad > 0 {
			return false, fmt.Errorf("%w: dest_list_too_broad", domain.ErrValidation)
		}
		if len(policies) == 1 {
			if equalDestList(row, old) {
				return false, nil
			}
			row.UpdatedAt = nextDestRowTime(now, old.UpdatedAt)
			if err := tx.Model(&destListRow{}).Where("id = ?", old.ID).Updates(map[string]any{"source_text": row.SourceText, "entries": row.Entries, "content_sha256": row.ContentSHA256, "entry_count": row.EntryCount, "regexp_count": row.RegexpCount, "parse_report": row.ParseReport, "updated_at": row.UpdatedAt}).Error; err != nil {
				return false, err
			}
			return row.ContentSHA256 != old.ContentSHA256, nil
		}
		name, err := globalExceptionPolicyName(tx)
		if err != nil {
			return false, err
		}
		var existing []destPolicyRow
		if err := tx.Select("id", "priority", "updated_at").Where("action = ?", string(domain.DestAllow)).Order("priority ASC, id ASC").Find(&existing).Error; err != nil {
			return false, err
		}
		for index, p := range existing {
			if p.Priority == index+2 {
				continue
			}
			if err := tx.Model(&destPolicyRow{}).Where("id = ?", p.ID).Updates(map[string]any{"priority": index + 2, "updated_at": nextDestRowTime(now, p.UpdatedAt)}).Error; err != nil {
				return false, err
			}
		}
		if err := tx.Create(&row).Error; err != nil {
			return false, err
		}
		policy := destPolicyRow{Name: name, Action: string(domain.DestAllow), Scope: string(domain.DestScopeAll), Enabled: true, Priority: 1, ListIDs: jsonInt64s{row.ID}, TemplateKey: domain.DestGlobalExceptionTemplateKey, CreatedAt: now, UpdatedAt: now}
		if err := tx.Create(&policy).Error; err != nil {
			return false, err
		}
		result = domain.DestGlobalExceptionCommit{ListID: row.ID, PolicyID: policy.ID, Created: true}
		return true, nil
	})
	if err != nil {
		return domain.DestGlobalExceptionCommit{}, err
	}
	return result, nil
}
