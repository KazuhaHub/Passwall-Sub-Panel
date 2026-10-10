package sqlstore

import (
	"context"
	"database/sql"
	"time"

	"github.com/KazuhaHub/passwall-protocol/protocol"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/destpolicy"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func (r *legalRepo) DataCollection(ctx context.Context) (domain.LegalDataCollection, error) {
	var result domain.LegalDataCollection
	err := r.collectionReadTransaction(ctx, func(tx *gorm.DB) error {
		var err error
		result, err = r.collectionFromTransaction(ctx, tx)
		return err
	})
	if err != nil {
		return domain.LegalDataCollection{}, legalCollectionReadError(ctx)
	}
	return result, nil
}

func (r *legalRepo) collectionFromTransaction(ctx context.Context, tx *gorm.DB) (domain.LegalDataCollection, error) {
	settings, err := newKVSettingsRepo(tx).Load(ctx, ports.UISettings{})
	if err != nil {
		return domain.LegalDataCollection{}, err
	}
	now := time.Now().UTC()
	poll := settings.NodePollSeconds
	if poll <= 0 {
		poll = protocol.DefaultNextPollSeconds
	}
	current, err := readDestStatusContext(tx, func(p domain.DestStatusPanel, load func() ([]byte, error)) (domain.DestCollectionFacts, error) {
		if !destpolicy.NeedsCollectionProof(p, time.Duration(poll)*time.Second, now) {
			return domain.DestCollectionFacts{}, nil
		}
		return r.facts.Read(p.Agent.AgentID, p.Runtime.MintedSHA256, load)
	}, false)
	if err != nil {
		return domain.LegalDataCollection{}, err
	}
	if err := ctx.Err(); err != nil {
		return domain.LegalDataCollection{}, err
	}
	result := ports.LegalDataCollectionFromSettings(settings)
	result.Access = destpolicy.BuildCollectionDisclosure(current, settings.DestinationSettings(), poll, now)
	return result, nil
}

func (r *legalRepo) collectionReadTransaction(ctx context.Context, read func(*gorm.DB) error) error {
	var options *sql.TxOptions
	if r.db.Dialector.Name() != "sqlite" {
		options = &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true}
	}
	return r.db.WithContext(ctx).Session(&gorm.Session{Logger: logger.Discard}).Transaction(read, options)
}

func legalCollectionReadError(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return domain.ErrUnavailable
}
