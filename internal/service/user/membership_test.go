package user

import (
	"context"
	"errors"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

type membershipFailRepo struct{ *memoryUserRepo }

func (*membershipFailRepo) Create(context.Context, *domain.User) error { return domain.ErrUnavailable }
func (*membershipFailRepo) Update(context.Context, *domain.User) error { return domain.ErrUnavailable }
func (*membershipFailRepo) Delete(context.Context, int64) error        { return domain.ErrUnavailable }

func TestMembershipInvalidationFollowsCommittedCreateMoveAndDelete(t *testing.T) {
	repo := &memoryUserRepo{byID: map[int64]*domain.User{}}
	svc := &Service{users: repo, groups: ssoGroupFixture(), ownership: emptyOwnershipRepo{}, selector: unreachableSelector{}, tasks: &recordingTaskRepo{}}
	calls := 0
	svc.SetMembershipInvalidator(func() { calls++ })
	created, err := svc.CreateLocal(t.Context(), CreateLocalInput{UPN: "membership@example.test", GroupID: 1, InitialPassword: "test-password"})
	if err != nil || calls != 1 || repo.byID[created.User.ID] == nil {
		t.Fatalf("create did not invalidate committed membership: calls=%d / %v", calls, err)
	}
	svc.SetMembershipInvalidator(func() {
		calls++
		if repo.byID[created.User.ID].GroupID != 2 {
			t.Error("group invalidation preceded persistence")
		}
	})
	if err := svc.ChangeGroupAndSync(t.Context(), created.User.ID, 2); err != nil || calls != 2 {
		t.Fatalf("move invalidation: calls=%d / %v", calls, err)
	}
	if err := svc.ChangeGroupAndSync(t.Context(), created.User.ID, 2); err != nil || calls != 2 {
		t.Fatalf("equal move invalidated membership: calls=%d / %v", calls, err)
	}
	remark := "display only"
	if err := svc.UpdateProfile(t.Context(), created.User.ID, UpdateInput{Remark: &remark}); err != nil || calls != 2 {
		t.Fatalf("unrelated profile edit invalidated membership: calls=%d / %v", calls, err)
	}
	svc.SetMembershipInvalidator(func() {
		calls++
		if repo.byID[created.User.ID] != nil {
			t.Error("delete invalidation preceded persistence")
		}
	})
	if err := svc.deleteUser(t.Context(), created.User.ID); err != nil || calls != 3 {
		t.Fatalf("delete invalidation: calls=%d / %v", calls, err)
	}
}

func TestMembershipInvalidationCoversSSOAndProfileGroupChanges(t *testing.T) {
	repo := &memoryUserRepo{byID: map[int64]*domain.User{}}
	svc := &Service{users: repo, groups: ssoGroupFixture(), ownership: emptyOwnershipRepo{}, selector: unreachableSelector{}, tasks: &recordingTaskRepo{}}
	calls := 0
	svc.SetMembershipInvalidator(func() { calls++ })
	u, err := svc.EnsureSSO(t.Context(), ssoIn("membership-sso", "member-sso@example.test", []string{"idp-vip"}, vipRule()))
	if err != nil || calls != 1 {
		t.Fatalf("SSO creation invalidation: calls=%d / %v", calls, err)
	}
	if _, err := svc.EnsureSSO(t.Context(), ssoIn("membership-sso", u.UPN, []string{"idp-other"}, vipRule())); err != nil || calls != 2 {
		t.Fatalf("SSO group move invalidation: calls=%d / %v", calls, err)
	}
	if _, err := svc.EnsureSSO(t.Context(), ssoIn("membership-sso", u.UPN, []string{"idp-other"}, vipRule())); err != nil || calls != 2 {
		t.Fatalf("unchanged SSO login invalidated membership: calls=%d / %v", calls, err)
	}
	groupID := int64(2)
	if err := svc.UpdateProfile(t.Context(), u.ID, UpdateInput{GroupID: &groupID}); err != nil || calls != 3 {
		t.Fatalf("profile group move invalidation: calls=%d / %v", calls, err)
	}
}

func TestMembershipInvalidationSkipsFailedPersistence(t *testing.T) {
	repo := &membershipFailRepo{&memoryUserRepo{byID: map[int64]*domain.User{7: {ID: 7, UPN: "stored@example.test", GroupID: 1}}}}
	svc := &Service{users: repo, groups: ssoGroupFixture()}
	calls := 0
	svc.SetMembershipInvalidator(func() { calls++ })
	if _, err := svc.CreateLocal(t.Context(), CreateLocalInput{UPN: "new@example.test", GroupID: 1, InitialPassword: "test-password"}); !errors.Is(err, domain.ErrUnavailable) {
		t.Fatalf("create failure: %v", err)
	}
	if err := svc.ChangeGroupAndSync(t.Context(), 7, 2); !errors.Is(err, domain.ErrUnavailable) {
		t.Fatalf("move failure: %v", err)
	}
	if err := svc.deleteUser(t.Context(), 7); !errors.Is(err, domain.ErrUnavailable) {
		t.Fatalf("delete failure: %v", err)
	}
	if calls != 0 {
		t.Fatalf("failed writes invalidated membership: %d", calls)
	}
}
