package app

import (
	"context"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

func (a *App) destinationUserAccess(ctx context.Context, userID int64) (domain.DestUserAccess, error) {
	ctx, release, err := a.operationGate.Read(ctx)
	if err != nil {
		return domain.DestUserAccess{}, err
	}
	defer release()
	return a.destDefinitions.UserAccess(ctx, userID)
}
