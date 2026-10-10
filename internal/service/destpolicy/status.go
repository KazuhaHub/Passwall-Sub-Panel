package destpolicy

import (
	"bytes"
	"encoding/json"
	"slices"
	"sync"
	"time"

	"github.com/KazuhaHub/passwall-protocol/protocol"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

type DestinationStatus struct {
	Generation          int64                    `json:"generation"`
	PublishedGeneration int64                    `json:"published_generation"`
	LastWriteAt         *int64                   `json:"last_write_at"`
	NextPublishAt       *int64                   `json:"next_publish_at"`
	ApplyETAMS          int64                    `json:"apply_eta_ms"`
	Paused              bool                     `json:"paused"`
	PublishError        *domain.DestPublishError `json:"publish_error"`
	Totals              map[string]int           `json:"totals"`
	Nodes               []DestinationNodeStatus  `json:"nodes"`
}

type DestinationSupports struct {
	Policy bool `json:"policy"`
	Hits   bool `json:"hits"`
	Usage  bool `json:"usage"`
}
type DestinationStatusGroup struct {
	ID   int64   `json:"id"`
	Name *string `json:"name"`
}
type DestinationNodeStatus struct {
	PanelID              int64                       `json:"panel_id"`
	AgentID              *string                     `json:"agent_id"`
	PanelName            string                      `json:"panel_name"`
	Kind                 domain.PanelKind            `json:"kind"`
	Engine               *string                     `json:"engine"`
	AgentVersion         *string                     `json:"agent_version"`
	Supports             DestinationSupports         `json:"supports"`
	Collect              domain.AuditCollect         `json:"collect"`
	CollectEffective     string                      `json:"collect_effective"`
	Collecting           bool                        `json:"collecting"`
	UsageCollecting      bool                        `json:"-"`
	State                string                      `json:"state"`
	FallbackReason       string                      `json:"fallback_reason"`
	FallbackExhausted    bool                        `json:"fallback_exhausted"`
	MintedKind           domain.DestCandidateKind    `json:"minted_kind"`
	Losses               *domain.DestAuditLosses     `json:"losses"`
	OverLimit            *domain.DestPublishError    `json:"over_limit"`
	SniffingInsufficient []domain.DestStatusListener `json:"sniffing_insufficient"`
	MintedAt             *int64                      `json:"minted_at"`
	PendingSince         *int64                      `json:"pending_since"`
	AppliedAt            *int64                      `json:"applied_at"`
	AppliedRules         int                         `json:"applied_rules"`
	AllowlistGroups      []DestinationStatusGroup    `json:"allowlist_groups"`
	LastReportAt         *int64                      `json:"last_report_at"`
	Hits24h              *int64                      `json:"hits_24h"`
}

func statusMillis(t *time.Time) *int64 {
	if t == nil {
		return nil
	}
	v := t.UnixMilli()
	return &v
}

// PublicationDeadline is shared by the publisher and the read-only ETA.
func PublicationDeadline(state domain.DestPolicyState, delay time.Duration) (*time.Time, error) {
	if state.Generation < 0 || state.PublishedGeneration < 0 || state.PublishedGeneration > state.Generation {
		return nil, domain.ErrUnavailable
	}
	if state.Generation == state.PublishedGeneration {
		return nil, nil
	}
	if state.LastWriteAt == nil || state.FirstUnpublishedAt == nil {
		return nil, domain.ErrUnavailable
	}
	deadline := state.LastWriteAt.Add(delay)
	if maximum := state.FirstUnpublishedAt.Add(5 * delay); maximum.Before(deadline) {
		deadline = maximum
	}
	return &deadline, nil
}

func BuildDestinationStatus(current domain.DestStatusContext, minSeconds, pollSeconds int, now time.Time) (DestinationStatus, error) {
	if minSeconds < 30 || minSeconds > 3600 {
		return DestinationStatus{}, domain.ErrValidation
	}
	if pollSeconds <= 0 {
		pollSeconds = protocol.DefaultNextPollSeconds
	}
	poll := time.Duration(pollSeconds) * time.Second
	deadline, err := PublicationDeadline(current.State, time.Duration(minSeconds)*time.Second)
	if err != nil {
		return DestinationStatus{}, err
	}
	s := current.State
	result := DestinationStatus{Generation: s.Generation, PublishedGeneration: s.PublishedGeneration, LastWriteAt: statusMillis(s.LastWriteAt), NextPublishAt: statusMillis(deadline), ApplyETAMS: poll.Milliseconds(), Paused: s.Paused, PublishError: s.PublishError, Nodes: []DestinationNodeStatus{}, Totals: map[string]int{}}
	for _, state := range []string{"none", "paused", "unsupported_kind", "unsupported_version", "pending", "applied", "rejected", "over_limit", "sniffing", "offline", "collecting", "total"} {
		result.Totals[state] = 0
	}
	if deadline != nil && deadline.After(now) {
		result.ApplyETAMS += deadline.Sub(now).Milliseconds()
	}
	for _, p := range current.Panels {
		n := DestinationNodeStatus{PanelID: p.ID, PanelName: p.Name, Kind: p.Kind, Collect: p.Collect, State: destinationTestNodeState(p.DestTestPanel, s, poll, now), SniffingInsufficient: []domain.DestStatusListener{}, AllowlistGroups: []DestinationStatusGroup{}}
		if p.Kind != domain.PanelKindPSP {
			n.Collect = domain.AuditCollectOff
		}
		if p.Version != "" && p.Kind == domain.PanelKindPSP {
			v := p.Version
			n.AgentVersion = &v
		}
		if a := p.Agent; a != nil {
			id := a.AgentID
			n.AgentID = &id
			if a.ObservedCoreEngine != "" {
				engine := string(a.ObservedCoreEngine)
				n.Engine = &engine
			}
			n.LastReportAt = statusMillis(a.LastSeen)
			n.Supports = DestinationSupports{Policy: slices.Contains(a.ObservedCapabilities, protocol.CapabilityDestinationPolicy), Hits: a.ObservedCoreEngine == domain.NodeCoreXray && slices.Contains(a.ObservedCapabilities, "audit.hits.v1"), Usage: a.ObservedCoreEngine == domain.NodeCoreXray && slices.Contains(a.ObservedCapabilities, "audit.usage.v1") && slices.Contains(a.ObservedCapabilities, "audit.hits.v1")}
			n.CollectEffective = string(EffectiveCollect(p.Collect, a.ObservedCapabilities, string(a.ObservedCoreEngine)))
			if a.ObservedCoreEngine == "" {
				n.CollectEffective = ""
			}
		}
		if r := p.Runtime; r != nil {
			n.FallbackReason, n.FallbackExhausted, n.MintedKind = r.FallbackReason, r.FallbackExhausted, r.MintedKind
			n.OverLimit = r.OverLimit
			n.MintedAt = statusMillis(r.MintedAt)
			n.AppliedAt = statusMillis(r.AppliedAt)
			n.AppliedRules = r.AppliedRuleCount
			if r.MintedAt != nil && (r.MintedSHA256 != r.ReportedSHA256 || r.ReportedState != "applied") {
				n.PendingSince = statusMillis(r.MintedAt)
			}
			for _, id := range r.AppliedGroups {
				g := DestinationStatusGroup{ID: id}
				if name, ok := current.GroupNames[id]; ok {
					g.Name = &name
				}
				n.AllowlistGroups = append(n.AllowlistGroups, g)
			}
			// Empty/paused acknowledgements stop execution while the stored LKG
			// remains available for a later resume. It is not a live rule count.
			if r.MintedAt != nil && r.MintedSHA256 == r.ReportedSHA256 && r.ReportedState == "applied" && (r.MintedKind == domain.DestCandidateEmpty || r.MintedKind == domain.DestCandidatePaused) {
				n.AppliedRules = 0
				n.AppliedAt = statusMillis(r.ReportedAt)
				n.AllowlistGroups = []DestinationStatusGroup{}
			}
			for _, key := range slices.Concat(r.PrecheckListeners, r.ReportedListeners) {
				if slices.ContainsFunc(n.SniffingInsufficient, func(v domain.DestStatusListener) bool { return v.Listener == key }) {
					continue
				}
				v, ok := p.Listeners[key]
				if !ok {
					v = domain.DestStatusListener{Listener: key, Label: key}
				}
				n.SniffingInsufficient = append(n.SniffingInsufficient, v)
			}
		}
		n.Collecting = NeedsCollectionProof(p, poll, now) && p.Facts.Hits && p.Facts.Revision == p.CollectRevision && p.Facts.Collect == n.CollectEffective
		n.UsageCollecting = NeedsCollectionProof(p, poll, now) && n.CollectEffective == "hits_and_usage" && p.Facts.Revision == p.CollectRevision && p.Facts.Collect == n.CollectEffective
		result.Nodes = append(result.Nodes, n)
		result.Totals[n.State]++
		result.Totals["total"]++
		if n.Collecting {
			result.Totals["collecting"]++
		}
	}
	return result, nil
}

func NeedsCollectionProof(p domain.DestStatusPanel, poll time.Duration, now time.Time) bool {
	a, r := p.Agent, p.Runtime
	return p.Kind == domain.PanelKindPSP && a != nil && a.ObservedCoreEngine == domain.NodeCoreXray && a.LastSeen != nil && now.Sub(*a.LastSeen) <= 3*poll && slices.Contains(a.ObservedCapabilities, protocol.CapabilityDestinationPolicy) && EffectiveCollect(p.Collect, a.ObservedCapabilities, string(a.ObservedCoreEngine)) != "" && r != nil && r.MintedAt != nil && r.MintedSHA256 != "" && r.MintedSHA256 == r.ReportedSHA256 && r.ReportedState == "applied" && !r.FallbackExhausted
}

// The bounded cache retains only digest-verified facts, never policy bytes.
// Each caller still verifies current metadata, capabilities and revision.
type CollectionFactsCache struct {
	mu    sync.Mutex
	facts map[string]domain.DestCollectionFacts
	order []string
}

func (c *CollectionFactsCache) Read(agentID, digest string, load func() ([]byte, error)) (domain.DestCollectionFacts, error) {
	key := agentID + ":" + digest
	c.mu.Lock()
	fact, ok := c.facts[key]
	c.mu.Unlock()
	if ok {
		return fact, nil
	}
	body, err := load()
	if err != nil {
		return domain.DestCollectionFacts{}, err
	}
	var policy *protocol.DestinationPolicy
	if json.Unmarshal(body, &policy) != nil || policy == nil || protocol.ValidateDestinationPolicy(policy) != nil || protocol.PolicyDigest(policy) != digest {
		return domain.DestCollectionFacts{}, domain.ErrUnavailable
	}
	canonical, err := json.Marshal(policy)
	if err != nil || !bytes.Equal(canonical, body) {
		return domain.DestCollectionFacts{}, domain.ErrUnavailable
	}
	fact = domain.DestCollectionFacts{Collect: string(policy.Collect), Revision: policy.CollectRevision}
	for _, rule := range policy.Rules {
		if rule.Action == protocol.RuleBlock || rule.Action == protocol.RuleObserve {
			fact.Hits = true
		}
		if trialFallback(rule) {
			fact.Trial = true
		}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.facts == nil {
		c.facts = map[string]domain.DestCollectionFacts{}
	}
	if _, exists := c.facts[key]; !exists {
		if len(c.order) >= 256 {
			delete(c.facts, c.order[0])
			c.order = c.order[1:]
		}
		c.order = append(c.order, key)
		c.facts[key] = fact
	}
	return fact, nil
}
