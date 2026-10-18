package domain

import "testing"

func TestRelayIDsStableAcrossLegacyRenameAndReorder(t *testing.T) {
	before := []RelayLine{{Name: "one", Address: "one.example", Enabled: true}, {Name: "two", Address: "two.example", Enabled: true}}
	if !AssignRelayIDs(before) || AssignRelayIDs(before) {
		t.Fatal("assignment must be idempotent")
	}
	next := []RelayLine{{Name: "renamed", Address: "two.example"}, {Name: "one", Address: "one.example", Enabled: true}}
	if err := ReconcileRelayIDs(before, next); err != nil {
		t.Fatal(err)
	}
	if next[0].ID != before[1].ID || next[1].ID != before[0].ID {
		t.Fatal("IDs followed positions rather than lines")
	}
	// Modern clients carry IDs, so even endpoint edits preserve identity.
	next[0].Address = "new.example"
	if err := ReconcileRelayIDs(before, next); err != nil {
		t.Fatal(err)
	}
}

func TestRelayIDsRejectAmbiguousOrUnknownIdentity(t *testing.T) {
	previous := []RelayLine{{ID: "one", Address: "same"}, {ID: "two", Address: "same"}}
	for _, next := range [][]RelayLine{
		{{Address: "same"}}, {{ID: "unknown", Address: "new"}}, {{ID: "one"}, {ID: "one"}},
	} {
		if ReconcileRelayIDs(previous, next) == nil {
			t.Fatalf("accepted ambiguous IDs: %+v", next)
		}
	}
}

func TestLegacyRelaysSharingAnEndpointKeepTheirNamedIdentities(t *testing.T) {
	previous := []RelayLine{{ID: "one", Name: "one", Address: "shared.example", Port: 443}, {ID: "two", Name: "two", Address: "shared.example", Port: 443}}
	next := []RelayLine{{Name: "two", Address: "shared.example", Port: 443}, {Name: "one", Address: "shared.example", Port: 443, Enabled: true}}
	if err := ReconcileRelayIDs(previous, next); err != nil {
		t.Fatal(err)
	}
	if next[0].ID != "two" || next[1].ID != "one" {
		t.Fatal("legacy reorder changed line identities")
	}
}

func TestOriginalLineIDCannotBeClaimedByARelay(t *testing.T) {
	for _, lines := range [][]RelayLine{{{ID: DirectLineID}}, {{ID: "same"}, {ID: "same"}}, {{ID: " "}}} {
		if ValidateRelayIDs(lines) == nil {
			t.Fatal("ambiguous precise-line identity was accepted")
		}
	}
	lines := []RelayLine{{Name: "one"}, {Name: "two"}}
	AssignRelayIDs(lines)
	if err := ValidateRelayIDs(lines); err != nil {
		t.Fatal(err)
	}
}
