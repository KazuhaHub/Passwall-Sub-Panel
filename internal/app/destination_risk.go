package app

import (
	"context"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

type destinationRiskReader struct{ app *App }

func (r destinationRiskReader) ReadDestinationRisk(ctx context.Context, at time.Time) (map[int64]domain.DestBlockInput, error) {
	return r.app.destinationRiskInputs(ctx, at)
}

// destinationRiskInputs proves collection once for the union of current
// client panels. It never performs per-user SQL or reads fleet hit totals.
// Admission covers both the counter snapshot and the current metadata read.
func (a *App) destinationRiskInputs(ctx context.Context, at time.Time) (map[int64]domain.DestBlockInput, error) {
	ctx, release, err := a.operationGate.Read(ctx)
	if err != nil {
		return nil, err
	}
	defer release()
	if a.destRiskRead == nil {
		return nil, domain.ErrUnavailable
	}
	window, err := a.destRiskRead.ReadDestinationRiskWindow(ctx, at.Add(-domain.RiskDestBlockWindowHours*time.Hour), at)
	if err != nil {
		return nil, destinationRiskReadError(ctx)
	}
	related := map[int64]bool{}
	for _, user := range window.Users {
		for _, panel := range user.ClientPanelIDs {
			if panel > 0 {
				related[panel] = true
			}
		}
	}
	collecting := map[int64]bool{}
	if len(related) > 0 {
		settings, err := a.settings.Load(ctx, ports.UISettings{})
		if err != nil {
			return nil, destinationRiskReadError(ctx)
		}
		status, err := a.destinationStatusMetadata(ctx, settings, at, related)
		if err != nil {
			return nil, destinationRiskReadError(ctx)
		}
		for _, node := range status.Nodes {
			if related[node.PanelID] && node.Collecting {
				collecting[node.PanelID] = true
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	out := make(map[int64]domain.DestBlockInput, len(window.Users))
	for id, user := range window.Users {
		in := domain.DestBlockInput{Sources: user.Sources, Losses: user.Losses}
		seen := map[int64]bool{}
		for _, panel := range user.ClientPanelIDs {
			if collecting[panel] && !seen[panel] {
				in.CollectingNodes++
				seen[panel] = true
			}
		}
		out[id] = in
	}
	return out, nil
}

func destinationRiskReadError(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return domain.ErrUnavailable
}
