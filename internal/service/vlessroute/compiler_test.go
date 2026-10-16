package vlessroute

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

func TestCompileOnlyOwnsRegisteredRulesAndAppendsAtTail(t *testing.T) {
	template := ports.RouteTemplate{Config: json.RawMessage(`{"unknown":9007199254740993,"outbounds":[{"tag":"warp","settings":{"password":"keep"}}],"routing":{"domainStrategy":"IPIfNonMatch","unknown":{"keep":true},"rules":[{"domain":["example.test"],"outboundTag":"custom"},{"ruleTag":"psp-vlessroute:2:other","vlessRoute":"12","outboundTag":"other"},{"network":"tcp,udp","outboundTag":"custom"}]}}`), TestURL: "keep-url", Outbounds: []ports.RouteOutbound{{Tag: "warp"}}}
	line := domain.VlessRouteLine{ID: "one", Name: "B", Route: 1001, OutboundTag: "warp", Enabled: true}
	got, warnings, err := Compile(template, 1, "inbound-443", nil, []domain.VlessRouteLine{line})
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) == 0 || got.TestURL != "keep-url" || !strings.Contains(string(got.Config), "9007199254740993") || !strings.Contains(string(got.Config), `"password":"keep"`) {
		t.Fatal("lost unrelated template settings")
	}
	var parsed struct {
		Routing struct {
			Rules []struct {
				RuleTag    string   `json:"ruleTag"`
				InboundTag []string `json:"inboundTag"`
				Route      string   `json:"vlessRoute"`
			} `json:"rules"`
		} `json:"routing"`
	}
	if err := json.Unmarshal(got.Config, &parsed); err != nil {
		t.Fatal(err)
	}
	rules := parsed.Routing.Rules
	if len(rules) != 4 || rules[1].RuleTag != "psp-vlessroute:2:other" || rules[3].RuleTag != RuleTag(1, "one") || rules[3].Route != "1001" || rules[3].InboundTag[0] != "inbound-443" {
		t.Fatalf("bad rules: %+v", rules)
	}
	again, _, err := Compile(got, 1, "inbound-443", []domain.VlessRouteLine{line}, []domain.VlessRouteLine{line})
	if err != nil {
		t.Fatal(err)
	}
	if string(again.Config) != string(got.Config) {
		t.Fatal("repeat save was not idempotent")
	}
	line.Enabled = false
	disabled, _, err := Compile(again, 1, "inbound-443", []domain.VlessRouteLine{line}, []domain.VlessRouteLine{line})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(disabled.Config), RuleTag(1, "one")) || !strings.Contains(string(disabled.Config), "psp-vlessroute:2:other") {
		t.Fatal("removed unrelated rule or retained disabled rule")
	}
}

func TestCompileRejectsMissingOutboundAndRoutingScript(t *testing.T) {
	line := domain.VlessRouteLine{ID: "one", Route: 1001, OutboundTag: "missing", Enabled: true}
	for _, config := range []string{`{"routing":{"rules":[]}}`, `{"routing":{"script":"route.lua"}}`} {
		if _, _, err := Compile(ports.RouteTemplate{Config: json.RawMessage(config)}, 1, "inbound", nil, []domain.VlessRouteLine{line}); err == nil {
			t.Fatal("accepted unsafe routing target")
		}
	}
}
