package destpolicy

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"slices"
	"strconv"
	"strings"

	"github.com/KazuhaHub/passwall-protocol/protocol"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/destlist"
)

// base is the snapshot expanded into rules, before it is specialised for one
// node. It is the heavy half of a compile (geosite entries, country ranges)
// and depends on nothing but the published generation, so it is built once per
// generation and shared by every node.
type base struct {
	generation int64
	geoip      string
	geosite    string

	// allow and block are in emission order (priority, then policy id).
	allow, block []baseRule
	// sources are the source-deny groups, by the lowest node id of each group.
	sources []baseSource
	exempt  []int64

	// policies and sourceNotes are what the status page says about each
	// definition: which part could not be compiled and why.
	policies    map[int64]*PolicyNote
	sourceNotes map[int64]string
	// geoipUnavailable: some country could not be expanded for want of a
	// database. A rule that needs it is skipped, never treated as compiled.
	geoipUnavailable bool
}

// PolicyNote says how a policy compiled.
type PolicyNote struct {
	// State is "ok", "lists_not_ready" (some list part was skipped) or "empty"
	// (nothing was left to emit, so the policy is not in the document).
	State string `json:"state"`
	// NotReady / Empty are the ids of the lists that were skipped.
	NotReady []int64 `json:"not_ready_lists,omitempty"`
	Empty    []int64 `json:"empty_lists,omitempty"`
	// GeoIPUnavailable: a list part was skipped because a country in it needs
	// the GeoIP database and there is none.
	GeoIPUnavailable bool `json:"geoip_unavailable,omitempty"`
}

type baseRule struct {
	policyID int64
	// id is the rule id: p<policy> or p<policy>x1 / x2 when the policy split.
	id        string
	action    protocol.RuleAction
	scope     domain.DestScope
	groups    []int64
	panels    []int64
	domains   []string
	cidrs     []string
	ports     string
	network   string
	protocols []string
	private   bool
}

type baseSource struct {
	nodes []int64
	cidrs []string
	// key is "CN,US|1.2.3.0/24": the rule's own definition, what makes two
	// inbounds share one rule.
	key string
}

