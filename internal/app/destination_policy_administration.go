package app

import (
	"context"

	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/destpolicy"
)

func (a *App) destinationPolicyContext(ctx context.Context) (destpolicy.AdministrationContext, error) {
	settings, err := a.settings.Load(ctx, ports.UISettings{})
	if err != nil {
		return destpolicy.AdministrationContext{}, err
	}
	groups, err := a.repos.Group.List(ctx)
	if err != nil {
		return destpolicy.AdministrationContext{}, err
	}
	names := map[int64]string{}
	for _, g := range groups {
		names[g.ID] = g.Name
	}
	return destpolicy.AdministrationContext{GroupNames: names, HitWindowDays: min(7, settings.DestinationSettings().Effective().HitRetentionDays)}, nil
}

func (a *App) destinationPublishedAccessContext(ctx context.Context) (int64, bool, error) {
	state, snapshot, published, err := a.destDefinitions.PublishedState(ctx)
	if err != nil {
		return 0, false, err
	}
	hasAccessControl := false
	if published {
		defs, err := destpolicy.DecodeDefinitionSnapshot(snapshot)
		if err != nil {
			return 0, false, err
		}
		hasAccessControl = destpolicy.HasEnabledAccessControl(defs)
	}
	return state.PublishedGeneration, hasAccessControl, nil
}
