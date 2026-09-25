package domain

import (
	"testing"
	"time"
)

// everyAutoDisabledReason is the full AutoDisabledReason space. Shared by the
// guards below so a reason added to the enum is added in one place and every
// guard then holds it to account.
var everyAutoDisabledReason = []AutoDisabledReason{
	DisabledNone, DisabledTrafficExceeded, DisabledExpired, DisabledManual,
	DisabledPendingDelete, DisabledPendingApproval, DisabledBlockedClient,
	DisabledServiceManual, DisabledPendingEmailVerify, DisabledGeoAnomaly,
	DisabledGeoAutoSuspend,
}

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
	all := everyAutoDisabledReason
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

// The detector's own suspension is held to the same bar as a human's geo
// suspension, and for the same reasons — plus one more: it lifts itself on a
// timer, so the user has to be able to sign in and read when.
func TestGeoAutoSuspensionKeepsPanelLogin(t *testing.T) {
	u := &User{ID: 1, Enabled: true, ServiceDisabledReason: DisabledGeoAutoSuspend}
	snap := u.AccessSnapshot(time.Now())

	if !snap.CanLogin || !snap.CanUsePortal {
		t.Fatal("an automatic geo suspension must not lock the user out of the panel")
	}
	if snap.ProxyEnabled || snap.CanSubscribe {
		t.Fatalf("proxy access must be cut, got ProxyEnabled=%v CanSubscribe=%v", snap.ProxyEnabled, snap.CanSubscribe)
	}
	// ManualSuspended is reused as the STATUS so the SPA's resume button and
	// badges work unchanged; the REASON is what tells a machine's decision
	// from a person's.
	if snap.ServiceStatus != ServiceStatusManualSuspended {
		t.Fatalf("ServiceStatus = %q, want %q", snap.ServiceStatus, ServiceStatusManualSuspended)
	}
	if snap.ServiceReason != DisabledGeoAutoSuspend {
		t.Fatalf("ServiceReason = %q, want %q", snap.ServiceReason, DisabledGeoAutoSuspend)
	}
	u.ServiceDisabledReason = DisabledNone
	if back := u.AccessSnapshot(time.Now()); !back.ProxyEnabled {
		t.Fatal("clearing the reason must restore proxy access")
	}
}

// HardServiceHold is the name the quota, rollover and emergency machinery ask
// before they touch a service reason, and AccessSnapshot is what decides who
// actually gets proxy access. The two must describe the same set, or one of
// two things happens: a hold that AccessSnapshot ranks above emergency access
// gets overwritten by a quota suspension and later lifted by a rollover (the
// hold silently evaporates), or a reason emergency access CAN override is
// shielded from the quota path (the user is never re-suspended when the window
// ends).
//
// So the predicate is pinned to behaviour rather than to a list: with a live
// emergency window, a writable service reason is a hard hold exactly when the
// snapshot still refuses service, and nothing outside the service axis is a
// hard hold at all.
func TestHardServiceHoldIsExactlyWhatOutranksEmergency(t *testing.T) {
	now := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	future := now.Add(time.Hour)

	for _, r := range everyAutoDisabledReason {
		if !ServiceSuspensionReason(r) {
			if HardServiceHold(r) {
				t.Errorf("HardServiceHold(%q) = true, but %q cannot be written to service_disabled_reason", r, r)
			}
			continue
		}
		u := &User{
			ID: 1, Enabled: true,
			ServiceDisabledReason: r,
			EmergencyUntil:        &future,
		}
		snap := u.AccessSnapshot(now)
		outranks := snap.ServiceStatus != ServiceStatusEmergencyActive && !snap.ProxyEnabled
		if got := HardServiceHold(r); got != outranks {
			t.Errorf("HardServiceHold(%q) = %v, but with a live emergency window the snapshot is %q (proxy=%v)",
				r, got, snap.ServiceStatus, snap.ProxyEnabled)
		}
	}
}
