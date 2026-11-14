package domain

import (
	"encoding/json"
	"fmt"
	"net/netip"
	"slices"
	"strings"
	"testing"
)

// dest_block counts an account's BLOCKED connections to destinations an admin
// chose to count as risk, over the last 24 hours. It is observe-only like the
// other risk signals, and it has six states: it never reads "exempt" (a trusted
// account is trusted about WHERE it signs in from, not about what it tries to
// reach), and never counts a report-only hit (a connection that was allowed).

func destBlockPolicy() DestBlockPolicy {
	return DestBlockPolicy{Threshold: RiskDefaultDestBlockThreshold}
}

func src(source string, n int64) DestBlockSourceCount {
	return DestBlockSourceCount{Source: source, Count: n}
}

func wantDestBlock(t *testing.T, v RiskVerdict, state GeoState, code RiskCode) {
	t.Helper()
	if v.State != state || v.Code != code {
		t.Fatalf("verdict = %s/%s, want %s/%s", v.State, v.Code, state, code)
	}
}

func TestEvaluateDestBlock_TheSixStates(t *testing.T) {
	p := destBlockPolicy() // threshold 20: suspect from 10
	for _, c := range []struct {
		name  string
		pol   DestBlockPolicy
		in    DestBlockInput
		state GeoState
		code  RiskCode
		ev    bool
	}{
		{"switched off", DestBlockPolicy{Off: true, Threshold: 20}, DestBlockInput{RecordingNodes: 2, Sources: []DestBlockSourceCount{src("p1", 99)}}, GeoStateDisabled, RiskCodeSignalOff, false},
		{"off wins over no collector", DestBlockPolicy{Off: true, Threshold: 20}, DestBlockInput{}, GeoStateDisabled, RiskCodeSignalOff, false},
		// An account none of whose nodes records is NOT clean: nothing was
		// watching, and "zero blocks" would be a claim.
		{"no node is recording", p, DestBlockInput{RecordingNodes: 0, Sources: []DestBlockSourceCount{src("p1", 99)}}, GeoStateUnknown, RiskCodeNoCollector, false},
		{"recording, nothing blocked", p, DestBlockInput{RecordingNodes: 1}, GeoStateIdle, RiskCodeNoHits, false},
		{"below half", p, DestBlockInput{RecordingNodes: 1, Sources: []DestBlockSourceCount{src("p1", 9)}}, GeoStateClean, RiskCodeWithin, true},
		{"at half", p, DestBlockInput{RecordingNodes: 1, Sources: []DestBlockSourceCount{src("p1", 10)}}, GeoStateSuspect, RiskCodeOverBuilding, true},
		{"just under the threshold", p, DestBlockInput{RecordingNodes: 1, Sources: []DestBlockSourceCount{src("p1", 19)}}, GeoStateSuspect, RiskCodeOverBuilding, true},
		{"at the threshold", p, DestBlockInput{RecordingNodes: 1, Sources: []DestBlockSourceCount{src("p1", 20)}}, GeoStateFlagged, RiskCodeOver, true},
		{"sources add up", p, DestBlockInput{RecordingNodes: 3, Sources: []DestBlockSourceCount{src("p1", 12), src("p2", 8)}}, GeoStateFlagged, RiskCodeOver, true},
		// ceil(threshold/2): an odd threshold rounds UP, so 3/2 is 2.
		{"odd threshold rounds the half up", DestBlockPolicy{Threshold: 3}, DestBlockInput{RecordingNodes: 1, Sources: []DestBlockSourceCount{src("p1", 1)}}, GeoStateClean, RiskCodeWithin, true},
		{"odd threshold suspect", DestBlockPolicy{Threshold: 3}, DestBlockInput{RecordingNodes: 1, Sources: []DestBlockSourceCount{src("p1", 2)}}, GeoStateSuspect, RiskCodeOverBuilding, true},
		// With a threshold of 1 a single block is already the threshold: there
		// is no state between idle and flagged.
		{"threshold one", DestBlockPolicy{Threshold: 1}, DestBlockInput{RecordingNodes: 1, Sources: []DestBlockSourceCount{src("p1", 1)}}, GeoStateFlagged, RiskCodeOver, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			v, ev := EvaluateDestBlock(c.pol, c.in)
			wantDestBlock(t, v, c.state, c.code)
			if (ev != nil) != c.ev {
				t.Fatalf("evidence = %+v, want present=%v", ev, c.ev)
			}
		})
	}
}

