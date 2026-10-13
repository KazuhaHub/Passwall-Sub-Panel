package destpolicy

import (
	"net/netip"
	"net/url"
	"regexp"
	"slices"
	"strings"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/destlist"
	"gopkg.in/yaml.v3"
)

const unresolvedDNSPlaceholder = "__psp_template_placeholder__"

var dnsTemplatePlaceholder = regexp.MustCompile(`\{\{[^{}\r\n]+\}\}`)

type baseDNSRule struct {
	Server string        `yaml:"server"`
	Rules  []baseDNSRule `yaml:"rules"`
}
type baseDNSConfig struct {
	NameserverPolicy map[string]any `yaml:"nameserver-policy"`
	Servers          []struct {
		Tag    string `yaml:"tag"`
		Type   string `yaml:"type"`
		Server string `yaml:"server"`
		Detour string `yaml:"detour"`
	} `yaml:"servers"`
	Rules []baseDNSRule `yaml:"rules"`
}

// Read the DNS subtree of current subscription templates without rendering
// accounts, loading nodes or resolving hostnames. Placeholder markers outside
// DNS preserve parseable YAML/JSON; a selected unresolved DNS host is rejected.
func ProxiedDNSHosts(templates ...domain.Template) ([]string, error) {
	hosts := []string{}
	for _, template := range templates {
		var root struct {
			DNS baseDNSConfig `yaml:"dns"`
		}
		body := dnsTemplatePlaceholder.ReplaceAllString(template.Content, unresolvedDNSPlaceholder)
		if err := yaml.Unmarshal([]byte(body), &root); err != nil {
			return nil, invalid("template.dns")
		}
		switch template.ClientType {
		case domain.ClientMihomo:
			for _, value := range root.DNS.NameserverPolicy {
				var servers []string
				switch v := value.(type) {
				case string:
					servers = []string{v}
				case []any:
					for _, item := range v {
						s, ok := item.(string)
						if !ok {
							return nil, invalid("template.dns")
						}
						servers = append(servers, s)
					}
				case nil:
					continue
				default:
					return nil, invalid("template.dns")
				}
				for _, server := range servers {
					if !strings.HasPrefix(server, "https://") {
						continue
					}
					u, err := url.Parse(server)
					if err != nil || u.User != nil {
						return nil, invalid("template.dns")
					}
					proxy, _, _ := strings.Cut(u.Fragment, "&")
					if proxy == "" || strings.Contains(proxy, "=") || strings.EqualFold(proxy, "direct") {
						continue
					}
					host, err := baseDNSHostname(u.Hostname())
					if err != nil {
						return nil, err
					}
					if host != "" {
						hosts = append(hosts, host)
					}
				}
			}
		case domain.ClientSingBox:
			referenced := map[string]bool{}
			var walk func([]baseDNSRule)
			walk = func(rules []baseDNSRule) {
				for _, rule := range rules {
					if rule.Server != "" {
						referenced[rule.Server] = true
					}
					walk(rule.Rules)
				}
			}
			walk(root.DNS.Rules)
			seen := map[string]bool{}
			for _, server := range root.DNS.Servers {
				if seen[server.Tag] {
					return nil, invalid("template.dns")
				}
				seen[server.Tag] = true
				if !referenced[server.Tag] || server.Type != "https" || server.Detour == "direct" {
					continue
				}
				host, err := baseDNSHostname(server.Server)
				if err != nil {
					return nil, err
				}
				if host != "" {
					hosts = append(hosts, host)
				}
			}
		default:
			return nil, invalid("template.client_type")
		}
	}
	slices.Sort(hosts)
	return slices.Compact(hosts), nil
}

func baseDNSHostname(host string) (string, error) {
	if _, err := netip.ParseAddr(host); err == nil {
		return "", nil
	}
	if strings.Contains(host, unresolvedDNSPlaceholder) || strings.ContainsAny(host, "/\\?#@:\r\n\t") {
		return "", invalid("template.dns")
	}
	parsed, err := destlist.ParseCustom([]byte("full:" + host))
	if err != nil || parsed.EntryCount != 1 || parsed.Report.IgnoredBroad != 0 {
		return "", invalid("template.dns")
	}
	return strings.TrimPrefix(strings.TrimSpace(string(parsed.Entries)), "full:"), nil
}
