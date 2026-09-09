package domain

import (
	"testing"
	"time"
)

// Every reason ServiceSuspensionReason accepts must actually suspend the user.
//
// The two live in different files and neither mentions the other, so adding a
// reason to the allowlist without a matching case in AccessSnapshot compiles,
// passes review, and writes the row — and then falls through every branch to
// ServiceStatusActive with ProxyEnabled true. The suspension is committed and
// does nothing, with no error anywhere. That is the exact failure family
// v3.9.2-beta.20 was released to fix, and DisabledGeoAnomaly nearly reproduced
// it while being added.
//
// Bounding this to the reasons the column will accept is what makes it a
// closed set: a reason that cannot be written cannot suspend anyone, and a
// reason that can be written must.
func TestEverySuspensionReasonActuallySuspends(t *testing.T) {
	// The full AutoDisabledReason space; ServiceSuspensionReason picks the
	// subset this guard then holds to account.
	all := []AutoDisabledReason{
		DisabledNone, DisabledTrafficExceeded, DisabledExpired, DisabledManual,
		DisabledPendingDelete, DisabledPendingApproval, DisabledBlockedClient,
		DisabledServiceManual, DisabledPendingEmailVerify, DisabledGeoAnomaly,
	}
	now := time.Now()

	checked := 0
	for _, r := range all {
		if !ServiceSuspensionReason(r) {
			continue
		}
		checked++
		u := &User{
			ID: 1, Enabled: true,
			ServiceDisabledReason: r,
			// No expiry and no quota, so nothing ELSE can be the thing that
			// suspends them — the reason under test has to do it alone.
			TrafficLimitBytes: 0,
		}
		snap := u.AccessSnapshot(now)
		if snap.ProxyEnabled {
			t.Errorf("reason %q is writable to service_disabled_reason but leaves ProxyEnabled true: "+
				"the suspension would be stored and have no effect. Add a case to AccessSnapshot.", r)
		}
		if snap.CanSubscribe {
			t.Errorf("reason %q leaves CanSubscribe true: the user keeps fetching a working subscription", r)
		}
		if snap.ServiceStatus == ServiceStatusActive {
			t.Errorf("reason %q reports ServiceStatusActive", r)
		}
		// The reason must survive into the snapshot, or the admin side and the
		// false-positive count cannot tell WHY someone was suspended.
		if snap.ServiceReason == "" {
			t.Errorf("reason %q is dropped from the snapshot", r)
		}
	}
	if checked == 0 {
		t.Fatal("no reasons were exercised — the allowlist or this list drifted")
	}
}

// A geo suspension must stay reversible and must keep the panel reachable:
// suspicion is not proof, and a user who cannot log in cannot read why they
// were cut off or dispute it.
func TestGeoSuspensionKeepsPanelLogin(t *testing.T) {
	u := &User{ID: 1, Enabled: true, ServiceDisabledReason: DisabledGeoAnomaly}
	snap := u.AccessSnapshot(time.Now())

	if !snap.CanLogin || !snap.CanUsePortal {
		t.Fatal("a geo suspension must not lock the user out of the panel")
	}
	if snap.ProxyEnabled {
		t.Fatal("proxy access must be cut")
	}
	if snap.ServiceReason != DisabledGeoAnomaly {
		t.Fatalf("ServiceReason = %q, want %q — a resume of THIS reason is the false-positive signal",
			snap.ServiceReason, DisabledGeoAnomaly)
	}
	// Clearing the reason restores service with no other write, which is what
	// "every step must be reversible" means in practice.
	u.ServiceDisabledReason = DisabledNone
	if back := u.AccessSnapshot(time.Now()); !back.ProxyEnabled {
		t.Fatal("clearing the reason must restore proxy access")
	}
}
