package destgroup

import (
	"errors"
	"fmt"
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
		// Address is the legacy spelling of a server (sing-box before 1.12): a URL.
		Address string `yaml:"address"`
		Detour  string `yaml:"detour"`
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
			// A server whose detour is a direct outbound (whatever its tag) is not
			// resolved through the proxy.
			direct := map[string]bool{"direct": true}
			// Read loosely and apart: a placeholder stands in the list where the
			// nodes go, and an outbound section that does not read only means the
			// default tag is all that is known.
			var outs struct {
				Outbounds []any `yaml:"outbounds"`
			}
			_ = yaml.Unmarshal([]byte(body), &outs)
			for _, o := range outs.Outbounds {
				if m, ok := o.(map[string]any); ok && m["type"] == "direct" {
					if tag, ok := m["tag"].(string); ok {
						direct[tag] = true
					}
				}
			}
			seen := map[string]bool{}
			for _, server := range root.DNS.Servers {
				if seen[server.Tag] {
					return nil, invalid("template.dns")
				}
				seen[server.Tag] = true
				legacy := server.Type == "" && strings.HasPrefix(server.Address, "https://")
				if !referenced[server.Tag] || (server.Type != "https" && !legacy) || direct[server.Detour] {
					continue
				}
				name := server.Server
				if legacy {
					u, err := url.Parse(server.Address)
					if err != nil || u.User != nil {
						return nil, invalid("template.dns")
					}
					name = u.Hostname()
				}
				host, err := baseDNSHostname(name)
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
	parsed, err := destlist.ParseCustom([]byte("full:"+host), domain.DestSideDest)
	if err != nil || parsed.EntryCount != 1 || parsed.Report.Ignored != 0 {
		return "", invalid("template.dns")
	}
	return strings.TrimPrefix(strings.TrimSpace(string(parsed.Entries)), "full:"), nil
}

// ErrBaseHostsUnavailable is a current subscription template whose DNS section
// cannot be read: no suggestion is made rather than a partial or invented one.
var ErrBaseHostsUnavailable = errors.New("destgroup: the subscription templates' DNS section cannot be read")

func invalid(field string) error { return fmt.Errorf("%w: %s", ErrBaseHostsUnavailable, field) }
