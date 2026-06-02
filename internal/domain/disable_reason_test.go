package domain

import "testing"

// TestSelfServiceDisableReason pins which auto-disable reasons still let a user
// authenticate (log in AND refresh) to reach the self-service emergency-access
// page: traffic-exceeded and expired can self-rescue; everything else cannot.
func TestSelfServiceDisableReason(t *testing.T) {
	for _, r := range []AutoDisabledReason{DisabledTrafficExceeded, DisabledExpired} {
		if !SelfServiceDisableReason(r) {
			t.Errorf("%q should be self-service allowed", r)
		}
	}
	for _, r := range []AutoDisabledReason{
		DisabledNone, DisabledManual, DisabledPendingDelete, DisabledPendingApproval, DisabledBlockedClient,
	} {
		if SelfServiceDisableReason(r) {
			t.Errorf("%q must NOT be self-service allowed", r)
		}
	}
}
