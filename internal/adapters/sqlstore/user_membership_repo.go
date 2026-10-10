package sqlstore

import (
	"context"
	"fmt"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

func (r *userRepo) GroupIDsByIDs(ctx context.Context, ids []int64) (map[int64]int64, error) {
	if r == nil || r.db == nil {
		return nil, domain.ErrUnavailable
	}
	result := map[int64]int64{}
	for _, chunk := range idChunks(ids) {
		var rows []struct{ ID, GroupID int64 }
		if err := r.db.WithContext(ctx).Model(&userRow{}).Select("id", "group_id").Where("id IN ?", chunk).Find(&rows).Error; err != nil {
			return nil, fmt.Errorf("read user group ids: %w", err)
		}
		for _, row := range rows {
			result[row.ID] = row.GroupID
		}
	}
	return result, nil
}
func (r *userRepo) MembersByGroupIDs(ctx context.Context, ids []int64) (map[int64][]int64, error) {
	if r == nil || r.db == nil {
		return nil, domain.ErrUnavailable
	}
	result := map[int64][]int64{}
	for _, chunk := range idChunks(ids) {
		var rows []struct{ ID, GroupID int64 }
		if err := r.db.WithContext(ctx).Model(&userRow{}).Select("id", "group_id").Where("group_id IN ?", chunk).Order("id").Find(&rows).Error; err != nil {
			return nil, fmt.Errorf("read group members: %w", err)
		}
		for _, row := range rows {
			result[row.GroupID] = append(result[row.GroupID], row.ID)
		}
	}
	return result, nil
}

var _ ports.UserMembershipRepo = (*userRepo)(nil)
