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

type enableOKClient struct{ *stubXUIClient }

func (enableOKClient) SetInboundEnable(context.Context, int, bool) error { return nil }

// An inbound whose sniffing changes (or that is enabled or disabled) can make
// its panel able or unable to enforce an allowlist, which nothing else would
// notice: the panel is told, and only then.
func TestInboundEditsTriggerTheAllowlistResync(t *testing.T) {
	newSvc := func(sniffing string, enabled bool) (*Service, *[]int64) {
		repo := &captureNodeRepo{node: &domain.Node{ID: 1, PanelID: 4, InboundID: 3, Enabled: enabled, Sniffing: sniffing}}
		svc := &Service{nodes: repo, pool: stubXUIPool{c: enableOKClient{&stubXUIClient{}}}}
		var panels []int64
		svc.SetAllowlistResyncer(func(_ context.Context, panelID int64) { panels = append(panels, panelID) })
		return svc, &panels
	}
	spec := func(sniffing string) ports.InboundSpec {
		s := updateSpec()
		s.Sniffing = sniffing
		return s
	}

	svc, panels := newSvc(`{"enabled":true}`, true)
	if err := svc.UpdateInboundConfig(context.Background(), 1, spec(`{"enabled":true,"destOverride":["http","tls","quic"]}`)); err != nil {
		t.Fatal(err)
	}
	if len(*panels) != 1 || (*panels)[0] != 4 {
		t.Fatalf("a sniffing change told %v, want panel 4 once", *panels)
	}

	svc, panels = newSvc(`{"enabled":true}`, true)
	if err := svc.UpdateInboundConfig(context.Background(), 1, spec(`{"enabled":true}`)); err != nil {
		t.Fatal(err)
	}
	if len(*panels) != 0 {
		t.Fatalf("an edit that left the sniffing alone told %v", *panels)
	}

	svc, panels = newSvc(`{"enabled":true}`, true)
	if err := svc.SetEnabled(context.Background(), 1, false); err != nil {
		t.Fatal(err)
	}
	if len(*panels) != 1 || (*panels)[0] != 4 {
		t.Fatalf("disabling an inbound told %v, want panel 4 once", *panels)
	}

	svc, panels = newSvc(`{"enabled":true}`, true)
	if err := svc.SetEnabled(context.Background(), 1, true); err != nil {
		t.Fatal(err)
	}
	if len(*panels) != 0 {
		t.Fatalf("a no-op enable told %v", *panels)
	}
}

// An inbound that appears on or leaves a native panel changes what that panel's
// sniffing judgement covers, so creating, importing, deleting and detaching one
// tell the panel too.
func TestInboundCreateAndRemovalTriggerTheAllowlistResync(t *testing.T) {
	var panels []int64
	resync := func(_ context.Context, id int64) { panels = append(panels, id) }

	repo := &captureNodeRepo{}
	client := &stubXUIClient{addID: 7, getResp: &ports.Inbound{Protocol: "vless", Settings: `{"decryption":"none"}`}}
	svc := &Service{nodes: repo, pool: stubXUIPool{c: client}, groups: emptyGroups{}}
	svc.SetAllowlistResyncer(resync)
	spec := ports.InboundSpec{Protocol: "vless", Port: 443, Settings: `{"decryption":"none"}`, StreamSettings: `{"network":"tcp"}`}
	if err := svc.CreateInbound(context.Background(), &domain.Node{DisplayName: "x", Region: "us", PanelID: 4}, spec); err != nil {
		t.Fatal(err)
	}
	if len(panels) != 1 || panels[0] != 4 {
		t.Fatalf("creating an inbound told %v, want panel 4", panels)
	}

	panels = nil
	dsvc, drepo, _ := detachFixture()
	dsvc.SetAllowlistResyncer(resync)
	if err := dsvc.DetachAndSync(context.Background(), 7); err != nil {
		t.Fatal(err)
	}
	if !drepo.deleted || len(panels) != 1 {
		t.Fatalf("detaching told %v (deleted=%v), want one panel", panels, drepo.deleted)
	}
}