// listRefs are the expansions of the lists of one policy.
func (s *Service) buildBase(ctx context.Context, doc *snapshotDoc) (*base, error) {
	b := &base{
		generation: doc.Generation, geoip: doc.GeoIP, geosite: doc.Geosite,
		exempt: slices.Clone(doc.Exempt), policies: map[int64]*PolicyNote{}, sourceNotes: map[int64]string{},
	}
	lists := make(map[int64]*snapList, len(doc.Lists))
	for i := range doc.Lists {
		lists[doc.Lists[i].ID] = &doc.Lists[i]
	}
	for _, p := range doc.Policy {
		note := &PolicyNote{State: "ok"}
		b.policies[p.ID] = note
		var domains, cidrs []string
		for _, id := range p.Lists {
			l := lists[id]
			if l == nil || !l.Ready || l.Side != string(domain.DestSideDest) {
				note.NotReady = append(note.NotReady, id)
				continue
			}
			exp, err := s.lists.Expand(ctx, &domain.DestList{ID: l.ID, Name: l.Name, Entries: []byte(l.Entries)})
			switch {
			case err == nil:
			case errors.Is(err, destlist.ErrGeoIPUnavailable):
				note.GeoIPUnavailable, b.geoipUnavailable = true, true
				note.NotReady = append(note.NotReady, id)
				continue
			case errors.Is(err, destlist.ErrGeositeUnavailable):
				note.NotReady = append(note.NotReady, id)
				continue
			default:
				return nil, fmt.Errorf("destpolicy: expand list %d: %w", id, err)
			}
			if len(exp.Domains)+len(exp.CIDRs) == 0 {
				note.Empty = append(note.Empty, id)
				continue
			}
			domains = append(domains, exp.Domains...)
			cidrs = append(cidrs, exp.CIDRs...)
		}
		cidrs = append(cidrs, p.Inline.CIDRs...)
		domains = sortedUnique(domains)
		cidrs = mergeCIDRStrings(cidrs)

		// A part of the match the lists were supposed to supply and did not is
		// SKIPPED, and the narrowing fields (ports, network) are not left to
		// stand in for it: a block rule on "this list, port 25" whose list is
		// not ready would otherwise become "port 25 for everyone", a rule
		// nobody wrote. The ports-and-network-only rule exists only for a
		// policy that has no address side at all (the SMTP group).
		addrIntent := len(p.Lists) > 0 || len(p.Inline.CIDRs) > 0 || p.Inline.Private
		hasAddr := len(domains) > 0 || len(cidrs) > 0 || p.Inline.Private
		hasProto := len(p.Inline.Protocols) > 0

		var parts []baseRule
		mk := func() baseRule {
			return baseRule{
				policyID: p.ID, action: protocol.RuleAction(p.Action), scope: domain.DestScope(p.Scope),
				groups: p.Groups, panels: p.Panels, ports: p.Inline.Ports, network: p.Inline.Network,
			}
		}
		switch {
		case addrIntent:
			if hasAddr {
				r := mk()
				r.domains, r.cidrs, r.private = domains, cidrs, p.Inline.Private
				parts = append(parts, r)
			}
			if hasProto {
				r := mk()
				r.protocols = slices.Clone(p.Inline.Protocols)
				parts = append(parts, r)
			}
		case hasProto:
			r := mk()
			r.protocols = slices.Clone(p.Inline.Protocols)
			parts = append(parts, r)
		case p.Inline.Ports != "" || p.Inline.Network != "":
			parts = append(parts, mk())
		}
		if len(parts) == 0 {
			note.State = "empty"
			continue
		}
		if len(note.NotReady) > 0 || len(note.Empty) > 0 {
			note.State = "lists_not_ready"
		}
		for i := range parts {
			parts[i].id = "p" + strconv.FormatInt(p.ID, 10)
			if len(parts) > 1 {
				parts[i].id += "x" + strconv.Itoa(i+1)
			}
			if p.Action == string(domain.DestActionAllow) {
				b.allow = append(b.allow, parts[i])
			} else {
				b.block = append(b.block, parts[i])
			}
		}
	}

	groups := map[string]*baseSource{}
	for _, src := range doc.Sources {
		var cidrs []string
		geoipMissing := false
		if len(src.Countries) > 0 {
			expanded, err := s.countries.CIDRs(ctx, src.Countries)
			switch {
			case err == nil:
				cidrs = append(cidrs, expanded...)
			case errors.Is(err, destlist.ErrGeoIPUnavailable):
				geoipMissing, b.geoipUnavailable = true, true
			default:
				return nil, fmt.Errorf("destpolicy: expand source countries of node %d: %w", src.Node, err)
			}
		}
		cidrs = mergeCIDRStrings(append(cidrs, src.CIDRs...))
		switch {
		case geoipMissing:
			b.sourceNotes[src.Node] = "geoip_unavailable"
		case len(cidrs) == 0:
			b.sourceNotes[src.Node] = "empty"
		}
		if len(cidrs) == 0 {
			continue
		}
		key := strings.Join(src.Countries, ",") + "|" + strings.Join(src.CIDRs, ",")
		g := groups[key]
		if g == nil {
			g = &baseSource{key: key, cidrs: cidrs}
			groups[key] = g
		}
		g.nodes = append(g.nodes, src.Node)
	}
	for _, g := range groups {
		slices.Sort(g.nodes)
		b.sources = append(b.sources, *g)
	}
	slices.SortFunc(b.sources, func(x, y baseSource) int { return int(x.nodes[0] - y.nodes[0]) })
	return b, nil
}

func sortedUnique(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := slices.Clone(in)
	slices.Sort(out)
	return slices.Compact(out)
}

// mergeCIDRStrings merges overlapping and adjacent prefixes into the smallest
// canonical set and returns it in the wire's bytewise string order. A string
// that does not parse as a prefix is dropped: the stores only hold canonical
// ones, so it cannot occur, and the validation before publishing would catch
// it if it did.
func mergeCIDRStrings(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	ps := make([]netip.Prefix, 0, len(in))
	for _, c := range in {
		if p, err := netip.ParsePrefix(c); err == nil {
			ps = append(ps, p)
		}
	}
	merged := destlist.MergePrefixes(ps)
	out := make([]string, 0, len(merged))
	for _, p := range merged {
		out = append(out, p.String())
	}
	slices.Sort(out)
	return out
}
