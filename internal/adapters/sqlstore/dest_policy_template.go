package sqlstore

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/destpolicy"
	"gorm.io/gorm"
)

// SavePolicyWithList allocates both identities under the destination generation
// lock. The committed definitions are checked inside this same transaction,
// including quota changes made by another writer after the form's preview.
func (r *DestDefinitionRepo) SavePolicyWithList(ctx context.Context, policy *domain.DestPolicy, list *domain.DestList, now time.Time) error {
	if policy == nil || list == nil || policy.ID != 0 || list.ID != 0 || strings.TrimSpace(policy.Name) == "" || utf8.RuneCountInString(policy.Name) > 128 || strings.TrimSpace(list.Name) == "" || utf8.RuneCountInString(list.Name) > 128 || list.OwnerGroupID != 0 || list.Kind != domain.DestListGeosite || list.GeositeCategory == "" || list.SourceURL != "" || len(list.SourceText) != 0 || list.EntryCount == 0 || list.LastError != "" {
		return domain.ErrValidation
	}
	now, err := destWriteTime(now)
	if err != nil {
		return err
	}
	category := destListFromDomain(*list)
	row := destPolicyFromDomain(*policy)
	err = r.mutate(ctx, now, func(tx *gorm.DB) (bool, error) {
		if err := destinationListReferences(tx, row.ListIDs, 0, false); err != nil {
			return false, err
		}
		category.CreatedAt, category.UpdatedAt = now, now
		if err := tx.Create(&category).Error; err != nil {
			return false, err
		}
		row.ListIDs = append(append(jsonInt64s(nil), row.ListIDs...), category.ID)
		if err := destinationListReferences(tx, row.ListIDs, 0, false); err != nil {
			return false, err
		}
		row.Priority, err = nextDestPriority(tx, policy.Action)
		if err != nil {
			return false, err
		}
		row.CreatedAt, row.UpdatedAt = now, now
		if err := tx.Create(&row).Error; err != nil {
			if isUniqueViolationErr(err) {
				return false, fmt.Errorf("%w: dest_name_taken", domain.ErrAlreadyExists)
			}
			return false, err
		}
		defs, err := readDestinationDefinitions(tx)
		if err != nil {
			return false, err
		}
		return true, destpolicy.CheckDefinitions(defs)
	})
	if err != nil {
		return err
	}
	*policy, *list = destPolicyToDomain(row), destListToDomain(category)
	return nil
}
