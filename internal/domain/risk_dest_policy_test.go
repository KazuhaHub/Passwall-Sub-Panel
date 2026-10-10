package domain

import "testing"

func TestDestinationRiskPolicySanitizesThresholdAndKeepsGroupSwitch(t *testing.T) {
	for _, tc := range []struct{ raw, want int }{{0, 20}, {-1, 20}, {1, 1}, {19, 19}, {20, 20}, {10000, 10000}, {10001, 10000}} {
		for _, off := range []bool{false, true} {
			got := RiskPolicyFromSettings(RiskPolicySettings{DestBlockOff: off, DestBlockThreshold: tc.raw})
			if got.DestBlockOff != off || got.DestBlockThreshold != tc.want {
				t.Fatalf("raw=%d off=%t got=%+v", tc.raw, off, got)
			}
			bounded := got.Bounded(RiskRuntime{WindowDays: 1, UsageRecentDays: 1})
			if bounded.DestBlockOff != off || bounded.DestBlockThreshold != tc.want {
				t.Fatal("unrelated runtime changed fixed-window destination policy")
			}
		}
	}
	if got := DefaultRiskPolicy(); got.DestBlockOff || got.DestBlockThreshold != 20 {
		t.Fatal("default destination risk policy unavailable")
	}
}
