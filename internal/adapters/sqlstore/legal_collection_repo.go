package sqlstore

import (
	"context"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

func (r *legalRepo) DataCollection(ctx context.Context) (domain.LegalDataCollection, error) {
	settings, err := newKVSettingsRepo(r.db).Load(ctx, ports.UISettings{})
	if err != nil {
		return domain.LegalDataCollection{}, err
	}
	return ports.LegalDataCollectionFromSettings(settings), nil
}
