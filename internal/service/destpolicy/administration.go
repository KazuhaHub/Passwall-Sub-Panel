package destpolicy

import (
	"context"
	"fmt"
	"math"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/operationgate"
)

type AdministrationStore interface {
	ReadDefinitions(context.Context) (domain.DestDefinitions, error)
	SavePolicy(context.Context, *domain.DestPolicy, time.Time, time.Time) error
	DeletePolicy(context.Context, int64, time.Time) error
	ReorderPolicies(context.Context, domain.DestAction, []int64, time.Time) error
}

type AdministrationContext struct {
	GroupNames                map[int64]string
	HitWindowDays             int
	PublishedGeneration       int64
	PublishedHasAccessControl bool
}

// HasEnabledAccessControl reads published definitions, not current drafts or
// node receipts. A pause preserves the enabled definitions for resume.
func HasEnabledAccessControl(defs domain.DestDefinitions) bool {
	return slices.ContainsFunc(defs.Policies, func(p domain.DestPolicy) bool { return p.Enabled }) ||
		slices.ContainsFunc(defs.Groups, func(g domain.DestGroupMode) bool { return g.Mode == "allowlist" })
}

type PolicyOverview struct {
	Definitions domain.DestDefinitions
	Context     AdministrationContext
	Budget      Budget
	At          time.Time
}

type Administrator struct {
	store                AdministrationStore
	readContext          func(context.Context) (AdministrationContext, error)
	readPublishedContext func(context.Context) (int64, bool, error)
	budget               func(context.Context, domain.DestDefinitions) (Budget, error)
	gate                 *operationgate.Gate
	now                  func() time.Time
}

func NewAdministrator(store AdministrationStore, readContext func(context.Context) (AdministrationContext, error), budget func(context.Context, domain.DestDefinitions) (Budget, error)) *Administrator {
	return &Administrator{store: store, readContext: readContext, budget: budget, now: time.Now}
}
func (a *Administrator) SetOperationGate(gate *operationgate.Gate) { a.gate = gate }

// Published facts belong to the overview only. Draft preview/write paths must
// remain able to repair definitions when an old published snapshot is corrupt.
func (a *Administrator) SetPublishedContextReader(reader func(context.Context) (int64, bool, error)) {
	a.readPublishedContext = reader
}
func (a *Administrator) available() error {
	if a == nil || a.store == nil || a.readContext == nil || a.budget == nil {
		return domain.ErrUnavailable
	}
	return nil
}
func (a *Administrator) Read(ctx context.Context) (PolicyOverview, error) {
	if err := a.available(); err != nil {
		return PolicyOverview{}, err
	}
	ctx, release, err := a.gate.Read(ctx)
	if err != nil {
		return PolicyOverview{}, err
	}
	defer release()
	defs, err := a.store.ReadDefinitions(ctx)
	if err != nil {
		return PolicyOverview{}, err
	}
	metadata, err := a.readContext(ctx)
	if err != nil {
		return PolicyOverview{}, err
	}
	if a.readPublishedContext != nil {
		metadata.PublishedGeneration, metadata.PublishedHasAccessControl, err = a.readPublishedContext(ctx)
		if err != nil {
			return PolicyOverview{}, err
		}
	}
	budget, err := a.budget(ctx, defs)
	if err != nil {
		return PolicyOverview{}, err
	}
	return PolicyOverview{Definitions: defs, Context: metadata, Budget: budget, At: a.now().UTC()}, nil
}

