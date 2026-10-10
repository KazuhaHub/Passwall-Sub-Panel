package destpolicy

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/KazuhaHub/passwall-protocol/protocol"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// PruneFallback retains confirmed matches while removing retired sources and
// roster subjects. Definitions must come from one published snapshot.
func PruneFallback(lkg *protocol.DestinationPolicy, defs domain.DestDefinitions, input RosterInput) (*protocol.DestinationPolicy, error) {
	if !slices.Contains(input.Capabilities, protocol.CapabilityDestinationPolicy) {
		return nil, nil
	}
	p := &protocol.DestinationPolicy{Rules: []protocol.DestinationRule{}, Collect: EffectiveCollect(input.Collect, input.Capabilities, input.Engine)}
	if p.Collect != "" {
		p.CollectRevision = input.CollectRevision
	}
	// Pause and an absent LKG do not depend on the old executable body.
	if defs.State.Paused || lkg == nil {
		return finishPolicy(p, true)
	}
	if err := protocol.ValidateDestinationPolicy(lkg); err != nil {
		return nil, fmt.Errorf("%w: corrupt confirmed destination policy", domain.ErrUnavailable)
	}
	roster := make(map[protocol.SubjectKey]bool, len(input.UserIDs))
	for _, id := range input.UserIDs {
		if id <= 0 {
			return nil, invalid("roster.user_id")
		}
		roster[protocol.NewSubjectKey(id)] = true
	}
	policies := map[int64]bool{}
	for _, policy := range defs.Policies {
		if policy.Enabled {
			policies[policy.ID] = true
		}
	}
	groups := map[int64]bool{}
	for _, group := range defs.Groups {
		if group.Mode == "allowlist" {
			groups[group.GroupID] = true
		}
	}
	for _, confirmed := range lkg.Rules {
		raw, _, _ := strings.Cut(confirmed.ID[1:], "x")
		id, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || id <= 0 {
			return nil, fmt.Errorf("%w: invalid confirmed rule source", domain.ErrUnavailable)
		}
		if confirmed.ID[0] == 'p' && !policies[id] || confirmed.ID[0] == 'g' && !groups[id] {
			continue
		}
		rule := confirmed
		rule.Subjects = nil
		for _, subject := range confirmed.Subjects {
			if roster[subject] {
				rule.Subjects = append(rule.Subjects, subject)
			}
		}
		if len(confirmed.Subjects) > 0 && len(rule.Subjects) == 0 {
			continue
		}
		rule.Domains = append([]string(nil), confirmed.Domains...)
		rule.CIDRs = append([]string(nil), confirmed.CIDRs...)
		rule.Protocols = append([]string(nil), confirmed.Protocols...)
		p.Rules = append(p.Rules, rule)
	}
	for _, exemption := range defs.Exemptions {
		subject := protocol.NewSubjectKey(exemption.UserID)
		if roster[subject] {
			p.Exempt = append(p.Exempt, subject)
		}
	}
	p.Exempt = unique(p.Exempt)
	return finishPolicy(p, true)
}
