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
	view, err := destpolicy.BuildDestinationStatus(current, settings.DestinationSettings().Effective().PolicyApplyMinSeconds, poll, now)
	if err != nil {
		return destpolicy.DestinationStatus{}, err
	}
	// Historical observations survive turning collection off. Capability and
	// engine checks still distinguish unavailable telemetry from a stored zero;
	// neither history nor a saved mode is proof of current collection.
	ids := []int64{}
	for _, node := range view.Nodes {
		if node.Kind == domain.PanelKindPSP && node.Supports.Hits {
			ids = append(ids, node.PanelID)
		}
	}
	if len(ids) == 0 || a.destAuditRead == nil {
		return view, nil
	}
	stats, err := a.destAuditRead.ReadDestinationAuditPanelStats(ctx, now.Add(-24*time.Hour), now, ids)
	if err != nil {
		return destpolicy.DestinationStatus{}, err
	}
	for i := range view.Nodes {
		if value, ok := stats[view.Nodes[i].PanelID]; ok {
			view.Nodes[i].Hits24h = &value.Hits
			view.Nodes[i].Losses = &value.Losses
		}
	}
	return view, nil
}
