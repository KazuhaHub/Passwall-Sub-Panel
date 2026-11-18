package node

import (
	"context"
	"fmt"
	"strconv"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// Source guard rules at save time (ADR 0044).
//
// A direct line, a direct forward, an external line and a CDN line all end on
// the landing's own listener. A landing that can check where a connection comes
// from keeps their identities apart; one that cannot (3X-UI, S-UI, a Passwall
// Node without transit.source_guard.v1) would let a user bill traffic at
// whichever of those lines is cheapest. So on such a landing they must all
// cost the same, and on a landing that can guard an external line that costs
// something else must say which addresses its traffic comes from.

const (
	// CodeSourceGuardFactorMismatch refuses a line whose factor differs from the
	// direct line's on a landing that cannot guard.
	CodeSourceGuardFactorMismatch = "source_guard_factor_mismatch"
	// CodeSourceGuardNeedsCIDRs refuses an external line whose factor differs
	// from the direct line's and that names no source addresses.
	CodeSourceGuardNeedsCIDRs = "source_guard_needs_cidrs"
)

// SetSourceGuardCheck binds the question "can a landing on this server check
// where connections come from". Left unbound the save rules are not applied
// (tests that do not exercise them).
func (s *Service) SetSourceGuardCheck(canGuard func(ctx context.Context, panelID int64) bool) {
	s.canGuard = canGuard
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

// sourceGuardProblems are the lines of the landing the rules above refuse, by
// line id, in the form they would be refused with.
func sourceGuardProblems(n *domain.Node, canGuard bool) map[string]*domain.CodedValidationError {
	out := map[string]*domain.CodedValidationError{}
	direct := n.EffectiveTrafficFactor()
	for _, line := range n.Relays {
		factor := line.EffectiveTrafficFactor()
		if !line.Guarded() || factor == direct {
			continue
		}
		params := map[string]string{
			"node": n.DisplayName, "line": line.Label(),
			"line_factor": strconv.Itoa(factor), "direct_factor": strconv.Itoa(direct),
		}
		switch {
		case !canGuard:
			out[line.ID] = &domain.CodedValidationError{
				Code: CodeSourceGuardFactorMismatch, Params: params,
				Msg: fmt.Sprintf("line %q of %s costs %d but the direct line costs %d: this server cannot check where connections come from, so every line ending on the node must cost the same (use a Passwall Node that supports source checks to price lines apart)",
					line.Label(), n.DisplayName, factor, direct),
			}
		case !line.Managed() && !line.CDN() && len(line.SourceCIDRs) == 0:
			out[line.ID] = &domain.CodedValidationError{
				Code: CodeSourceGuardNeedsCIDRs, Params: params,
				Msg: fmt.Sprintf("external line %q of %s costs %d but the direct line costs %d: list the addresses its traffic comes from in the line's source addresses, so the two can be told apart",
					line.Label(), n.DisplayName, factor, direct),
			}
		}
	}
	return out
}

// checkSourceGuard refuses a save that introduces or changes a line the rules
// refuse. A refusal that was already true before the save (a landing that lost
// the capability, factors saved before the rules existed) does not block an
// unrelated edit: such a line is withheld from subscriptions instead.
func (s *Service) checkSourceGuard(ctx context.Context, previous, next *domain.Node) error {
	if s.canGuard == nil || next == nil || next.IsTunnelChild() || next.IsSeparator() {
		return nil
	}
	canGuard := s.canGuard(ctx, next.PanelID)
	after := sourceGuardProblems(next, canGuard)
	if len(after) == 0 {
		return nil
	}
	var before map[string]*domain.CodedValidationError
	if previous != nil {
		before = sourceGuardProblems(previous, s.canGuard(ctx, previous.PanelID))
	}
	for _, line := range next.Relays {
		problem, bad := after[line.ID]
		if !bad {
			continue
		}
		if was, existed := before[line.ID]; existed && sameProblem(was, problem) {
			continue
		}
		return problem
	}
	return nil
}

// sameProblem is whether two refusals are about the same prices: names are
// not, so renaming a node or a line is not a new problem.
func sameProblem(a, b *domain.CodedValidationError) bool {
	return a.Code == b.Code && a.Params["line_factor"] == b.Params["line_factor"] && a.Params["direct_factor"] == b.Params["direct_factor"]
}
