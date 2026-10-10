package app

import (
	"context"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

func (a *App) destinationUserAccess(ctx context.Context, userID int64) (domain.DestUserAccess, error) {
	ctx, release, err := a.operationGate.Read(ctx)
	if err != nil {
		return domain.DestUserAccess{}, err
	}
	defer release()
	access, err := a.destDefinitions.UserAccess(ctx, userID)
	if err != nil {
		return domain.DestUserAccess{}, err
	}
	if a.destUserHitsRead == nil {
		return domain.DestUserAccess{}, domain.ErrUnavailable
	}
	settings, err := a.settings.Load(ctx, ports.UISettings{})
	if err != nil {
		return domain.DestUserAccess{}, domain.ErrUnavailable
	}
	now := time.Now().UTC()
	days := min(7, settings.DestinationSettings().Effective().HitRetentionDays)
	hits, err := a.destUserHitsRead.ReadDestinationUserHits(ctx, userID, now.Add(-time.Duration(days)*24*time.Hour), now)
	if err != nil {
		return domain.DestUserAccess{}, domain.ErrUnavailable
	}
	hits.Days = days
	available := false
	usageAvailable := false
	usageNodes := []domain.DestUsageNode{}
	if len(hits.ClientPanelIDs) > 0 {
		related := make(map[int64]bool, len(hits.ClientPanelIDs))
		for _, id := range hits.ClientPanelIDs {
			related[id] = true
		}
		status, err := a.destinationStatusMetadata(ctx, settings, now, related)
		if err != nil {
			return domain.DestUserAccess{}, domain.ErrUnavailable
		}
		for _, node := range status.Nodes {
			if related[node.PanelID] && node.Collecting {
				available = true
			}
			if related[node.PanelID] && node.UsageCollecting {
				usageAvailable = true
				usageNodes = append(usageNodes, domain.DestUsageNode{PanelID: node.PanelID, Name: node.PanelName})
			}
		}
	}
	access.HitsAvailable, access.RecentHits = &available, &hits
	access.UsageAvailable, access.UsageNodes, access.UsageRetentionDays = &usageAvailable, usageNodes, settings.DestinationSettings().Effective().UsageRetentionDays
	return access, nil
}
