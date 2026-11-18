package domain

import "testing"

// Goldens: a change here re-keys every non-direct line of every user.
func TestDeriveLineUUIDGolden(t *testing.T) {
	const user = "a265b1ec-cd81-43e7-8239-09f322ef22d6"
	line := LineKey{NodeID: 7, LineID: "r1"}
	if got := DeriveLineUUID(user, line, 1); got != "c4b0e7f9-05eb-40ce-a204-5a443f2ef4e3" {
		t.Fatalf("attempt 1 = %s", got)
	}
	if got := DeriveLineUUID(user, line, 2); got != "d94e0af3-a2e2-49c4-a3b7-50ef6ad8c3ec" {
		t.Fatalf("attempt 2 = %s", got)
	}
	if DeriveLineUUID("other-user", line, 1) == DeriveLineUUID(user, line, 1) ||
		DeriveLineUUID(user, LineKey{NodeID: 7, LineID: "r2"}, 1) == DeriveLineUUID(user, line, 1) {
		t.Fatal("identity does not depend on user and line")
	}
	if got := LineEmailSuffix(line); got != "-l4163395866" {
		t.Fatalf("email suffix = %s", got)
	}
}

func TestLineUUIDKeyMasksBytesSixAndSeven(t *testing.T) {
	a := LineUUIDKey("C4B0E7F9-05EB-40CE-A204-5A443F2EF4E3")
	b := LineUUIDKey("c4b0e7f9-05eb-11ff-a204-5a443f2ef4e3")
	if a != b || a != "c4b0e7f905eb0000a2045a443f2ef4e3" {
		t.Fatalf("keys %s %s", a, b)
	}
	if LineUUIDKey("c4b0e7f9-05eb-40ce-a205-5a443f2ef4e3") == a {
		t.Fatal("byte 8 must not be masked")
	}
}

func TestClientTrafficFactor(t *testing.T) {
	half := 500
	factors := map[LineKey]int{{NodeID: 1, LineID: "r"}: 2000}
	cases := []struct {
		c    *PSPClient
		want int
	}{
		{&PSPClient{NodeID: 1, LineID: "r"}, 2000},
		{&PSPClient{NodeID: 1, LineID: "gone", LastFactorMilli: &half}, 500}, // deleted line keeps its last price
		{&PSPClient{NodeID: 1, LineID: "gone"}, TrafficFactorDefault},
		{&PSPClient{LastFactorMilli: &half}, TrafficFactorDefault}, // legacy row
	}
	for i, c := range cases {
		if got := ClientTrafficFactor(factors, c.c); got != c.want {
			t.Errorf("case %d = %d, want %d", i, got, c.want)
		}
	}
	zero, three := 0, 3000
	got := LineFactors([]*Node{{ID: 4, TrafficFactorMilli: &zero, Relays: []RelayLine{{ID: "x", TrafficFactorMilli: &three}, {ID: "y"}}}})
	if got[DirectLine(4)] != 0 || got[LineKey{NodeID: 4, LineID: "x"}] != 3000 || got[LineKey{NodeID: 4, LineID: "y"}] != 1000 {
		t.Fatalf("LineFactors = %v", got)
	}
}

func TestCountsSourceIPs(t *testing.T) {
	if !(&PSPClient{}).CountsSourceIPs() || !(&PSPClient{NodeID: 1, LineID: DirectLineID}).CountsSourceIPs() {
		t.Fatal("legacy and direct rows must count")
	}
	if (&PSPClient{NodeID: 1, LineID: "relay"}).CountsSourceIPs() {
		t.Fatal("a relay line's source addresses are the relay's, not the person's")
	}
}
