package destpolicy

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/destlist"
)

func groupExceptionPatch(list domain.DestList, groupID int64, entry string) (domain.DestList, error) {
	if list.Kind != domain.DestListCustom || list.OwnerGroupID != groupID {
		return domain.DestList{}, fmt.Errorf("%w: dest_exception_conflict", domain.ErrConflict)
	}
	for _, old := range strings.Split(string(list.Entries), "\n") {
		if old == entry {
			return list, nil
		}
	}
	return destlist.PrepareEntryPatch(list, true, []string{entry}, nil)
}

func hypotheticalGroupException(defs domain.DestDefinitions, groupID int64, entry string) (domain.DestDefinitions, error) {
	var extraID int64
	for _, g := range defs.Groups {
		if g.GroupID != groupID {
			continue
		}
		if g.Mode != "allowlist" || g.Stage != "trial" && g.Stage != "enforce" || g.ExtraListID <= 0 || g.ExtraListID == g.BaseListID {
			return domain.DestDefinitions{}, fmt.Errorf("%w: dest_mode_invalid_transition", domain.ErrConflict)
		}
		extraID = g.ExtraListID
		break
	}
	if extraID == 0 {
		return domain.DestDefinitions{}, fmt.Errorf("%w: dest_group_not_found", domain.ErrNotFound)
	}
	defs.Lists = slices.Clone(defs.Lists)
	for i, list := range defs.Lists {
		if list.ID != extraID {
			continue
		}
		patched, err := groupExceptionPatch(list, groupID, entry)
		if err != nil {
			return domain.DestDefinitions{}, err
		}
		defs.Lists[i] = patched
		return defs, CheckDefinitions(defs)
	}
	return domain.DestDefinitions{}, fmt.Errorf("%w: dest_exception_conflict", domain.ErrConflict)
}

// The repository resolves the current group's extra-list identity again under
// the definition lock. Preflight never supplies a stale list ID or full source.
func (m *ExceptionManager) Group(ctx context.Context, groupID int64, target, match string) (ExceptionResult, error) {
	if m == nil || m.store == nil {
		return ExceptionResult{}, domain.ErrUnavailable
	}
	if groupID <= 0 {
		return ExceptionResult{}, invalid("group_id")
	}
	entry, err := exceptionEntry(target, match)
	if err != nil {
		return ExceptionResult{}, err
	}
	ctx, release, err := m.gate.Read(ctx)
	if err != nil {
		return ExceptionResult{}, err
	}
	defer release()
	mode, err := m.store.GetGroupMode(ctx, groupID)
	if err != nil {
		return ExceptionResult{}, err
	}
	if mode.Mode != "allowlist" {
		return ExceptionResult{}, fmt.Errorf("%w: dest_mode_invalid_transition", domain.ErrConflict)
	}
	defs, err := m.store.ReadDefinitions(ctx)
	if err != nil {
		return ExceptionResult{}, err
	}
	if _, err := hypotheticalGroupException(defs, groupID, entry); err != nil {
		return ExceptionResult{}, err
	}
	listID, err := m.store.AddGroupException(ctx, groupID, m.now().UTC(), func(list domain.DestList) (domain.DestList, error) { return groupExceptionPatch(list, groupID, entry) })
	if err != nil {
		return ExceptionResult{}, err
	}
	return ExceptionResult{Commit: domain.DestGlobalExceptionCommit{ListID: listID}, Entry: entry}, nil
}
