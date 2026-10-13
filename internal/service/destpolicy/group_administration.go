package destpolicy

import (
	"context"
	"fmt"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/operationgate"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/destlist"
)

type GroupAdministrationStore interface {
	ReadDefinitions(context.Context) (domain.DestDefinitions, error)
	GetGroupMode(context.Context, int64) (domain.DestGroupMode, error)
	SaveGroupMode(context.Context, *domain.DestGroupMode, time.Time, [2]domain.DestList, time.Time) error
}
type GroupModeInput struct {
	Mode, Stage string
	ListIDs     []int64
	BaseHosts   []string
}
type GroupModePreview struct {
	Mode        domain.DestGroupMode
	Definitions domain.DestDefinitions `json:"-"`
	Budget      Budget
}
type GroupAdministrator struct {
	store     GroupAdministrationStore
	readGroup func(context.Context, int64) (*domain.Group, error)
	budget    func(context.Context, domain.DestDefinitions) (Budget, error)
	changed   func(context.Context, int64)
	gate      *operationgate.Gate
	now       func() time.Time
}

func NewGroupAdministrator(store GroupAdministrationStore, readGroup func(context.Context, int64) (*domain.Group, error), budget func(context.Context, domain.DestDefinitions) (Budget, error), changed func(context.Context, int64)) *GroupAdministrator {
	return &GroupAdministrator{store: store, readGroup: readGroup, budget: budget, changed: changed, now: time.Now}
}
func (a *GroupAdministrator) SetOperationGate(gate *operationgate.Gate) { a.gate = gate }
func (a *GroupAdministrator) available() error {
	if a == nil || a.store == nil || a.readGroup == nil || a.budget == nil || a.changed == nil {
		return domain.ErrUnavailable
	}
	return nil
}

type groupModePreparation struct {
	old, candidate domain.DestGroupMode
	initial        [2]domain.DestList
	preview        GroupModePreview
}

func initialGroupOwnedList(name string, hosts []string) (domain.DestList, error) {
	if len(hosts) > destlist.MaxEntries {
		return domain.DestList{}, invalid("base_hosts")
	}
	entries := make([]string, 0, len(hosts))
	for _, host := range hosts {
		canonical, err := baseDNSHostname(strings.TrimSpace(host))
		if err != nil || canonical == "" {
			return domain.DestList{}, invalid("base_hosts")
		}
		entries = append(entries, "full:"+canonical)
	}
	entries = unique(entries)
	text := strings.Join(entries, "\n")
	if text != "" {
		text += "\n"
	}
	parsed, err := destlist.ParseCustom([]byte(text))
	if err != nil {
		return domain.DestList{}, err
	}
	return domain.DestList{Name: name, Kind: domain.DestListCustom, SourceText: []byte(text), Entries: parsed.Entries, EntryCount: parsed.EntryCount, RegexpCount: parsed.RegexpCount, ContentSHA256: parsed.ContentSHA256, ParseReport: &parsed.Report}, nil
}
func groupOwnedListName(name, suffix string) string {
	runes, tail := []rune(strings.TrimSpace(name)), []rune(" · "+suffix)
	if len(runes) > 128-len(tail) {
		runes = runes[:128-len(tail)]
	}
	return string(runes) + string(tail)
}

