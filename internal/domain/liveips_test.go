package domain

import (
	"errors"
	"reflect"
	"testing"
)

// Every branch here decides a number an operator will read as "is this person
// sharing their account". The two directions are not symmetric: an inflated
// count accuses someone who did nothing, and a silently truncated one clears
// someone who did. Each test names which way it is guarding.

func owners(pairs ...any) map[ClientKey]int64 {
	m := map[ClientKey]int64{}
	for i := 0; i+2 < len(pairs)+1; i += 3 {
		m[NewClientKey(int64(pairs[i].(int)), pairs[i+1].(string))] = int64(pairs[i+2].(int))
	}
	return m
}

func panelIPs(id int64, byEmail map[string][]string) PanelLiveIPs {
	return PanelLiveIPs{PanelID: id, ByEmail: byEmail}
}

// The whole reason this layer exists: no single panel can see the total.
func TestAggregate_UnionsAcrossPanels(t *testing.T) {
	got := AggregateLiveIPsByUser(
		[]PanelLiveIPs{
			panelIPs(1, map[string][]string{"u7@x": {"1.1.1.1"}}),
			panelIPs(2, map[string][]string{"u7@x": {"2.2.2.2"}}),
		},
		owners(1, "u7@x", 7, 2, "u7@x", 7),
	)
	u := got[7]
	if u.Count() != 2 || u.Panels != 2 {
		t.Fatalf("cross-panel union: got count=%d panels=%d, want 2/2", u.Count(), u.Panels)
	}
	if !u.Complete() {
		t.Fatal("all panels answered, so the count is a total")
	}
}

// One person on one connection path who happens to reach two panels is ONE
// address. Counting it twice would inflate exactly the number this exists to
// make trustworthy — and inflation is the direction that accuses.
func TestAggregate_SameIPOnTwoPanelsCountsOnce(t *testing.T) {
	got := AggregateLiveIPsByUser(
		[]PanelLiveIPs{
			panelIPs(1, map[string][]string{"u7@x": {"1.1.1.1"}}),
			panelIPs(2, map[string][]string{"u7@x": {"1.1.1.1"}}),
		},
		owners(1, "u7@x", 7, 2, "u7@x", 7),
	)
	if n := got[7].Count(); n != 1 {
		t.Fatalf("one address seen from two panels: got %d, want 1", n)
	}
}

// A user split across credential partitions has several emails on ONE panel.
// The per-panel caps are applied per email, which is the multiplication this
// aggregate is meant to replace; the union must fold them back into a person.
func TestAggregate_FoldsCredentialPartitionsOfOneUser(t *testing.T) {
	got := AggregateLiveIPsByUser(
		[]PanelLiveIPs{panelIPs(1, map[string][]string{
			"u7@x":    {"1.1.1.1"},
			"u7-c1@x": {"1.1.1.1", "3.3.3.3"},
		})},
		owners(1, "u7@x", 7, 1, "u7-c1@x", 7),
	)
	u := got[7]
	if u.Count() != 2 || u.Panels != 1 {
		t.Fatalf("partitions of one user: got count=%d panels=%d, want 2/1", u.Count(), u.Panels)
	}
}

// An email is unique only WITHIN a panel. Keying on the string alone would
// merge two different people who happen to share it.
func TestAggregate_SameEmailOnTwoPanelsIsTwoClients(t *testing.T) {
	got := AggregateLiveIPsByUser(
		[]PanelLiveIPs{
			panelIPs(1, map[string][]string{"shared@x": {"1.1.1.1"}}),
			panelIPs(2, map[string][]string{"shared@x": {"2.2.2.2"}}),
		},
		owners(1, "shared@x", 7, 2, "shared@x", 8),
	)
	if got[7].Count() != 1 || got[8].Count() != 1 {
		t.Fatalf("same email on two panels must not merge: got %d and %d", got[7].Count(), got[8].Count())
	}
}

// A panel that could not be read makes the count a FLOOR. Presenting a floor
// as a total is the failure this whole thread is about: it reads as "this
// account is fine" exactly when the evidence is missing.
func TestAggregate_UnreadPanelMakesTheCountAFloor(t *testing.T) {
	got := AggregateLiveIPsByUser(
		[]PanelLiveIPs{
			panelIPs(1, map[string][]string{"u7@x": {"1.1.1.1"}}),
			{PanelID: 2, Err: errors.New("timeout")},
		},
		owners(1, "u7@x", 7, 2, "u7@x", 7),
	)
	u := got[7]
	if u.Unread != 1 || u.Complete() {
		t.Fatalf("unread panel: got unread=%d complete=%v, want 1/false", u.Unread, u.Complete())
	}
	if u.Count() != 1 {
		t.Fatalf("the readable panel still contributes: got %d, want 1", u.Count())
	}
}

// Only users who actually have a client on the unread panel are affected.
// Marking everyone incomplete would make the flag meaningless the first time
// one panel of fifty goes down.
func TestAggregate_UnreadPanelOnlyTouchesItsOwnUsers(t *testing.T) {
	got := AggregateLiveIPsByUser(
		[]PanelLiveIPs{
			panelIPs(1, map[string][]string{"u7@x": {"1.1.1.1"}}),
			{PanelID: 2, Err: errors.New("down")},
		},
		owners(1, "u7@x", 7, 2, "u8@x", 8),
	)
	if !got[7].Complete() {
		t.Fatal("user 7 has no client on the failed panel; their count is complete")
	}
	if got[8].Complete() {
		t.Fatal("user 8's only panel failed; their count cannot be complete")
	}
}

// A client PSP does not own — created by hand on the panel, or already
// released — must not be attributed to anyone. Attribution here would inflate
// a real person's count with a stranger's connections.
func TestAggregate_UnownedEmailIsSkippedNotGuessed(t *testing.T) {
	got := AggregateLiveIPsByUser(
		[]PanelLiveIPs{panelIPs(1, map[string][]string{
			"u7@x":          {"1.1.1.1"},
			"hand-made@pnl": {"9.9.9.9"},
		})},
		owners(1, "u7@x", 7),
	)
	if len(got) != 1 {
		t.Fatalf("only known users get rows: got %d", len(got))
	}
	if got[7].Count() != 1 {
		t.Fatalf("an unowned client leaked into a user's count: got %d, want 1", got[7].Count())
	}
}

// A user with nobody online still gets a row. Without it a caller cannot tell
// "idle" from "not looked at", and a distribution built from the result would
// be conditioned on being online.
func TestAggregate_IdleUserStillGetsARow(t *testing.T) {
	got := AggregateLiveIPsByUser(
		[]PanelLiveIPs{panelIPs(1, map[string][]string{})},
		owners(1, "u7@x", 7),
	)
	u, ok := got[7]
	if !ok {
		t.Fatal("an idle user must be present with zero, not absent")
	}
	if u.Count() != 0 || !u.Complete() {
		t.Fatalf("idle user: got count=%d complete=%v, want 0/true", u.Count(), u.Complete())
	}
}

func TestAggregate_IPsAreSortedNotMapOrdered(t *testing.T) {
	got := AggregateLiveIPsByUser(
		[]PanelLiveIPs{panelIPs(1, map[string][]string{"u7@x": {"3.3.3.3", "1.1.1.1", "2.2.2.2"}})},
		owners(1, "u7@x", 7),
	)
	want := []string{"1.1.1.1", "2.2.2.2", "3.3.3.3"}
	if !reflect.DeepEqual(got[7].IPs, want) {
		t.Fatalf("IPs = %v, want %v", got[7].IPs, want)
	}
}
