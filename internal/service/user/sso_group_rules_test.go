package user

import (
	"context"
	"errors"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/config"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// Which OU an SSO principal lands in, and whether losing an IdP group
// actually costs them that OU.
//
// internal/service/auth covers the decision matrix in isolation. What it
// cannot cover is that EnsureSSO USES it — and that is the half that fails
// silently. A creation path ignoring the rules puts everyone in the default
// bucket, which looks like working segmentation until someone checks; a
// reconcile path ignoring them leaves a principal removed from the IdP group
// behind a premium OU holding that OU's quota and nodes indefinitely. Neither
// logs, neither errors.
//
// The reconcile tests also assert the panels are TOLD. Moving the stored row
// changes nothing on a node: the OU decides inbound membership and, since
// v3.9.2-beta.10, the inherited entitlements. A move that never reaches the
// panels is the same defect as a group-limit edit that never reaches them,
// one layer up.

// A repo with more than one group, so "which group" is a real question.
// Resolution looks rows up by id; the rules speak slugs.
type multiGroupRepo struct {
	ports.GroupRepo
	bySlug    map[string]*domain.Group
	byID      map[int64]*domain.Group
	byIDCalls int
}

func newMultiGroupRepo(gs ...*domain.Group) *multiGroupRepo {
	r := &multiGroupRepo{bySlug: map[string]*domain.Group{}, byID: map[int64]*domain.Group{}}
	for _, g := range gs {
		r.bySlug[g.Slug] = g
		r.byID[g.ID] = g
	}
	return r
}

func (r *multiGroupRepo) GetByID(_ context.Context, id int64) (*domain.Group, error) {
	r.byIDCalls++
	if g, ok := r.byID[id]; ok {
		return g, nil
	}
	return nil, domain.ErrNotFound
}

func (r *multiGroupRepo) GetBySlug(_ context.Context, slug string) (*domain.Group, error) {
	if g, ok := r.bySlug[slug]; ok {
		return g, nil
	}
	return nil, domain.ErrNotFound
}

func ssoGroupFixture() *multiGroupRepo {
	return newMultiGroupRepo(
		&domain.Group{ID: 1, Slug: "default", Name: "Default"},
		&domain.Group{ID: 2, Slug: "vip", Name: "VIP"},
		&domain.Group{ID: 3, Slug: "staff", Name: "Staff"},
	)
}

// The resync is observed where production actually leaves its trace, not
// through a hook added for the tests. A node selector that cannot reach the
// fleet — a panel being down — is a real condition, and it drives
// ResyncMembershipOrEnqueue down its documented fallback: attempt, fail,
// leave a durable task. So "the panels were told" is asserted as "a resync
// task exists for this user", which is the same thing the running system
// relies on.
type unreachableSelector struct{}

func (unreachableSelector) NodesFor(context.Context, *domain.Group) ([]*domain.Node, error) {
	return nil, errors.New("panel unreachable")
}

type recordingTaskRepo struct {
	ports.SyncTaskRepo
	created []*domain.SyncTask
}

func (r *recordingTaskRepo) GetActiveByTarget(context.Context, domain.SyncTaskType, string, int64) (*domain.SyncTask, error) {
	return nil, domain.ErrNotFound
}

func (r *recordingTaskRepo) Create(_ context.Context, t *domain.SyncTask) error {
	r.created = append(r.created, t)
	return nil
}

func (r *recordingTaskRepo) resyncedUsers() []int64 {
	var ids []int64
	for _, t := range r.created {
		if t.Type == domain.SyncTaskUserResync {
			ids = append(ids, t.TargetID)
		}
	}
	return ids
}

func vipRule() []config.SSOGroupRule {
	return []config.SSOGroupRule{{Attribute: "", Value: "idp-vip", Group: "vip"}}
}

func ssoIn(subject, upn string, groups []string, rules []config.SSOGroupRule) EnsureSSOInput {
	return EnsureSSOInput{
		Provider:         domain.SSOProviderSAML,
		Subject:          subject,
		UPN:              upn,
		Email:            upn,
		Groups:           groups,
		GroupsAttrName:   "groups",
		AllowAutoCreate:  true,
		DefaultGroupSlug: "default",
		GroupRules:       rules,
	}
}

// Creation: a matching rule outranks the deployment default. Without this the
// whole feature is inert and every SSO user lands in one bucket.
func TestEnsureSSO_CreatePlacesByGroupRule(t *testing.T) {
	repo := &memoryUserRepo{byID: map[int64]*domain.User{}}
	svc := &Service{users: repo, groups: ssoGroupFixture()}

	u, err := svc.EnsureSSO(context.Background(), ssoIn("s1", "vip@corp.com", []string{"idp-vip"}, vipRule()))
	if err != nil {
		t.Fatalf("EnsureSSO: %v", err)
	}
	if u.GroupID != 2 {
		t.Fatalf("group rule ignored on create: got group %d, want 2 (vip)", u.GroupID)
	}
}

// Creation with no rule matching falls back to the default group — the
// behaviour every deployment had before this feature, which must not change.
func TestEnsureSSO_CreateFallsBackToDefaultGroup(t *testing.T) {
	repo := &memoryUserRepo{byID: map[int64]*domain.User{}}
	svc := &Service{users: repo, groups: ssoGroupFixture()}

	u, err := svc.EnsureSSO(context.Background(), ssoIn("s2", "plain@corp.com", []string{"idp-other"}, vipRule()))
	if err != nil {
		t.Fatalf("EnsureSSO: %v", err)
	}
	if u.GroupID != 1 {
		t.Fatalf("no rule matched: got group %d, want 1 (default)", u.GroupID)
	}
}

// A rule naming a group that does not exist must refuse to provision rather
// than quietly dropping the principal into the default OU: a rule meant to
// RESTRICT would otherwise land them somewhere more permissive.
func TestEnsureSSO_CreateRefusesUnknownRuleGroup(t *testing.T) {
	repo := &memoryUserRepo{byID: map[int64]*domain.User{}}
	svc := &Service{users: repo, groups: ssoGroupFixture()}

	rules := []config.SSOGroupRule{{Attribute: "", Value: "idp-vip", Group: "typo-slug"}}
	if _, err := svc.EnsureSSO(context.Background(), ssoIn("s3", "x@corp.com", []string{"idp-vip"}, rules)); err == nil {
		t.Fatal("a group rule pointing at a nonexistent group must fail provisioning, not fall back")
	}
	if len(repo.byID) != 0 {
		t.Fatalf("refused provisioning still wrote a user: %+v", repo.byID)
	}
}

// The hole this closes. The principal is already in vip; the IdP no longer
// puts them in the group that backs it. They must leave vip — otherwise the
// premium OU's quota and nodes are theirs for good.
func TestEnsureSSO_ReconcileDemotesWhenIdPRevokesTheGroup(t *testing.T) {
	existing := &domain.User{
		ID: 5, UPN: "vip@corp.com", Enabled: true, GroupID: 2,
		SSOProvider: domain.SSOProviderSAML, SSOSubject: "s1",
	}
	repo := &memoryUserRepo{byID: map[int64]*domain.User{5: existing}}
	tasks := &recordingTaskRepo{}
	svc := &Service{users: repo, groups: ssoGroupFixture(), ownership: emptyOwnershipRepo{},
		selector: unreachableSelector{}, tasks: tasks}

	u, err := svc.EnsureSSO(context.Background(), ssoIn("s1", "vip@corp.com", []string{"idp-other"}, vipRule()))
	if err != nil {
		t.Fatalf("EnsureSSO: %v", err)
	}
	if u.GroupID != 1 {
		t.Fatalf("revoked IdP group did not cost the OU: got group %d, want 1 (default)", u.GroupID)
	}
	if repo.byID[5].GroupID != 1 {
		t.Fatalf("move was not persisted: stored group %d, want 1", repo.byID[5].GroupID)
	}
	// Demotion is the direction where reaching the panels matters most:
	// until they are told, the principal keeps the premium OU's inbounds
	// and the entitlements inherited from it.
	if got := tasks.resyncedUsers(); len(got) != 1 || got[0] != 5 {
		t.Fatalf("demotion did not reach the panels: resync tasks %v, want [5]", got)
	}
}

// The promotion direction, and the assertion that the panels are told. A
// stored group change that never reaches the nodes leaves the user on the old
// OU's inbounds and old inherited limits.
func TestEnsureSSO_ReconcilePromotesAndResyncsPanels(t *testing.T) {
	existing := &domain.User{
		ID: 6, UPN: "up@corp.com", Enabled: true, GroupID: 1,
		SSOProvider: domain.SSOProviderSAML, SSOSubject: "s6",
	}
	repo := &memoryUserRepo{byID: map[int64]*domain.User{6: existing}}
	tasks := &recordingTaskRepo{}
	svc := &Service{users: repo, groups: ssoGroupFixture(), ownership: emptyOwnershipRepo{},
		selector: unreachableSelector{}, tasks: tasks}

	u, err := svc.EnsureSSO(context.Background(), ssoIn("s6", "up@corp.com", []string{"idp-vip"}, vipRule()))
	if err != nil {
		t.Fatalf("EnsureSSO: %v", err)
	}
	if u.GroupID != 2 {
		t.Fatalf("group rule ignored on login: got group %d, want 2 (vip)", u.GroupID)
	}
	if got := tasks.resyncedUsers(); len(got) != 1 || got[0] != 6 {
		t.Fatalf("OU move did not reach the panels: resync tasks %v, want [6]", got)
	}
}

// A login that does not move the OU must not churn the panels. Every SSO
// login runs this path, so a resync per login would be a fan-out of
// UpdateClient calls and Xray restarts across the fleet for no change at all.
func TestEnsureSSO_ReconcileWithoutMoveDoesNotResync(t *testing.T) {
	existing := &domain.User{
		ID: 7, UPN: "same@corp.com", Enabled: true, GroupID: 2,
		SSOProvider: domain.SSOProviderSAML, SSOSubject: "s7",
	}
	repo := &memoryUserRepo{byID: map[int64]*domain.User{7: existing}}
	tasks := &recordingTaskRepo{}
	svc := &Service{users: repo, groups: ssoGroupFixture(), ownership: emptyOwnershipRepo{},
		selector: unreachableSelector{}, tasks: tasks}

	if _, err := svc.EnsureSSO(context.Background(), ssoIn("s7", "same@corp.com", []string{"idp-vip"}, vipRule())); err != nil {
		t.Fatalf("EnsureSSO: %v", err)
	}
	if got := tasks.resyncedUsers(); len(got) != 0 {
		t.Fatalf("unchanged OU still resynced the panels: %v", got)
	}
}

// Upgrade safety, at the service level rather than the resolver's: a
// deployment that has written no group rules must not move anyone, whatever
// their IdP groups say.
func TestEnsureSSO_NoRulesLeavesExistingGroupAlone(t *testing.T) {
	existing := &domain.User{
		ID: 8, UPN: "keep@corp.com", Enabled: true, GroupID: 3,
		SSOProvider: domain.SSOProviderSAML, SSOSubject: "s8",
	}
	repo := &memoryUserRepo{byID: map[int64]*domain.User{8: existing}}
	tasks := &recordingTaskRepo{}
	svc := &Service{users: repo, groups: ssoGroupFixture(), ownership: emptyOwnershipRepo{},
		selector: unreachableSelector{}, tasks: tasks}

	u, err := svc.EnsureSSO(context.Background(), ssoIn("s8", "keep@corp.com", []string{"idp-vip"}, nil))
	if err != nil {
		t.Fatalf("EnsureSSO: %v", err)
	}
	if got := tasks.resyncedUsers(); u.GroupID != 3 || len(got) != 0 {
		t.Fatalf("upgrade moved a user with no rules configured: group %d, resyncs %v; want 3 and none", u.GroupID, got)
	}
}

// A rule target that has since been deleted must not lock an existing user
// out. Sign-in is not the place to enforce a provisioning-mapping error: the
// loud signals are settings-save validation and the log line.
func TestEnsureSSO_ReconcileUnknownRuleGroupDoesNotBlockLogin(t *testing.T) {
	existing := &domain.User{
		ID: 9, UPN: "live@corp.com", Enabled: true, GroupID: 3,
		SSOProvider: domain.SSOProviderSAML, SSOSubject: "s9",
	}
	repo := &memoryUserRepo{byID: map[int64]*domain.User{9: existing}}
	svc := &Service{users: repo, groups: ssoGroupFixture(), ownership: emptyOwnershipRepo{}}

	rules := []config.SSOGroupRule{{Attribute: "", Value: "idp-vip", Group: "deleted-slug"}}
	u, err := svc.EnsureSSO(context.Background(), ssoIn("s9", "live@corp.com", []string{"idp-vip"}, rules))
	if err != nil {
		t.Fatalf("a dangling rule target must not fail an existing user's login: %v", err)
	}
	if u.GroupID != 3 {
		t.Fatalf("unresolvable target should leave the group alone, got %d", u.GroupID)
	}
}

// The service-level half of the two guards adversarial review found. The
// resolver tests cover the decision; these cover it reaching the stored row,
// which is what actually decides a user's entitlements.

// Demoting with no default group configured would persist GroupID=0, and a
// user in no group inherits nothing — resolving to unlimited traffic and no
// connection caps. A revocation that grants more than it takes is worse than
// no revocation at all.
func TestEnsureSSO_DemotionWithoutDefaultGroupLeavesTheUserPut(t *testing.T) {
	existing := &domain.User{
		ID: 11, UPN: "vip@corp.com", Enabled: true, GroupID: 2,
		SSOProvider: domain.SSOProviderSAML, SSOSubject: "s11",
	}
	repo := &memoryUserRepo{byID: map[int64]*domain.User{11: existing}}
	tasks := &recordingTaskRepo{}
	svc := &Service{users: repo, groups: ssoGroupFixture(), ownership: emptyOwnershipRepo{},
		selector: unreachableSelector{}, tasks: tasks}

	in := ssoIn("s11", "vip@corp.com", []string{"idp-other"}, vipRule())
	in.DefaultGroupSlug = ""

	u, err := svc.EnsureSSO(context.Background(), in)
	if err != nil {
		t.Fatalf("EnsureSSO: %v", err)
	}
	if u.GroupID != 2 {
		t.Fatalf("demoted to group %d with no default configured; group 0 resolves to UNLIMITED", u.GroupID)
	}
	if got := tasks.resyncedUsers(); len(got) != 0 {
		t.Fatalf("no move should mean no panel churn, got resyncs %v", got)
	}
}

// An IdP that sends no groups at all — Entra past its group-overage limit, a
// broken claim mapping — must not read as "everyone was revoked".
func TestEnsureSSO_AbsentGroupsClaimDoesNotMoveAnyone(t *testing.T) {
	existing := &domain.User{
		ID: 12, UPN: "vip@corp.com", Enabled: true, GroupID: 2,
		SSOProvider: domain.SSOProviderSAML, SSOSubject: "s12",
	}
	repo := &memoryUserRepo{byID: map[int64]*domain.User{12: existing}}
	tasks := &recordingTaskRepo{}
	svc := &Service{users: repo, groups: ssoGroupFixture(), ownership: emptyOwnershipRepo{},
		selector: unreachableSelector{}, tasks: tasks}

	u, err := svc.EnsureSSO(context.Background(), ssoIn("s12", "vip@corp.com", nil, vipRule()))
	if err != nil {
		t.Fatalf("EnsureSSO: %v", err)
	}
	if u.GroupID != 2 {
		t.Fatalf("an absent claim moved the user from group 2 to %d", u.GroupID)
	}
	if got := tasks.resyncedUsers(); len(got) != 0 {
		t.Fatalf("silence should push nothing, got resyncs %v", got)
	}
}

// A deployment with no rules must not even read the group row: the answer
// cannot change, and a transient failure there would log a warning about a
// feature nobody configured.
func TestEnsureSSO_NoRulesDoesNotReadTheGroup(t *testing.T) {
	existing := &domain.User{
		ID: 13, UPN: "plain@corp.com", Enabled: true, GroupID: 3,
		SSOProvider: domain.SSOProviderSAML, SSOSubject: "s13",
	}
	repo := &memoryUserRepo{byID: map[int64]*domain.User{13: existing}}
	groups := ssoGroupFixture()
	svc := &Service{users: repo, groups: groups, ownership: emptyOwnershipRepo{}}

	before := groups.byIDCalls
	if _, err := svc.EnsureSSO(context.Background(), ssoIn("s13", "plain@corp.com", []string{"idp-vip"}, nil)); err != nil {
		t.Fatalf("EnsureSSO: %v", err)
	}
	if groups.byIDCalls != before {
		t.Fatalf("group read %d times with no rules configured, want 0", groups.byIDCalls-before)
	}
}
