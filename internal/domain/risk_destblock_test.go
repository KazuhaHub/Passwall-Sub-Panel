package domain

import (
	"encoding/json"
	"math"
	"net/netip"
	"reflect"
	"slices"
	"testing"
)

func TestEvaluateDestBlockSixStatesAndThresholdBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name             string
		off              bool
		nodes, threshold int
		count            int64
		state            GeoState
		code             RiskCode
	}{
		{"disabled precedes collector", true, 0, 20, 37, GeoStateDisabled, "signal_off"},
		{"history does not prove a collector", false, 0, 20, 37, GeoStateUnknown, "no_collector"},
		{"no hits", false, 1, 20, 0, GeoStateIdle, "no_hits"},
		{"positive below half", false, 1, 20, 9, GeoStateClean, "within"},
		{"half", false, 1, 20, 10, GeoStateSuspect, "over_building"},
		{"below threshold", false, 2, 20, 19, GeoStateSuspect, "over_building"},
		{"threshold", false, 2, 20, 20, GeoStateFlagged, "over"},
		{"above threshold", false, 2, 20, 37, GeoStateFlagged, "over"},
		{"odd below ceil half", false, 1, 3, 1, GeoStateClean, "within"},
		{"odd ceil half", false, 1, 3, 2, GeoStateSuspect, "over_building"},
		{"one is flagged", false, 1, 1, 1, GeoStateFlagged, "over"},
		{"zero uses shipped default", false, 1, 0, 19, GeoStateSuspect, "over_building"},
		{"negative uses shipped default", false, 1, -1, 20, GeoStateFlagged, "over"},
		{"ceiling", false, 1, math.MaxInt, 10000, GeoStateFlagged, "over"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v, ev := EvaluateDestBlock(DestBlockPolicy{Off: tc.off, Threshold: tc.threshold}, DestBlockInput{CollectingNodes: tc.nodes, Sources: []DestBlockSource{{Source: "p12", Count: tc.count}}})
			if v != (RiskVerdict{State: tc.state, Code: tc.code}) || v.State == GeoStateExempt {
				t.Fatalf("verdict=%+v", v)
			}
			if tc.off || tc.nodes == 0 {
				if ev != nil {
					t.Fatal("unavailable signal produced evidence")
				}
				return
			}
			if ev == nil || ev.V != 1 || ev.WindowHours != 24 || ev.Total != tc.count || ev.Nodes != tc.nodes || ev.CoverageComplete || ev.Losses.Complete || ev.Losses.Scope != "panel" {
				t.Fatalf("fixed-window lower-bound evidence=%+v", ev)
			}
		})
	}
}

func TestEvaluateDestBlockFoldsBeforeTruncatingWithoutMutatingInput(t *testing.T) {
	in := DestBlockInput{CollectingNodes: 2, Sources: []DestBlockSource{{"p12", 3}, {"p2", 7}, {"p12", 9}, {"p3", 6}, {"p4", 5}, {"p5", 4}, {"p6", 3}, {"g1", 999}, {"p01", 999}, {"p+1", 999}, {"p0", 999}, {"P1", 999}, {"p9223372036854775808", 999}, {"192.0.2.1", 999}, {"p7", -1}}}
	before := slices.Clone(in.Sources)
	v, ev := EvaluateDestBlock(DestBlockPolicy{Threshold: 20}, in)
	want := []DestBlockSource{{"p12", 12}, {"p2", 7}, {"p3", 6}, {"p4", 5}, {"p5", 4}}
	if v.State != GeoStateFlagged || ev.Total != 37 || !reflect.DeepEqual(ev.BySource, want) || !reflect.DeepEqual(in.Sources, before) {
		t.Fatalf("source folding/limit changed totals or input: %+v", ev)
	}
}

func TestEvaluateDestBlockSaturatesCountsAndSeparatesLossUnits(t *testing.T) {
	in := DestBlockInput{CollectingNodes: 1, Sources: []DestBlockSource{{"p1", math.MaxInt64}, {"p1", 1}, {"p2", math.MaxInt64}}, Losses: DestAuditLosses{Rows: 2, Events: 3, Unmatched: 4, Complete: true, Scope: "192.0.2.1"}}
	v, ev := EvaluateDestBlock(DestBlockPolicy{Threshold: 20}, in)
	if v.State != GeoStateFlagged || ev.Total != math.MaxInt64 || ev.BySource[0].Count != math.MaxInt64 || ev.Losses != (DestAuditLosses{Rows: 2, Events: 3, Unmatched: 4, Scope: "panel"}) || ev.CoverageComplete {
		t.Fatalf("overflow or mixed units: %+v", ev)
	}
	data, err := json.Marshal(ev)
	if err != nil {
		t.Fatal(err)
	}
	var walk func(any)
	walk = func(v any) {
		switch value := v.(type) {
		case string:
			if _, err := netip.ParseAddr(value); err == nil {
				t.Fatal("evidence contains an IP")
			}
			if _, err := netip.ParsePrefix(value); err == nil {
				t.Fatal("evidence contains a prefix")
			}
		case map[string]any:
			for key, child := range value {
				if slices.Contains([]string{"dest", "port", "ip", "address"}, key) {
					t.Fatalf("address-bearing field %s", key)
				}
				walk(child)
			}
		case []any:
			for _, child := range value {
				walk(child)
			}
		}
	}
	var stored any
	if err := json.Unmarshal(data, &stored); err != nil {
		t.Fatal(err)
	}
	walk(stored)
}

func TestEvaluateDestBlockIdleKeepsIncompleteCoverageEvidence(t *testing.T) {
	v, ev := EvaluateDestBlock(DestBlockPolicy{}, DestBlockInput{CollectingNodes: 1, Losses: DestAuditLosses{Rows: 13, Events: 17, Unmatched: 19}})
	if v.State != GeoStateIdle || ev == nil || ev.Total != 0 || ev.CoverageComplete || ev.BySource == nil || ev.Losses.Rows != 13 || ev.Losses.Events != 17 || ev.Losses.Unmatched != 19 {
		t.Fatal("no observations became proof of complete safety")
	}
}
