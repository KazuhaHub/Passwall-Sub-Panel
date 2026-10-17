package vlessroute

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

func matchRules(t *testing.T, config json.RawMessage) []map[string]any {
	t.Helper()
	var root struct {
		Routing struct {
			Rules []map[string]any `json:"rules"`
		} `json:"routing"`
	}
	if err := json.Unmarshal(config, &root); err != nil {
		t.Fatal(err)
	}
	return root.Routing.Rules
}

func TestOptionalMatchValidation(t *testing.T) {
	if _, err := normalizeRouteMatch(domain.VlessRouteLine{}); err != nil {
		t.Fatal("empty legacy conditions rejected", err)
	}
	line, err := normalizeRouteMatch(domain.VlessRouteLine{Network: " TCP ", Protocols: []string{"TLS", "http"}, IP: []string{"192.0.2.0/24", "geoip:!cn", "!geoip:private", "ext:geoip.dat:cn", "192.0.2.0/24", ""}, Domain: []string{"domain:example.com", "regexp:^a{1,3}\\.example$", "dotless:"}, Port: " 443, 1000 - 2000 "})
	if err != nil || line.Network != "tcp" || line.Port != "443,1000-2000" || !reflect.DeepEqual(line.Protocols, []string{"http", "tls"}) || len(line.IP) != 4 || line.Domain[1] != "regexp:^a{1,3}\\.example$" {
		t.Fatalf("wrong normalization: %+v %v", line, err)
	}
	for _, line := range []domain.VlessRouteLine{
		{Network: "grpc"}, {Protocols: []string{"vless"}}, {IP: []string{"not-ip"}}, {IP: []string{"192.0.2.0/99"}}, {IP: []string{"ext:../../secret:cn"}},
		{Domain: []string{"regexp:["}}, {Domain: []string{"https://example.com"}}, {Domain: []string{"domain:"}}, {Domain: []string{"geosite:cn@"}}, {Domain: []string{"dotless:a.b"}},
		{Port: "0"}, {Port: "65536"}, {Port: "100-1"}, {Port: "443,"},
	} {
		if _, err := normalizeRouteMatch(line); !errors.Is(err, domain.ErrValidation) {
			t.Fatalf("invalid match accepted: %+v %v", line, err)
		}
	}
}

func TestMatchConditionsStayOnOneOwnedRuleWithoutFallback(t *testing.T) {
	template := ports.RouteTemplate{Config: json.RawMessage(`{"outbounds":[{"tag":"direct","protocol":"freedom"}],"routing":{"rules":[{"domain":["keep.example"],"outboundTag":"direct"}]}}`), Outbounds: []ports.RouteOutbound{{Tag: "direct"}}}
	line := domain.VlessRouteLine{ID: "one", Route: 1, Enabled: true, OutboundTag: "direct", Network: "tcp", Protocols: []string{"tls"}, IP: []string{"192.0.2.0/24"}, Domain: []string{"domain:example.com"}, Port: "443"}
	compiled, _, err := Compile(template, 1, "in-A", nil, []domain.VlessRouteLine{line})
	if err != nil {
		t.Fatal(err)
	}
	rules := matchRules(t, compiled.Config)
	if len(rules) != 2 || rules[0]["ruleTag"] != nil || rules[1]["ruleTag"] != RuleTag(1, "one") {
		t.Fatalf("extra rule or changed identity: %+v", rules)
	}
	rule := rules[1]
	if rule["network"] != "tcp" || rule["port"] != "443" || rule["outboundTag"] != "direct" || rule["vlessRoute"] != "1" || !reflect.DeepEqual(rule["inboundTag"], []any{"in-A"}) || !reflect.DeepEqual(rule["protocol"], []any{"tls"}) || !reflect.DeepEqual(rule["ip"], []any{"192.0.2.0/24"}) || !reflect.DeepEqual(rule["domain"], []any{"domain:example.com"}) {
		t.Fatalf("wrong fields: %+v", rule)
	}
	again, _, err := Compile(compiled, 1, "in-A", []domain.VlessRouteLine{line}, []domain.VlessRouteLine{line})
	if err != nil || !sameConfig(compiled.Config, again.Config) {
		t.Fatal("not idempotent", err)
	}
	line.Network, line.Protocols, line.IP, line.Domain, line.Port = "", nil, nil, nil, ""
	cleared, _, err := Compile(compiled, 1, "in-A", []domain.VlessRouteLine{line}, []domain.VlessRouteLine{line})
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"network", "protocol", "ip", "domain", "port"} {
		if _, ok := matchRules(t, cleared.Config)[1][key]; ok {
			t.Fatalf("clear left %s", key)
		}
	}
	for _, remove := range []bool{false, true} {
		line.Enabled = false
		desired := []domain.VlessRouteLine{line}
		if remove {
			desired = nil
		}
		clean, _, err := Compile(compiled, 1, "in-A", []domain.VlessRouteLine{line}, desired)
		if err != nil || len(matchRules(t, clean.Config)) != 1 {
			t.Fatal("cleanup left owned rule", err)
		}
	}
}

func TestSaveMatchFieldsRefreshAndDrift(t *testing.T) {
	s, _, c := serviceFixture(t)
	input := LineInput{Name: "B", Enabled: true, OutboundTag: "direct", Network: "tcp", Protocols: []string{"tls"}, IP: []string{"192.0.2.0/24"}, Domain: []string{"domain:example.com"}, Port: "443"}
	view, err := s.Save(context.Background(), 1, SaveRequest{Lines: []LineInput{input}})
	if err != nil {
		t.Fatal(err)
	}
	line := view.State.Lines[0]
	if line.Port != "443" || line.Network != "tcp" || len(line.Protocols) != 1 || len(line.IP) != 1 || len(line.Domain) != 1 {
		t.Fatal("five fields not persisted", line)
	}
	original := append(json.RawMessage(nil), c.template.Config...)
	for key, value := range map[string]any{"network": "udp", "protocol": []string{"http"}, "ip": []string{"203.0.113.0/24"}, "domain": []string{"domain:other.example"}, "port": "8443"} {
		t.Run(key, func(t *testing.T) {
			var root map[string]any
			_ = json.Unmarshal(original, &root)
			root["routing"].(map[string]any)["rules"].([]any)[1].(map[string]any)[key] = value
			changed, _ := json.Marshal(root)
			c.template.Config = changed
			read, err := s.Refresh(context.Background(), 1)
			if err != nil || read.State.Status != "drift" || c.writes != 1 || !sameConfig(changed, c.template.Config) {
				t.Fatalf("change not detected read-only: %+v %v", read, err)
			}
			c.template.Config = original
			if read, err = s.Refresh(context.Background(), 1); err != nil || read.State.Status != "applied" {
				t.Fatal("healthy rules did not recover", err)
			}
		})
	}
	read, err := s.Get(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	input.ID = line.ID
	input.Network = ""
	input.Protocols = nil
	input.IP = nil
	input.Domain = nil
	input.Port = ""
	saved, err := s.Save(context.Background(), 1, SaveRequest{Revision: read.State.Revision, Lines: []LineInput{input}})
	if err != nil || saved.State.Lines[0].Route != line.Route || saved.State.Lines[0].ID != line.ID || saved.State.Status != "applied" {
		t.Fatal("clearing changed line identity", err)
	}
	if len(matchRules(t, c.template.Config)) != 2 {
		t.Fatal("save added fallback or extra rule")
	}
}
