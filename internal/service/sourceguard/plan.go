package sourceguard

import (
	"context"
	"fmt"
	"slices"
	"sort"

	nodeprotocol "github.com/KazuhaHub/passwall-protocol/protocol"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/log"
)

// AgentLister is the slice of the agent store the planner reads: the entry
// servers' addresses and what each landing server's agent can do.
type AgentLister interface {
	ListByPanelIDs(ctx context.Context, panelIDs []int64) ([]*domain.NodeAgent, error)
}

// Key is the wire key of the source set of a landing listener's factor class:
// stable per (landing, factor).
func Key(nodeID int64, factorMilli int) nodeprotocol.SourceSetKey {
	return domain.SourceSetKeyOf(nodeID, factorMilli)
}

// NodeIDOf reads the landing back out of a set key, 0 for a key that is not one
// of ours.
func NodeIDOf(key string) int64 {
	id, _, _ := domain.ParseSourceSetKey(key)
	return id
}

// FactorOf reads the class's factor back out of a set key; ok is false for a
// key that is not one of ours.
func FactorOf(key string) (int, bool) {
	_, factor, ok := domain.ParseSourceSetKey(key)
	return factor, ok
}

// Plan is what a landing is sent: the sets for its config, and for its roster
// the guard each client gets.
type Plan struct {
	// Sets go in the transit body, sorted by key.
	Sets []nodeprotocol.SourceSet
	// Allow is, per line of a priced class, the set its identity is accepted
	// from. The key is there even when the set is not sent (its addresses are
	// unknown, or the class was withheld): the landing then refuses the client
	// entirely, which is the fail-closed answer.
	Allow map[domain.LineKey]nodeprotocol.SourceSetKey
	// Deny is, per landing, the sets a direct-class identity is refused from:
	// the priced classes whose set is sent.
	Deny map[int64][]nodeprotocol.SourceSetKey
	// Withheld are the lines the guard keeps out of subscriptions.
	Withheld []Withheld
	// Analyses is what each landing's lines came to, by node.
	Analyses map[int64]domain.SourceGuardAnalysis
}

// Empty reports whether nothing is guarded, in which case nothing is sent and
// the config and roster are what they were before source guards existed.
func (p Plan) Empty() bool { return len(p.Sets) == 0 && len(p.Allow) == 0 && len(p.Deny) == 0 }

// Guard returns the guard of one client row and whether it can be sent. nodes
// are the landings the row is attached to.
//
//   - A client of a priced class is accepted only from its class's set: allow_from
//     always names the class, whether or not the set is sent.
//   - A client of the direct class (the direct line, or a relay line that costs
//     the same) is refused from every sent priced class of every landing it is
//     attached to (a legacy row can be attached to several). ok is false when
//     those are more than a client's deny list holds: such a client must not be
//     sent unguarded.
//   - A row of a tunnel line, or one the plan does not know, has none.
func (p Plan) Guard(client *domain.PSPClient, nodes []int64) (guard *nodeprotocol.ClientSourceGuard, ok bool) {
	if p.Empty() || client == nil {
		return nil, true
	}
	if line, has := client.Line(); has && !line.IsDirect() {
		if key, priced := p.Allow[line]; priced {
			return &nodeprotocol.ClientSourceGuard{AllowFrom: key}, true
		}
	}
	var deny []nodeprotocol.SourceSetKey
	for _, id := range nodes {
		for _, key := range p.Deny[id] {
			if !slices.Contains(deny, key) {
				deny = append(deny, key)
			}
		}
	}
	if len(deny) == 0 {
		return nil, true
	}
	if len(deny) > nodeprotocol.MaxSourceGuardDenyFrom {
		return nil, false
	}
	sort.Slice(deny, func(i, j int) bool { return deny[i] < deny[j] })
	return &nodeprotocol.ClientSourceGuard{DenyFrom: deny}, true
}

