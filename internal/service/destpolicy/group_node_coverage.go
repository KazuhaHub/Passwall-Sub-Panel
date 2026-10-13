package destpolicy

import (
	"slices"

	"github.com/KazuhaHub/passwall-protocol/protocol"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

type GroupIneligibleNode struct {
	PanelID int64  `json:"panel_id"`
	Name    string `json:"name"`
	Reason  string `json:"reason"`
}

type GroupNodeInput struct {
	Panel     domain.DestTestPanel
	Quota     RosterInput
	Engine    string
	Listeners []protocol.Listener
}

type GroupNodeCoverage struct {
	TotalNodes       int                   `json:"total_nodes"`
	EligibleNodes    int                   `json:"eligible_nodes"`
	Ineligible       []GroupIneligibleNode `json:"ineligible"`
	EligiblePanelIDs []int64               `json:"-"`
}

type GroupAccessPreview struct {
	GroupNodeCoverage
	Members            int64    `json:"members"`
	BaseHosts          []string `json:"base_hosts"`
	BaseHostsAvailable bool     `json:"base_hosts_available"`
	BaseListID         int64    `json:"base_list_id"`
	ExtraListID        int64    `json:"extra_list_id"`
	Budget             Budget   `json:"budget"`
}

// Inputs contain tag-matched panels, not the current open-mode selection.
// Quota membership includes disabled members and never comes from the roster.
// The result is a prospective preflight, not an applied-policy receipt.
func AssessGroupNodes(defs domain.DestDefinitions, groupID int64, inputs []GroupNodeInput) (GroupNodeCoverage, error) {
	if groupID <= 0 || !slices.ContainsFunc(defs.Groups, func(g domain.DestGroupMode) bool { return g.GroupID == groupID && g.Mode == "allowlist" }) || CheckDefinitions(defs) != nil {
		return GroupNodeCoverage{}, domain.ErrUnavailable
	}
	check := defs
	check.State.Paused = false
	ordered := slices.Clone(inputs)
	slices.SortFunc(ordered, func(a, b GroupNodeInput) int {
		if a.Panel.ID < b.Panel.ID {
			return -1
		}
		if a.Panel.ID > b.Panel.ID {
			return 1
		}
		return 0
	})
	out := GroupNodeCoverage{Ineligible: []GroupIneligibleNode{}, EligiblePanelIDs: []int64{}}
	var previous int64
	for _, input := range ordered {
		if input.Panel.ID <= previous {
			return GroupNodeCoverage{}, domain.ErrUnavailable
		}
		previous = input.Panel.ID
		reason, err := groupNodeExclusion(check, input)
		if err != nil {
			return GroupNodeCoverage{}, err
		}
		out.TotalNodes++
		if reason == "" {
			out.EligibleNodes++
			out.EligiblePanelIDs = append(out.EligiblePanelIDs, input.Panel.ID)
		} else {
			out.Ineligible = append(out.Ineligible, GroupIneligibleNode{PanelID: input.Panel.ID, Name: input.Panel.Name, Reason: reason})
		}
	}
	return out, nil
}

func groupNodeExclusion(defs domain.DestDefinitions, input GroupNodeInput) (string, error) {
	switch domain.NormalizePanelKind(input.Panel.Kind) {
	case domain.PanelKind3XUI:
		return "xui", nil
	case domain.PanelKindSUI:
		return "sui", nil
	case domain.PanelKindPSP:
	default:
		return "", domain.ErrUnavailable
	}
	agent := input.Panel.Agent
	if agent == nil || !slices.Contains(agent.ObservedCapabilities, protocol.CapabilityDestinationPolicy) {
		return "capability", nil
	}
	if agent.AgentID == "" {
		return "", domain.ErrUnavailable
	}
	if runtime := input.Panel.Runtime; runtime != nil {
		switch runtime.FallbackReason {
		case "over_limit", "rejected", "sniffing":
			return runtime.FallbackReason, nil
		case "":
		default:
			return "", domain.ErrUnavailable
		}
		if runtime.FallbackExhausted {
			return "rejected", nil
		}
	}
	if !domain.NodeCoreEngine(input.Engine).Valid() || !input.Quota.Collect.Valid() {
		return "", domain.ErrUnavailable
	}
	quota := input.Quota
	quota.Capabilities, quota.Engine = agent.ObservedCapabilities, input.Engine
	for _, id := range quota.UserIDs {
		if id <= 0 || quota.UserGroups[id] <= 0 {
			return "", domain.ErrUnavailable
		}
	}
	policy, err := buildPolicy(defs, quota, false)
	if err != nil {
		return "", domain.ErrUnavailable
	}
	if limit := nodePolicyLimit(policy); limit != nil {
		if limit.Kind == "invalid" {
			return "", domain.ErrUnavailable
		}
		return "over_limit", nil
	}
	if len(PreflightPolicy(policy, input.Engine, input.Listeners)) > 0 {
		return "sniffing", nil
	}
	return "", nil
}
