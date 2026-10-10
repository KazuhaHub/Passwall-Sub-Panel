package app

import (
	"context"
	"errors"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

func (a *App) destinationUsage(ctx context.Context, q domain.DestUsageQuery) (domain.DestUsagePage, error) {
	ctx, release, err := a.operationGate.Read(ctx)
	if err != nil {
		return domain.DestUsagePage{}, err
	}
	defer release()
	q, err = domain.NormalizeDestinationUsageQuery(q)
	if err != nil {
		return domain.DestUsagePage{}, err
	}
	if a.destUsageRead == nil {
		return domain.DestUsagePage{}, domain.ErrUnavailable
	}
	settings, err := a.settings.Load(ctx, ports.UISettings{})
	if err != nil {
		return domain.DestUsagePage{}, domain.ErrUnavailable
	}
	now := time.Now().UTC()
	retention := time.Duration(settings.DestinationSettings().Effective().UsageRetentionDays) * 24 * time.Hour
	// Storage is hourly. Bound both the duration and the oldest retained bucket;
	// a historical end time must not turn a short query into an archive bypass.
	if q.Until.After(now) || q.Until.Sub(q.Since) > retention || q.Since.Before(now.Add(-retention).Truncate(time.Hour)) {
		return domain.DestUsagePage{}, domain.ErrValidation
	}
	if _, err := a.destDefinitions.UserAccess(ctx, q.UserID); err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return domain.DestUsagePage{}, domain.ErrNotFound
		}
		return domain.DestUsagePage{}, domain.ErrUnavailable
	}
	return a.destUsageRead.ReadDestinationUsage(ctx, q)
}
