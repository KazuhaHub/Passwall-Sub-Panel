package render

import (
	"fmt"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// expandRelays turns each real node that carries relay lines into multiple
// render items: the direct entry (unless the node hides it) followed by one
// entry per ENABLED relay line. Separators pass through unchanged; direct
// entries obey HideDirect independently of how many relays are enabled.
//
// Runs after applyLayout (so relay variants inherit their node's sort
// position, clustering right after their landing) and before the region-flag
// prefix (so each variant is flagged by its own line Region). Each variant
// shares the node pointer; only `relay` and `name` differ — the emit
// dispatchers read the dialed endpoint from `relay`, the display labels from
// renderItem.region/tags, and everything else from the node.
func expandRelays(items []renderItem) []renderItem {
	out := make([]renderItem, 0, len(items))
	for _, it := range items {
		if it.isSeparator || it.node == nil {
			out = append(out, it)
			continue
		}
		relays := enabledRelays(it.node.Relays)
		// A CDN line whose landing can no longer go through a CDN (its transport
		// was changed outside PSP, or adopted that way) is withheld rather than
		// rendered broken; the node sync raises the cdn_landing_unfit issue.
		// A node whose transport is not known here (no snapshot yet) is not judged.
		if f := domain.CDNLandingFault(it.node); f != nil && f.Code != domain.CDNFaultUnreadable {
			kept := relays[:0:0]
			for _, r := range relays {
				if !r.CDN() {
					kept = append(kept, r)
				}
			}
			relays = kept
		}
		// Hiding the direct entry is independent of enabled relay/route lines.
		// The landing's scale position is set here rather than trusted from
		// applyLayout, so a bare item (as the tests build them) is placed the
		// same way.
		it.slot = domain.LineSlot{Key: it.node.SortOrder, Keyed: true}
		if !it.node.EffectiveHideDirect() {
			out = append(out, it)
		}
		for i := range relays {
			// An unpositioned line carries its landing's key and is emitted right
			// after it, as always; a positioned one floats (placeLines).
			slot := it.slot
			if relays[i].Position != nil {
				slot = domain.LineSlot{Key: *relays[i].Position, Keyed: true, Floating: true}
			}
			out = append(out, renderItem{
				name:  relayEntryName(it.name, relays[i]),
				node:  it.node,
				relay: &relays[i],
				slot:  slot,
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
		if r.Publishable() {
			out = append(out, r)
		}
	}
	return out
}

// relaySupportsInbound checks the inbound selected for this render, including
// a live inbound fetched for a node without a local snapshot.
func relaySupportsInbound(relay *domain.RelayLine, n *domain.Node, inb *ports.Inbound, protocol domain.Protocol) bool {
	if relay == nil || !relay.CDN() {
		return true
	}
	landing := domain.Node{
		DisplayName: n.DisplayName, Flow: n.Flow,
		DesiredProtocol: string(protocol), StreamSettings: inb.StreamSettings,
	}
	return domain.ValidateCDNRelays(&landing, []domain.RelayLine{*relay}) == nil
}

// relayEntryName uses the line's name (or its address when unnamed), either
// on its own or appended to the node's display name, space-separated.
func relayEntryName(base string, r domain.RelayLine) string {
	label := r.Label()
	if r.OnlyLineName {
		return label
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
	if stream != nil {
		stream.normalizeHosts()
	}
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
		// The line's Host is the Host of whichever transport the landing uses: the
		// WebSocket and HTTPUpgrade Host header, the gRPC authority.
		if relay.Host != "" {
			if stream.WSSettings != nil {
				if stream.WSSettings.Headers == nil {
					stream.WSSettings.Headers = map[string]string{}
				}
				stream.WSSettings.Headers["Host"] = relay.Host
			}
			if h := stream.HTTPUpgradeSettings; h != nil {
				h.Host = relay.Host
				if h.Headers == nil {
					h.Headers = map[string]string{}
				}
				h.Headers["Host"] = relay.Host
			}
			if g := stream.GRPCSettings; g != nil {
				g.Authority = relay.Host
			}
		}
		if relay.CDN() {
			applyCDNStream(relay, stream)
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

// applyLegacyLineNames keeps URI lists on their legacy naming before
// publication filtering and flagging: a name shared by several entries gains a
// "[node:line]" suffix. Configuration subscriptions instead allocate names
// after reference pruning.
func applyLegacyLineNames(out []renderItem) {
	counts := map[string]int{}
	for i := range out {
		counts[out[i].name]++
	}
	for i := range out {
		if counts[out[i].name] > 1 && out[i].node != nil && !out[i].isSeparator {
			out[i].name = fmt.Sprintf("%s [%d:%s]", out[i].name, out[i].node.ID, out[i].lineID())
		}
	}
}

// applyCDNStream is what a CDN line adds to the stream it shares with its
// landing: how clients reach the CDN, which is the line's own setting and
// independent of the landing's TLS. With TLS on, clients present the line's SNI
// and the landing's fingerprint and always verify the certificate (the CDN
// presents its own, so the landing's allowInsecure is not carried); a landing
// without TLS gets a plain TLS client section. With TLS off, no TLS is emitted.
// Path, serviceName and ALPN stay the landing's. A line that states no choice
// follows the landing.
func applyCDNStream(relay *domain.RelayLine, stream *xuiStreamSettings) {
	stream.cdn = true
	if !relay.ClientUsesTLS(stream.Security == "tls") {
		stream.Security, stream.TLSSettings, stream.RealitySettings = "", nil, nil
		return
	}
	if stream.Security != "tls" || stream.TLSSettings == nil {
		stream.Security, stream.TLSSettings, stream.RealitySettings = "tls", &xuiTLSSettings{}, nil
	}
	stream.TLSSettings.AllowInsecure = false
	if relay.SNI != "" {
		stream.TLSSettings.ServerName = relay.SNI
	}
}

// placeLines applies the per-line placement rule to the expanded sequence: lines
// with an explicit position (relay.Position) move to their place on the shared
// sort scale, everything else stays where applyLayout + expandRelays put it.
// With no positioned line it returns items untouched, which is what keeps every
// subscription byte-identical to the pre-feature output until an administrator
// first reorders lines.
//
// It runs AFTER per-user filtering (tag filter, line visibility, unready egress,
// Publishable, the CDN landing fault) because all of that already happened to
// the node set before layout. Each user's order is therefore the global admin
// order with the entries that user may not see removed: a position never
// brings a withheld line back, and a line whose landing is filtered out is gone
// with it. Emit-time drops (no inbound, unconfirmed credentials, a protocol the
// format cannot carry) happen later still and only remove entries.
func placeLines(items []renderItem) []renderItem {
	slots := make([]domain.LineSlot, len(items))
	floating := false
	for i, it := range items {
		slots[i] = it.slot
		floating = floating || it.slot.Floating
	}
	if !floating {
		return items
	}
	order := domain.PlaceFloatingLines(slots)
	out := make([]renderItem, len(items))
	for i, from := range order {
		out[i] = items[from]
	}
	return out
}
