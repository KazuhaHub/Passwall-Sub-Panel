package node

import (
	"context"
	"errors"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// eligibilityByGroup answers Eligible per group id: true, false or an error.
type eligibilityByGroup map[int64]error

var errNotEligible = errors.New("test: not eligible")

func (e eligibilityByGroup) Eligible(_ context.Context, _ *domain.Node, g *domain.Group) (bool, error) {
	err, listed := e[g.ID]
	switch {
	case !listed:
		return true, nil
	case errors.Is(err, errNotEligible):
		return false, nil
	default:
		return false, err
	}
}

type twoGroupRepo struct{ ports.GroupRepo }

func (twoGroupRepo) List(context.Context) ([]*domain.Group, error) {
	all := domain.TagFilter{All: true}
	return []*domain.Group{{ID: 1, TagFilter: all}, {ID: 2, TagFilter: all}}, nil
}

type membersByGroup struct{ ports.UserRepo }

func (membersByGroup) ListByGroup(_ context.Context, gid int64) ([]*domain.User, error) {
	return []*domain.User{{ID: gid * 100, Enabled: true, UUID: "u"}}, nil
}

func newEligibilityService(e GroupEligibility) (*Service, *recordingTasks, *recreateResyncer) {
	tasks, resync := &recordingTasks{}, &recreateResyncer{}
	svc := &Service{
		pool:   stubXUIPool{c: nativeClient{getErr: ports.ErrNativePanelSnapshotMissing}},
		groups: twoGroupRepo{}, users: membersByGroup{}, tasks: tasks, resyncer: resync,
	}
	if e != nil {
		svc.SetGroupEligibility(e)
	}
	return svc, tasks, resync
}

// An allowlist group's members get no client on a node that cannot enforce it,
// on either add path the node service has.
func TestAddPathsSkipGroupsTheNodeIsNotEligibleFor(t *testing.T) {
	n := &domain.Node{ID: 5, PanelID: 10, InboundID: 20, DesiredProtocol: "vless"}
	svc, tasks, resync := newEligibilityService(eligibilityByGroup{1: errNotEligible})

	if err := svc.syncExistingUsersToNode(context.Background(), n); err != nil {
		t.Fatal(err)
	}
	if len(tasks.created) != 1 || tasks.created[0].TargetID != 200 {
		t.Fatalf("new-node enrolment enqueued %+v, want only group 2's member (200)", tasks.created)
	}

	svc.provisionNodeMembers(context.Background(), n)
	if len(resync.ids) != 1 || resync.ids[0] != 200 {
		t.Fatalf("recreate provisioned %v, want only group 2's member (200)", resync.ids)
	}
}

// A failed eligibility read skips that group for this round (the safe
// direction: nothing is added, and the next round or the heal sweep adds it)
// and does not stop the other groups.
func TestAddPathsSkipAGroupWhoseEligibilityCannotBeRead(t *testing.T) {
	n := &domain.Node{ID: 5, PanelID: 10, InboundID: 20, DesiredProtocol: "vless"}
	svc, tasks, resync := newEligibilityService(eligibilityByGroup{2: errors.New("db down")})

	if err := svc.syncExistingUsersToNode(context.Background(), n); err != nil {
		t.Fatalf("a failed eligibility read must not fail the whole enrolment: %v", err)
	}
	if len(tasks.created) != 1 || tasks.created[0].TargetID != 100 {
		t.Fatalf("new-node enrolment enqueued %+v, want only group 1's member (100)", tasks.created)
	}
	svc.provisionNodeMembers(context.Background(), n)
	if len(resync.ids) != 1 || resync.ids[0] != 100 {
		t.Fatalf("recreate provisioned %v, want only group 1's member (100)", resync.ids)
	}
}

// Until an eligibility is wired the paths behave as before.
func TestAddPathsDefaultToMatchOnly(t *testing.T) {
	n := &domain.Node{ID: 5, PanelID: 10, InboundID: 20, DesiredProtocol: "vless"}
	svc, tasks, resync := newEligibilityService(nil)
	if err := svc.syncExistingUsersToNode(context.Background(), n); err != nil {
		t.Fatal(err)
	}
	svc.provisionNodeMembers(context.Background(), n)
	if len(tasks.created) != 2 || len(resync.ids) != 2 {
		t.Fatalf("unwired: enqueued %d, provisioned %d; want both groups' members (2 each)", len(tasks.created), len(resync.ids))
	}
}
