package group

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

func previewEnv() (*Service, *eligGroups) {
	hk := &domain.Group{ID: 1, Name: "HK", TagFilter: domain.TagFilter{Tags: []string{"region:HK"}}}
	jp := &domain.Group{ID: 2, Name: "JP", TagFilter: domain.TagFilter{Tags: []string{"region:JP"}}}
	nodes := []*domain.Node{
		{ID: 1, Enabled: true, Region: "HK", Relays: []domain.RelayLine{{ID: "a", Region: "HK"}, {ID: "b", Region: "CN"}},
			RouteState: domain.NodeRouteState{Lines: []domain.VlessRouteLine{{ID: "gone", Deleted: true, Region: "HK"}}}},
		{ID: 2, Enabled: true, Region: "JP"},
	}
	groups := &eligGroups{groups: []*domain.Group{hk, jp}, members: map[int64]int64{}}
	return New(groups, &eligNodes{nodes: nodes}, nil), groups
}

// Moving HK -> JP loses HK-1 (its lines counted under HK: 1 of 2, the deleted
// route line not counted) and gains JP-1.
func TestPreviewGroupChangeCountsEachSideUnderItsGroup(t *testing.T) {
	svc, _ := previewEnv()
	got, err := svc.PreviewGroupChange(context.Background(), 1, 2)
	if err != nil {
		t.Fatal(err)
	}
	if got.Kept != 0 || len(got.Added) != 1 || got.Added[0].Node.ID != 2 || len(got.Removed) != 1 {
		t.Fatalf("change %+v", got)
	}
	if r := got.Removed[0]; r.Node.ID != 1 || r.VisibleLines != 1 || r.TotalLines != 2 {
		t.Fatalf("removed node counted wrong: %+v", r)
	}
	same, err := svc.PreviewGroupChange(context.Background(), 1, 1)
	if err != nil || same.Kept != 1 || len(same.Added) != 0 || len(same.Removed) != 0 {
		t.Fatalf("same group: %+v %v", same, err)
	}
	if _, err := svc.PreviewGroupChange(context.Background(), 1, 99); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("unknown target: %v", err)
	}
}

func TestGroupsForSeparatorUsesTheRenderRuleAsIfEnabled(t *testing.T) {
	svc, groups := previewEnv()
	got, err := svc.GroupsForSeparator(context.Background(), domain.SeparatorEntry{Mode: domain.SeparatorModeNodeBound, NodeIDs: []int64{2}, Enabled: false})
	if err != nil || !reflect.DeepEqual(got, []int64{2}) {
		t.Fatalf("node-bound: %v %v (a disabled draft is evaluated as enabled)", got, err)
	}
	got, _ = svc.GroupsForSeparator(context.Background(), domain.SeparatorEntry{Mode: domain.SeparatorModeGlobal})
	if !reflect.DeepEqual(got, []int64{1, 2}) {
		t.Fatalf("global: %v", got)
	}
	groups.err = errors.New("db down")
	if _, err := svc.GroupsForSeparator(context.Background(), domain.SeparatorEntry{Mode: domain.SeparatorModeGlobal}); err == nil {
		t.Fatal("a failed read must be an error, never an answer")
	}
}

func TestGroupsForNodeIsTheNodeLevelAnswer(t *testing.T) {
	svc, _ := previewEnv()
	got, err := svc.GroupsForNode(context.Background(), &domain.Node{ID: 9, Region: "hk"})
	if err != nil || !reflect.DeepEqual(got, []int64{1}) {
		t.Fatalf("groups for node: %v %v", got, err)
	}
}
