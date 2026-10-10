package node

import (
	"context"
	"errors"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

type memberEligibility struct {
	allow bool
	err   error
	calls int
}

func (e *memberEligibility) Eligible(context.Context, *domain.Node, *domain.Group) (bool, error) {
	e.calls++
	return e.allow, e.err
}

func TestNodeMemberAdditionUsesUnifiedEligibility(t *testing.T) {
	for _, tc := range []struct {
		name  string
		allow bool
		err   error
		want  int
	}{{"allowed", true, nil, 2}, {"blocked", false, nil, 0}, {"read failed", false, errors.New("eligibility unavailable"), 0}} {
		t.Run(tc.name, func(t *testing.T) {
			tasks := &recordingTasks{}
			resync := &recreateResyncer{}
			selector := &memberEligibility{allow: tc.allow, err: tc.err}
			s := &Service{pool: stubXUIPool{c: &stubXUIClient{getResp: &ports.Inbound{ID: 20, Protocol: "vless"}}}, groups: oneAllGroup{}, users: twoMembers{}, tasks: tasks, resyncer: resync}
			s.SetGroupEligibility(selector)
			n := &domain.Node{ID: 5, PanelID: 10, InboundID: 20}
			if err := s.syncExistingUsersToNode(t.Context(), n); err != nil {
				t.Fatal(err)
			}
			s.provisionNodeMembers(t.Context(), n)
			if len(tasks.created) != tc.want || len(resync.ids) != tc.want || len(resync.bulkWarmed) != tc.want || selector.calls != 2 {
				t.Fatalf("addition bypassed eligibility: queued=%d resynced=%d bulk=%d checks=%d", len(tasks.created), len(resync.ids), len(resync.bulkWarmed), selector.calls)
			}
		})
	}
}
