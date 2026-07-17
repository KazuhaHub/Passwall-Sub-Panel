package handler

import (
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

func TestToUserAccessDTOPreservesLegacyFieldsAsDerivedAccess(t *testing.T) {
	now := time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)
	future := now.Add(time.Hour)
	u := &domain.User{
		Enabled:            false,
		AutoDisabledReason: domain.DisabledExpired,
		ExpireAt:           &future,
	}

	got := toUserAccessDTO(u, now)
	if got.AccountState != domain.AccountStatusActive || got.ServiceState != domain.ServiceStatusActive {
		t.Fatalf("derived states = account %q service %q", got.AccountState, got.ServiceState)
	}
	if !got.CanLogin || !got.CanUsePortal || !got.CanSubscribe || !got.ProxyEnabled || !got.LegacyServiceEncoding {
		t.Fatalf("derived access = %+v", got)
	}
}
