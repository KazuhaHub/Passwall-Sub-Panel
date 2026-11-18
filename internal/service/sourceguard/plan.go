package sourceguard

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"

	nodeprotocol "github.com/KazuhaHub/passwall-protocol/protocol"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/log"
)

// AgentLister is the slice of the agent store the planner reads: the entry
// servers' addresses.
type AgentLister interface {
	ListByPanelIDs(ctx context.Context, panelIDs []int64) ([]*domain.NodeAgent, error)
}

// Key is the wire key of a line's source set: stable per (landing, line).
func Key(nodeID int64, lineID string) nodeprotocol.SourceSetKey {
	return nodeprotocol.SourceSetKey("src_" + domain.SourceSetSuffix(nodeID, lineID))
}

// NodeIDOf reads the landing back out of a set key, 0 for a key that is not one
// of ours.
func NodeIDOf(key string) int64 {
	suffix, ok := strings.CutPrefix(key, "src_")
	if !ok {
		return 0
	}
	idText, _, ok := strings.Cut(suffix, "_")
	if !ok {
		return 0
	}
	id, err := strconv.ParseInt(idText, 10, 64)
	if err != nil || id <= 0 {
		return 0
	}
	return id
}

// Plan is what a capable landing is sent: the sets for its config, and for its
// roster the guard each client gets.
type Plan struct {
	// Sets go in the transit body, sorted by key.
	Sets []nodeprotocol.SourceSet
	// Allow is the set a relay line's identity is accepted from.
	Allow map[domain.LineKey]nodeprotocol.SourceSetKey
	// Deny is, per landing, the sets a direct identity is refused from.
	Deny map[int64][]nodeprotocol.SourceSetKey
}

// Empty reports whether nothing is guarded, in which case nothing is sent and
// the config and roster are what they were before source guards existed.
func (p Plan) Empty() bool { return len(p.Sets) == 0 }

// Guard returns the guard of one client row, nil for none. A relay line's
// client is accepted only from its line's set; a direct client is refused from
// every relay line's set on its landing. A row that is not the one identity of
// one line on one listener (a legacy row attached to several nodes, a tunnel
// line, a line whose set is unknown) gets none.
func (p Plan) Guard(client *domain.PSPClient, listeners int) *nodeprotocol.ClientSourceGuard {
	if p.Empty() || client == nil || listeners != 1 || client.NodeID == 0 || client.LineID == "" {
		return nil
	}
	line := domain.LineKey{NodeID: client.NodeID, LineID: client.LineID}
	if line.IsDirect() {
		if deny := p.Deny[client.NodeID]; len(deny) > 0 {
			return &nodeprotocol.ClientSourceGuard{DenyFrom: slices.Clone(deny)}
		}
		return nil
	}
	if key, ok := p.Allow[line]; ok {
		return &nodeprotocol.ClientSourceGuard{AllowFrom: key}
	}
	return nil
}

// Compute works out the sets of the given landings. Only a landing whose
// enabled, guarded lines have a known set contributes; everything else is left
// out so that a config without guards stays what it was.
func Compute(nodes []*domain.Node, in domain.SourceInputs) Plan {
	plan := Plan{Allow: map[domain.LineKey]nodeprotocol.SourceSetKey{}, Deny: map[int64][]nodeprotocol.SourceSetKey{}}
	for _, node := range nodes {
		if node == nil || node.IsTunnelChild() || node.IsSeparator() {
			continue
		}
		var keys []nodeprotocol.SourceSetKey
		for _, line := range node.Relays {
			if !line.Guarded() || !line.Enabled || line.ID == "" {
				continue
			}
			cidrs := line.EffectiveSourceCIDRs(in)
			if len(cidrs) == 0 {
				continue
			}
			if len(keys) >= nodeprotocol.MaxSourceGuardDenyFrom {
				// A direct client's deny list holds this many sets; one more
				// would not fit, and a guard that names only some of them is the
				// same as leaving the rest unguarded.
				log.Warn("source guard: more relay lines than a direct client can deny; the rest are not guarded",
					"node_id", node.ID, "limit", nodeprotocol.MaxSourceGuardDenyFrom)
				break
			}
			key := Key(node.ID, line.ID)
			keys = append(keys, key)
			plan.Allow[domain.LineKey{NodeID: node.ID, LineID: line.ID}] = key
			plan.Sets = append(plan.Sets, nodeprotocol.SourceSet{Key: key, CIDRs: cidrs})
		}
		if len(keys) > 0 {
			sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
			plan.Deny[node.ID] = keys
		}
	}
	sort.Slice(plan.Sets, func(i, j int) bool { return plan.Sets[i].Key < plan.Sets[j].Key })
	return plan
}

