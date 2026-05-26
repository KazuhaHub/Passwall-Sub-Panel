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

func (s *Service) ListPaged(ctx context.Context, p ports.Pagination) ([]*domain.Group, int64, error) {
	return s.groups.ListPaged(ctx, p)
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
		if matchFilter(n, g.TagFilter) {
			out = append(out, n)
		}
	}
	return out, nil
}

// Matches reports whether a node satisfies a group's tag_filter. Exported so
// callers (e.g. node.Service when syncing clients onto a freshly-created
// inbound) can ask "which groups would now include this node?" without
// duplicating the filter semantics.
func Matches(n *domain.Node, filter domain.TagFilter) bool {
	if filter.All {
		return true
	}
	return matchFilter(n, filter)
}

// matchFilter dispatches on TagFilter.Mode. Empty / "all" → AND; "any" → OR.
// An empty Tags list under AND matches everything (vacuous truth, same as
// the original behavior); under OR it matches nothing (no condition can be
// satisfied) — explicit, less surprising than the AND default.
func matchFilter(n *domain.Node, f domain.TagFilter) bool {
	switch f.Mode {
	case "any":
		return matchAny(n, f.Tags)
	default:
		return matchAll(n, f.Tags)
	}
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

// matchAny returns true when at least one condition matches.
func matchAny(n *domain.Node, conds []string) bool {
	if len(conds) == 0 {
		return false
	}
	for _, c := range conds {
		if matchOne(n, c) {
			return true
		}
	}
	return false
}

func matchOne(n *domain.Node, cond string) bool {
	// Trim the whole condition AND each side of the colon. The settings UI
	// shows examples like "region:XX / tag:YY" without spaces, but admins
	// commonly type "tag: reality" with a space after the colon — without
	// trimming, val would be " reality" and HasTag(" reality") wouldn't
	// match the stored tag "reality".
	cond = strings.TrimSpace(cond)
	if i := strings.IndexByte(cond, ':'); i > 0 {
		key := strings.TrimSpace(cond[:i])
		val := strings.TrimSpace(cond[i+1:])
		switch key {
		case "region":
			return strings.EqualFold(n.Region, val)
		case "tag":
			return n.HasTag(val)
		case "node":
			// node:<address> matches the node's ServerAddress — the
			// hostname clients connect to. Added in v3.0.0-beta.11 when
			// the admin UI's "Server" picker was renamed "Node" and
			// rewired to surface server_address values.
			return strings.EqualFold(n.ServerAddress, val)
		default:
			// server:xxx / vendor:yyy / any unknown custom key — stored
			// as a tag verbatim. Reassemble from the trimmed parts so
			// "server: foo" still matches a stored "server:foo".
			// `server:` stays as a literal tag lookup (not ServerAddress)
			// for backward compatibility with any pre-beta.11 groups —
			// admins who want ServerAddress matching use `node:`.
			return n.HasTag(key + ":" + val)
		}
	}
	// no colon: treat the whole condition as a tag
	return n.HasTag(cond)
}
