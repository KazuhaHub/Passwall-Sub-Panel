package sqlstore

import (
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

func TestDestinationRiskSettingsPersistAndRespectExplicitGroupZero(t *testing.T) {
	global, scope, resolver := newScopedTestRepos(t)
	ctx := t.Context()
	settings, err := global.Load(ctx, ports.UISettings{})
	if err != nil {
		t.Fatal(err)
	}
	settings.RiskDestBlockOff, settings.RiskDestBlockThreshold = true, 37
	if err := global.Save(ctx, settings); err != nil {
		t.Fatal(err)
	}
	check := func(group int64, wantOff bool, wantRaw, wantEffective int) {
		t.Helper()
		got, err := resolver.LoadForGroup(ctx, group, ports.UISettings{})
		if err != nil || got.RiskDestBlockOff != wantOff || got.RiskDestBlockThreshold != wantRaw {
			t.Fatalf("destination settings did not survive scoped resolution group=%d error=%v", group, err)
		}
		if domain.RiskPolicyFromSettings(got.RiskPolicySettings()).DestBlockThreshold != wantEffective {
			t.Fatal("group zero did not resolve to shipped destination threshold")
		}
	}
	check(1, true, 37, 37)
	for _, o := range []ports.ScopeOverride{{Type: "risk", Name: "dest_block_off", Value: "0"}, {Type: "risk", Name: "dest_block_threshold", Value: "0"}} {
		if err := scope.SetOverride(ctx, "group", 1, o); err != nil {
			t.Fatal(err)
		}
	}
	check(1, false, 0, 20)
	check(2, true, 37, 37)
	if err := scope.SetOverride(ctx, "group", 1, ports.ScopeOverride{Type: "risk", Name: "dest_block_threshold", Value: "10001"}); err != nil {
		t.Fatal(err)
	}
	check(1, false, 10001, 10000)
}
