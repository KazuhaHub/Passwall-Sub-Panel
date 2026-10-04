package destpolicy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"github.com/KazuhaHub/passwall-protocol/protocol"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/destlist"
)

// These are executable definitions, not administrative rows or a node policy.
// Member enumeration and collection revision are deliberately absent.
type definitionSnapshot struct {
	Schema     int              `json:"schema"`
	Generation int64            `json:"generation"`
	Paused     bool             `json:"paused"`
	Lists      []snapshotList   `json:"lists"`
	Policies   []snapshotPolicy `json:"policies"`
	Exemptions []int64          `json:"exemptions"`
	Groups     []snapshotGroup  `json:"groups"`
}
type snapshotList struct {
	ID      int64               `json:"id"`
	Kind    domain.DestListKind `json:"kind"`
	Entries string              `json:"entries"`
	Ready   bool                `json:"ready"`
}
type snapshotPolicy struct {
	ID       int64             `json:"id"`
	Action   domain.DestAction `json:"action"`
	ListIDs  []int64           `json:"list_ids,omitempty"`
	Inline   domain.DestInline `json:"inline"`
	Scope    domain.DestScope  `json:"scope"`
	GroupIDs []int64           `json:"group_ids,omitempty"`
	Priority int               `json:"priority"`
	Enabled  bool              `json:"enabled"`
}
type snapshotGroup struct {
	ID          int64   `json:"id"`
	Mode        string  `json:"mode"`
	Stage       string  `json:"stage"`
	ListIDs     []int64 `json:"list_ids,omitempty"`
	BaseListID  int64   `json:"base_list_id,omitempty"`
	ExtraListID int64   `json:"extra_list_id,omitempty"`
}

