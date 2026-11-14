package destpolicy

import (
	"context"
	"encoding/json"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/KazuhaHub/passwall-protocol/protocol"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/log"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

func zeroSettings() ports.UISettings { return ports.UISettings{} }

// describeRules names the rules of the published snapshot, so a test trace can
// say "blocked by BitTorrent" instead of "p12x2".
func describeRules(cb *cachedBase) map[string]TestStep {
	names := map[int64]string{}
	for _, p := range cb.doc.Policy {
		names[p.ID] = p.Name
	}
	out := map[string]TestStep{}
	for _, group := range [][]baseRule{cb.base.allow, cb.base.block, cb.base.watch} {
		for _, r := range group {
			kind := "block"
			if r.action == protocol.RuleAllow {
				kind = "allow"
			}
			// A report-only rule is named like an enforced one; report_only is
			// what says a hit on it does not block.
			out[r.id] = TestStep{Kind: kind, ReportOnly: r.report, PolicyID: r.policyID, Name: names[r.policyID]}
		}
	}
	for _, g := range cb.base.sources {
		for _, n := range g.nodes {
			out["s"+strconv.FormatInt(n, 10)] = TestStep{Kind: "source", ReportOnly: g.report, NodeID: n}
		}
	}
	return out
}

// NodeState is one node's delivery state, as the status page shows it.
type NodeState struct {
	PanelID      int64  `json:"panel_id"`
	AgentID      string `json:"agent_id"`
	PanelName    string `json:"panel_name"`
	Kind         string `json:"kind"`
	Engine       string `json:"engine"`
	AgentVersion string `json:"agent_version"`
	Supports     struct {
		Policy bool `json:"policy"`
		Hits   bool `json:"hits"`
		Usage  bool `json:"usage"`
	} `json:"supports"`
	// Collect is the panel's recording choice (off, hits, hits_and_usage),
	// CollectEffective what the node was told, and Collecting whether it is
	// recording now (derived, never reported). Empty/false for a node that is not
	// a Passwall Node.
	Collect          string `json:"collect"`
	CollectEffective string `json:"collect_effective"`
	Collecting       bool   `json:"collecting"`
	// State is none, paused, unsupported_kind, unsupported_version, pending,
	// applied, rejected, over_limit, sniffing or offline.
	State          string                   `json:"state"`
	FallbackReason string                   `json:"fallback_reason"`
	OverLimit      *domain.DestPublishError `json:"over_limit"`
	Sniffing       []SniffView              `json:"sniffing_insufficient"`
	MintedAt       *int64                   `json:"minted_at"`
	PendingSince   *int64                   `json:"pending_since"`
	AppliedAt      *int64                   `json:"applied_at"`
	AppliedRules   int                      `json:"applied_rules"`
	LastReportAt   *int64                   `json:"last_report_at"`
	// RulesDataVersion is the geosite release behind the policy the node
	// reports it runs (null when unknown: a policy minted before this process
	// started, or none).
	RulesDataVersion *string `json:"rules_data_version"`
	// GeoIPUnavailable: a source rule of one of this node's inbounds could
	// not be expanded for want of a GeoIP database and is not enforced.
	GeoIPUnavailable bool    `json:"geoip_unavailable"`
	AllowlistGroups  []int64 `json:"allowlist_groups"`
	Hits24h          *int64  `json:"hits_24h"`
	// ReportOnlyNotDeployed says why this node was sent the policy without its
	// report-only rules: "sniffing" (an inbound's sniffing cannot supply what
	// they need) or "limit" (they overrun the node's budget). Null when all
	// deployed. Enforced rules are unaffected either way. It is the status-page
	// face of the node issue destination_policy_report_only_not_deployed.
	ReportOnlyNotDeployed *string `json:"report_only_not_deployed"`
}

// SniffView names an inbound whose sniffing cannot supply the policy's needs.
type SniffView struct {
	Listener string `json:"listener"`
	Label    string `json:"label"`
	NodeID   int64  `json:"node_id"`
	Reason   string `json:"reason,omitempty"`
}

// StatusView is the whole status.
type StatusView struct {
	Generation          int64                    `json:"generation"`
	PublishedGeneration int64                    `json:"published_generation"`
	LastWriteAt         *int64                   `json:"last_write_at"`
	NextPublishAt       *int64                   `json:"next_publish_at"`
	ApplyETAMS          int64                    `json:"apply_eta_ms"`
	Paused              bool                     `json:"paused"`
	PublishError        *domain.DestPublishError `json:"publish_error"`
	PublishErrorAt      *int64                   `json:"publish_error_at"`
	// GeositeRelease is the rule-data version in force; GeoIPUnavailable that
	// a country in force cannot be expanded.
	GeositeRelease   string         `json:"geosite_release"`
	GeoIPUnavailable bool           `json:"geoip_unavailable"`
	Totals           map[string]int `json:"totals"`
	Nodes            []NodeState    `json:"nodes"`
	Policies         map[string]any `json:"policy_notes"`
}

func ms(t *time.Time) *int64 {
	if t == nil {
		return nil
	}
	v := t.UnixMilli()
	return &v
}

// Status reads the publish state and every node's delivery state.
func (a *Admin) Status(ctx context.Context) (*StatusView, error) {
	st, err := a.Service.state.Get(ctx)
	if err != nil {
		return nil, err
	}
	now := a.Now()
	eff := a.Service.effective(ctx)
	poll := 0
	if a.Settings != nil {
		if set, err := a.Settings.Load(ctx, zeroSettings()); err == nil {
			poll = set.NodePollSeconds
		}
	}
	if poll <= 0 {
		poll = protocol.DefaultNextPollSeconds
	}
	cycle := time.Duration(poll) * time.Second

	v := &StatusView{
		Generation: st.Generation, PublishedGeneration: st.PublishedGeneration, LastWriteAt: ms(st.LastWriteAt),
		Paused: st.Paused, PublishError: st.PublishError, PublishErrorAt: ms(st.PublishErrorAt),
		Totals: map[string]int{}, Nodes: []NodeState{}, Policies: map[string]any{},
	}
	// next_publish_at: when the debounce (or its cap) lets the pending change
	// out. apply_eta_ms adds one sync cycle: the node still has to fetch it.
	if st.Unpublished() {
		window := eff.PolicyApplyMin()
		next := now
		if st.LastWriteAt != nil {
			next = st.LastWriteAt.Add(window)
		}
		if st.FirstUnpublishedAt != nil {
			if capAt := st.FirstUnpublishedAt.Add(maxWaitFactor * window); capAt.Before(next) {
				next = capAt
			}
		}
		if next.Before(now) {
			next = now
		}
		v.NextPublishAt = ms(&next)
		v.ApplyETAMS = int64(next.Sub(now)/time.Millisecond) + cycle.Milliseconds()
	}
	info, err := a.Service.Info(ctx)
	if err != nil {
		return nil, err
	}
	v.GeositeRelease, v.GeoIPUnavailable = info.Geosite, info.GeoIPUnavailable
	for id, n := range info.Policies {
		v.Policies[strconv.FormatInt(id, 10)] = n
	}

	panels, err := a.Panels.List(ctx)
	if err != nil {
		return nil, err
	}
	agentByPanel := map[int64]*domain.NodeAgent{}
	if a.Agents != nil {
		agents, err := a.Agents.List(ctx)
		if err != nil {
			return nil, err
		}
		for _, ag := range agents {
			agentByPanel[ag.PanelID] = ag
		}
	}
	var panelHits map[int64]int64
	if a.Hits != nil {
		since := now.UTC().Add(-24 * time.Hour).Truncate(time.Hour).UnixMilli()
		// A count that cannot be read costs hits_24h (null), never the status.
		if panelHits, err = a.Hits.PanelCounts(ctx, since); err != nil {
			log.Warn("destination status: 24-hour hit count unavailable", "err", err)
			panelHits = nil
		}
	}
	rows := map[string]*domain.DestAgentPolicy{}
	rowList, err := a.Service.agents.List(ctx)
	if err != nil {
		return nil, err
	}
	for _, r := range rowList {
		rows[r.AgentID] = r
	}

	for _, panel := range panels {
		ns := NodeState{
			PanelID: panel.ID, PanelName: panel.Name, Kind: string(domain.NormalizePanelKind(panel.Kind)),
			Sniffing: []SniffView{}, AllowlistGroups: []int64{},
		}
		agent := agentByPanel[panel.ID]
		switch {
		case domain.NormalizePanelKind(panel.Kind) != domain.PanelKindPSP:
			ns.State = "unsupported_kind"
			v.Nodes = append(v.Nodes, ns)
			v.Totals[ns.State]++
			continue
		case agent == nil:
			ns.State = "unsupported_version"
			v.Nodes = append(v.Nodes, ns)
			v.Totals[ns.State]++
			continue
		}
		ns.AgentID, ns.Engine, ns.AgentVersion = agent.AgentID, string(agent.ObservedCoreEngine), panel.PanelVersion
		ns.Supports.Policy = policyHasCapability(agent.ObservedCapabilities)
		ns.Supports.Hits = slices.Contains(agent.ObservedCapabilities, capabilityAuditHits)
		ns.Supports.Usage = slices.Contains(agent.ObservedCapabilities, capabilityAuditUsage)
		ns.LastReportAt = ms(agent.LastSeen)
		row := rows[agent.AgentID]
		if row == nil {
			row = &domain.DestAgentPolicy{AgentID: agent.AgentID}
		}
		// Recording: what the panel is set to (collect), what the node was told
		// (collect_effective), whether it is recording now (collecting, derived,
		// never reported: a quiet node sends nothing) and its hits in 24 hours,
		// null where the node cannot record.
		ns.Collect = string(panel.AuditCollect.Normalized())
		ns.CollectEffective = row.CollectEffective
		ns.Collecting = Collecting(row, agent.ObservedCapabilities, agent.ObservedCoreEngine)
		if panelHits != nil && ns.Supports.Hits {
			n := panelHits[panel.ID]
			ns.Hits24h = &n
		}
		if row.ReportOnlyShed != "" {
			shed := row.ReportOnlyShed
			ns.ReportOnlyNotDeployed = &shed
		}
		ns.FallbackReason = string(row.FallbackReason)
		ns.MintedAt, ns.AppliedAt, ns.AppliedRules = ms(row.MintedAt), ms(row.AppliedAt), row.AppliedRuleCount
		if row.OverLimit != "" {
			var o domain.DestPublishError
			if json.Unmarshal([]byte(row.OverLimit), &o) == nil {
				ns.OverLimit = &o
			}
		}
		sniffRaw := row.PrecheckListeners
		if sniffRaw == "" && row.FallbackReason == domain.DestFallbackRejected && row.ReportedIssue == IssueSniffInsufficient {
			// The node itself reported the listeners (a plain list of keys).
			var keys []string
			if json.Unmarshal([]byte(row.ReportedListeners), &keys) == nil {
				var f []sniffFinding
				for _, k := range keys {
					id, _ := protocol.ListenerKey(k).RowID()
					f = append(f, sniffFinding{Listener: k, NodeID: id, Reason: "reported by the node"})
				}
				b, _ := json.Marshal(f)
				sniffRaw = string(b)
			}
		}
		if sniffRaw != "" {
			var f []sniffFinding
			if json.Unmarshal([]byte(sniffRaw), &f) == nil {
				for _, x := range f {
					ns.Sniffing = append(ns.Sniffing, SniffView{Listener: x.Listener, Label: x.Label, NodeID: x.NodeID, Reason: x.Reason})
				}
			}
		}
		if row.ReportedState == protocol.PolicyStateApplied && row.ReportedSHA256 != "" {
			a.Service.mu.Lock()
			if info, ok := a.Service.digests[row.ReportedSHA256]; ok {
				rel := info.geosite
				ns.RulesDataVersion = &rel
			}
			a.Service.mu.Unlock()
		}
		for nodeID, note := range info.SourceNotes {
			if note == "geoip_unavailable" && a.nodeOnPanel(ctx, nodeID, panel.ID) {
				ns.GeoIPUnavailable = true
			}
		}

		offline := agent.OfflineAt(now, 3*cycle)
		unchanged := row.MintedSHA256 == row.ReportedSHA256
		switch {
		case !ns.Supports.Policy:
			ns.State = "unsupported_version"
		case st.Paused:
			ns.State = "paused"
		case offline:
			ns.State = "offline"
		case row.FallbackReason == domain.DestFallbackOverLimit:
			ns.State = "over_limit"
		case row.FallbackReason == domain.DestFallbackSniffing ||
			(row.FallbackReason == domain.DestFallbackRejected && row.ReportedIssue == IssueSniffInsufficient):
			ns.State = "sniffing"
		case row.FallbackReason == domain.DestFallbackRejected:
			ns.State = "rejected"
		case row.MintedSHA256 == "" && row.ReportedSHA256 == "":
			ns.State = "none"
		case !unchanged || row.ReportedState != protocol.PolicyStateApplied:
			ns.State = "pending"
			if row.MintedAt != nil {
				ns.PendingSince = ms(row.MintedAt)
			}
		default:
			ns.State = "applied"
		}
		v.Nodes = append(v.Nodes, ns)
		v.Totals[ns.State]++
	}
	slices.SortFunc(v.Nodes, func(x, y NodeState) int { return strings.Compare(x.PanelName, y.PanelName) })
	return v, nil
}

// nodeOnPanel reports whether inbound nodeID belongs to the panel.
func (a *Admin) nodeOnPanel(ctx context.Context, nodeID, panelID int64) bool {
	n, err := a.Nodes.GetByID(ctx, nodeID)
	return err == nil && n.PanelID == panelID
}
