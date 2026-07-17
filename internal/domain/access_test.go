package domain

import (
	"testing"
	"time"
)

func TestUserAccessSnapshotMatrix(t *testing.T) {
	now := time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)
	past := now.Add(-time.Hour)
	future := now.Add(time.Hour)

	tests := []struct {
		name     string
		user     *User
		account  AccountStatus
		service  ServiceStatus
		canLogin bool
		proxy    bool
		legacy   bool
	}{
		{name: "nil", account: AccountStatusDisabled, service: ServiceStatusAccountDisabled},
		{name: "active", user: &User{Enabled: true}, account: AccountStatusActive, service: ServiceStatusActive, canLogin: true, proxy: true},
		{name: "manual account suspension", user: &User{Enabled: false, AutoDisabledReason: DisabledManual}, account: AccountStatusDisabled, service: ServiceStatusAccountDisabled},
		{name: "pending approval", user: &User{Enabled: false, AutoDisabledReason: DisabledPendingApproval}, account: AccountStatusPendingApproval, service: ServiceStatusAccountDisabled},
		{name: "manual service pause", user: &User{Enabled: true, ServiceDisabledReason: DisabledServiceManual}, account: AccountStatusActive, service: ServiceStatusManualSuspended, canLogin: true},
		{name: "policy block wins over emergency", user: &User{Enabled: true, ServiceDisabledReason: DisabledBlockedClient, EmergencyUntil: &future}, account: AccountStatusActive, service: ServiceStatusBlockedClient, canLogin: true},
		{name: "expired", user: &User{Enabled: true, ExpireAt: &past}, account: AccountStatusActive, service: ServiceStatusExpired, canLogin: true},
		{name: "quota exhausted", user: &User{Enabled: true, TrafficLimitBytes: 10, LifetimeTotalBytes: 10}, account: AccountStatusActive, service: ServiceStatusTrafficExceeded, canLogin: true},
		{name: "emergency overrides expiry", user: &User{Enabled: true, ExpireAt: &past, EmergencyUntil: &future}, account: AccountStatusActive, service: ServiceStatusEmergencyActive, canLogin: true, proxy: true},
		{name: "legacy expired remains service-only", user: &User{Enabled: false, AutoDisabledReason: DisabledExpired, ExpireAt: &past}, account: AccountStatusActive, service: ServiceStatusExpired, canLogin: true, legacy: true},
		{name: "legacy expired converges after renewal", user: &User{Enabled: false, AutoDisabledReason: DisabledExpired, ExpireAt: &future}, account: AccountStatusActive, service: ServiceStatusActive, canLogin: true, proxy: true, legacy: true},
		{name: "legacy quota converges after reset", user: &User{Enabled: false, AutoDisabledReason: DisabledTrafficExceeded, TrafficLimitBytes: 10, LifetimeTotalBytes: 3}, account: AccountStatusActive, service: ServiceStatusActive, canLogin: true, proxy: true, legacy: true},
		{name: "legacy blocked client stays account disabled", user: &User{Enabled: false, AutoDisabledReason: DisabledBlockedClient}, account: AccountStatusDisabled, service: ServiceStatusAccountDisabled},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.user.AccessSnapshot(now)
			if got.AccountStatus != tt.account || got.ServiceStatus != tt.service || got.CanLogin != tt.canLogin || got.ProxyEnabled != tt.proxy || got.LegacyServiceEncoding != tt.legacy {
				t.Fatalf("AccessSnapshot = %+v, want account=%q service=%q login=%v proxy=%v legacy=%v", got, tt.account, tt.service, tt.canLogin, tt.proxy, tt.legacy)
			}
			if got.CanSubscribe != got.ProxyEnabled {
				t.Fatalf("CanSubscribe=%v, ProxyEnabled=%v", got.CanSubscribe, got.ProxyEnabled)
			}
		})
	}
}

func TestAccountLoginAllowed(t *testing.T) {
	if !AccountLoginAllowed(true, DisabledManual) {
		t.Fatal("enabled account should be loginable")
	}
	for _, reason := range []AutoDisabledReason{DisabledExpired, DisabledTrafficExceeded} {
		if !AccountLoginAllowed(false, reason) {
			t.Fatalf("legacy service reason %q should permit self-service login", reason)
		}
	}
	for _, reason := range []AutoDisabledReason{DisabledNone, DisabledManual, DisabledBlockedClient, DisabledPendingApproval, DisabledPendingDelete, DisabledPendingEmailVerify} {
		if AccountLoginAllowed(false, reason) {
			t.Fatalf("account reason %q should block login", reason)
		}
	}
}

func TestDisableReasonAxesDoNotOverlap(t *testing.T) {
	accountReasons := []AutoDisabledReason{DisabledManual, DisabledPendingDelete, DisabledPendingApproval, DisabledPendingEmailVerify}
	serviceReasons := []AutoDisabledReason{DisabledServiceManual, DisabledBlockedClient, DisabledTrafficExceeded, DisabledExpired}
	for _, reason := range accountReasons {
		if !AccountDisableReason(reason) || ServiceSuspensionReason(reason) {
			t.Fatalf("account reason %q crossed axes", reason)
		}
	}
	for _, reason := range serviceReasons {
		if !ServiceSuspensionReason(reason) || AccountDisableReason(reason) {
			t.Fatalf("service reason %q crossed axes", reason)
		}
	}
}
