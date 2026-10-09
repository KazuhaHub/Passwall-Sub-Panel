package sqlstore

import (
	"context"
	"slices"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

func destinationExemptionToDomain(row destExemptionRow) domain.DestExemption {
	return domain.DestExemption{UserID: row.UserID, Reason: row.Reason, CreatedBy: row.CreatedBy, CreatedAt: row.CreatedAt, ExpiresAt: row.ExpiresAt}
}

func (r *DestDefinitionRepo) ListExemptions(ctx context.Context) ([]domain.DestExemption, error) {
	var rows []destExemptionRow
	if err := r.db.WithContext(ctx).Order("user_id ASC").Find(&rows).Error; err != nil {
		return nil, err
	}
	result := make([]domain.DestExemption, 0, len(rows))
	for _, row := range rows {
		result = append(result, destinationExemptionToDomain(row))
	}
	return result, nil
}
func (r *DestDefinitionRepo) GetExemption(ctx context.Context, userID int64) (domain.DestExemption, error) {
	var row destExemptionRow
	if err := r.db.WithContext(ctx).First(&row, "user_id = ?", userID).Error; err != nil {
		return domain.DestExemption{}, destinationRowError(err)
	}
	return destinationExemptionToDomain(row), nil
}

// Read only display identifiers, never credentials or user entitlement state.
// Bounded chunks also work on SQLite builds with a low bind-parameter limit.
func (r *DestDefinitionRepo) ExemptionUserUPNs(ctx context.Context, ids []int64) (map[int64]string, error) {
	ids = slices.Clone(ids)
	slices.Sort(ids)
	ids = slices.Compact(ids)
	result := map[int64]string{}
	for start := 0; start < len(ids); start += 512 {
		var rows []userRow
		if err := r.db.WithContext(ctx).Select("id", "upn").Where("id IN ?", ids[start:min(start+512, len(ids))]).Find(&rows).Error; err != nil {
			return nil, err
		}
		for _, row := range rows {
			result[row.ID] = row.UPN
		}
	}
	return result, nil
}
