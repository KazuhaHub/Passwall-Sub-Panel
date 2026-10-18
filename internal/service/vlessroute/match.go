package vlessroute

import (
	"fmt"
	"net/netip"
	"regexp"
	"strconv"
	"strings"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

var resourceTag = regexp.MustCompile(`^[A-Za-z0-9_@.\-]+$`)

func validResource(value, prefix string) bool {
	validCode := func(code string) bool {
		if prefix == "geoip:" {
			code = strings.TrimLeft(code, "!")
		}
		return resourceTag.MatchString(code) && !strings.HasSuffix(code, "@") && !strings.Contains(code, "@@")
	}
	if strings.HasPrefix(value, prefix) {
		return validCode(strings.TrimPrefix(value, prefix))
	}
	if strings.HasPrefix(value, "ext:") {
		parts := strings.Split(value, ":")
		return len(parts) == 3 && resourceTag.MatchString(parts[1]) && !strings.Contains(parts[1], "..") && validCode(parts[2])
	}
	return false
}

// normalizeRouteMatch validates only the five optional conditions on a line.
func normalizeRouteMatch(rule domain.VlessRouteLine) (domain.VlessRouteLine, error) {
	invalid := func(message string) (domain.VlessRouteLine, error) {
		return rule, fmt.Errorf("%w: routing match: %s", domain.ErrValidation, message)
	}
	rule.Network = strings.ToLower(strings.TrimSpace(rule.Network))
	if rule.Network == "tcp,udp" {
		rule.Network = ""
	}
	if rule.Network != "" && rule.Network != "tcp" && rule.Network != "udp" {
		return invalid("invalid network")
	}
	clean := func(values []string) ([]string, error) {
		if len(values) > 128 {
			return nil, fmt.Errorf("too many values")
		}
		var out []string
		seen := map[string]bool{}
		for _, value := range values {
			value = strings.TrimSpace(value)
			if len(value) > 1024 {
				return nil, fmt.Errorf("value is too long")
			}
			if value != "" && !seen[value] {
				out = append(out, value)
				seen[value] = true
			}
		}
		return out, nil
	}
	var err error
	if rule.IP, err = clean(rule.IP); err != nil {
		return invalid("invalid IP list: " + err.Error())
	}
	for _, ip := range rule.IP {
		value := strings.TrimLeft(ip, "!")
		_, addrErr := netip.ParseAddr(value)
		_, prefixErr := netip.ParsePrefix(value)
		if addrErr != nil && prefixErr != nil && !validResource(value, "geoip:") {
			return invalid("invalid target IP or CIDR")
		}
	}
	if rule.Domain, err = clean(rule.Domain); err != nil {
		return invalid("invalid domain list: " + err.Error())
	}
	for _, value := range rule.Domain {
		switch {
		case strings.HasPrefix(value, "regexp:"):
			pattern := strings.TrimPrefix(value, "regexp:")
			if pattern == "" {
				return invalid("empty domain regexp")
			}
			if _, err := regexp.Compile(pattern); err != nil {
				return invalid("invalid domain regexp")
			}
		case strings.HasPrefix(value, "geosite:"), strings.HasPrefix(value, "ext:"):
			if !validResource(value, "geosite:") {
				return invalid("invalid domain resource")
			}
		case strings.HasPrefix(value, "dotless:"):
			pattern := strings.TrimPrefix(value, "dotless:")
			if strings.Contains(pattern, ".") {
				return invalid("dotless pattern must not contain a dot")
			}
			if _, err := regexp.Compile("^[^.]*" + pattern + "[^.]*$"); err != nil {
				return invalid("invalid dotless pattern")
			}
		case strings.Contains(value, ":"):
			parts := strings.SplitN(value, ":", 2)
			if (parts[0] != "domain" && parts[0] != "full" && parts[0] != "keyword") || strings.TrimSpace(parts[1]) == "" {
				return invalid("invalid domain matcher")
			}
		}
	}
	if len(rule.Protocols) > 4 {
		return invalid("invalid sniffed protocols")
	}
	protocols := map[string]bool{}
	for _, value := range rule.Protocols {
		value = strings.ToLower(strings.TrimSpace(value))
		if value != "http" && value != "tls" && value != "quic" && value != "bittorrent" {
			return invalid("invalid sniffed protocol")
		}
		protocols[value] = true
	}
	rule.Protocols = nil
	for _, value := range []string{"http", "tls", "quic", "bittorrent"} {
		if protocols[value] {
			rule.Protocols = append(rule.Protocols, value)
		}
	}
	rule.Port = strings.TrimSpace(rule.Port)
	if len(rule.Port) > 2048 {
		return invalid("port list is too long")
	}
	if rule.Port != "" {
		var ports []string
		for _, value := range strings.Split(rule.Port, ",") {
			bounds := strings.Split(strings.TrimSpace(value), "-")
			if len(bounds) > 2 {
				return invalid("invalid target port range")
			}
			start, err := strconv.Atoi(strings.TrimSpace(bounds[0]))
			if err != nil || start < 1 || start > 65535 {
				return invalid("invalid target port")
			}
			port := strconv.Itoa(start)
			if len(bounds) == 2 {
				end, err := strconv.Atoi(strings.TrimSpace(bounds[1]))
				if err != nil || end < start || end > 65535 {
					return invalid("invalid target port range")
				}
				port += "-" + strconv.Itoa(end)
			}
			ports = append(ports, port)
		}
		rule.Port = strings.Join(ports, ",")
	}
	return rule, nil
}
