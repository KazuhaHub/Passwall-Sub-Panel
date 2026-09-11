package render

import (
	"strings"
	"testing"
)

func TestSeedRulesSeparateQUICFromGeneralUDP(t *testing.T) {
	rules := readTemplateContent(t, "../../seed/files/rulesets/default-rules.yaml")
	private := strings.Index(rules, "IP-CIDR6,fd00::/8,🎯 全球直连")
	quic := strings.Index(rules, "AND,((NETWORK,UDP),(DST-PORT,443)),⚡ QUIC控制")
	udp := strings.Index(rules, "NETWORK,udp,🎮 UDP控制")
	if private < 0 || quic <= private || udp <= quic {
		t.Fatalf("private/QUIC/UDP rules are missing or out of order: private=%d quic=%d udp=%d", private, quic, udp)
	}
	if strings.Contains(rules, "GEOSITE,geolocation-cn,DIRECT") || strings.Contains(rules, "GEOIP,CN,DIRECT") {
		t.Fatal("QUIC policy must not hard-code a country-specific direct route")
	}

	template := readTemplateContent(t, "../../seed/files/templates/default-mihomo.yaml")
	if strings.Contains(template, "DST-PORT,443)),REJECT") {
		t.Fatal("mihomo template must not preempt the user-selectable QUIC policy")
	}
	personal := strings.Index(template, "{{ rules_personal }}")
	common := strings.Index(template, "{{ rules_common }}")
	if personal < 0 || common <= personal {
		t.Fatal("personal rules must remain ahead of the shared QUIC/UDP controls")
	}
}
