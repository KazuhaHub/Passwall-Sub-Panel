package app

import (
	"context"
	"math"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/destpolicy"
	"github.com/KazuhaHub/passwall-sub-panel/internal/transport/http/handler"
)

func (a *App) destinationListOverview(ctx context.Context) (handler.DestinationListOverview, error) {
	defs, err := a.destDefinitions.ReadDefinitions(ctx)
	if err != nil {
		return handler.DestinationListOverview{}, err
	}
	refs, err := a.destDefinitions.ListReferences(ctx, defs)
	if err != nil {
		return handler.DestinationListOverview{}, err
	}
	hours, err := a.destinationRefreshHours(ctx)
	if err != nil {
		return handler.DestinationListOverview{}, err
	}
	budget, err := a.destinationBudget(ctx, defs)
	if err != nil {
		return handler.DestinationListOverview{}, err
	}
	return handler.DestinationListOverview{Lists: defs.Lists, UsedBy: refs, RefreshHours: hours, Budget: budget}, nil
}

func (a *App) destinationBudget(ctx context.Context, defs domain.DestDefinitions) (destpolicy.Budget, error) {
	panels, err := a.repos.XUIPanel.List(ctx)
	if err != nil {
		return destpolicy.Budget{}, err
	}
	var inputs []destpolicy.RosterInput
	for _, panel := range panels {
		members, err := a.destTagMembers.TagMatchedMembers(ctx, panel.ID)
		if err != nil {
			return destpolicy.Budget{}, err
		}
		input := destpolicy.RosterInput{UserGroups: map[int64]int64{}}
		for groupID, users := range members {
			for _, userID := range users {
				input.UserIDs = append(input.UserIDs, userID)
				input.UserGroups[userID] = groupID
			}
		}
		inputs = append(inputs, input)
	}
	return destpolicy.DefinitionBudget(defs, inputs)
}

func (a *App) validateDestinationListSave(ctx context.Context, list domain.DestList) error {
	defs, err := a.destDefinitions.ReadDefinitions(ctx)
	if err != nil {
		return err
	}
	found := false
	maxID := int64(0)
	for i, old := range defs.Lists {
		if old.ID > maxID {
			maxID = old.ID
		}
		if old.ID == list.ID {
			defs.Lists[i] = list
			found = true
		}
	}
	if !found {
		if list.ID != 0 {
			return domain.ErrNotFound
		}
		if maxID == math.MaxInt64 {
			return domain.ErrResourceExhausted
		}
		list.ID = maxID + 1
		defs.Lists = append(defs.Lists, list)
	}
	return destpolicy.CheckDefinitions(defs)
}
