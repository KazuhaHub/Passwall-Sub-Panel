package destpolicy

import (
	"context"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

type pairedPolicyStore interface {
	SavePolicyWithList(context.Context, *domain.DestPolicy, *domain.DestList, time.Time) error
}

func checkTemplateList(policy domain.DestPolicy, list domain.DestList) error {
	if policy.ID != 0 || list.ID != 0 || list.OwnerGroupID != 0 || list.Kind != domain.DestListGeosite || strings.TrimSpace(list.Name) == "" || utf8.RuneCountInString(list.Name) > 128 || list.GeositeCategory == "" || len(list.GeositeCategory) > 128 || len(list.GeositeAttrs) > 128 || list.SourceURL != "" || len(list.SourceText) != 0 || list.EntryCount == 0 || list.LastError != "" {
		return invalid("new_list")
	}
	return nil
}

// PreviewWithList expands an unsaved cached category in the definition budget.
// Synthetic IDs are confined to this read; opening a template creates no rows.
func (a *Administrator) PreviewWithList(ctx context.Context, candidate domain.DestPolicy, list domain.DestList) (Budget, error) {
	if err := a.available(); err != nil {
		return Budget{}, err
	}
	if err := checkTemplateList(candidate, list); err != nil {
		return Budget{}, err
	}
	ctx, release, err := a.gate.Read(ctx)
	if err != nil {
		return Budget{}, err
	}
	defer release()
	defs, err := a.prepare(ctx, candidate, time.Time{}, list)
	if err != nil {
		return Budget{}, err
	}
	return a.budget(ctx, defs)
}

// SaveWithList commits through one storage transaction. Failed policy validation,
// uniqueness or generation allocation must not leave an unused category list.
func (a *Administrator) SaveWithList(ctx context.Context, candidate *domain.DestPolicy, list *domain.DestList) error {
	if err := a.available(); err != nil {
		return err
	}
	if candidate == nil || list == nil {
		return invalid("new_list")
	}
	if err := checkTemplateList(*candidate, *list); err != nil {
		return err
	}
	store, ok := a.store.(pairedPolicyStore)
	if !ok {
		return domain.ErrUnavailable
	}
	ctx, release, err := a.gate.Read(ctx)
	if err != nil {
		return err
	}
	defer release()
	prepared, category := *candidate, *list
	prepared.ListIDs, prepared.GroupIDs = uniqueIDs(prepared.ListIDs), uniqueIDs(prepared.GroupIDs)
	prepared.Inline.CIDRs, prepared.Inline.Protocols = unique(prepared.Inline.CIDRs), unique(prepared.Inline.Protocols)
	if _, err := a.prepare(ctx, prepared, time.Time{}, category); err != nil {
		return err
	}
	if err := store.SavePolicyWithList(ctx, &prepared, &category, a.now().UTC()); err != nil {
		return err
	}
	*candidate, *list = prepared, category
	return nil
}