func (a *GroupAdministrator) prepare(ctx context.Context, groupID int64, input GroupModeInput, expected *time.Time) (groupModePreparation, error) {
	var result groupModePreparation
	if groupID <= 0 {
		return result, invalid("group_id")
	}
	g, err := a.readGroup(ctx, groupID)
	if err != nil {
		return result, err
	}
	if g == nil || g.ID != groupID {
		return result, domain.ErrNotFound
	}
	old, err := a.store.GetGroupMode(ctx, groupID)
	if err != nil {
		return result, err
	}
	if old.GroupID != groupID {
		return result, domain.ErrUnavailable
	}
	if _, err := domain.DestinationGroupAccessMode(old.Mode, old.Stage); err != nil {
		return result, domain.ErrUnavailable
	}
	if expected != nil && !old.UpdatedAt.Equal(*expected) {
		return result, fmt.Errorf("%w: dest_mode_stale", domain.ErrConflict)
	}
	if _, err := domain.DestinationGroupAccessMode(input.Mode, input.Stage); err != nil {
		return result, fmt.Errorf("%w: dest_mode_invalid_transition", domain.ErrConflict)
	}
	if old.Mode == "open" && input.Mode == "allowlist" && input.Stage != "trial" {
		return result, fmt.Errorf("%w: dest_mode_invalid_transition", domain.ErrConflict)
	}
	if input.BaseHosts != nil && (input.Mode != "allowlist" || old.BaseListID != 0) {
		return result, invalid("base_hosts")
	}
	for _, id := range input.ListIDs {
		if id <= 0 {
			return result, invalid("list_ids")
		}
	}
	defs, err := a.store.ReadDefinitions(ctx)
	if err != nil {
		return result, err
	}
	defs.Lists, defs.Groups = slices.Clone(defs.Lists), slices.Clone(defs.Groups)
	index := -1
	for i, mode := range defs.Groups {
		if mode.GroupID == groupID {
			if !mode.UpdatedAt.Equal(old.UpdatedAt) {
				return result, fmt.Errorf("%w: dest_mode_stale", domain.ErrConflict)
			}
			index = i
			break
		}
	}
	if index < 0 && !old.UpdatedAt.IsZero() {
		return result, fmt.Errorf("%w: dest_mode_stale", domain.ErrConflict)
	}
	lists := map[int64]domain.DestList{}
	var highest int64
	for _, list := range defs.Lists {
		lists[list.ID] = list
		highest = max(highest, list.ID)
	}
	for _, id := range []int64{old.BaseListID, old.ExtraListID} {
		if id == 0 {
			continue
		}
		list, exists := lists[id]
		if id < 0 || !exists || list.OwnerGroupID != groupID || list.Kind != domain.DestListCustom {
			return result, domain.ErrUnavailable
		}
	}
	if old.BaseListID > 0 && old.BaseListID == old.ExtraListID {
		return result, domain.ErrUnavailable
	}
	candidate := old
	candidate.Mode, candidate.Stage, candidate.ListIDs = input.Mode, input.Stage, uniqueIDs(input.ListIDs)
	for _, id := range candidate.ListIDs {
		list, exists := lists[id]
		if !exists || list.OwnerGroupID != 0 && list.OwnerGroupID != groupID {
			return result, invalid("list_ids")
		}
		if input.Stage == "enforce" && list.Kind != domain.DestListCustom && list.LastFetchedAt == nil {
			return result, fmt.Errorf("%w: dest_list_not_ready", domain.ErrConflict)
		}
	}
	prospective := candidate
	if candidate.Mode == "allowlist" {
		for i, part := range []struct {
			id     *int64
			hosts  []string
			suffix string
		}{{&prospective.BaseListID, input.BaseHosts, "基础放行"}, {&prospective.ExtraListID, nil, "补充"}} {
			if *part.id != 0 {
				continue
			}
			initial, err := initialGroupOwnedList(groupOwnedListName(g.Name, part.suffix), part.hosts)
			if err != nil {
				return result, err
			}
			if highest == math.MaxInt64 {
				return result, domain.ErrResourceExhausted
			}
			highest++
			result.initial[i] = initial
			initial.ID, initial.OwnerGroupID = highest, groupID
			*part.id = highest
			defs.Lists = append(defs.Lists, initial)
		}
	}
	if index < 0 {
		defs.Groups = append(defs.Groups, prospective)
	} else {
		defs.Groups[index] = prospective
	}
	if err := CheckDefinitions(defs); err != nil {
		return groupModePreparation{}, err
	}
	budget, err := a.budget(ctx, defs)
	if err != nil {
		return groupModePreparation{}, err
	}
	result.old, result.candidate = old, candidate
	result.preview = GroupModePreview{Mode: candidate, Definitions: defs, Budget: budget}
	return result, nil
}

func (a *GroupAdministrator) Preview(ctx context.Context, groupID int64, input GroupModeInput) (GroupModePreview, error) {
	if err := a.available(); err != nil {
		return GroupModePreview{}, err
	}
	ctx, release, err := a.gate.Read(ctx)
	if err != nil {
		return GroupModePreview{}, err
	}
	defer release()
	prepared, err := a.prepare(ctx, groupID, input, nil)
	if err != nil {
		return GroupModePreview{}, err
	}
	return prepared.preview, nil
}
func (a *GroupAdministrator) Save(ctx context.Context, groupID int64, input GroupModeInput, expected time.Time) (domain.DestGroupMode, error) {
	if err := a.available(); err != nil {
		return domain.DestGroupMode{}, err
	}
	ctx, release, err := a.gate.Read(ctx)
	if err != nil {
		return domain.DestGroupMode{}, err
	}
	defer release()
	prepared, err := a.prepare(ctx, groupID, input, &expected)
	if err != nil {
		return domain.DestGroupMode{}, err
	}
	if err := a.store.SaveGroupMode(ctx, &prepared.candidate, expected, prepared.initial, a.now().UTC()); err != nil {
		return domain.DestGroupMode{}, err
	}
	if !prepared.candidate.UpdatedAt.Equal(prepared.old.UpdatedAt) {
		a.changed(ctx, groupID)
	}
	return prepared.candidate, nil
}
