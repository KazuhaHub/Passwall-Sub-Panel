package destpolicy

import (
	"context"
	"reflect"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

type inputPanels struct{}

func (inputPanels) GetAuditSettings(context.Context, int64) (ports.PanelAuditSettings, error) {
	return ports.PanelAuditSettings{Collect: domain.AuditCollectHitsAndUsage, Revision: 42}, nil
}

type inputMembers struct {
	ids   []int64
	calls int
}

func (m *inputMembers) GroupIDsByIDs(_ context.Context, ids []int64) (map[int64]int64, error) {
	m.calls++
	m.ids = append([]int64(nil), ids...)
	return map[int64]int64{2: 8}, nil
}
func (*inputMembers) MembersByGroupIDs(context.Context, []int64) (map[int64][]int64, error) {
	panic("input provider must use group tag-matching")
}

type inputGroups struct{ calls int }

func (g *inputGroups) TagMatchedMembers(context.Context, int64) (map[int64][]int64, error) {
	g.calls++
	return map[int64][]int64{8: {2, 3, 4}, 9: {5}}, nil
}

func TestCompilerInputsSeparatesRosterFromFullTagMatchedQuota(t *testing.T) {
	members, groups := &inputMembers{}, &inputGroups{}
	inputs, err := NewInputs(inputPanels{}, members, groups)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := &ports.NativeDesiredSnapshot{Clients: []ports.NativeDesiredClient{{Client: &domain.PSPClient{UserID: 2}}, {Client: &domain.PSPClient{UserID: 2}}}}
	roster, quota, err := inputs.ForNode(t.Context(), 81, snapshot, true)
	if err != nil || !reflect.DeepEqual(roster.UserIDs, []int64{2}) || !reflect.DeepEqual(members.ids, []int64{2}) || roster.UserGroups[2] != 8 || roster.CollectRevision != 42 || roster.Collect != domain.AuditCollectHitsAndUsage {
		t.Fatalf("roster/settings inputs incorrect: %+v / %v", roster, err)
	}
	if !reflect.DeepEqual(quota.UserIDs, []int64{2, 3, 4, 5}) || len(quota.UserGroups) != 4 || quota.UserGroups[3] != 8 || quota.UserGroups[5] != 9 || groups.calls != 1 || members.calls != 1 {
		t.Fatalf("quota followed roster instead of tag matches: %+v", quota)
	}
	if _, _, err := inputs.ForNode(t.Context(), 81, snapshot, false); err != nil || members.calls != 1 || groups.calls != 1 {
		t.Fatalf("paused/old node performed membership reads: %v", err)
	}
}
