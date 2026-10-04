package destpolicy

import (
	"fmt"
	"net/netip"
	"slices"
	"strconv"
	"strings"

	"github.com/KazuhaHub/passwall-protocol/protocol"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/destlist"
)

type RosterInput struct {
	UserIDs         []int64
	UserGroups      map[int64]int64
	Capabilities    []string
	Engine          string
	Collect         domain.AuditCollect
	CollectRevision uint64
}

func EffectiveCollect(level domain.AuditCollect, capabilities []string, engine string) protocol.CollectLevel {
	if engine != "" && engine != "xray" {
		return ""
	}
	if level == domain.AuditCollectOff || !slices.Contains(capabilities, "audit.hits.v1") {
		return ""
	}
	switch level {
	case domain.AuditCollectHits:
		return protocol.CollectHits
	case domain.AuditCollectHitsAndUsage:
		if slices.Contains(capabilities, "audit.usage.v1") {
			return protocol.CollectHitsAndUsage
		}
		return protocol.CollectHits
	default:
		return ""
	}
}

// BuildPolicy is the deterministic normal candidate builder. It performs no
// database or network I/O and never mutates definitions, membership or roster.
// Publication, preflight and the LKG state machine are separate operations.
func BuildPolicy(defs domain.DestDefinitions, input RosterInput) (*protocol.DestinationPolicy, error) {
	return buildPolicy(defs, input, true)
}

func buildPolicy(defs domain.DestDefinitions, input RosterInput, validate bool) (*protocol.DestinationPolicy, error) {
	if !slices.Contains(input.Capabilities, protocol.CapabilityDestinationPolicy) {
		return nil, nil
	}
	p := &protocol.DestinationPolicy{Rules: []protocol.DestinationRule{}, Collect: EffectiveCollect(input.Collect, input.Capabilities, input.Engine)}
	if p.Collect != "" {
		p.CollectRevision = input.CollectRevision
	}
	if defs.State.Paused {
		return finishPolicy(p, validate)
	}
	users := map[int64]bool{}
	for _, id := range input.UserIDs {
		if id <= 0 {
			return nil, invalid("roster.user_id")
		}
		users[id] = true
	}
	lists := map[int64]domain.DestList{}
	for _, list := range defs.Lists {
		if list.ID <= 0 {
			return nil, invalid("list.id")
		}
		if _, exists := lists[list.ID]; exists {
			return nil, invalid("list.duplicate_id")
		}
		lists[list.ID] = list
	}
	policies := append([]domain.DestPolicy(nil), defs.Policies...)
	slices.SortFunc(policies, func(a, b domain.DestPolicy) int {
		if a.Priority < b.Priority {
			return -1
		}
		if a.Priority > b.Priority {
			return 1
		}
		if a.ID < b.ID {
			return -1
		}
		if a.ID > b.ID {
			return 1
		}
		return 0
	})
	appendAction := func(action domain.DestAction) error {
		for _, policy := range policies {
			if !policy.Enabled || policy.Action != action {
				continue
			}
			if policy.ID <= 0 {
				return invalid("policy.id")
			}
			var subjects []protocol.SubjectKey
			switch policy.Scope {
			case domain.DestScopeAll:
			case domain.DestScopeGroups:
				if len(policy.GroupIDs) == 0 {
					return invalid("policy.group_ids")
				}
				for _, groupID := range policy.GroupIDs {
					if groupID <= 0 {
						return invalid("policy.group_ids")
					}
				}
				subjects = groupSubjects(users, input.UserGroups, policy.GroupIDs)
				if len(subjects) == 0 {
					continue
				}
			default:
				return invalid("policy.scope")
			}
			domains, cidrs, err := listMatches(lists, policy.ListIDs)
			if err != nil {
				return err
			}
			for _, cidr := range policy.Inline.CIDRs {
				if destlist.IsBroad(cidr) {
					return fmt.Errorf("%w: dest_list_too_broad", domain.ErrValidation)
				}
				cidrs = append(cidrs, cidr)
			}
			r := protocol.DestinationRule{ID: "p" + strconv.FormatInt(policy.ID, 10), Action: protocol.RuleAction(action), Subjects: subjects, Domains: unique(domains), CIDRs: unique(cidrs), Ports: policy.Inline.Ports, Network: policy.Inline.Network, Protocols: unique(policy.Inline.Protocols), Private: policy.Inline.Private}
			addresses := len(r.Domains) > 0 || len(r.CIDRs) > 0 || r.Private
			if addresses && len(r.Protocols) > 0 {
				addressRule, protocolRule := r, r
				addressRule.ID += "x1"
				addressRule.Protocols = nil
				protocolRule.ID += "x2"
				protocolRule.Domains = nil
				protocolRule.CIDRs = nil
				protocolRule.Private = false
				p.Rules = append(p.Rules, addressRule, protocolRule)
			} else if addresses || r.Ports != "" || r.Network != "" || len(r.Protocols) > 0 {
				p.Rules = append(p.Rules, r)
			}
		}
		return nil
	}
	// Keep action segments, never sort the final rule sequence by ID.
	if err := appendAction(domain.DestAllow); err != nil {
		return nil, err
	}
	for _, exemption := range defs.Exemptions {
		if users[exemption.UserID] {
			p.Exempt = append(p.Exempt, protocol.NewSubjectKey(exemption.UserID))
		}
	}
	p.Exempt = unique(p.Exempt)
	if err := appendAction(domain.DestBlock); err != nil {
		return nil, err
	}
	groups := append([]domain.DestGroupMode(nil), defs.Groups...)
	slices.SortFunc(groups, func(a, b domain.DestGroupMode) int {
		if a.GroupID < b.GroupID {
			return -1
		}
		if a.GroupID > b.GroupID {
			return 1
		}
		return 0
	})
	for _, group := range groups {
		if group.Mode == "open" {
			continue
		}
		if group.Mode != "allowlist" || group.GroupID <= 0 || (group.Stage != "trial" && group.Stage != "enforce") {
			return nil, invalid("group.mode")
		}
		subjects := groupSubjects(users, input.UserGroups, []int64{group.GroupID})
		if len(subjects) == 0 {
			continue
		}
		id := "g" + strconv.FormatInt(group.GroupID, 10)
		for _, part := range []struct {
			suffix string
			ids    []int64
		}{{"x1", group.ListIDs}, {"x2", positiveIDs(group.BaseListID, group.ExtraListID)}} {
			domains, cidrs, err := listMatches(lists, part.ids)
			if err != nil {
				return nil, err
			}
			if len(domains) == 0 && len(cidrs) == 0 {
				continue
			}
			p.Rules = append(p.Rules, protocol.DestinationRule{ID: id + part.suffix, Action: protocol.RuleAllow, Subjects: subjects, Domains: unique(domains), CIDRs: unique(cidrs)})
		}
		action := protocol.RuleBlock
		if group.Stage == "trial" {
			action = protocol.RuleObserve
		}
		p.Rules = append(p.Rules, protocol.DestinationRule{ID: id, Action: action, Subjects: subjects, CatchAll: true})
	}
	if err := appendAction(domain.DestObserve); err != nil {
		return nil, err
	}
	for _, policy := range policies {
		if policy.Enabled && policy.Action != domain.DestAllow && policy.Action != domain.DestBlock && policy.Action != domain.DestObserve {
			return nil, invalid("policy.action")
		}
	}
	return finishPolicy(p, validate)
}

