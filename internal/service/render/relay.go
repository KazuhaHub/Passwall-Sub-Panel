package render

import "github.com/KazuhaHub/passwall-sub-panel/internal/domain"

// expandRelays turns each real node that carries relay lines into multiple
// render items: the direct entry (unless the node hides it) followed by one
// entry per ENABLED relay line. Separators and relay-less nodes pass through
// unchanged with a nil relay (= direct).
//
// Runs after applyLayout (so relay variants inherit their node's sort
// position, clustering right after their landing) and before the region-flag
// prefix (so every variant of a node gets the same flag). Each variant shares
// the node pointer; only `relay` and `name` differ — the emit dispatchers
// read the dialed endpoint from `relay` and everything else from the node.
func expandRelays(items []renderItem) []renderItem {
	out := make([]renderItem, 0, len(items))
	for _, it := range items {
		if it.isSeparator || it.node == nil {
			out = append(out, it)
			continue
		}
		relays := enabledRelays(it.node.Relays)
		if len(relays) == 0 {
			// No relay lines → the node renders exactly as before.
			out = append(out, it)
			continue
		}
		// HideDirect only takes effect when at least one relay is enabled, so
		// a node can never silently vanish from the subscription.
		if !it.node.HideDirect {
			out = append(out, it)
		}
		for i := range relays {
			out = append(out, renderItem{
				name:  relayEntryName(it.name, relays[i]),
				node:  it.node,
				relay: &relays[i],
			})
		}
	}
	return out
}

// enabledRelays returns the enabled lines in declaration order. The returned
// slice is fresh, so &result[i] stays stable for expandRelays to reference.
func enabledRelays(relays []domain.RelayLine) []domain.RelayLine {
	if len(relays) == 0 {
		return nil
	}
	out := make([]domain.RelayLine, 0, len(relays))
	for _, r := range relays {
		if r.Enabled {
			out = append(out, r)
		}
	}
	return out
}

// relayEntryName labels a relay variant by appending the line's name (or its
// address when unnamed) after the node's display name, space-separated.
func relayEntryName(base string, r domain.RelayLine) string {
	label := r.Name
	if label == "" {
		label = r.Address
	}
	return base + " " + label
}

// effectiveEndpoint resolves the server + port a proxy entry dials. For a
// direct entry (relay == nil) it returns the landing's own address + inbound
// port unchanged. For a relay variant it substitutes the relay's address /
// port (port 0 → reuse the inbound port) and, when the line carries them,
// overrides the TLS SNI (TLS serverName + Reality serverName) and WS Host on
// the parsed stream settings IN PLACE — so every per-protocol builder picks
// the override up without extra threading. (Hysteria2 reads SNI from its own
// opts struct and is overridden separately at the call site.)
func effectiveEndpoint(relay *domain.RelayLine, server string, inboundPort int, stream *xuiStreamSettings) (string, int) {
	if relay == nil {
		return server, inboundPort
	}
	if relay.Address != "" {
		server = relay.Address
	}
	port := inboundPort
	if relay.Port != 0 {
		port = relay.Port
	}
	if stream != nil {
		if relay.SNI != "" {
			if stream.TLSSettings != nil {
				stream.TLSSettings.ServerName = relay.SNI
			}
			if stream.RealitySettings != nil {
				if len(stream.RealitySettings.ServerNames) == 0 {
					stream.RealitySettings.ServerNames = []string{relay.SNI}
				} else {
					stream.RealitySettings.ServerNames[0] = relay.SNI
				}
			}
		}
		if relay.Host != "" && stream.WSSettings != nil {
			if stream.WSSettings.Headers == nil {
				stream.WSSettings.Headers = map[string]string{}
			}
			stream.WSSettings.Headers["Host"] = relay.Host
		}
	}
	return server, port
}

// relaySNIOverride returns the SNI a relay line forces for protocols (Hysteria2)
// whose SNI lives outside xuiStreamSettings. Empty = keep the landing's.
func relaySNIOverride(relay *domain.RelayLine) string {
	if relay == nil {
		return ""
	}
	return relay.SNI
}