// Planner decides what a landing server is sent.
type Planner struct {
	agents AgentLister
	cf     *Cloudflare
}

// NewPlanner returns the planner. Either part may be nil: no agents means no
// managed entry's addresses are known, no Cloudflare keeper means the built-in
// snapshot.
func NewPlanner(agents AgentLister, cf *Cloudflare) *Planner {
	return &Planner{agents: agents, cf: cf}
}

// Inputs gathers the entry addresses of the managed lines of the nodes and the
// current Cloudflare ranges.
func (p *Planner) Inputs(ctx context.Context, nodes []*domain.Node) (domain.SourceInputs, error) {
	in := domain.SourceInputs{Cloudflare: slices.Clone(BuiltinCloudflare)}
	if p != nil && p.cf != nil {
		in.Cloudflare = p.cf.Current(ctx).CIDRs
	}
	if p == nil || p.agents == nil {
		return in, nil
	}
	seen := map[int64]bool{}
	var panels []int64
	for _, node := range nodes {
		if node == nil {
			continue
		}
		for _, line := range node.Relays {
			if line.Guarded() && line.Managed() && !seen[line.EntryPanelID] {
				seen[line.EntryPanelID] = true
				panels = append(panels, line.EntryPanelID)
			}
		}
	}
	if len(panels) == 0 {
		return in, nil
	}
	agents, err := p.agents.ListByPanelIDs(ctx, panels)
	if err != nil {
		return in, fmt.Errorf("source guard: entry servers: %w", err)
	}
	in.EntryAddresses = map[int64][]string{}
	for _, agent := range agents {
		if agent != nil {
			in.EntryAddresses[agent.PanelID] = agent.SourceAddresses()
		}
	}
	return in, nil
}

// PlanFor is what a landing server's agent is sent for these nodes: nothing
// unless it declared the capability (an agent that did not would ignore the
// guard and accept every connection while the config reads as applied).
func (p *Planner) PlanFor(ctx context.Context, capabilities []string, nodes []*domain.Node) (Plan, error) {
	if !slices.Contains(capabilities, nodeprotocol.CapabilitySourceGuard) {
		return Plan{}, nil
	}
	in, err := p.Inputs(ctx, nodes)
	if err != nil {
		return Plan{}, err
	}
	return Compute(nodes, in), nil
}

// Withheld is a relay line the source guard keeps out of subscriptions.
type Withheld struct {
	Node   *domain.Node
	Line   domain.RelayLine
	Reason string
}

// WithheldLines lists the relay lines of the nodes a landing server must not
// publish (domain.Node.SourceGuardWithheld), given what its agent declared.
func (p *Planner) WithheldLines(ctx context.Context, capabilities []string, nodes []*domain.Node) ([]Withheld, error) {
	canGuard := slices.Contains(capabilities, nodeprotocol.CapabilitySourceGuard)
	// A line the guard cannot touch costs no lookup: nothing to read unless a line
	// differs from the direct one or is a managed forward.
	needed := false
	for _, node := range nodes {
		if node == nil || node.IsTunnelChild() {
			continue
		}
		for _, line := range node.Relays {
			needed = needed || (line.Guarded() && (line.Managed() || line.EffectiveTrafficFactor() != node.EffectiveTrafficFactor()))
		}
	}
	if !needed {
		return nil, nil
	}
	in, err := p.Inputs(ctx, nodes)
	if err != nil {
		return nil, err
	}
	var out []Withheld
	for _, node := range nodes {
		if node == nil || node.IsTunnelChild() {
			continue
		}
		for _, line := range node.Relays {
			entryKnown := line.Managed() && len(in.EntryAddresses[line.EntryPanelID]) > 0
			if reason := node.SourceGuardWithheld(line, canGuard, entryKnown); reason != "" {
				out = append(out, Withheld{Node: node, Line: line, Reason: reason})
			}
		}
	}
	return out, nil
}
