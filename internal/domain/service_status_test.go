package domain

import (
	"testing"
	"time"
)

func TestUserAccountAndServiceStatus(t *testing.T) {
	now := time.Date(2026, 6, 25, 12, 0, 0, 0, time.UTC)
	past := now.Add(-time.Hour)
	future := now.Add(time.Hour)

	cases := []struct {
		name        string
		u           *User
		wantAccount AccountStatus
		wantService ServiceStatus
		wantProxy   bool
	}{
		{
			name:        "active account active service",
			u:           &User{Enabled: true},
			wantAccount: AccountStatusActive,
			wantService: ServiceStatusActive,
			wantProxy:   true,
		},
		{
			name:        "pending approval account disables service",
			u:           &User{Enabled: false, AutoDisabledReason: DisabledPendingApproval},
			wantAccount: AccountStatusPendingApproval,
			wantService: ServiceStatusAccountDisabled,
			wantProxy:   false,
		},
		{
			name:        "blocked client is service suspension only",
			u:           &User{Enabled: true, ServiceDisabledReason: DisabledBlockedClient},
			wantAccount: AccountStatusActive,
			wantService: ServiceStatusBlockedClient,
			wantProxy:   false,
		},
		{
			name:        "expired emergency is temporarily active",
			u:           &User{Enabled: true, ExpireAt: &past, EmergencyUntil: &future},
			wantAccount: AccountStatusActive,
			wantService: ServiceStatusEmergencyActive,
			wantProxy:   true,
		},
		{
			name: "traffic limit exceeded by period usage",
			u: &User{
				Enabled:             true,
				TrafficLimitBytes:   100,
				LifetimeTotalBytes:  150,
				PeriodBaselineBytes: 25,
			},
			wantAccount: AccountStatusActive,
			wantService: ServiceStatusTrafficExceeded,
			wantProxy:   false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.u.AccountStatus(); got != tc.wantAccount {
				t.Fatalf("AccountStatus = %q, want %q", got, tc.wantAccount)
			}
			if got := tc.u.ServiceStatus(now); got != tc.wantService {
				t.Fatalf("ServiceStatus = %q, want %q", got, tc.wantService)
			}
			if got := tc.u.ProxyAccessEnabled(now); got != tc.wantProxy {
				t.Fatalf("ProxyAccessEnabled = %v, want %v", got, tc.wantProxy)
			}
		})
	}
}
