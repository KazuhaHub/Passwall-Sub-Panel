package group

import (
	"context"
	"errors"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

type selectionGroups struct {
	ports.GroupRepo
	committed int
	err       error
	members   int64
}

func (r *selectionGroups) Create(context.Context, *domain.Group) error {
	if r.err == nil {
		r.committed++
	}
	return r.err
}
func (r *selectionGroups) Update(context.Context, *domain.Group) error {
	if r.err == nil {
		r.committed++
	}
	return r.err
}
func (r *selectionGroups) Delete(context.Context, int64) error {
	if r.err == nil {
		r.committed++
	}
	return r.err
}
func (r *selectionGroups) CountMembers(context.Context, int64) (int64, error) { return r.members, nil }

type failedGroupScopeCleanup struct{ ports.ScopeSettingsRepo }

func (failedGroupScopeCleanup) DeleteScope(context.Context, string, int64) error {
	return domain.ErrUnavailable
}

func TestGroupSelectionInvalidationFollowsPersistedWrites(t *testing.T) {
	r := &selectionGroups{}
	s := New(r, nil, nil)
	calls := 0
	s.SetMembershipInvalidator(func() {
		calls++
		if r.committed != calls {
			t.Error("group invalidation preceded persistence")
		}
	})
	g := &domain.Group{ID: 8, Slug: "selected", Name: "Selected"}
	if err := s.Create(t.Context(), g); err != nil || calls != 1 {
		t.Fatalf("create invalidation: calls=%d / %v", calls, err)
	}
	g.TagFilter = domain.TagFilter{All: true}
	if err := s.Update(t.Context(), g); err != nil || calls != 2 {
		t.Fatalf("filter invalidation: calls=%d / %v", calls, err)
	}
	s.scope = failedGroupScopeCleanup{}
	if err := s.Delete(t.Context(), g.ID); !errors.Is(err, domain.ErrUnavailable) || calls != 3 {
		t.Fatalf("committed delete hidden by cleanup failure: calls=%d / %v", calls, err)
	}
}

func TestGroupSelectionInvalidationSkipsRejectedAndFailedWrites(t *testing.T) {
	r := &selectionGroups{err: domain.ErrUnavailable}
	s := New(r, nil, nil)
	calls := 0
	s.SetMembershipInvalidator(func() { calls++ })
	g := &domain.Group{ID: 8, Slug: "selected", Name: "Selected"}
	if err := s.Create(t.Context(), g); !errors.Is(err, domain.ErrUnavailable) {
		t.Fatal(err)
	}
	if err := s.Update(t.Context(), g); !errors.Is(err, domain.ErrUnavailable) {
		t.Fatal(err)
	}
	if err := s.Delete(t.Context(), g.ID); !errors.Is(err, domain.ErrUnavailable) {
		t.Fatal(err)
	}
	r.err, r.members = nil, 1
	if err := s.Delete(t.Context(), g.ID); !errors.Is(err, domain.ErrConflict) {
		t.Fatal(err)
	}
	if calls != 0 {
		t.Fatalf("failed/rejected changes invalidated membership: %d", calls)
	}
}
