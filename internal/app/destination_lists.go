package app

import (
	"context"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/log"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

func (a *App) destinationRefreshHours(ctx context.Context) (int, error) {
	if a.settings == nil {
		return 0, domain.ErrUnavailable
	}
	settings, err := a.settings.Load(ctx, ports.UISettings{})
	if err != nil {
		return 0, err
	}
	return settings.DestinationSettings().Effective().ListRefreshHours, nil
}

func (a *App) startDestinationListRefresh() {
	if a.destLists == nil {
		return
	}
	a.destLists.Start(a.bgRootCtx, &a.bgWG, a.destinationRefreshHours, func(error) {
		// List-specific errors remain in stored refresh status. Avoid logging
		// source URLs, parser entries or storage-driver text here.
		log.Warn("destination list refresh failed")
	})
}