// A threshold that did not come from RiskPolicyFromSettings is repaired, never
// read literally: zero would flag every account that is blocked once.
func TestEvaluateDestBlock_ARawThresholdIsRepaired(t *testing.T) {
	in := DestBlockInput{RecordingNodes: 1, Sources: []DestBlockSourceCount{src("p1", 5)}}
	for _, th := range []int{0, -3} {
		v, ev := EvaluateDestBlock(DestBlockPolicy{Threshold: th}, in)
		wantDestBlock(t, v, GeoStateClean, RiskCodeWithin)
		if ev.Threshold != RiskDefaultDestBlockThreshold {
			t.Fatalf("threshold %d judged as %d, want the default", th, ev.Threshold)
		}
	}
	v, ev := EvaluateDestBlock(DestBlockPolicy{Threshold: RiskDestBlockThresholdMax * 3}, in)
	wantDestBlock(t, v, GeoStateClean, RiskCodeWithin)
	if ev.Threshold != RiskDestBlockThresholdMax {
		t.Fatalf("threshold clamped to %d", ev.Threshold)
	}
}

// The evaluator never returns "exempt", whatever it is handed.
func TestEvaluateDestBlock_NeverExempt(t *testing.T) {
	for _, off := range []bool{false, true} {
		for _, th := range []int{-1, 0, 1, 2, 20, 10000, 99999} {
			for _, nodes := range []int{-1, 0, 1, 5} {
				for _, total := range []int64{-5, 0, 1, 9, 10, 20, 1 << 40} {
					in := DestBlockInput{RecordingNodes: nodes}
					if total != 0 {
						in.Sources = []DestBlockSourceCount{src("p1", total)}
					}
					v, _ := EvaluateDestBlock(DestBlockPolicy{Off: off, Threshold: th}, in)
					if v.State == GeoStateExempt {
						t.Fatalf("exempt for off=%v th=%d nodes=%d total=%d", off, th, nodes, total)
					}
				}
			}
		}
	}
}

// Every code the evaluator can return is in AllRiskCodes, and every code listed
// is reachable: the SPA's strings are checked against that list.
func TestEvaluateDestBlock_CodesAreExactlyAllRiskCodes(t *testing.T) {
	seen := map[RiskCode]bool{}
	for _, off := range []bool{false, true} {
		for _, th := range []int{1, 2, 3, 20} {
			for _, nodes := range []int{0, 1} {
				for _, total := range []int64{0, 1, 2, 3, 9, 10, 19, 20, 500} {
					in := DestBlockInput{RecordingNodes: nodes}
					if total != 0 {
						in.Sources = []DestBlockSourceCount{src("p1", total)}
					}
					v, _ := EvaluateDestBlock(DestBlockPolicy{Off: off, Threshold: th}, in)
					seen[v.Code] = true
				}
			}
		}
	}
	want := AllRiskCodes()[RiskKindDestBlock]
	if len(want) != 6 {
		t.Fatalf("dest_block has %d codes, want 6: %v", len(want), want)
	}
	for _, c := range want {
		if !seen[c] {
			t.Errorf("code %s is listed in AllRiskCodes but no input reaches it", c)
		}
	}
	for c := range seen {
		if !slices.Contains(want, c) {
			t.Errorf("the evaluator returned %s, which AllRiskCodes does not list", c)
		}
	}
}

