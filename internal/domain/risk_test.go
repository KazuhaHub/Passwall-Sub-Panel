package domain

import (
	"reflect"
	"testing"
)

// The kinds are stored (risk_signals.kind), sent to the SPA and named by the
// four risk.*_off switches, so they are a contract in their spelling. The
// order is the admin table's column order: the store and the API return
// signals in it, and a reorder would silently move every column.
func TestRiskKinds_AreTheFourSignalsInDisplayOrder(t *testing.T) {
	want := []RiskKind{"sub_spread", "devices", "usage_shift", "login_country"}
	if got := RiskKinds(); !reflect.DeepEqual(got, want) {
		t.Fatalf("RiskKinds() = %q, want %q", got, want)
	}
}

// risk_signals.kind is varchar(24) and half the primary key. A longer kind
// is refused by the store (and by the stricter dialects on their own), so a
// kind that does not fit is a signal whose rows can never be written.
func TestRiskKindsFitTheColumn(t *testing.T) {
	kinds := RiskKinds()
	if len(kinds) == 0 {
		t.Fatal("RiskKinds() is empty")
	}
	for _, k := range kinds {
		if k == "" || len(k) > 24 {
			t.Fatalf("kind %q is %d bytes, want 1..24 (risk_signals.kind is varchar(24))", k, len(k))
		}
	}
}

// AllRiskCodes is the table the SPA's locale keys and each evaluator's
// code-exactness test read. Its keys must be exactly the kinds, each list
// must fit risk_signals.code (varchar(32)), and no code may be listed twice
// for one kind — a duplicate would be a translation counted twice and hide
// a missing one. Every kind has its own risk.*_off switch, so every kind can
// answer signal_off.
func TestAllRiskCodesMatchesKinds(t *testing.T) {
	all := AllRiskCodes()
	kinds := RiskKinds()
	if len(kinds) == 0 {
		t.Fatal("RiskKinds() is empty, so there is nothing to hold AllRiskCodes to")
	}
	if len(all) != len(kinds) {
		t.Fatalf("AllRiskCodes has %d kinds, RiskKinds has %d (%v vs %q)", len(all), len(kinds), all, kinds)
	}
	for _, k := range kinds {
		codes, ok := all[k]
		if !ok {
			t.Fatalf("AllRiskCodes has no entry for kind %q", k)
		}
		if len(codes) == 0 {
			t.Fatalf("AllRiskCodes[%q] is empty", k)
		}
		seen := map[RiskCode]bool{}
		for _, c := range codes {
			if c == "" || len(c) > 32 {
				t.Fatalf("AllRiskCodes[%q] has %q, %d bytes, want 1..32 (risk_signals.code is varchar(32))", k, c, len(c))
			}
			if seen[c] {
				t.Fatalf("AllRiskCodes[%q] lists %q twice", k, c)
			}
			seen[c] = true
		}
		if !seen[RiskCodeSignalOff] {
			t.Fatalf("AllRiskCodes[%q] = %q lacks %q, but every kind has an off switch", k, codes, RiskCodeSignalOff)
		}
	}
}
