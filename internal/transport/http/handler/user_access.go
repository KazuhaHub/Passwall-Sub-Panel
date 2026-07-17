package handler

import (
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// userAccessDTO exposes the derived access decision while the legacy flat
// fields remain in API responses for older clients. It is computed entirely
// from the existing user row; no schema or data migration is involved.
type userAccessDTO struct {
	AccountState domain.AccountStatus `json:"account_state"`
	ServiceState domain.ServiceStatus `json:"service_state"`

	CanLogin     bool `json:"can_login"`
	CanUsePortal bool `json:"can_use_portal"`
	CanSubscribe bool `json:"can_subscribe"`
	ProxyEnabled bool `json:"proxy_enabled"`

	AccountReason         domain.AutoDisabledReason `json:"account_reason,omitempty"`
	ServiceReason         domain.AutoDisabledReason `json:"service_reason,omitempty"`
	LegacyServiceEncoding bool                      `json:"legacy_service_encoding,omitempty"`
}

func toUserAccessDTO(u *domain.User, now time.Time) userAccessDTO {
	return userAccessDTOFromSnapshot(u.AccessSnapshot(now))
}

func userAccessDTOFromSnapshot(a domain.UserAccessSnapshot) userAccessDTO {
	return userAccessDTO{
		AccountState:          a.AccountStatus,
		ServiceState:          a.ServiceStatus,
		CanLogin:              a.CanLogin,
		CanUsePortal:          a.CanUsePortal,
		CanSubscribe:          a.CanSubscribe,
		ProxyEnabled:          a.ProxyEnabled,
		AccountReason:         a.AccountReason,
		ServiceReason:         a.ServiceReason,
		LegacyServiceEncoding: a.LegacyServiceEncoding,
	}
}
