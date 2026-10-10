package app

import (
	"context"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

func (a *App) destinationHits(ctx context.Context, q domain.DestHitQuery) (domain.DestHitPage, error) {
	ctx, release, err := a.operationGate.Read(ctx)
	if err != nil {
		return domain.DestHitPage{}, err
	}
	defer release()
	if a.destHitsRead == nil {
		return domain.DestHitPage{}, domain.ErrUnavailable
	}
	return a.destHitsRead.ReadDestinationHits(ctx, q)
}