func finishPolicy(p *protocol.DestinationPolicy, validate bool) (*protocol.DestinationPolicy, error) {
	if len(p.Rules) == 0 {
		if p.Collect != protocol.CollectHitsAndUsage {
			return nil, nil
		}
		p.Exempt = nil
	}
	if validate {
		if err := protocol.ValidateDestinationPolicy(p); err != nil {
			return nil, fmt.Errorf("%w: dest_policy_invalid: %v", domain.ErrValidation, err)
		}
	}
	return p, nil
}
func invalid(field string) error {
	return &DefinitionError{Detail: domain.DestPublishError{Kind: "invalid", Field: field}}
}
func unique[T ~string](values []T) []T {
	if len(values) == 0 {
		return nil
	}
	result := append([]T(nil), values...)
	slices.Sort(result)
	return slices.Compact(result)
}
func groupSubjects(users map[int64]bool, membership map[int64]int64, groups []int64) []protocol.SubjectKey {
	var result []protocol.SubjectKey
	for id := range users {
		if slices.Contains(groups, membership[id]) {
			result = append(result, protocol.NewSubjectKey(id))
		}
	}
	return unique(result)
}
func positiveIDs(ids ...int64) []int64 {
	return slices.DeleteFunc(ids, func(id int64) bool { return id <= 0 })
}
func listMatches(lists map[int64]domain.DestList, ids []int64) (domains, cidrs []string, err error) {
	for _, id := range ids {
		list, exists := lists[id]
		if !exists {
			return nil, nil, invalid("list.reference")
		}
		if list.EntryCount == 0 || (list.Kind != domain.DestListCustom && list.LastFetchedAt == nil) {
			continue
		}
		for _, entry := range strings.Split(string(list.Entries), "\n") {
			if entry == "" {
				continue
			}
			if destlist.IsBroad(entry) {
				return nil, nil, fmt.Errorf("%w: dest_list_too_broad", domain.ErrValidation)
			}
			if prefix, e := netip.ParsePrefix(entry); e == nil {
				if prefix.Masked().String() != entry {
					return nil, nil, invalid("list.cidr")
				}
				cidrs = append(cidrs, entry)
			} else {
				domains = append(domains, entry)
			}
		}
	}
	return domains, cidrs, nil
}
