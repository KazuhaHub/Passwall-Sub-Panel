package sqlstore

import (
	"context"
	"reflect"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

func TestLegalDataCollection_DisabledAndDurable(t *testing.T) {
	_, repos := legalTestRepos(t)
	ctx := context.Background()
	for _, settings := range []ports.UISettings{
		{LegalEnabled: false},
		{LegalEnabled: false, SubLogRetentionDays: 14, AuthEventRetentionDays: 30, RiskHWIDCaptureOff: true, RiskRefreshIntervalMinutes: 25},
	} {
		if err := repos.Settings.Save(ctx, settings); err != nil {
			t.Fatal(err)
		}
		got, err := repos.Legal.DataCollection(ctx)
		if err != nil || !reflect.DeepEqual(got, ports.LegalDataCollectionFromSettings(settings)) {
			t.Fatalf("disabled/unpublished disclosure %+v: %v", got, err)
		}
	}
}
