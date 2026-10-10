package ports

import (
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

func LegalDataCollectionFromSettings(s UISettings) domain.LegalDataCollection {
	rt := domain.RiskRuntimeFromSettings(s.RiskRuntimeSettings())
	return domain.LegalDataCollection{
		SubLogRetentionDays:                 max(s.SubLogRetentionDays, 0),
		AuthEventRetentionDays:              max(s.AuthEventRetentionDays, 0),
		ConnectionRetentionDays:             rt.ConnectionRetentionDays,
		HWIDCaptured:                        !s.RiskHWIDCaptureOff,
		HWIDRetentionDays:                   max(s.SubLogRetentionDays, 0),
		FlagRecordRetentionDays:             rt.FlagRecordRetentionDays,
		RiskAssessmentRefreshMinutes:        int(rt.RefreshInterval.Minutes()),
		RiskReviewPurgeAfterDeletionMinutes: 60,
		Access:                              []domain.LegalAccessCollection{},
	}
}
