package sqlstore

import (
	"context"
	"slices"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"gorm.io/gorm"
)

// List references include disabled policies. Closed group modes keep private
// pointers for reopening but do not prevent deletion, so are not active uses.
func (r *DestDefinitionRepo) ListReferences(ctx context.Context, defs domain.DestDefinitions) (map[int64][]domain.DestReference, error) {
	refs := map[int64][]domain.DestReference{}
	for _, list := range defs.Lists {
		refs[list.ID] = []domain.DestReference{}
	}
	for _, p := range defs.Policies {
		for _, id := range p.ListIDs {
			refs[id] = append(refs[id], domain.DestReference{Kind: "policy", ID: p.ID, Name: p.Name})
		}
	}
	var ids []int64
	for _, g := range defs.Groups {
		if g.Mode == "allowlist" {
			ids = append(ids, g.GroupID)
		}
	}
	if len(ids) == 0 {
		return refs, nil
	}
	var groups []groupRow
	if err := r.db.WithContext(ctx).Select("id", "name").Where("id IN ?", ids).Find(&groups).Error; err != nil {
		return nil, err
	}
	names := map[int64]string{}
	for _, g := range groups {
		names[g.ID] = g.Name
	}
	for _, g := range defs.Groups {
		if g.Mode != "allowlist" {
			continue
		}
		all := append(slices.Clone(g.ListIDs), g.BaseListID, g.ExtraListID)
		slices.Sort(all)
		all = slices.Compact(all)
		for _, id := range all {
			if id > 0 {
				refs[id] = append(refs[id], domain.DestReference{Kind: "group", ID: g.GroupID, Name: names[g.GroupID]})
			}
		}
	}
	return refs, nil
}

func destListUsedForAllow(tx *gorm.DB, id int64) (bool, error) {
	var policies []destPolicyRow
	if err := tx.Select("list_ids").Where("action = ?", string(domain.DestAllow)).Find(&policies).Error; err != nil {
		return false, err
	}
	for _, p := range policies {
		if slices.Contains(p.ListIDs, id) {
			return true, nil
		}
	}
	var groups []destGroupModeRow
	if err := tx.Select("list_ids", "base_list_id", "extra_list_id").Where("mode = ?", "allowlist").Find(&groups).Error; err != nil {
		return false, err
	}
	for _, g := range groups {
		if slices.Contains(g.ListIDs, id) || g.BaseListID == id || g.ExtraListID == id {
			return true, nil
		}
	}
	return false, nil
}