// Compute works out the sets, the guards and the withheld lines of the given
// landings. A landing that cannot guard contributes only the lines it withholds.
func Compute(nodes []*domain.Node, in domain.SourceInputs) Plan {
	plan := Plan{
		Allow: map[domain.LineKey]nodeprotocol.SourceSetKey{}, Deny: map[int64][]nodeprotocol.SourceSetKey{},
		Analyses: map[int64]domain.SourceGuardAnalysis{},
	}
	for _, node := range nodes {
		if node == nil || node.IsTunnelChild() || node.IsSeparator() {
			continue
		}
		analysis := node.AnalyzeSourceGuard(in)
		plan.Analyses[node.ID] = analysis
		for _, line := range node.Relays {
			if reason := analysis.Withheld[line.ID]; reason != "" {
				plan.Withheld = append(plan.Withheld, Withheld{Node: node, Line: line, Reason: reason})
			}
		}
		if !analysis.Guards {
			continue
		}
		direct := node.EffectiveTrafficFactor()
		for _, line := range node.Relays {
			if line.Guarded() && line.ID != "" && line.EffectiveTrafficFactor() != direct {
				plan.Allow[domain.LineKey{NodeID: node.ID, LineID: line.ID}] = domain.SourceSetKeyOf(node.ID, line.EffectiveTrafficFactor())
			}
		}
		var keys []nodeprotocol.SourceSetKey
		for _, class := range analysis.Priced {
			if !class.Emitted() {
				continue
			}
			if len(plan.Sets) >= nodeprotocol.MaxSourceSets {
				log.Warn("source guard: more source sets than a config holds; the class is withheld",
					"node_id", node.ID, "limit", nodeprotocol.MaxSourceSets)
				for _, line := range node.Relays {
					if slices.Contains(class.LineIDs, line.ID) {
						plan.Withheld = append(plan.Withheld, Withheld{Node: node, Line: line, Reason: domain.SourceWithheldLimit})
					}
				}
				continue
			}
			key := nodeprotocol.SourceSetKey(class.Key)
			keys = append(keys, key)
			plan.Sets = append(plan.Sets, nodeprotocol.SourceSet{Key: key, CIDRs: class.CIDRs})
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
// managed entry's addresses are known and no landing can guard, no Cloudflare
// keeper means the built-in snapshot.
func NewPlanner(agents AgentLister, cf *Cloudflare) *Planner {
	return &Planner{agents: agents, cf: cf}
}

// Inputs gathers what the guard is worked out from: the entry addresses of the
// managed lines of the nodes, what each landing server's agent can do and the current Cloudflare ranges.
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
		if node == nil || node.IsSeparator() {
			continue
		}
		if !seen[node.PanelID] {
			seen[node.PanelID] = true
			panels = append(panels, node.PanelID)
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
		return in, fmt.Errorf("source guard: servers: %w", err)
	}
	in.EntryAddresses = map[int64][]string{}
	in.Servers = map[int64]domain.SourceGuardServer{}
	for _, agent := range agents {
		if agent == nil {
			continue
		}
		in.EntryAddresses[agent.PanelID] = agent.SourceAddresses()
		in.Servers[agent.PanelID] = domain.SourceGuardServer{Capable: agent.CanSourceGuard(), Engines: agent.GuardEngines()}
	}
	return in, nil
}

// AgentFacts is what a landing server's agent has just declared, which is read
// in preference to what was stored: an agent that did not declare the
// capability would ignore a guard and accept every connection while the config
// reads as applied.
type AgentFacts struct {
	PanelID      int64
	Capabilities []string
	// Engines are the cores the agent runs and is asked to run.
	Engines []domain.NodeCoreEngine
}

// PlanFor is what a landing server's agent is sent for these nodes.
func (p *Planner) PlanFor(ctx context.Context, agent AgentFacts, nodes []*domain.Node) (Plan, error) {
	in, err := p.Inputs(ctx, nodes)
	if err != nil {
		return Plan{}, err
	}
	if in.Servers == nil {
		in.Servers = map[int64]domain.SourceGuardServer{}
	}
	in.Servers[agent.PanelID] = domain.SourceGuardServer{
		Capable: slices.Contains(agent.Capabilities, nodeprotocol.CapabilitySourceGuard), Engines: agent.Engines,
	}
	return Compute(nodes, in), nil
}

// Withheld is a relay line the source guard keeps out of subscriptions.
type Withheld struct {
	Node   *domain.Node
	Line   domain.RelayLine
	Reason string
}