func TestEvaluateDestBlock_EvidenceIsBoundedAndHoldsNoAddress(t *testing.T) {
	sources := []DestBlockSourceCount{src("p3", 3), src("p1", 30), src("s4", 12), src("p9", 12), src("p2", 7), src("p5", 5), src("p6", 1)}
	_, ev := EvaluateDestBlock(destBlockPolicy(), DestBlockInput{RecordingNodes: 2, Sources: sources})
	if ev == nil {
		t.Fatal("no evidence")
	}
	if ev.V != RiskEvidenceVersion || ev.WindowHours != 24 || ev.Threshold != 20 || ev.Total != 70 || ev.Nodes != 2 {
		t.Fatalf("evidence = %+v", ev)
	}
	// At most five sources, most-hit first, equal counts in source order.
	var order []string
	for _, s := range ev.BySource {
		order = append(order, fmt.Sprintf("%s:%d", s.Source, s.Count))
	}
	if got := strings.Join(order, ","); got != "p1:30,p9:12,s4:12,p2:7,p5:5" {
		t.Fatalf("by_source = %s", got)
	}
	// RISK EVIDENCE NEVER HOLDS AN ADDRESS (connection-limits.md 14.14), and it
	// is copied into flag_records, which outlive the hits: no string in it may
	// parse as an IP or a network.
	raw, _ := json.Marshal(ev)
	var generic any
	_ = json.Unmarshal(raw, &generic)
	var walk func(v any)
	walk = func(v any) {
		switch x := v.(type) {
		case string:
			if _, err := netip.ParseAddr(x); err == nil {
				t.Errorf("evidence holds an address: %q", x)
			}
			if _, err := netip.ParsePrefix(x); err == nil {
				t.Errorf("evidence holds a network: %q", x)
			}
			if strings.ContainsAny(x, ".:/") {
				t.Errorf("evidence holds a string that looks like a destination: %q", x)
			}
		case map[string]any:
			for _, e := range x {
				walk(e)
			}
		case []any:
			for _, e := range x {
				walk(e)
			}
		}
	}
	walk(generic)
}

// What is counted is the caller's to decide (block rows of risk-counting
// policies); the evaluator only sums what it is handed, and a negative or empty
// count is nothing.
func TestEvaluateDestBlock_IgnoresNonPositiveCounts(t *testing.T) {
	v, _ := EvaluateDestBlock(destBlockPolicy(), DestBlockInput{RecordingNodes: 1, Sources: []DestBlockSourceCount{src("p1", -4), src("p2", 0)}})
	wantDestBlock(t, v, GeoStateIdle, RiskCodeNoHits)
}

func TestRiskKinds_DestBlockIsLast(t *testing.T) {
	kinds := RiskKinds()
	if kinds[len(kinds)-1] != RiskKindDestBlock || len(kinds) != 5 {
		t.Fatalf("kinds = %v", kinds)
	}
}

func TestRiskPolicyFromSettings_DestBlock(t *testing.T) {
	if p := RiskPolicyFromSettings(RiskPolicySettings{}); p.DestBlockOff || p.DestBlockThreshold != 20 {
		t.Fatalf("unset = %+v: the signal is on and the threshold is 20", p)
	}
	for in, want := range map[int]int{-5: 20, 0: 20, 1: 1, 7: 7, 10000: 10000, 10001: 10000, 1 << 30: 10000} {
		if p := RiskPolicyFromSettings(RiskPolicySettings{DestBlockThreshold: in}); p.DestBlockThreshold != want {
			t.Errorf("threshold %d resolved to %d, want %d", in, p.DestBlockThreshold, want)
		}
	}
	if p := RiskPolicyFromSettings(RiskPolicySettings{DestBlockOff: true}); !p.DestBlockOff {
		t.Fatal("the switch did not pass through")
	}
	// The runtime bounds do not touch the threshold: it is a count of events,
	// not a number of days of a window.
	if p := RiskPolicyFromSettings(RiskPolicySettings{DestBlockThreshold: 500}).Bounded(RiskRuntime{WindowDays: 1}); p.DestBlockThreshold != 500 {
		t.Fatalf("Bounded changed the threshold to %d", p.DestBlockThreshold)
	}
}
