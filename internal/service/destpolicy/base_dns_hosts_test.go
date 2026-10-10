package destpolicy

import (
	"os"
	"reflect"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"gopkg.in/yaml.v3"
)

func TestProxiedDNSHostsUsesOnlyPolicyRoutedMihomoDoH(t *testing.T) {
	template := domain.Template{ClientType: domain.ClientMihomo, Content: `dns:
  default-nameserver: ['https://bootstrap.example.test/dns-query#Proxy']
  proxy-server-nameserver: ['https://node-dns.example.test/dns-query#Proxy']
  nameserver: ['https://fallback.example.test/dns-query#Proxy']
  nameserver-policy:
    cn: ['https://direct.example.test/dns-query', 'https://explicit-direct.example.test/dns-query#DIRECT']
    foreign: ['https://DNS.Example.test./dns-query#Proxy', 'https://1.1.1.1/dns-query#Proxy']
    scalar: 'https://dns.example.test/dns-query#Proxy&h3=true'
    parameters: 'https://parameters.example.test/dns-query#h3=true'
proxies:
  {{ proxies }}
`}
	got, err := ProxiedDNSHosts(template)
	if err != nil || !reflect.DeepEqual(got, []string{"dns.example.test"}) {
		t.Fatalf("policy DNS hosts=%v: %v", got, err)
	}
}

func TestProxiedDNSHostsUsesReferencedSingBoxHTTPSAndSkipsDirectUnusedAndIP(t *testing.T) {
	template := domain.Template{ClientType: domain.ClientSingBox, Content: `{
"dns": {
  "servers": [
    {"tag":"used", "type":"https", "server":"doh.example.test", "detour":"Proxy"},
    {"tag":"implicit", "type":"https", "server":"implicit.example.test"},
    {"tag":"unused", "type":"https", "server":"unused.example.test"},
    {"tag":"direct", "type":"https", "server":"direct.example.test", "detour":"direct"},
    {"tag":"ip", "type":"https", "server":"1.1.1.1"},
    {"tag":"udp", "type":"udp", "server":"udp.example.test"}
  ],
  "rules": [
    {"server":"used"},
    {"type":"logical", "rules":[{"server":"implicit"}]},
    {"server":"direct"}, {"server":"ip"}, {"server":"udp"}
  ],
  "final":"unused"
},
"outbounds": [{{ outbounds }}, {"type":"direct","tag":"direct"}]
}`}
	got, err := ProxiedDNSHosts(template)
	if err != nil || !reflect.DeepEqual(got, []string{"doh.example.test", "implicit.example.test"}) {
		t.Fatalf("sing-box DNS hosts=%v: %v", got, err)
	}
}

func TestProxiedDNSHostsReadsCurrentSeedContentsRatherThanPlanExamples(t *testing.T) {
	var templates []domain.Template
	for _, client := range []domain.ClientType{domain.ClientMihomo, domain.ClientSingBox} {
		data, err := os.ReadFile("../../seed/files/templates/default-" + string(client) + ".yaml")
		if err != nil {
			t.Fatal(err)
		}
		var file struct {
			Content string `yaml:"content"`
		}
		if err := yaml.Unmarshal(data, &file); err != nil {
			t.Fatal(err)
		}
		templates = append(templates, domain.Template{ClientType: client, Content: file.Content})
	}
	got, err := ProxiedDNSHosts(templates...)
	if err != nil || !reflect.DeepEqual(got, []string{"dns.alidns.com", "l9f26nnn5d.cloudflare-gateway.com"}) {
		t.Fatalf("current seed DNS hosts=%v: %v", got, err)
	}
}

func TestProxiedDNSHostsDoesNotReturnPartialOrInventUnresolvedHosts(t *testing.T) {
	good := domain.Template{ClientType: domain.ClientMihomo, Content: "dns:\n  nameserver-policy:\n    foreign: https://good.example.test/dns-query#Proxy\n"}
	for _, content := range []string{"dns: [", "dns:\n  nameserver-policy:\n    foreign: https://{{ secret_host }}/dns-query#Proxy\n", "dns:\n  nameserver-policy:\n    foreign: https://user:secret@private.example.test/dns-query#Proxy\n"} {
		got, err := ProxiedDNSHosts(good, domain.Template{ClientType: domain.ClientMihomo, Content: content})
		if err == nil || got != nil {
			t.Fatal("invalid DNS template returned partial or invented hosts")
		}
	}
	got, err := ProxiedDNSHosts(domain.Template{ClientType: domain.ClientMihomo, Content: "dns: {}"})
	if err != nil || got == nil || len(got) != 0 {
		t.Fatal("empty DNS suggestions must remain an explicit empty list")
	}
}
