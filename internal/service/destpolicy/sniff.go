package destpolicy

import (
	"encoding/json"
	"github.com/KazuhaHub/passwall-protocol/protocol"
	"slices"
	"strings"
)

type SniffingCheck struct {
	Sufficient, Inject bool
	Reason             string
}

func CheckSniffing(raw json.RawMessage) SniffingCheck {
	if len(raw) == 0 {
		return SniffingCheck{Sufficient: true, Inject: true}
	}
	var spec struct {
		Enabled         bool            `json:"enabled"`
		DestOverride    []string        `json:"destOverride"`
		MetadataOnly    bool            `json:"metadataOnly"`
		DomainsExcluded json.RawMessage `json:"domainsExcluded"`
	}
	if json.Unmarshal(raw, &spec) != nil {
		return SniffingCheck{Reason: "invalid_config"}
	}
	if !spec.Enabled {
		return SniffingCheck{Sufficient: true, Inject: true}
	}
	if spec.MetadataOnly {
		return SniffingCheck{Reason: "metadata_only"}
	}
	if !slices.Contains(spec.DestOverride, "http") || !slices.Contains(spec.DestOverride, "tls") || !slices.Contains(spec.DestOverride, "quic") {
		return SniffingCheck{Reason: "missing_dest_override"}
	}
	if len(spec.DomainsExcluded) > 0 {
		return SniffingCheck{Reason: "domains_excluded"}
	}
	return SniffingCheck{Sufficient: true}
}

// Only an enabled, valid listener whose sniffing is insufficient causes a
// policy preflight failure. Malformed listener data is the existing compiler's
// object error; treating it as policy rejection would wrongly exclude a node.
func PreflightPolicy(policy *protocol.DestinationPolicy, engine string, listeners []protocol.Listener) []protocol.ListenerKey {
	if policy == nil || (engine != "" && engine != "xray") {
		return nil
	}
	needsSniffing := false
	for _, r := range policy.Rules {
		needsSniffing = needsSniffing || len(r.Domains) > 0 || len(r.Protocols) > 0
	}
	if !needsSniffing {
		return nil
	}
	var result []protocol.ListenerKey
	for _, listener := range listeners {
		var spec struct {
			Enabled  bool   `json:"enabled"`
			Sniffing string `json:"sniffing"`
		}
		if json.Unmarshal(listener.Config, &spec) != nil || !spec.Enabled {
			continue
		}
		check := CheckSniffing(json.RawMessage(strings.TrimSpace(spec.Sniffing)))
		if check.Reason != "invalid_config" && !check.Sufficient {
			result = append(result, listener.Key)
		}
	}
	return unique(result)
}
