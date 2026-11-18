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

// Kinds of refused source.
const (
	// RefusedUnregistered: the address is in no class's set; it is probably a
	// server of the line's class that the set does not list yet.
	RefusedUnregistered = "unregistered"
	// RefusedWrongLine: the address belongs to a class's set, so the connection
	// came through one line with the identity of another (a direct-class identity
	// used through a priced path, or a priced identity from another class's
	// addresses). Adding the address to a line would not fix it.
	RefusedWrongLine = "wrong_line"
)

// Refused is one source address a landing refused for a line's class.
type Refused struct {
	IP       string
	Count    int64
	LastSeen time.Time
	// Kind is RefusedUnregistered or RefusedWrongLine.
	Kind string
	// Addable is whether adding the address to this line's source addresses is
	// the fix: an unregistered source, on a line whose own addresses it can join
	// (a CDN line on the Cloudflare default has none of its own: adding one would
	// replace the whole default).
	Addable bool
}

// LineView is what the line editor shows of a line's source guard.
type LineView struct {
	LineID string
	// Guarded is false for a tunnel line, which has no source set.
	Guarded bool
	// Direct is whether the line costs what the direct line costs: its class
	// needs no sources.
	Direct bool
	// Factor is the line's effective factor in thousandths.
	Factor int
	// Effective is the line's own effective set (empty: not known).
	Effective []string
	// Withheld is why the guard keeps the line out of subscriptions, or "".
	Withheld string
	Refused  []Refused
}

// NodeView is the source guard of one landing.
type NodeView struct {
	// CanGuard is whether the landing can check where connections come from: its
	// server's agent can, and its listener is one the core reads the connection's
	// own address for.
	CanGuard bool
	// Reason says why not when CanGuard is false: "server" (no agent that
	// declared the capability) or "listener" (the core would take the source
	// from a header the client sends).
	Reason string
	Lines  []LineView
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

// cdnDefault is whether a CDN line uses the Cloudflare ranges: it names no
// addresses of its own.
func cdnDefault(line domain.RelayLine) bool { return line.CDN() && len(line.SourceCIDRs) == 0 }

// NodeView works out the source guard of one landing as the next sync would
// send it.
func (a *Admin) NodeView(ctx context.Context, node *domain.Node) (NodeView, error) {
	var view NodeView
	if node == nil {
		return view, nil
	}
	in, err := a.planner.Inputs(ctx, []*domain.Node{node})
	if err != nil {
		return view, err
	}
	analysis := node.AnalyzeSourceGuard(in)
	view.CanGuard = analysis.Guards
	if !view.CanGuard {
		view.Reason = "server"
		if in.Servers[node.PanelID].Capable {
			view.Reason = "listener"
		}
	}
	var hits []domain.SourceGuardHit
	if a != nil && a.hits != nil && view.CanGuard {
		all, err := a.hits.ListHits(ctx, []int64{node.PanelID})
		if err != nil {
			return view, err
		}
		for _, hit := range all {
			if NodeIDOf(hit.Set) == node.ID {
				hits = append(hits, hit)
			}
		}
	}
	for _, line := range node.Relays {
		factor := line.EffectiveTrafficFactor()
		lv := LineView{LineID: line.ID, Guarded: line.Guarded(), Factor: factor, Direct: factor == node.EffectiveTrafficFactor()}
		if lv.Guarded {
			lv.Effective = line.EffectiveSourceCIDRs(in)
			lv.Withheld = analysis.Withheld[line.ID]
			lv.Refused = refusedFor(node, line, analysis, hits)
		}
		view.Lines = append(view.Lines, lv)
	}
	return view, nil
}

// refusedFor are the refusals shown on a line: those of its class's set.
func refusedFor(node *domain.Node, line domain.RelayLine, analysis domain.SourceGuardAnalysis, hits []domain.SourceGuardHit) []Refused {
	class, priced := analysis.PricedClassOf(line.EffectiveTrafficFactor())
	if !priced || !slices.Contains(class.LineIDs, line.ID) {
		return nil
	}
	var out []Refused
	for _, hit := range hits {
		factor, ok := FactorOf(hit.Set)
		if !ok || factor != class.Factor {
			continue
		}
		kind := ClassifyRefused(analysis, class, hit.IP)
		out = append(out, Refused{
			IP: hit.IP, Count: hit.Count, LastSeen: hit.LastSeen, Kind: kind,
			Addable: kind == RefusedUnregistered && !cdnDefault(line),
		})
	}
	return out
}

// ClassifyRefused says what a refused source is, given the class whose set
// refused it: inside that set, or inside the set of any other class, it is a
// connection through the wrong line; otherwise an unregistered address.
func ClassifyRefused(analysis domain.SourceGuardAnalysis, class domain.SourceClass, ip string) string {
	addr, err := netip.ParseAddr(ip)
	if err != nil {
		return RefusedUnregistered
	}
	addr = addr.Unmap()
	inside := func(cidrs []string) bool {
		for _, text := range cidrs {
			if p, err := netip.ParsePrefix(text); err == nil && p.Contains(addr) {
				return true
			}
		}
		return false
	}
	if inside(class.CIDRs) || inside(analysis.Direct.CIDRs) {
		return RefusedWrongLine
	}
	for _, other := range analysis.Priced {
		if other.Factor != class.Factor && inside(other.CIDRs) {
			return RefusedWrongLine
		}
	}
	return RefusedUnregistered
}

// CheckAddable refuses the one-click addition of a refused source to a line
// unless the landing really refused it as an unregistered source of the line's
// class and the line has addresses of its own to add it to.
func (a *Admin) CheckAddable(ctx context.Context, node *domain.Node, lineID, ip string) error {
	view, err := a.NodeView(ctx, node)
	if err != nil {
		return err
	}
	canonical, err := SourceToAdd(ip)
	if err != nil {
		return err
	}
	for _, lv := range view.Lines {
		if lv.LineID != lineID {
			continue
		}
		for _, r := range lv.Refused {
			if rc, err := SourceToAdd(r.IP); err == nil && rc == canonical && r.Addable {
				return nil
			}
		}
	}
	return fmt.Errorf("%w: %s is not an unregistered source that can be added to this line (a line on the Cloudflare default has no addresses of its own, and an address that belongs to another line's set is a connection through the wrong line)", domain.ErrValidation, ip)
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
	_, err := a.hits.ClearHits(ctx, string(Key(node.ID, line.EffectiveTrafficFactor())), ip)
	return err
}
