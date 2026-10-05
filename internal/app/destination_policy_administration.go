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
