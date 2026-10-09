package app

import (
	"context"
	"github.com/KazuhaHub/passwall-protocol/protocol"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/destpolicy"
	"time"
)

func (a *App) destinationStatus(ctx context.Context) (destpolicy.DestinationStatus, error) {
	ctx, release, err := a.operationGate.Read(ctx)
	if err != nil {
		return destpolicy.DestinationStatus{}, err
	}
	defer release()
	settings, err := a.settings.Load(ctx, ports.UISettings{})
	if err != nil {
		return destpolicy.DestinationStatus{}, err
	}
	poll := settings.NodePollSeconds
	if poll <= 0 {
		poll = protocol.DefaultNextPollSeconds
	}
	now := time.Now().UTC()
	current, err := a.destDefinitions.StatusContext(ctx, func(p domain.DestStatusPanel, load func() ([]byte, error)) (domain.DestCollectionFacts, error) {
		if !destpolicy.NeedsCollectionProof(p, time.Duration(poll)*time.Second, now) {
			return domain.DestCollectionFacts{}, nil
		}
		return a.destFacts.Read(p.Agent.AgentID, p.Runtime.MintedSHA256, load)
	})
	if err != nil {
		return destpolicy.DestinationStatus{}, err
	}
	return destpolicy.BuildDestinationStatus(current, settings.DestinationSettings().Effective().PolicyApplyMinSeconds, poll, now)
}