// CheckDefinitions covers inactive/scoped definitions as well as live rules.
// It uses synthetic membership only for checking; no fake subjects are saved.
// Only enabled rules contribute to the executable definition quota. Actual
// subjects and encoded bytes are checked later for each node's real candidate.
func CheckDefinitions(defs domain.DestDefinitions) error {
	seenLists := map[int64]bool{}
	for _, list := range defs.Lists {
		if list.ID <= 0 || seenLists[list.ID] {
			return invalid("list.id")
		}
		seenLists[list.ID] = true
		if list.Kind != domain.DestListCustom && list.Kind != domain.DestListRemote && list.Kind != domain.DestListGeosite {
			return invalid("list.kind")
		}
		if list.EntryCount < 0 || list.EntryCount > destlist.MaxEntries {
			return invalid("list.entry_count")
		}
		if list.EntryCount == 0 {
			if len(list.Entries) != 0 {
				return invalid("list.entries")
			}
			continue
		}
		// Inspect stored bodies even when a source is pending or unused. The
		// source parser has already filtered broad entries; the publication
		// boundary must catch corrupted or directly modified SQL rows as well.
		list.Kind = domain.DestListCustom
		domains, cidrs, err := listMatches(map[int64]domain.DestList{list.ID: list}, []int64{list.ID})
		if err != nil {
			return err
		}
		domains, cidrs = unique(domains), unique(cidrs)
		if len(domains)+len(cidrs) != list.EntryCount {
			return invalid("list.entry_count")
		}
		if err := validateDefinitionRules(&protocol.DestinationPolicy{Rules: []protocol.DestinationRule{{ID: "p1", Action: protocol.RuleAllow, Domains: domains, CIDRs: cidrs}}}, false); err != nil {
			return err
		}
	}
	input := RosterInput{Capabilities: []string{protocol.CapabilityDestinationPolicy}, UserGroups: map[int64]int64{}}
	groups := map[int64]bool{}
	for _, p := range defs.Policies {
		for _, id := range p.GroupIDs {
			groups[id] = true
		}
	}
	for _, g := range defs.Groups {
		groups[g.GroupID] = true
	}
	var ids []int64
	for id := range groups {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	for i, id := range ids {
		user := int64(i + 1)
		input.UserIDs = append(input.UserIDs, user)
		input.UserGroups[user] = id
	}
	check := defs
	check.State.Paused, check.Exemptions = false, nil
	// Inactive definitions still have to be structurally safe to publish.
	for _, policy := range defs.Policies {
		if policy.Enabled {
			continue
		}
		policy.Enabled = true
		one := check
		one.Policies, one.Groups = []domain.DestPolicy{policy}, nil
		p, err := buildPolicy(one, input, false)
		if err != nil {
			return err
		}
		if err = validateDefinitionRules(p, false); err != nil {
			return err
		}
	}
	seenExemptions := map[int64]bool{}
	for _, e := range defs.Exemptions {
		if e.UserID <= 0 || seenExemptions[e.UserID] {
			return invalid("exemption.user_id")
		}
		seenExemptions[e.UserID] = true
	}
	seenPolicies := map[int64]bool{}
	for _, p := range defs.Policies {
		if p.ID <= 0 || seenPolicies[p.ID] {
			return invalid("policy.id")
		}
		seenPolicies[p.ID] = true
	}
	seenGroups := map[int64]bool{}
	for _, g := range defs.Groups {
		if g.GroupID <= 0 || seenGroups[g.GroupID] {
			return invalid("group.id")
		}
		seenGroups[g.GroupID] = true
	}
	p, err := buildPolicy(check, input, false)
	if err != nil {
		return err
	}
	return validateDefinitionRules(p, true)
}

func validateDefinitionRules(p *protocol.DestinationPolicy, budget bool) error {
	if p == nil {
		return nil
	}
	domains, regexps, cidrs := 0, 0, 0
	for _, r := range p.Rules {
		domains += len(r.Domains)
		cidrs += len(r.CIDRs)
		for _, v := range r.Domains {
			if strings.HasPrefix(v, "regexp:") {
				regexps++
			}
		}
	}
	if budget {
		for _, q := range []struct {
			kind        string
			used, limit int
		}{{"rules", len(p.Rules), protocol.MaxDestinationRules}, {"domains", domains, protocol.MaxDestinationDomains}, {"regexps", regexps, protocol.MaxDestinationRegexps}, {"cidrs", cidrs, protocol.MaxDestinationCIDRs}} {
			if q.used > q.limit {
				return &DefinitionError{Detail: domain.DestPublishError{Kind: q.kind, Used: int64(q.used), Limit: int64(q.limit)}}
			}
		}
	}
	// Validate bounded chunks so large valid definitions are not rejected by
	// the per-node byte cap or synthetic subject count. Every match field is
	// still checked by the shared contract, including explicit catch-alls.
	seen := map[string]bool{}
	for _, r := range p.Rules {
		if seen[r.ID] {
			return invalid("policy.rule_id")
		}
		seen[r.ID] = true
		if len(r.Subjects) > 0 {
			r.Subjects = []protocol.SubjectKey{"usr_1"}
		}
		for start := 0; ; start += 128 {
			chunk := r
			chunk.Domains = sliceChunk(r.Domains, start, 128)
			chunk.CIDRs = sliceChunk(r.CIDRs, start, 128)
			if err := protocol.ValidateDestinationPolicy(&protocol.DestinationPolicy{Rules: []protocol.DestinationRule{chunk}}); err != nil {
				return invalid("rule." + r.ID)
			}
			if start+128 >= len(r.Domains) && start+128 >= len(r.CIDRs) {
				break
			}
		}
	}
	return nil
}
func sliceChunk[T any](items []T, start, size int) []T {
	if start >= len(items) {
		return nil
	}
	return items[start:min(start+size, len(items))]
}

func BuildDefinitionSnapshot(defs domain.DestDefinitions) ([]byte, error) {
	if defs.State.Generation <= 0 {
		return nil, invalid("generation")
	}
	if err := CheckDefinitions(defs); err != nil {
		return nil, err
	}
	s := definitionSnapshot{Schema: 1, Generation: defs.State.Generation, Paused: defs.State.Paused}
	for _, l := range defs.Lists {
		s.Lists = append(s.Lists, snapshotList{l.ID, l.Kind, string(l.Entries), l.LastFetchedAt != nil})
	}
	for _, p := range defs.Policies {
		inline := p.Inline
		inline.CIDRs, inline.Protocols = unique(inline.CIDRs), unique(inline.Protocols)
		s.Policies = append(s.Policies, snapshotPolicy{p.ID, p.Action, uniqueIDs(p.ListIDs), inline, p.Scope, uniqueIDs(p.GroupIDs), p.Priority, p.Enabled})
	}
	for _, e := range defs.Exemptions {
		s.Exemptions = append(s.Exemptions, e.UserID)
	}
	for _, g := range defs.Groups {
		s.Groups = append(s.Groups, snapshotGroup{g.GroupID, g.Mode, g.Stage, uniqueIDs(g.ListIDs), g.BaseListID, g.ExtraListID})
	}
	slices.SortFunc(s.Lists, func(a, b snapshotList) int { return compareID(a.ID, b.ID) })
	slices.SortFunc(s.Policies, func(a, b snapshotPolicy) int { return compareID(a.ID, b.ID) })
	slices.Sort(s.Exemptions)
	slices.SortFunc(s.Groups, func(a, b snapshotGroup) int { return compareID(a.ID, b.ID) })
	return json.Marshal(s)
}
func uniqueIDs(ids []int64) []int64 {
	result := append([]int64(nil), ids...)
	slices.Sort(result)
	return slices.Compact(result)
}
func compareID(a, b int64) int {
	if a < b {
		return -1
	}
	if a > b {
		return 1
	}
	return 0
}

func DecodeDefinitionSnapshot(snapshot domain.DestPolicySnapshot) (domain.DestDefinitions, error) {
	bad := func() (domain.DestDefinitions, error) {
		return domain.DestDefinitions{}, fmt.Errorf("%w: corrupt destination definition snapshot", domain.ErrUnavailable)
	}
	var s definitionSnapshot
	d := json.NewDecoder(bytes.NewReader(snapshot.Body))
	d.DisallowUnknownFields()
	if err := d.Decode(&s); err != nil {
		return bad()
	}
	if err := d.Decode(new(any)); err != io.EOF || s.Schema != 1 || s.Generation != snapshot.Generation || s.Generation <= 0 {
		return bad()
	}
	canonical, err := json.Marshal(s)
	if err != nil || !bytes.Equal(bytes.TrimSpace(snapshot.Body), canonical) {
		return bad()
	}
	defs := domain.DestDefinitions{State: domain.DestPolicyState{Generation: s.Generation, PublishedGeneration: s.Generation, Paused: s.Paused}}
	for _, l := range s.Lists {
		if l.Kind != domain.DestListCustom && l.Kind != domain.DestListRemote && l.Kind != domain.DestListGeosite {
			return bad()
		}
		count := 0
		if l.Entries != "" {
			count = len(strings.Split(strings.TrimSuffix(l.Entries, "\n"), "\n"))
		}
		v := domain.DestList{ID: l.ID, Kind: l.Kind, Entries: []byte(l.Entries), EntryCount: count}
		if l.Ready {
			at := time.UnixMilli(1).UTC()
			v.LastFetchedAt = &at
		}
		defs.Lists = append(defs.Lists, v)
	}
	for _, p := range s.Policies {
		defs.Policies = append(defs.Policies, domain.DestPolicy{ID: p.ID, Action: p.Action, ListIDs: p.ListIDs, Inline: p.Inline, Scope: p.Scope, GroupIDs: p.GroupIDs, Priority: p.Priority, Enabled: p.Enabled})
	}
	for _, id := range s.Exemptions {
		defs.Exemptions = append(defs.Exemptions, domain.DestExemption{UserID: id})
	}
	for _, g := range s.Groups {
		defs.Groups = append(defs.Groups, domain.DestGroupMode{GroupID: g.ID, Mode: g.Mode, Stage: g.Stage, ListIDs: g.ListIDs, BaseListID: g.BaseListID, ExtraListID: g.ExtraListID})
	}
	if err := CheckDefinitions(defs); err != nil {
		return bad()
	}
	return defs, nil
}
