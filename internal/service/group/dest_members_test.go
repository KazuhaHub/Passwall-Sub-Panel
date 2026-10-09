package group

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

type destMemberGroups struct {
	ports.GroupRepo
	groups []*domain.Group
}

func (r destMemberGroups) List(context.Context) ([]*domain.Group, error) { return r.groups, nil }

type destMemberNodes struct {
	ports.NodeRepo
	nodes []*domain.Node
}

func (r destMemberNodes) ListEnabled(context.Context) ([]*domain.Node, error) { return r.nodes, nil }

type destMemberUsers struct {
	ids   []int64
	calls int
}

func (r *destMemberUsers) GroupIDsByIDs(context.Context, []int64) (map[int64]int64, error) {
	return nil, errors.New("quota must not read roster group ids")
}
func (r *destMemberUsers) MembersByGroupIDs(_ context.Context, ids []int64) (map[int64][]int64, error) {
	r.calls++
	r.ids = append([]int64(nil), ids...)
	return map[int64][]int64{8: {1, 2}, 10: {3}}, nil
}

func TestTagMatchedMembersUsesPanelTagsWithoutRosterOrEligibility(t *testing.T) {
	groups := destMemberGroups{groups: []*domain.Group{
		{ID: 8, TagFilter: domain.TagFilter{Tags: []string{"region:TW"}}},
		{ID: 9, TagFilter: domain.TagFilter{Tags: []string{"region:JP"}}},
		{ID: 10, TagFilter: domain.TagFilter{All: true}},
	}}
	nodes := destMemberNodes{nodes: []*domain.Node{{ID: 1, PanelID: 81, Enabled: true, Region: "TW"}, {ID: 2, PanelID: 81, Enabled: true, Region: "TW"}, {ID: 3, PanelID: 82, Enabled: true, Region: "JP"}, {ID: 4, PanelID: 81, Enabled: false, Region: "JP"}}}
	members := &destMemberUsers{}
	s := New(groups, nodes, nil)
	s.SetMembershipRepo(members)
	got, err := s.TagMatchedMembers(t.Context(), 81)
	if err != nil || !reflect.DeepEqual(got, map[int64][]int64{8: {1, 2}, 10: {3}}) || !reflect.DeepEqual(members.ids, []int64{8, 10}) || members.calls != 1 {
		t.Fatalf("tag-matched quota changed by unrelated/disabled inbounds: %+v ids=%v calls=%d / %v", got, members.ids, members.calls, err)
	}
	if got, err := s.TagMatchedMembers(t.Context(), 999); err != nil || len(got) != 0 || members.calls != 1 {
		t.Fatalf("empty panel enumerated users: %+v calls=%d / %v", got, members.calls, err)
	}
}
