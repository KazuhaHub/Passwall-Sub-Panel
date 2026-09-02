package domain

import "testing"

// The table is written from upstream's own gates rather than from the
// implementation, so a change to either side has to be reconciled here:
//
//	Run()            returns early unless isFail2banEnabled()
//	resolveEnforce() returns false when !windows && !installed
func TestClassifyIPLimit(t *testing.T) {
	cases := []struct {
		name string
		in   Fail2banStatus
		want IPLimitEnforcement
	}{
		{
			name: "everything in place",
			in:   Fail2banStatus{Enabled: true, Installed: true, Usable: true},
			want: IPLimitEnforcementEnforced,
		},
		{
			name: "gate open, fail2ban absent",
			in:   Fail2banStatus{Enabled: true},
			want: IPLimitEnforcementNotInstalled,
		},
		{
			name: "env var set to something that is not the literal true",
			in:   Fail2banStatus{Installed: true},
			want: IPLimitEnforcementDisabled,
		},
		{
			name: "windows needs no fail2ban",
			in:   Fail2banStatus{Enabled: true, Windows: true},
			want: IPLimitEnforcementDisconnectOnly,
		},
		{
			// The trap: upstream's Run() checks the env var BEFORE it looks at
			// the platform, so a disabled Windows node enforces nothing. Reading
			// Windows first would report this one as working.
			name: "windows with the env var disabled is still disabled",
			in:   Fail2banStatus{Windows: true, Installed: true},
			want: IPLimitEnforcementDisabled,
		},
		{
			// Usable is upstream's composite and is trusted over our own
			// recomputation, so a future third gate demotes us rather than
			// leaving PSP claiming enforcement forever.
			name: "installed but upstream says not usable",
			in:   Fail2banStatus{Enabled: true, Installed: true},
			want: IPLimitEnforcementNotInstalled,
		},
		{
			// A Windows node CAN run fail2ban, and then it gets the full
			// treatment. Reading the platform before the composite would
			// downgrade it to disconnect_only and understate what it does.
			name: "windows with a working fail2ban is fully enforced",
			in:   Fail2banStatus{Enabled: true, Installed: true, Usable: true, Windows: true},
			want: IPLimitEnforcementEnforced,
		},
		{
			name: "windows, installed, but upstream says not usable",
			in:   Fail2banStatus{Enabled: true, Installed: true, Windows: true},
			want: IPLimitEnforcementDisconnectOnly,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ClassifyIPLimit(tc.in); got != tc.want {
				t.Fatalf("ClassifyIPLimit(%+v) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// Unknown is the state a fleet sits in the moment the probe breaks, and it must
// not read as either good news or a call to action. Both halves are asserted
// because a mutation that made Unknown "enforcing" would hide every dead limit,
// and one that made it "actionable" would tell operators to install fail2ban on
// nodes that already have it.
func TestUnknownIsNeitherReassuringNorAccusing(t *testing.T) {
	for _, e := range []IPLimitEnforcement{IPLimitEnforcementUnknown, IPLimitEnforcementUnsupported} {
		if e.Enforcing() {
			t.Fatalf("%q must not read as enforcing: an unread precondition is not a working one", e)
		}
		if e.Actionable() {
			t.Fatalf("%q must not read as actionable: we have no idea what is wrong, if anything", e)
		}
	}
}

func TestEnforcingAndActionablePartitionTheKnownStates(t *testing.T) {
	known := []IPLimitEnforcement{
		IPLimitEnforcementEnforced,
		IPLimitEnforcementDisconnectOnly,
		IPLimitEnforcementDisabled,
		IPLimitEnforcementNotInstalled,
	}
	for _, e := range known {
		if e.Enforcing() == e.Actionable() {
			t.Fatalf("%q is %v for both Enforcing and Actionable; every known state is exactly one",
				e, e.Enforcing())
		}
	}
}

// disconnect_only is deliberately Enforcing: the cap does drop the client, it
// just cannot keep the IP out afterwards. Reporting it as broken would send
// operators chasing a fail2ban install that upstream does not use on Windows.
func TestDisconnectOnlyCounts(t *testing.T) {
	if !IPLimitEnforcementDisconnectOnly.Enforcing() {
		t.Fatal("windows still disconnects the offending client; that is enforcement")
	}
}

func TestValidRejectsJunkFromStorage(t *testing.T) {
	for _, e := range []IPLimitEnforcement{
		IPLimitEnforcementUnknown, IPLimitEnforcementUnsupported,
		IPLimitEnforcementEnforced, IPLimitEnforcementDisconnectOnly,
		IPLimitEnforcementDisabled, IPLimitEnforcementNotInstalled,
	} {
		if !e.Valid() {
			t.Fatalf("%q should be valid", e)
		}
	}
	for _, e := range []IPLimitEnforcement{"", "true", "ENFORCED", "ok"} {
		if e.Valid() {
			t.Fatalf("%q should not be valid", e)
		}
	}
}
