package destpolicy

import (
	"slices"

	"github.com/KazuhaHub/passwall-protocol/protocol"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// EffectiveCollect is the recording level a node is told to run: the LOWER of
// what the panel's admin chose and what the node can do.
//
//   - off records nothing;
//   - hits needs the node to declare audit.hits.v1;
//   - hits_and_usage needs audit.usage.v1 as well, and falls back to hits (and
//     to nothing when hits is missing too: usage presupposes hits).
//
// Telling a node to record at a level it cannot would make it write every
// connection to its log for nothing: the leak the access-log hot fix closed.
func EffectiveCollect(panel domain.AuditCollect, caps []string) protocol.CollectLevel {
	hasHits := slices.Contains(caps, protocol.CapabilityAuditHits)
	hasUsage := slices.Contains(caps, protocol.CapabilityAuditUsage)
	switch domain.NormalizeAuditCollect(panel) {
	case domain.AuditCollectHits:
		if hasHits {
			return protocol.CollectHits
		}
	case domain.AuditCollectHitsAndUsage:
		switch {
		case hasHits && hasUsage:
			return protocol.CollectHitsAndUsage
		case hasHits:
			return protocol.CollectHits
		}
	}
	return protocol.CollectOff
}

// recordsHits reports whether any rule of p records a hit: a block, or a
// report-only observe. An allow records nothing, so a policy of allows alone
// has nothing to collect.
func recordsHits(p *protocol.DestinationPolicy) bool {
	if p == nil {
		return false
	}
	for _, r := range p.Rules {
		if r.Action == protocol.RuleBlock || r.Action == protocol.RuleObserve {
			return true
		}
	}
	return false
}

// withCollect sets the policy's collection level where it is needed and leaves
// it empty where it is not: emitting it for a policy that records nothing, or
// for a panel that has recording off, would change the digest and restart the
// core for no benefit.
//
// TIER B IS THE EXCEPTION. Per-site usage counts every connection that no rule
// blocks or reports, so the level is kept whatever the rules are: a policy of
// allows alone, or of none, still asks the node to collect.
func withCollect(p *protocol.DestinationPolicy, level protocol.CollectLevel) {
	if p == nil {
		return
	}
	p.Collect = protocol.CollectOff
	switch {
	case level == protocol.CollectHitsAndUsage:
		p.Collect = level
	case level != protocol.CollectOff && recordsHits(p):
		p.Collect = level
	}
}

// collectOnlyPolicy is what a tier-B node is sent when there is nothing to
// enforce (nothing in force, a pause, every rule left out of the node): no
// rules, and the request to collect. It is a policy the node runs, but never
// one the panel records as that node's last known good (see mintedPolicy): it
// says nothing about what the node enforces.
func collectOnlyPolicy() *protocol.DestinationPolicy {
	return &protocol.DestinationPolicy{Rules: []protocol.DestinationRule{}, Collect: protocol.CollectHitsAndUsage}
}

// collectOnlyDigest is the digest of collectOnlyPolicy.
var collectOnlyDigest = protocol.PolicyDigest(collectOnlyPolicy())

// lowerCollect is the lower of two recording levels (off < hits <
// hits_and_usage).
func lowerCollect(a, b protocol.CollectLevel) protocol.CollectLevel {
	rank := func(l protocol.CollectLevel) int {
		switch l {
		case protocol.CollectHits:
			return 1
		case protocol.CollectHitsAndUsage:
			return 2
		}
		return 0
	}
	if rank(a) <= rank(b) {
		return a
	}
	return b
}

// Collecting is whether a node is, as far as the panel can tell, recording hits
// right now (R17). It is derived and not reported, because a node with nothing
// to say sends no audit at all and silence cannot distinguish "recording and
// quiet" from "not recording". It is true only when ALL of these hold:
//
//   - the node runs Xray (sing-box nodes enforce but do not record);
//   - it declares audit.hits.v1;
//   - the policy the panel minted for it carries a non-empty level;
//   - it reported that very policy applied (not an older one, not a refusal).
func Collecting(m *domain.DestAgentPolicy, caps []string, engine domain.NodeCoreEngine) bool {
	if m == nil || domain.NormalizeNodeCoreEngine(engine) != domain.NodeCoreXray {
		return false
	}
	return slices.Contains(caps, protocol.CapabilityAuditHits) &&
		m.CollectEffective != "" &&
		m.MintedSHA256 != "" &&
		m.ReportedState == protocol.PolicyStateApplied &&
		m.ReportedSHA256 == m.MintedSHA256
}

// CollectingUsage is whether a node is, as far as the panel can tell, recording
// per-site usage right now: it is Collecting at the tier-B level. Like Collecting
// it is derived, never reported.
func CollectingUsage(m *domain.DestAgentPolicy, caps []string, engine domain.NodeCoreEngine) bool {
	return Collecting(m, caps, engine) && m.CollectEffective == string(protocol.CollectHitsAndUsage) &&
		slices.Contains(caps, protocol.CapabilityAuditUsage)
}

// CollectingForRisk is the steadier question the dest_block risk signal asks:
// does this node record, as far as the panel can tell, even while a newly
// minted policy has not been applied yet. Collecting is false for every node
// after every publish until each reports the new digest, which would flap the
// signal to "unknown" fleet-wide on each publish. Here a node counts when
//
//   - it runs Xray and declares audit.hits.v1,
//   - the panel still wants recording (the policy minted for it carries a level),
//   - it reported a policy applied, and that policy is either the minted one or
//     the last one it applied (AppliedSHA256), which was itself recorded with
//     recording on (AppliedCollect).
//
// Switching recording off, a node that never recorded, a refusal and a node
// running something the panel did not mint all still read false.
func CollectingForRisk(m *domain.DestAgentPolicy, caps []string, engine domain.NodeCoreEngine) bool {
	if m == nil || domain.NormalizeNodeCoreEngine(engine) != domain.NodeCoreXray {
		return false
	}
	if !slices.Contains(caps, protocol.CapabilityAuditHits) || m.CollectEffective == "" ||
		m.MintedSHA256 == "" || m.ReportedState != protocol.PolicyStateApplied || m.ReportedSHA256 == "" {
		return false
	}
	if m.ReportedSHA256 == m.MintedSHA256 {
		return true
	}
	return m.ReportedSHA256 == m.AppliedSHA256 && m.AppliedCollect != ""
}
