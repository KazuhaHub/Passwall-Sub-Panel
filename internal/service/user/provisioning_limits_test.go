package user

import (
	"context"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// Who gets to decide a new user's three entitlements.
//
// The create paths carry SCALARS — CreateLocalInput and EnsureSSOInput both
// predate the third state and neither can say "inherit" — so every one of them
// funnels through domain.LimitOverridesFromCreate, which reads 0 as "no
// opinion". internal/domain covers that mapping in isolation; what it cannot
// cover is that the provisioning paths actually USE it, and that is the half
// that breaks silently: a path assigning u.Limits directly, or writing a
// &zero, would pin every user it creates to "explicitly unlimited" and out of
// its group's policy. Nothing logs, nothing 500s, and the symptom surfaces
// later as "I set a group quota and it applies to nobody" — the exact
// per-user editing the OU work exists to remove.
//
// Asserted as the RESOLVED outcome against a group that does state a policy,
// not as u.Limits being nil, because the nil is only interesting for what it
// resolves to.

// A group that states all three entitlements, reachable by both id (what
// resolution uses) and slug (what SSO JIT uses to place the new user).
type policyGroupRepo struct {
	ports.GroupRepo
	g *domain.Group
}

func (r *policyGroupRepo) GetByID(context.Context, int64) (*domain.Group, error) {
	return r.g, nil
}

func (r *policyGroupRepo) GetBySlug(context.Context, string) (*domain.Group, error) {
	return r.g, nil
}

func policyGroup() *policyGroupRepo {
	return &policyGroupRepo{g: &domain.Group{ID: 7, Slug: "staff", Limits: domain.GroupLimits{
		TrafficLimitBytes: ptr[int64](100 << 30),
		IPLimit:           ptr(3),
		DeviceLimit:       ptr(5),
	}}}
}

func ptr[T any](v T) *T { return &v }

// SSO just-in-time provisioning. NewUserDefaults carries only a traffic value,
// and the two connection caps are passed as literal zeros at the call site —
// so if zero ever meant "explicitly unlimited", every SSO-provisioned user in
// the panel would sit outside its group's IP and device policy.
func TestEnsureSSOProvisionsInheritingLimits(t *testing.T) {
	repo := &memoryUserRepo{byID: map[int64]*domain.User{}}
	svc := &Service{users: repo, groups: policyGroup()}

	u, err := svc.EnsureSSO(context.Background(), EnsureSSOInput{
		Provider:         domain.SSOProviderSAML,
		Subject:          "idp-subject-jit",
		UPN:              "jit@corp.com",
		Email:            "jit@corp.com",
		AllowAutoCreate:  true,
		DefaultGroupSlug: "staff",
		// DefaultLimitBytes deliberately unset: the operator stated no
		// SSO-specific quota, so the group is the only thing with an opinion.
	})
	if err != nil {
		t.Fatalf("EnsureSSO: %v", err)
	}
	if !u.Limits.InheritsTrafficLimit() || !u.Limits.InheritsIPLimit() || !u.Limits.InheritsDeviceLimit() {
		t.Fatalf("SSO JIT pinned a new user out of its group's policy: stored %+v, want all three inheriting", u.Limits)
	}
	assertResolvesToGroupPolicy(t, u, svc)
}

// An SSO default that IS stated must still be an override — the operator asked
// for it explicitly — and must not drag the two connection caps with it.
func TestEnsureSSOStatedTrafficDefaultDoesNotPinTheConnectionCaps(t *testing.T) {
	repo := &memoryUserRepo{byID: map[int64]*domain.User{}}
	svc := &Service{users: repo, groups: policyGroup()}

	u, err := svc.EnsureSSO(context.Background(), EnsureSSOInput{
		Provider:          domain.SSOProviderSAML,
		Subject:           "idp-subject-quota",
		UPN:               "quota@corp.com",
		Email:             "quota@corp.com",
		AllowAutoCreate:   true,
		DefaultGroupSlug:  "staff",
		DefaultLimitBytes: 50 << 30,
	})
	if err != nil {
		t.Fatalf("EnsureSSO: %v", err)
	}
	if u.Limits.InheritsTrafficLimit() {
		t.Fatal("a stated SSO traffic default must be recorded as an override, not dropped")
	}
	if !u.Limits.InheritsIPLimit() || !u.Limits.InheritsDeviceLimit() {
		t.Fatalf("stating a traffic default pinned the connection caps too: %+v", u.Limits)
	}
	svc.resolveUserLimits(context.Background(), u)
	if u.TrafficLimitBytes != 50<<30 {
		t.Fatalf("traffic = %d, want the stated 50 GiB to win over the group's 100", u.TrafficLimitBytes)
	}
	if u.IPLimit != 3 || u.DeviceLimit != 5 {
		t.Fatalf("connection caps = %d/%d, want the group's 3/5", u.IPLimit, u.DeviceLimit)
	}
}

// Self-registration and the admin form share CreateLocal. Self-registration
// passes RegistrationDefaultTrafficGB, which is 0 on a default install — so
// the overwhelmingly common signup must inherit, not opt out.
func TestCreateLocalWithNoStatedLimitsInherits(t *testing.T) {
	repo := &memoryUserRepo{byID: map[int64]*domain.User{}}
	svc := &Service{users: repo, groups: policyGroup()}

	res, err := svc.CreateLocal(context.Background(), CreateLocalInput{
		UPN:     "signup@corp.com",
		Email:   "signup@corp.com",
		GroupID: 7,
	})
	if err != nil {
		t.Fatalf("CreateLocal: %v", err)
	}
	if !res.User.Limits.InheritsTrafficLimit() || !res.User.Limits.InheritsIPLimit() || !res.User.Limits.InheritsDeviceLimit() {
		t.Fatalf("a plain signup was pinned out of its group's policy: stored %+v", res.User.Limits)
	}
	assertResolvesToGroupPolicy(t, res.User, svc)
}

func assertResolvesToGroupPolicy(t *testing.T, u *domain.User, svc *Service) {
	t.Helper()
	svc.resolveUserLimits(context.Background(), u)
	if u.TrafficLimitBytes != 100<<30 || u.IPLimit != 3 || u.DeviceLimit != 5 {
		t.Fatalf("resolved to %d/%d/%d, want the group's 100GiB/3/5", u.TrafficLimitBytes, u.IPLimit, u.DeviceLimit)
	}
}

// A negative quota is not a small quota — it is NO quota.
//
// trafficFloor, PanelQuotaCap and the traffic-exceeded check all test `> 0`,
// so a stored -1 reads as "unlimited" in every one of them. The two connection
// caps were guarded from the start; traffic was not, and the comment on
// validateGroupLimits asserted the opposite. Both create and update are pinned
// here because they validate independently.
func TestNegativeTrafficLimitIsRejectedOnCreate(t *testing.T) {
	repo := &memoryUserRepo{byID: map[int64]*domain.User{}}
	svc := &Service{users: repo, groups: policyGroup()}

	_, err := svc.CreateLocal(context.Background(), CreateLocalInput{
		UPN: "neg@corp.com", GroupID: 7, TrafficLimitBytes: -(1 << 30),
	})
	if err == nil {
		t.Fatal("a negative quota must be rejected; stored, it reads as unlimited everywhere")
	}
	if len(repo.byID) != 0 {
		t.Fatalf("rejected input still created a user: %+v", repo.byID)
	}
}

func TestNegativeTrafficLimitIsRejectedOnUpdate(t *testing.T) {
	u := &domain.User{ID: 1, UPN: "u@corp.com", Enabled: true, GroupID: 7, Role: domain.RoleUser}
	repo := &memoryUserRepo{byID: map[int64]*domain.User{1: u}}
	svc := &Service{users: repo, groups: policyGroup(), ownership: emptyOwnershipRepo{}}

	neg := int64(-1)
	if err := svc.UpdateProfile(context.Background(), 1, UpdateInput{TrafficLimitBytes: &neg}); err == nil {
		t.Fatal("a negative quota must be rejected on update too")
	}
	if got := repo.byID[1].Limits.TrafficLimitBytes; got != nil {
		t.Fatalf("rejected update still wrote an override: %v", *got)
	}
}
