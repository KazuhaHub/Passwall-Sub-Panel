package group

import (
	"context"
	"fmt"
	"slices"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// TagMatchedMembers counts full group membership using only TagFilter matches.
// It never consults destination-policy eligibility or current roster clients.
func (s *Service) TagMatchedMembers(ctx context.Context, panelID int64) (map[int64][]int64, error) {
	if panelID <= 0 {
		return nil, domain.ErrValidation
	}
	if s == nil || s.groups == nil || s.nodes == nil || s.membership == nil {
		return nil, domain.ErrUnavailable
	}
	nodes, err := s.nodes.ListEnabled(ctx)
	if err != nil {
		return nil, err
	}
	var panelNodes []*domain.Node
	for _, node := range nodes {
		if node != nil && node.PanelID == panelID && node.Enabled {
			panelNodes = append(panelNodes, node)
		}
	}
	if len(panelNodes) == 0 {
		return map[int64][]int64{}, nil
	}
	groups, err := s.groups.List(ctx)
	if err != nil {
		return nil, err
	}
	var ids []int64
	for _, group := range groups {
		if group == nil || group.ID <= 0 {
			return nil, fmt.Errorf("%w: invalid quota group identity", domain.ErrUnavailable)
		}
		for _, node := range panelNodes {
			if Matches(node, group.TagFilter) {
				ids = append(ids, group.ID)
				break
			}
		}
	}
	slices.Sort(ids)
	ids = slices.Compact(ids)
	if len(ids) == 0 {
		return map[int64][]int64{}, nil
	}
	members, err := s.membership.MembersByGroupIDs(ctx, ids)
	if err != nil {
		return nil, err
	}
	result := make(map[int64][]int64, len(ids))
	for _, id := range ids {
		result[id] = append([]int64(nil), members[id]...)
	}
	return result, nil
}
