package sourceguard

import (
	"context"
	"fmt"
	"net/netip"
	"slices"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// HitStore is the stored sources a landing refused.
type HitStore interface {
	ListHits(ctx context.Context, panelIDs []int64) ([]domain.SourceGuardHit, error)
	ClearHits(ctx context.Context, set, ip string) (int64, error)
}

// Refused is one source address a landing refused for a line.
type Refused struct {
	IP       string
	Count    int64
	LastSeen time.Time
}

// LineView is what the line editor shows of a line's source guard.
type LineView struct {
	LineID string
	// Guarded is false for a tunnel line, which has no source set.
	Guarded bool
	// Effective is the set the landing is sent for the line (empty: not known).
	Effective []string
	// Withheld is why the guard keeps the line out of subscriptions, or "".
	Withheld string
	Refused  []Refused
}

// NodeView is the source guard of one landing.
type NodeView struct {
	// CanGuard is whether the landing's server can check where connections come
	// from.
	CanGuard bool
	Lines    []LineView
}

// Admin answers the administrator's questions about the source guard.
type Admin struct {
	planner *Planner
	agents  AgentLister
	hits    HitStore
	cf      *Cloudflare
}

// NewAdmin returns the administrator's view. Any part may be nil.
func NewAdmin(agents AgentLister, cf *Cloudflare, hits HitStore) *Admin {
	return &Admin{planner: NewPlanner(agents, cf), agents: agents, hits: hits, cf: cf}
}

// Cloudflare is the ranges a CDN line defaults to, and where they came from.
func (a *Admin) Cloudflare(ctx context.Context) Ranges {
	if a == nil || a.cf == nil {
		return Ranges{CIDRs: slices.Clone(BuiltinCloudflare), Source: SourceBuiltin, BuiltinDate: BuiltinCloudflareDate}
	}
	return a.cf.Current(ctx)
}

// NodeView works out the source guard of one landing as the next sync would
// send it.
func (a *Admin) NodeView(ctx context.Context, node *domain.Node) (NodeView, error) {
	var view NodeView
	if node == nil {
		return view, nil
	}
	if a != nil && a.agents != nil {
		agents, err := a.agents.ListByPanelIDs(ctx, []int64{node.PanelID})
		if err != nil {
			return view, err
		}
		view.CanGuard = len(agents) > 0 && agents[0].CanSourceGuard()
	}
	in, err := a.planner.Inputs(ctx, []*domain.Node{node})
	if err != nil {
		return view, err
	}
	refused := map[string][]Refused{}
	if a != nil && a.hits != nil {
		hits, err := a.hits.ListHits(ctx, []int64{node.PanelID})
		if err != nil {
			return view, err
		}
		for _, hit := range hits {
			if NodeIDOf(hit.Set) == node.ID {
				refused[hit.Set] = append(refused[hit.Set], Refused{IP: hit.IP, Count: hit.Count, LastSeen: hit.LastSeen})
			}
		}
	}
	for _, line := range node.Relays {
		lv := LineView{LineID: line.ID, Guarded: line.Guarded()}
		if lv.Guarded {
			lv.Effective = line.EffectiveSourceCIDRs(in)
			entryKnown := line.Managed() && len(in.EntryAddresses[line.EntryPanelID]) > 0
			lv.Withheld = node.SourceGuardWithheld(line, view.CanGuard, entryKnown)
			lv.Refused = refused[string(Key(node.ID, line.ID))]
		}
		view.Lines = append(view.Lines, lv)
	}
	return view, nil
}

// SourceToAdd is the prefix that adds one refused address to a line: the
// address as a single-host prefix.
func SourceToAdd(ip string) (string, error) {
	addr, err := netip.ParseAddr(ip)
	if err != nil || addr.Zone() != "" {
		return "", fmt.Errorf("%w: %q is not an IP address", domain.ErrValidation, ip)
	}
	addr = addr.Unmap()
	return netip.PrefixFrom(addr, addr.BitLen()).String(), nil
}

// Forget removes the stored refusals of one address on a line, after the
// address was added to the line.
func (a *Admin) Forget(ctx context.Context, node *domain.Node, line domain.RelayLine, ip string) error {
	if a == nil || a.hits == nil {
		return nil
	}
	_, err := a.hits.ClearHits(ctx, string(Key(node.ID, line.ID)), ip)
	return err
}