// prepare validates the proposed definition without changing stored definitions
// or the caller's form. Priority here models the SQL writer's segment assignment;
// only the writer allocates the committed ID, priority and edit version.
func (a *Administrator) prepare(ctx context.Context, candidate domain.DestPolicy, expected time.Time) (domain.DestDefinitions, error) {
	if candidate.ID < 0 {
		return domain.DestDefinitions{}, invalid("id")
	}
	if strings.TrimSpace(candidate.Name) == "" || utf8.RuneCountInString(candidate.Name) > 128 {
		return domain.DestDefinitions{}, invalid("name")
	}
	if utf8.RuneCountInString(candidate.TemplateKey) > 32 {
		return domain.DestDefinitions{}, invalid("template_key")
	}
	if candidate.Action != domain.DestAllow && candidate.Action != domain.DestBlock && candidate.Action != domain.DestObserve {
		return domain.DestDefinitions{}, invalid("action")
	}
	if candidate.Scope != domain.DestScopeAll && candidate.Scope != domain.DestScopeGroups {
		return domain.DestDefinitions{}, invalid("scope")
	}
	for _, id := range candidate.ListIDs {
		if id <= 0 {
			return domain.DestDefinitions{}, invalid("list_ids")
		}
	}
	for _, id := range candidate.GroupIDs {
		if id <= 0 {
			return domain.DestDefinitions{}, invalid("group_ids")
		}
	}
	candidate.ListIDs, candidate.GroupIDs = uniqueIDs(candidate.ListIDs), uniqueIDs(candidate.GroupIDs)
	if candidate.Scope == domain.DestScopeAll && len(candidate.GroupIDs) > 0 || candidate.Scope == domain.DestScopeGroups && len(candidate.GroupIDs) == 0 {
		return domain.DestDefinitions{}, invalid("group_ids")
	}
	defs, err := a.store.ReadDefinitions(ctx)
	if err != nil {
		return domain.DestDefinitions{}, err
	}
	metadata, err := a.readContext(ctx)
	if err != nil {
		return domain.DestDefinitions{}, err
	}
	for _, id := range candidate.GroupIDs {
		if _, exists := metadata.GroupNames[id]; !exists {
			return domain.DestDefinitions{}, invalid("group_ids")
		}
	}
	lists := map[int64]domain.DestList{}
	for _, list := range defs.Lists {
		lists[list.ID] = list
	}
	for _, id := range candidate.ListIDs {
		list, exists := lists[id]
		if !exists || list.OwnerGroupID != 0 {
			return domain.DestDefinitions{}, invalid("list_ids")
		}
		if list.Kind == domain.DestListCustom && list.EntryCount == 0 {
			return domain.DestDefinitions{}, fmt.Errorf("%w: dest_policy_no_match", domain.ErrValidation)
		}
	}
	if len(candidate.ListIDs) == 0 && len(candidate.Inline.CIDRs) == 0 && candidate.Inline.Ports == "" && candidate.Inline.Network == "" && len(candidate.Inline.Protocols) == 0 && !candidate.Inline.Private {
		return domain.DestDefinitions{}, fmt.Errorf("%w: dest_policy_no_match", domain.ErrValidation)
	}
	var maxID int64
	highest := 0
	index := -1
	for i, old := range defs.Policies {
		maxID = max(maxID, old.ID)
		if old.Action == candidate.Action {
			highest = max(highest, old.Priority)
		}
		if old.ID == candidate.ID {
			index = i
			if !expected.IsZero() && !old.UpdatedAt.Equal(expected) {
				return domain.DestDefinitions{}, fmt.Errorf("%w: dest_policy_stale", domain.ErrConflict)
			}
			candidate.CreatedAt, candidate.UpdatedAt = old.CreatedAt, old.UpdatedAt
			candidate.Priority = old.Priority
		}
		if old.ID != candidate.ID && old.Name == candidate.Name {
			return domain.DestDefinitions{}, fmt.Errorf("%w: dest_name_taken", domain.ErrAlreadyExists)
		}
	}
	if candidate.ID > 0 && index < 0 {
		return domain.DestDefinitions{}, domain.ErrNotFound
	}
	if candidate.TemplateKey == domain.DestGlobalExceptionTemplateKey || index >= 0 && defs.Policies[index].TemplateKey == domain.DestGlobalExceptionTemplateKey {
		if index < 0 || candidate.TemplateKey != defs.Policies[index].TemplateKey {
			return domain.DestDefinitions{}, invalid("template_key")
		}
	}
	if index < 0 || defs.Policies[index].Action != candidate.Action {
		if highest == math.MaxInt {
			return domain.DestDefinitions{}, domain.ErrResourceExhausted
		}
		candidate.Priority = highest + 1
	}
	defs.Policies = slices.Clone(defs.Policies)
	if index < 0 {
		if maxID == math.MaxInt64 {
			return domain.DestDefinitions{}, domain.ErrResourceExhausted
		}
		candidate.ID = maxID + 1
		defs.Policies = append(defs.Policies, candidate)
	} else {
		defs.Policies[index] = candidate
	}
	if err := CheckDefinitions(defs); err != nil {
		return domain.DestDefinitions{}, err
	}
	return defs, nil
}

func (a *Administrator) Preview(ctx context.Context, candidate domain.DestPolicy, expected time.Time) (Budget, error) {
	if err := a.available(); err != nil {
		return Budget{}, err
	}
	ctx, release, err := a.gate.Read(ctx)
	if err != nil {
		return Budget{}, err
	}
	defer release()
	defs, err := a.prepare(ctx, candidate, expected)
	if err != nil {
		return Budget{}, err
	}
	return a.budget(ctx, defs)
}
func (a *Administrator) Save(ctx context.Context, candidate *domain.DestPolicy, expected time.Time) error {
	if err := a.available(); err != nil {
		return err
	}
	if candidate == nil || candidate.ID > 0 && expected.IsZero() {
		return invalid("updated_at")
	}
	ctx, release, err := a.gate.Read(ctx)
	if err != nil {
		return err
	}
	defer release()
	prepared := *candidate
	prepared.ListIDs, prepared.GroupIDs = uniqueIDs(prepared.ListIDs), uniqueIDs(prepared.GroupIDs)
	prepared.Inline.CIDRs, prepared.Inline.Protocols = unique(prepared.Inline.CIDRs), unique(prepared.Inline.Protocols)
	if _, err := a.prepare(ctx, prepared, expected); err != nil {
		return err
	}
	if err := a.store.SavePolicy(ctx, &prepared, expected, a.now().UTC()); err != nil {
		return err
	}
	*candidate = prepared
	return nil
}
func (a *Administrator) Delete(ctx context.Context, id int64) error {
	if err := a.available(); err != nil {
		return err
	}
	if id <= 0 {
		return invalid("id")
	}
	return a.gate.RunRead(ctx, func(ctx context.Context) error { return a.store.DeletePolicy(ctx, id, a.now().UTC()) })
}
func (a *Administrator) Order(ctx context.Context, action domain.DestAction, ids []int64) error {
	if err := a.available(); err != nil {
		return err
	}
	return a.gate.RunRead(ctx, func(ctx context.Context) error { return a.store.ReorderPolicies(ctx, action, ids, a.now().UTC()) })
}
