package ports

import "testing"

func TestDestinationRiskPolicyMappingsAndRuntimeSeparation(t *testing.T) {
	s := UISettings{RiskDestBlockOff: true, RiskDestBlockThreshold: 37}
	p := s.RiskPolicySettings()
	if !p.DestBlockOff || p.DestBlockThreshold != 37 {
		t.Fatal("group/global destination fields omitted from evaluator policy")
	}
	defaults := RiskCenterPolicyDefaults()
	if defaults["risk_dest_block_threshold"] != 20 {
		t.Fatal("policy form omitted destination threshold default")
	}
	for _, key := range []string{"risk.dest_block_off", "risk.dest_block_threshold"} {
		if !OverridableScopeKeys[key] {
			t.Fatal("destination risk key missing group override")
		}
	}
	effective, runtimeDefaults := RuntimeEffective(s)
	for _, key := range []string{"risk_dest_block_off", "risk_dest_block_threshold"} {
		if _, exists := effective[key]; exists {
			t.Fatal("destination policy incorrectly added to runtime values")
		}
		if _, exists := runtimeDefaults[key]; exists {
			t.Fatal("destination policy incorrectly added to runtime defaults")
		}
	}
}
