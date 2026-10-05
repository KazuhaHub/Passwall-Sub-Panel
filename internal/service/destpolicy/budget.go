package destpolicy

import (
	"encoding/json"
	"slices"
	"strings"

	"github.com/KazuhaHub/passwall-protocol/protocol"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

type BudgetValue struct {
	Used  int64 `json:"used"`
	Limit int64 `json:"limit"`
}
type Budget struct {
	Rules    BudgetValue `json:"rules"`
	Domains  BudgetValue `json:"domains"`
	Regexps  BudgetValue `json:"regexps"`
	CIDRs    BudgetValue `json:"cidrs"`
	Subjects BudgetValue `json:"subjects"`
	Bytes    BudgetValue `json:"bytes"`
}

// DefinitionBudget uses the same expanded rule sequence as publication. It
// includes every scoped definition using synthetic membership, independently
// of current panel coverage. Subjects alone use full tag-matched panel inputs,
// including disabled members and panels currently ineligible for enforcement.
func DefinitionBudget(defs domain.DestDefinitions, panels []RosterInput) (Budget, error) {
	result := Budget{Rules: BudgetValue{Limit: protocol.MaxDestinationRules}, Domains: BudgetValue{Limit: protocol.MaxDestinationDomains}, Regexps: BudgetValue{Limit: protocol.MaxDestinationRegexps}, CIDRs: BudgetValue{Limit: protocol.MaxDestinationCIDRs}, Subjects: BudgetValue{Limit: protocol.MaxDestinationSubjects}, Bytes: BudgetValue{Limit: protocol.MaxDestinationPolicyBytes}}
	groups := map[int64]bool{}
	for _, p := range defs.Policies {
		for _, id := range p.GroupIDs {
			groups[id] = true
		}
	}
	for _, g := range defs.Groups {
		groups[g.GroupID] = true
	}
	ids := make([]int64, 0, len(groups))
	for id := range groups {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	input := RosterInput{Capabilities: []string{protocol.CapabilityDestinationPolicy}, UserGroups: map[int64]int64{}}
	for i, id := range ids {
		user := int64(i + 1)
		input.UserIDs = append(input.UserIDs, user)
		input.UserGroups[user] = id
	}
	check := defs
	check.State.Paused = false
	check.Exemptions = nil
	p, err := buildPolicy(check, input, false)
	if err != nil {
		return Budget{}, err
	}
	if p != nil {
		result.Rules.Used = int64(len(p.Rules))
		for i, r := range p.Rules {
			result.Domains.Used += int64(len(r.Domains))
			result.CIDRs.Used += int64(len(r.CIDRs))
			for _, d := range r.Domains {
				if strings.HasPrefix(d, "regexp:") {
					result.Regexps.Used++
				}
			}
			// Definition bytes omit real membership. Catch-all validation needs
			// one placeholder only; ordinary scoped rules need no placeholder.
			p.Rules[i].Subjects = nil
			if r.CatchAll {
				p.Rules[i].Subjects = []protocol.SubjectKey{"usr_1"}
			}
		}
		encoded, err := json.Marshal(p)
		if err != nil {
			return Budget{}, err
		}
		result.Bytes.Used = int64(len(encoded))
	}
	for _, panel := range panels {
		panel.Capabilities = []string{protocol.CapabilityDestinationPolicy}
		panel.Collect = domain.AuditCollectOff
		quotaDefs := defs
		quotaDefs.State.Paused = false
		policy, err := buildPolicy(quotaDefs, panel, false)
		if err != nil {
			return Budget{}, err
		}
		var subjects int64
		if policy != nil {
			subjects = int64(len(policy.Exempt))
			for _, rule := range policy.Rules {
				subjects += int64(len(rule.Subjects))
			}
		}
		if subjects > result.Subjects.Used {
			result.Subjects.Used = subjects
		}
	}
	return result, nil
}
