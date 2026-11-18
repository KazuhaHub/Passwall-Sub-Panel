package node

import (
	"context"
	"fmt"
	"slices"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// Source guard rules at save time (ADR 0044).
//
// A direct line, a direct forward, an external line and a CDN line all end on
// the landing's own listener. A landing that can check where a connection comes
// from keeps the identities of different factors apart; one that cannot (3X-UI,
// S-UI, a Passwall Node without transit.source_guard.v1, a listener whose core
// would take the source from a client-sent header) would let a user bill traffic
// at whichever of those lines is cheapest. So on such a landing they must all
// cost the same. On a landing that can guard, lines are grouped by factor
// (domain.AnalyzeSourceGuard) and the classes must arrive from distinct
// addresses.

const (
	CodeSourceGuardFactorMismatch = domain.CodeSourceGuardFactorMismatch
	CodeSourceGuardNeedsCIDRs     = domain.CodeSourceGuardNeedsCIDRs
	CodeSourceGuardOverlap        = domain.CodeSourceGuardOverlap
	CodeSourceGuardTooManyClasses = domain.CodeSourceGuardTooManyClasses
	CodeSourceGuardTooManyCIDRs   = domain.CodeSourceGuardTooManyCIDRs
)

// SetSourceGuardCheck binds the question "can a landing on this server check
// where connections come from". Left unbound the save rules are not applied
// (tests that do not exercise them).
func (s *Service) SetSourceGuardCheck(canGuard func(ctx context.Context, panelID int64) bool) {
	s.canGuard = canGuard
}

// SetSourceGuardInputs binds where the facts of the rules come from (the
// planner's inputs: Cloudflare, entry addresses, each server's agent, fronts).
// Left unbound, only the capability check applies, on the built-in Cloudflare
// ranges.
func (s *Service) SetSourceGuardInputs(inputs func(ctx context.Context, nodes []*domain.Node) (domain.SourceInputs, error)) {
	s.guardInputs = inputs
}

// resolveSourceCIDRs normalises each line's own source addresses. A line whose
// request omitted them keeps the stored ones, so an editor that does not know
// the field cannot clear them; a tunnel line has none.
func resolveSourceCIDRs(previous *domain.Node, n *domain.Node) error {
	saved := map[string][]string{}
	if previous != nil {
		for _, r := range previous.Relays {
			saved[r.ID] = r.SourceCIDRs
		}
	}
	for i := range n.Relays {
		r := &n.Relays[i]
		if r.Tunnel() {
			r.SourceCIDRs, r.SourceCIDRsUnresolved = nil, false
			continue
		}
		if r.SourceCIDRsUnresolved {
			r.SourceCIDRs, r.SourceCIDRsUnresolved = saved[r.ID], false
			continue
		}
		cidrs, err := domain.NormalizeSourceCIDRs(r.SourceCIDRs)
		if err != nil {
			return fmt.Errorf("relay line %d: %w", i+1, err)
		}
		r.SourceCIDRs = cidrs
	}
	return nil
}

// sourceGuardInputs are the facts the rules are judged by for one landing: the
// ranges Cloudflare publishes, the entry servers' addresses and what the
// landing's server can do. A failed read leaves them out, so the landing counts
// as unable to guard: the stricter rule is the safe one to save by.
func (s *Service) sourceGuardInputs(ctx context.Context, n *domain.Node) domain.SourceInputs {
	if s.guardInputs != nil {
		if in, err := s.guardInputs(ctx, []*domain.Node{n}); err == nil {
			return in
		}
		return domain.SourceInputs{}
	}
	in := domain.SourceInputs{Cloudflare: domain.BuiltinCloudflare, Servers: map[int64]domain.SourceGuardServer{}}
	if s.canGuard != nil {
		in.Servers[n.PanelID] = domain.SourceGuardServer{Capable: s.canGuard(ctx, n.PanelID)}
	}
	return in
}

// checkSourceGuard refuses a save that introduces or changes something the
// rules refuse (domain.AnalyzeSourceGuard: a price a landing that cannot guard
// cannot keep apart, factor classes that arrive from the same addresses, more
// classes or addresses than a set holds). A refusal that was already true
// before the save (a landing that lost the capability, factors saved before the
// rules existed) does not block an unrelated edit: the lines it affects are
// withheld from subscriptions instead.
func (s *Service) checkSourceGuard(ctx context.Context, previous, next *domain.Node) error {
	if (s.canGuard == nil && s.guardInputs == nil) || next == nil || next.IsTunnelChild() || next.IsSeparator() {
		return nil
	}
	after := next.AnalyzeSourceGuard(s.sourceGuardInputs(ctx, next)).Problems
	if len(after) == 0 {
		return nil
	}
	known := map[string]bool{}
	if previous != nil {
		for _, p := range previous.AnalyzeSourceGuard(s.sourceGuardInputs(ctx, previous)).Problems {
			known[p.ID] = true
		}
	}
	for _, p := range after {
		if !known[p.ID] {
			return p.Err
		}
	}
	return nil
}

// AddLineSourceCIDR adds one prefix to one line's own source addresses and
// returns the line's addresses after. It is a targeted update: the node is read
// under the same server locks every node save takes and only that line's
// addresses change, so an edit that was saved a moment ago is not written back
// over by a copy read before it. The save rules apply as they do to an edit.
//
// A CDN line that names no addresses of its own uses the Cloudflare ranges;
// adding one address would replace the whole default, so it is refused.
func (s *Service) AddLineSourceCIDR(ctx context.Context, nodeID int64, lineID, cidr string) ([]string, error) {
	previous, unlock, err := s.lockAndLoad(ctx, nodeID, nil)
	if err != nil {
		return nil, err
	}
	defer unlock()
	if previous.IsTunnelChild() {
		return nil, domain.ErrNotFound
	}
	index := slices.IndexFunc(previous.Relays, func(r domain.RelayLine) bool { return r.ID == lineID })
	if index < 0 || !previous.Relays[index].Guarded() {
		return nil, domain.ErrNotFound
	}
	line := previous.Relays[index]
	if line.CDN() && len(line.SourceCIDRs) == 0 {
		return nil, fmt.Errorf("%w: the CDN line %q uses the Cloudflare ranges; adding one address would replace all of them. Give the line its own list of addresses in the editor instead", domain.ErrValidation, line.Label())
	}
	added, err := domain.NormalizeSourceCIDRs([]string{cidr})
	if err != nil {
		return nil, err
	}
	if slices.Contains(line.SourceCIDRs, added[0]) {
		return slices.Clone(line.SourceCIDRs), nil
	}
	merged, err := domain.NormalizeSourceCIDRs(append(slices.Clone(line.SourceCIDRs), added[0]))
	if err != nil {
		return nil, err
	}
	next := *previous
	next.Relays = slices.Clone(previous.Relays)
	next.Relays[index].SourceCIDRs = merged
	if err := s.checkSourceGuard(ctx, previous, &next); err != nil {
		return nil, err
	}
	if err := s.nodes.UpdateMetadata(ctx, &next); err != nil {
		return nil, err
	}
	return merged, nil
}
