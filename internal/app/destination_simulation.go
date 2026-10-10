package app

import (
	"context"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/destpolicy"
)

func (a *App) destinationTest(ctx context.Context, input destpolicy.DestinationTestInput) (destpolicy.DestinationTestResult, error) {
	ctx, release, err := a.operationGate.Read(ctx)
	if err != nil {
		return destpolicy.DestinationTestResult{}, err
	}
	defer release()
	settings, err := a.settings.Load(ctx, ports.UISettings{})
	if err != nil {
		return destpolicy.DestinationTestResult{}, err
	}
	current, err := a.destDefinitions.TestContext(ctx, input.UserID, input.PanelID)
	if err != nil {
		return destpolicy.DestinationTestResult{}, err
	}
	return destpolicy.EvaluateDestinationTest(input, current, time.Duration(settings.NodePollSeconds)*time.Second, time.Now().UTC())
}
