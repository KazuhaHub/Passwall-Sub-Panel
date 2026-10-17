// Package vlessroute manages node-local subscription lines and only their
// explicitly registered tail routing rules. Outbound configuration is read-only.
package vlessroute

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

func RuleTag(nodeID int64, lineID string) string {
	return "psp-vlessroute:" + strconv.FormatInt(nodeID, 10) + ":" + lineID
}

// Compile preserves raw JSON values (including unknown fields and large
// integers) while replacing only rules identified in the persisted registry.
func Compile(template ports.RouteTemplate, nodeID int64, inboundTag string, registered, desired []domain.VlessRouteLine, otherOwned ...string) (ports.RouteTemplate, []string, error) {
	invalid := func(message string) (ports.RouteTemplate, []string, error) {
		return ports.RouteTemplate{}, nil, fmt.Errorf("%w: %s", domain.ErrValidation, message)
	}
	if inboundTag == "" {
		return invalid("missing actual inbound tag")
	}
	var root map[string]json.RawMessage
	if json.Unmarshal(template.Config, &root) != nil || root == nil {
		return invalid("invalid Xray template")
	}
	var routing map[string]json.RawMessage
	if raw := root["routing"]; len(raw) > 0 && string(raw) != "null" {
		if json.Unmarshal(raw, &routing) != nil {
			return invalid("invalid routing object")
		}
	}
	if routing == nil {
		routing = map[string]json.RawMessage{}
	}
	if raw := routing["script"]; len(raw) > 0 && string(raw) != `""` && string(raw) != "null" {
		return invalid("routing script is not supported")
	}
	var rules []json.RawMessage
	if raw := routing["rules"]; len(raw) > 0 {
		if json.Unmarshal(raw, &rules) != nil {
			return invalid("invalid routing rules")
		}
	}
	owned := map[string]bool{}
	for _, line := range registered {
		owned[RuleTag(nodeID, line.ID)] = true
	}
	for _, line := range desired {
		owned[RuleTag(nodeID, line.ID)] = true
	}
	keep := []json.RawMessage{}
	peers := []json.RawMessage{}
	peerTags := map[string]bool{}
	for _, tag := range otherOwned {
		peerTags[tag] = true
	}
	warnings := []string{}
	for _, raw := range rules {
		var rule map[string]json.RawMessage
		if json.Unmarshal(raw, &rule) != nil || rule == nil {
			return invalid("invalid existing routing rule")
		}
		var tag string
		_ = json.Unmarshal(rule["ruleTag"], &tag)
		if owned[tag] {
			continue
		}
		if peerTags[tag] {
			peers = append(peers, raw)
			continue
		}
		keep = append(keep, raw)
		// Report broad earlier rules; never move or rewrite them.
		if bytes.Contains(raw, []byte(`"vlessRoute"`)) {
			warnings = append(warnings, "Earlier routing rules take priority; inspect overlaps.")
		}
		if len(rule["domain"]) == 0 && len(rule["ip"]) == 0 && len(rule["protocol"]) == 0 && len(rule["vlessRoute"]) == 0 && (len(rule["network"]) > 0 || len(rule["port"]) > 0 || len(rule["inboundTag"]) > 0) {
			warnings = append(warnings, "Earlier broad routing rules may shadow this line.")
		}
	}
	keep = append(keep, peers...)
	outbounds := map[string]bool{}
	for _, outbound := range template.Outbounds {
		outbounds[outbound.Tag] = true
	}
	seen := map[uint16]bool{}
	for _, line := range desired {
		if line.Deleted || !line.Enabled {
			continue
		}
		if line.ID == "" || line.Route == 0 || seen[line.Route] {
			return invalid("invalid or duplicate route identity")
		}
		seen[line.Route] = true
		if !outbounds[line.OutboundTag] {
			return invalid("selected outbound tag no longer exists")
		}
		line, err := normalizeRouteMatch(line)
		if err != nil {
			return ports.RouteTemplate{}, nil, err
		}
		match := map[string]any{"type": "field", "ruleTag": RuleTag(nodeID, line.ID), "inboundTag": []string{inboundTag}, "vlessRoute": strconv.Itoa(int(line.Route)), "outboundTag": line.OutboundTag}
		if line.Network != "" {
			match["network"] = line.Network
		}
		if len(line.Protocols) > 0 {
			match["protocol"] = line.Protocols
		}
		if len(line.IP) > 0 {
			match["ip"] = line.IP
		}
		if len(line.Domain) > 0 {
			match["domain"] = line.Domain
		}
		if line.Port != "" {
			match["port"] = line.Port
		}
		raw, _ := json.Marshal(match)
		keep = append(keep, raw)
	}
	routing["rules"], _ = json.Marshal(keep)
	root["routing"], _ = json.Marshal(routing)
	template.Config, _ = json.Marshal(root)
	return template, warnings, nil
}
