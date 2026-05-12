// Package group implements panel-side Group CRUD and tag-filter evaluation.
package group

import (
	"context"
	"fmt"
	"strings"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

type Service struct {
	groups ports.GroupRepo
	nodes  ports.NodeRepo
}

func New(groups ports.GroupRepo, nodes ports.NodeRepo) *Service {
	return &Service{groups: groups, nodes: nodes}
}

func (s *Service) Get(ctx context.Context, id int64) (*domain.Group, error) {
	return s.groups.GetByID(ctx, id)
}

func (s *Service) GetBySlug(ctx context.Context, slug string) (*domain.Group, error) {
	return s.groups.GetBySlug(ctx, slug)
}

func (s *Service) List(ctx context.Context) ([]*domain.Group, error) {
	return s.groups.List(ctx)
}

// CountMembers exposes the repo-level member count for display in the
// admin UI without leaking the repo abstraction.
func (s *Service) CountMembers(ctx context.Context, id int64) (int64, error) {
	return s.groups.CountMembers(ctx, id)
}

func (s *Service) Create(ctx context.Context, g *domain.Group) error {
	if g.Slug == "" || g.Name == "" {
		return fmt.Errorf("%w: slug and name required", domain.ErrValidation)
	}
	return s.groups.Create(ctx, g)
}

func (s *Service) Update(ctx context.Context, g *domain.Group) error {
	return s.groups.Update(ctx, g)
}

// Delete refuses to remove a group that still has members; the admin must
// reassign users first.
func (s *Service) Delete(ctx context.Context, id int64) error {
	n, err := s.groups.CountMembers(ctx, id)
	if err != nil {
		return err
	}
	if n > 0 {
		return fmt.Errorf("%w: group has %d members", domain.ErrConflict, n)
	}
	return s.groups.Delete(ctx, id)
}

// NodesFor returns the nodes that a group's tag_filter selects, sorted by
// the global node sort_order (group.layout overrides happen later in the
// render pipeline).
func (s *Service) NodesFor(ctx context.Context, g *domain.Group) ([]*domain.Node, error) {
	all, err := s.nodes.ListEnabled(ctx)
	if err != nil {
		return nil, err
	}
	if g.TagFilter.All {
		return all, nil
	}
	out := make([]*domain.Node, 0, len(all))
	for _, n := range all {
		if matchAll(n, g.TagFilter.Tags) {
			out = append(out, n)
		}
	}
	return out, nil
}

// matchAll returns true when every condition matches. Conditions have the
// form "region:XX", "tag:yy", "server:zz" or any "key:value" — the implementation
// treats "region" specially and falls back to a literal tag match.
func matchAll(n *domain.Node, conds []string) bool {
	for _, c := range conds {
		if !matchOne(n, c) {
			return false
		}
	}
	return true
}

func matchOne(n *domain.Node, cond string) bool {
	if i := strings.IndexByte(cond, ':'); i > 0 {
		key, val := cond[:i], cond[i+1:]
		switch key {
		case "region":
			return strings.EqualFold(n.Region, val)
		case "tag":
			return n.HasTag(val)
		default:
			// server:xxx / vendor:yyy / any custom key — stored as a tag verbatim
			return n.HasTag(cond)
		}
	}
	// no colon: treat the whole condition as a tag
	return n.HasTag(cond)
}
