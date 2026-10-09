package ports

import (
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

func TestLegalDataCollection_UsesActualRetentionAndRefresh(t *testing.T) {
	for _, s := range []UISettings{
		{},
		{SubLogRetentionDays: -1, AuthEventRetentionDays: -3, RiskConnectionRetentionDays: -2, RiskFlagRecordRetentionDays: -1},
		{SubLogRetentionDays: 45, AuthEventRetentionDays: 12, RiskConnectionRetentionDays: 14, RiskFlagRecordRetentionDays: 365, RiskRefreshIntervalMinutes: 30, RiskHWIDCaptureOff: true},
		{RiskConnectionRetentionDays: 999, RiskFlagRecordRetentionDays: 99999, RiskRefreshIntervalMinutes: 99999},
	} {
		got := LegalDataCollectionFromSettings(s)
		rt := domain.RiskRuntimeFromSettings(s.RiskRuntimeSettings())
		if got.SubLogRetentionDays != max(s.SubLogRetentionDays, 0) || got.AuthEventRetentionDays != max(s.AuthEventRetentionDays, 0) ||
			got.ConnectionRetentionDays != rt.ConnectionRetentionDays || got.FlagRecordRetentionDays != rt.FlagRecordRetentionDays ||
			got.RiskAssessmentRefreshMinutes != int(rt.RefreshInterval.Minutes()) || got.HWIDCaptured != !s.RiskHWIDCaptureOff ||
			got.HWIDRetentionDays != got.SubLogRetentionDays || got.RiskReviewPurgeAfterDeletionMinutes != 60 {
			t.Fatalf("settings %+v disclosed %+v; runtime %+v", s, got, rt)
		}
		if got.Access == nil || len(got.Access) != 0 {
			t.Fatalf("without a destination collector, access must be an empty list: %+v", got.Access)
		}
	}
}
