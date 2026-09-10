package render

import (
	"reflect"
	"strings"
	"testing"

	yaml "gopkg.in/yaml.v3"
)

func TestDefaultMihomoTemplateQUICControl(t *testing.T) {
	content := readTemplateContent(t, "../../seed/files/templates/default-mihomo.yaml")
	skeleton := placeholderRE.ReplaceAllString(content, "")
	var cfg struct {
		ProxyGroups []struct {
			Name string `yaml:"name"`
		} `yaml:"proxy-groups"`
		Rules    []string            `yaml:"rules"`
		SubRules map[string][]string `yaml:"sub-rules"`
	}
	if err := yaml.Unmarshal([]byte(skeleton), &cfg); err != nil {
		t.Fatalf("mihomo template content invalid after placeholder strip: %v", err)
	}

	if len(cfg.Rules) == 0 || cfg.Rules[0] != "SUB-RULE,(AND,((NETWORK,UDP),(DST-PORT,443))),quic-control" {
		t.Fatalf("leading rule = %#v", cfg.Rules)
	}
	wantSubRules := []string{
		"GEOSITE,geolocation-cn,DIRECT",
		"GEOIP,CN,DIRECT",
		"MATCH,REJECT",
	}
	if got := cfg.SubRules["quic-control"]; !reflect.DeepEqual(got, wantSubRules) {
		t.Fatalf("quic-control sub-rules = %#v, want %#v", got, wantSubRules)
	}
	for _, group := range cfg.ProxyGroups {
		if strings.Contains(group.Name, "UDP控制") {
			t.Fatalf("unexpected UDP control group: %q", group.Name)
		}
	}
	if strings.Contains(content, "NETWORK,UDP,🎛 UDP控制") {
		t.Fatal("unexpected catch-all UDP control rule")
	}

	personal := strings.Index(content, "{{ rules_personal }}")
	common := strings.Index(content, "{{ rules_common }}")
	subRules := strings.Index(content, "sub-rules:")
	if personal < 0 || common < personal || subRules < common {
		t.Fatal("personal/common placeholders or sub-rules are out of order")
	}
}
