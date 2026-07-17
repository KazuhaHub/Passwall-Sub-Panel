package domain

import "time"

// UserAccessSnapshot is the single, derived view of a user's account and
// proxy-service access. It deliberately uses the existing persisted fields:
// callers get unambiguous decisions without requiring a schema migration.
//
// Enabled remains the account administrator's raw switch. Historical builds
// also wrote expiry / quota suspensions into that account switch; those rows
// are recognized by LegacyServiceEncoding and interpreted as service-only
// restrictions so users can still reach the self-service portal.
type UserAccessSnapshot struct {
	AccountStatus AccountStatus
	ServiceStatus ServiceStatus

	CanLogin     bool
	CanUsePortal bool
	CanSubscribe bool
	ProxyEnabled bool

	AccountReason         AutoDisabledReason
	ServiceReason         AutoDisabledReason
	LegacyServiceEncoding bool
}

// AccountLoginAllowed is the canonical authentication decision for the raw
// account fields. Expiry and traffic-exceeded are historical service-only
// encodings; every other disabled reason remains a hard account lock.
func AccountLoginAllowed(enabled bool, reason AutoDisabledReason) bool {
	return enabled || SelfServiceDisableReason(reason)
}

// AccessSnapshot folds persisted administrative intent together with live
// facts (expiry, traffic and emergency access). Precedence is intentionally
// explicit: an account lock wins over everything, hard service holds win over
// emergency access, then emergency access may temporarily override expiry or
// quota exhaustion.
func (u *User) AccessSnapshot(now time.Time) UserAccessSnapshot {
	if u == nil {
		return UserAccessSnapshot{
			AccountStatus: AccountStatusDisabled,
			ServiceStatus: ServiceStatusAccountDisabled,
		}
	}

	legacyService := !u.Enabled && SelfServiceDisableReason(u.AutoDisabledReason)
	canLogin := AccountLoginAllowed(u.Enabled, u.AutoDisabledReason)
	accountStatus := AccountStatusActive
	if !canLogin {
		switch u.AutoDisabledReason {
		case DisabledPendingDelete:
			accountStatus = AccountStatusPendingDelete
		case DisabledPendingApproval:
			accountStatus = AccountStatusPendingApproval
		case DisabledPendingEmailVerify:
			accountStatus = AccountStatusPendingEmailVerify
		default:
			accountStatus = AccountStatusDisabled
		}
	}

	snapshot := UserAccessSnapshot{
		AccountStatus:         accountStatus,
		CanLogin:              canLogin,
		CanUsePortal:          canLogin,
		AccountReason:         u.AutoDisabledReason,
		LegacyServiceEncoding: legacyService,
	}

	if !canLogin {
		snapshot.ServiceStatus = ServiceStatusAccountDisabled
		return snapshot
	}

	switch u.ServiceDisabledReason {
	case DisabledBlockedClient:
		snapshot.ServiceStatus = ServiceStatusBlockedClient
		snapshot.ServiceReason = DisabledBlockedClient
		return snapshot
	case DisabledServiceManual:
		snapshot.ServiceStatus = ServiceStatusManualSuspended
		snapshot.ServiceReason = DisabledServiceManual
		return snapshot
	}

	if u.EmergencyActive(now) {
		snapshot.ServiceStatus = ServiceStatusEmergencyActive
		snapshot.ServiceReason = u.ServiceDisabledReason
		snapshot.CanSubscribe = true
		snapshot.ProxyEnabled = true
		return snapshot
	}

	// Current service-axis markers remain authoritative for API compatibility.
	// Historical account-axis markers do not: once the underlying expiry/quota
	// fact is resolved, the row becomes effective again without a bulk rewrite.
	if u.IsExpired(now) || u.ServiceDisabledReason == DisabledExpired {
		snapshot.ServiceStatus = ServiceStatusExpired
		snapshot.ServiceReason = DisabledExpired
		return snapshot
	}
	if u.TrafficExceeded() || u.ServiceDisabledReason == DisabledTrafficExceeded {
		snapshot.ServiceStatus = ServiceStatusTrafficExceeded
		snapshot.ServiceReason = DisabledTrafficExceeded
		return snapshot
	}

	snapshot.ServiceStatus = ServiceStatusActive
	snapshot.CanSubscribe = true
	snapshot.ProxyEnabled = true
	return snapshot
}
